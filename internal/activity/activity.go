// Package activity is the read-only activity feed (GET /api/v1/activity, GAP-API-05): the audit_log
// table presented as a newest-first list of what happened and who did it.
//
// Only audit_log feeds it. Command outcomes (completed, timed_out) are not duplicated here: the
// audit row of a wake or shutdown names the command id and `GET /commands` holds its lifecycle.
//
// Exposure rules: the feed shows the username snapshot, the action, the target device and a
// message built from a whitelist of audit details (device or user name, command id, command
// status, error code, key id). It never exposes `remote_addr` (client addresses of other users),
// the failure reason of a login, or any other detail field.
package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/page"
	"github.com/thanhtunguet/homecore/internal/storage"
)

// Category groups audit actions for filtering and icons.
type Category string

const (
	CategoryAuth    Category = "auth"    // auth.*
	CategoryDevice  Category = "device"  // device.* except wake and shutdown
	CategoryCommand Category = "command" // device.wake, device.shutdown
	CategoryDDNS    Category = "ddns"    // ddns.*
	CategoryUser    Category = "user"    // user.*
	CategorySystem  Category = "system"  // everything else (controller_key.create, ...)
)

// Valid reports whether c is a known category.
func (c Category) Valid() bool {
	switch c {
	case CategoryAuth, CategoryDevice, CategoryCommand, CategoryDDNS, CategoryUser, CategorySystem:
		return true
	}
	return false
}

// ErrInvalid is a malformed argument (unknown category, bad device id): HTTP 400.
var ErrInvalid = errors.New("invalid argument")

// commandActions are the device.* actions that are command requests.
var commandActions = []string{"device.wake", "device.shutdown"}

// CategoryOf derives the category of an audit action. categoryWhere must stay equivalent
// (TestCategorySQLMatchesCategoryOf).
func CategoryOf(action string) Category {
	switch {
	case strings.HasPrefix(action, "auth."):
		return CategoryAuth
	case strings.HasPrefix(action, "user."):
		return CategoryUser
	case strings.HasPrefix(action, "ddns."):
		return CategoryDDNS
	case action == commandActions[0] || action == commandActions[1]:
		return CategoryCommand
	case strings.HasPrefix(action, "device."):
		return CategoryDevice
	}
	return CategorySystem
}

// Item is one entry of the feed.
type Item struct {
	ID        int64
	At        time.Time
	Actor     string // username snapshot, "cli" or "system"; empty when unknown (failed sign-in)
	Category  Category
	Action    string // machine-readable, e.g. "device.shutdown"
	DeviceID  string // when the entry is about a device
	CommandID string // wake/shutdown entries
	Result    string // "ok", "failed", or an error code
	Message   string // human-readable, English
}

// Raw is an audit row as the repository reads it.
type Raw struct {
	ID         int64
	At         time.Time
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	Result     string
	Details    string // JSON
	DeviceName string // current name of the target device, when it still exists
}

// Params selects a page.
type Params struct {
	Limit    int      // 0 = default
	Cursor   string   // opaque, from a previous page
	DeviceID string   // optional filter
	Category Category // optional filter
}

// Page is one page, newest first.
type Page struct {
	Items      []Item
	NextCursor string
}

// Filter is the validated selection passed to the repository.
type Filter struct {
	DeviceID string
	Category Category
}

// After is the keyset position (newest first: at DESC, id DESC).
type After struct {
	At time.Time
	ID int64
}

// Repository reads audit rows.
type Repository interface {
	List(ctx context.Context, f Filter, after *After, limit int) ([]Raw, error)
}

// Service builds the feed.
type Service struct{ repo Repository }

// NewService returns a Service on db.
func NewService(db *storage.DB) *Service { return &Service{repo: NewSQLiteRepository(db)} }

type cursor struct {
	T int64 `json:"t"`
	I int64 `json:"i"`
}

// List returns a page of the feed. page.ErrBadLimit, page.ErrBadCursor and ErrInvalid describe a
// bad request.
func (s *Service) List(ctx context.Context, p Params) (Page, error) {
	limit, err := page.Normalize(p.Limit)
	if err != nil {
		return Page{}, err
	}
	if p.Category != "" && !p.Category.Valid() {
		return Page{}, fmt.Errorf("%w: unknown category %q", ErrInvalid, p.Category)
	}
	if p.DeviceID != "" {
		if _, err := uuid.Parse(p.DeviceID); err != nil {
			return Page{}, fmt.Errorf("%w: device_id must be a UUID", ErrInvalid)
		}
	}
	var after *After
	if p.Cursor != "" {
		var c cursor
		if err := page.Decode(p.Cursor, &c); err != nil || c.I <= 0 {
			return Page{}, page.ErrBadCursor
		}
		after = &After{At: time.UnixMilli(c.T).UTC(), ID: c.I}
	}
	rows, err := s.repo.List(ctx, Filter{DeviceID: p.DeviceID, Category: p.Category}, after, limit+1)
	if err != nil {
		return Page{}, err
	}
	out := Page{}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		out.NextCursor = page.Encode(cursor{T: last.At.UnixMilli(), I: last.ID})
	}
	out.Items = make([]Item, len(rows))
	for i, r := range rows {
		out.Items[i] = toItem(r)
	}
	return out, nil
}

// details is the whitelist of audit detail fields the feed reads.
type details struct {
	Name      string `json:"name"`
	Username  string `json:"username"`
	CommandID string `json:"command_id"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
	KeyID     string `json:"key_id"`
	// DDNS entries: the record, the public address and what a sync did (never the token or an error text).
	RecordName string `json:"record_name"`
	IP         string `json:"ip"`
	SyncResult string `json:"sync_result"`
}

func toItem(r Raw) Item {
	var d details
	_ = json.Unmarshal([]byte(r.Details), &d) // a malformed details column only loses the extras
	it := Item{
		ID: r.ID, At: r.At, Actor: r.Actor, Category: CategoryOf(r.Action), Action: r.Action, Result: r.Result,
	}
	if r.TargetType == "device" {
		if _, err := uuid.Parse(r.TargetID); err == nil {
			it.DeviceID = r.TargetID
		}
	}
	if _, err := uuid.Parse(d.CommandID); err == nil {
		it.CommandID = d.CommandID
	}
	it.Message = message(r, d)
	return it
}

func message(r Raw, d details) string {
	device := firstNonEmpty(d.Name, r.DeviceName, "a device")
	who := firstNonEmpty(r.Actor, "someone")
	failed := r.Result != "ok"
	switch r.Action {
	case "auth.login":
		if failed {
			if r.Actor != "" {
				return "Failed sign-in for " + r.Actor
			}
			return "Failed sign-in attempt"
		}
		return who + " signed in"
	case "auth.logout":
		return who + " signed out"
	case "auth.refresh_reuse":
		return "A refresh token of " + who + " was used twice; the session family was revoked"
	case "user.create":
		return "User " + firstNonEmpty(d.Username, "account") + " created"
	case "user.password_change":
		return "Password of " + firstNonEmpty(d.Username, "an account") + " changed"
	case "device.create":
		return "Device " + device + " created"
	case "device.update":
		return "Device " + device + " updated"
	case "device.delete":
		return "Device " + device + " deleted"
	case "device.agent_remove":
		return "Agent binding of " + device + " removed"
	case "device.wake":
		return commandMessage("Wake", device, d, failed)
	case "device.shutdown":
		return commandMessage("Shutdown", device, d, failed)
	case "ddns.create":
		return "DDNS configuration " + ddnsLabel(d) + " created"
	case "ddns.update":
		return "DDNS configuration " + ddnsLabel(d) + " updated"
	case "ddns.delete":
		return "DDNS configuration " + ddnsLabel(d) + " deleted"
	case "ddns.sync":
		return ddnsSyncMessage(d, failed)
	case "controller_key.create":
		return "Controller signing key created" + suffix(" (key_id ", d.KeyID, ")")
	}
	return r.Action
}

// ddnsLabel names a DDNS configuration by its name and, when different, the record it manages.
func ddnsLabel(d details) string {
	switch {
	case d.Name != "" && d.RecordName != "" && d.Name != d.RecordName:
		return d.Name + " (" + d.RecordName + ")"
	case d.Name != "":
		return d.Name
	case d.RecordName != "":
		return d.RecordName
	}
	return "a record"
}

func ddnsSyncMessage(d details, failed bool) string {
	rec := firstNonEmpty(d.RecordName, d.Name, "a DDNS record")
	switch {
	case failed:
		return "DDNS sync of " + rec + " failed"
	case d.SyncResult == "updated":
		return rec + " updated" + suffix(" to ", d.IP, "")
	case d.SyncResult == "unchanged":
		return rec + " checked: already up to date" + suffix(" (", d.IP, ")")
	}
	return "DDNS sync of " + rec
}

func commandMessage(kind, device string, d details, failed bool) string {
	msg := kind + " requested for " + device
	switch {
	case failed && d.ErrorCode != "":
		return msg + ": failed (" + d.ErrorCode + ")"
	case failed:
		return msg + ": failed"
	case d.Status == "completed":
		return msg + ": nothing to do, already in that state"
	}
	return msg
}

func suffix(open, v, close string) string {
	if v == "" {
		return ""
	}
	return open + v + close
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

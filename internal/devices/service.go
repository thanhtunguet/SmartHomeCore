package devices

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/page"
)

// Service implements the device use cases: validation, defaults, ids and pagination on top of a
// Repository. It never touches observed state (status, last_seen_at, …); that is the monitor's.
type Service struct {
	repo Repository
	now  func() time.Time
}

// NewService returns a Service. now may be nil (time.Now).
func NewService(repo Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, now: now}
}

// Create validates in, applies the defaults and registers the device (and its agent binding).
func (s *Service) Create(ctx context.Context, in CreateInput) (Device, error) {
	var p problems
	d := Device{
		Name:                checkName(&p, in.Name),
		Description:         checkDescription(&p, in.Description),
		Hostname:            checkHostname(&p, in.Hostname),
		IPAddress:           checkAddress(&p, in.IPAddress),
		MACAddress:          checkMAC(&p, in.MACAddress),
		WOLBroadcastAddress: checkBroadcast(&p, in.WOLBroadcastAddress),
		WOLPort:             DefaultWOLPort,
		Type:                TypeDesktop,
		OS:                  OSLinux,
		MonitorPort:         in.MonitorPort,
		MonitorInterval:     DefaultMonitorInterval,
		MonitorTimeout:      DefaultMonitorTimeout,
		Enabled:             true,
		Status:              StatusUnknown,
	}
	if in.WOLPort != nil {
		d.WOLPort = *in.WOLPort
	}
	checkPort(&p, "wol_port", d.WOLPort)
	if in.MonitorPort == 0 {
		p.add("monitor_port: required")
	} else {
		checkPort(&p, "monitor_port", in.MonitorPort)
	}
	if in.DeviceType != "" {
		d.Type = checkType(&p, in.DeviceType)
	}
	if in.OS != "" {
		d.OS = checkOS(&p, in.OS)
	}
	if in.MonitorIntervalSeconds != nil {
		d.MonitorInterval = checkInterval(&p, *in.MonitorIntervalSeconds)
	}
	if in.MonitorTimeoutMs != nil {
		d.MonitorTimeout = checkTimeout(&p, *in.MonitorTimeoutMs)
	}
	if in.Enabled != nil {
		d.Enabled = *in.Enabled
	}
	if in.Agent != nil {
		d.Agent = checkAgent(&p, in.Agent)
	}
	if err := p.err(); err != nil {
		return Device{}, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Device{}, fmt.Errorf("new device id: %w", err)
	}
	d.ID = id.String()
	d.CreatedAt = s.now().UTC()
	d.UpdatedAt = d.CreatedAt
	if err := s.repo.Create(ctx, d); err != nil {
		return Device{}, err
	}
	return s.repo.Get(ctx, d.ID)
}

// Get returns one device. ErrNotFound if it does not exist.
func (s *Service) Get(ctx context.Context, id string) (Device, error) { return s.repo.Get(ctx, id) }

// Resolve finds a device by a reference that is either its id (a UUID) or its name (compared
// case-insensitively, surrounding space ignored). It is how the legacy endpoints, whose clients
// predate UUIDs, identify a device (ADR-017). ErrNotFound when nothing matches.
func (s *Service) Resolve(ctx context.Context, ref string) (Device, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Device{}, ErrNotFound
	}
	if id, err := uuid.Parse(ref); err == nil {
		d, err := s.repo.Get(ctx, id.String())
		if !errors.Is(err, ErrNotFound) {
			return d, err
		}
	}
	return s.repo.GetByName(ctx, ref)
}

// Update applies a partial update and returns the stored device. An empty UpdateInput changes
// nothing (not even updated_at).
func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (Device, error) {
	d, err := s.repo.Get(ctx, id)
	if err != nil {
		return Device{}, err
	}
	if in.IsEmpty() {
		return d, nil
	}
	var p problems
	if in.Name != nil {
		d.Name = checkName(&p, *in.Name)
	}
	if in.Description != nil {
		d.Description = checkDescription(&p, *in.Description)
	}
	if in.Hostname != nil {
		d.Hostname = checkHostname(&p, *in.Hostname)
	}
	if in.IPAddress != nil {
		d.IPAddress = checkAddress(&p, *in.IPAddress)
	}
	if in.MACAddress != nil {
		d.MACAddress = checkMAC(&p, *in.MACAddress)
	}
	if in.WOLBroadcastAddress != nil {
		d.WOLBroadcastAddress = checkBroadcast(&p, *in.WOLBroadcastAddress)
	}
	if in.WOLPort != nil {
		d.WOLPort = *in.WOLPort
		checkPort(&p, "wol_port", d.WOLPort)
	}
	if in.DeviceType != nil {
		d.Type = checkType(&p, *in.DeviceType)
	}
	if in.OS != nil {
		d.OS = checkOS(&p, *in.OS)
	}
	if in.MonitorPort != nil {
		d.MonitorPort = *in.MonitorPort
		checkPort(&p, "monitor_port", d.MonitorPort)
	}
	if in.MonitorIntervalSeconds != nil {
		d.MonitorInterval = checkInterval(&p, *in.MonitorIntervalSeconds)
	}
	if in.MonitorTimeoutMs != nil {
		d.MonitorTimeout = checkTimeout(&p, *in.MonitorTimeoutMs)
	}
	if in.Enabled != nil {
		d.Enabled = *in.Enabled
	}
	if in.Agent != nil {
		d.Agent = checkAgent(&p, in.Agent)
	}
	if err := p.err(); err != nil {
		return Device{}, err
	}
	d.UpdatedAt = s.now().UTC()
	if err := s.repo.Update(ctx, d, in.Agent != nil); err != nil {
		return Device{}, err
	}
	return s.repo.Get(ctx, id)
}

// Delete removes the device together with its binding, commands and status history.
func (s *Service) Delete(ctx context.Context, id string) error { return s.repo.Delete(ctx, id) }

// RemoveAgent unbinds the agent; idempotent for a device that has no binding.
func (s *Service) RemoveAgent(ctx context.Context, id string) error {
	return s.repo.RemoveAgent(ctx, id)
}

// DevicePage is one page of devices.
type DevicePage struct {
	Items      []Device
	NextCursor string // empty when there are no more items
}

type deviceCursor struct {
	N string `json:"n"` // name of the last item
	I string `json:"i"` // id of the last item
}

// List returns a page of devices ordered by name (case-insensitive). limit 0 = default (50).
// page.ErrBadLimit / page.ErrBadCursor describe a bad request.
func (s *Service) List(ctx context.Context, limit int, cursor string) (DevicePage, error) {
	limit, err := page.Normalize(limit)
	if err != nil {
		return DevicePage{}, err
	}
	var after *ListAfter
	if cursor != "" {
		var c deviceCursor
		if err := page.Decode(cursor, &c); err != nil || c.I == "" {
			return DevicePage{}, page.ErrBadCursor
		}
		after = &ListAfter{Name: c.N, ID: c.I}
	}
	items, err := s.repo.List(ctx, after, limit+1)
	if err != nil {
		return DevicePage{}, err
	}
	out := DevicePage{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		last := out.Items[limit-1]
		out.NextCursor = page.Encode(deviceCursor{N: last.Name, I: last.ID})
	}
	return out, nil
}

// HistoryPage is one page of status changes, newest first.
type HistoryPage struct {
	Items      []StatusChange
	NextCursor string
}

type historyCursor struct {
	T int64 `json:"t"` // changed_at (unix ms) of the last item
	I int64 `json:"i"` // row id of the last item
}

// History returns the status transitions of a device, newest first. ErrNotFound if the device
// does not exist.
func (s *Service) History(ctx context.Context, id string, limit int, cursor string) (HistoryPage, error) {
	limit, err := page.Normalize(limit)
	if err != nil {
		return HistoryPage{}, err
	}
	var after *HistoryAfter
	if cursor != "" {
		var c historyCursor
		if err := page.Decode(cursor, &c); err != nil || c.I == 0 {
			return HistoryPage{}, page.ErrBadCursor
		}
		after = &HistoryAfter{ChangedAt: time.UnixMilli(c.T).UTC(), ID: c.I}
	}
	if _, err := s.repo.Get(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return HistoryPage{}, ErrNotFound
		}
		return HistoryPage{}, err
	}
	items, err := s.repo.History(ctx, id, after, limit+1)
	if err != nil {
		return HistoryPage{}, err
	}
	out := HistoryPage{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		last := out.Items[limit-1]
		out.NextCursor = page.Encode(historyCursor{T: last.ChangedAt.UnixMilli(), I: last.ID})
	}
	return out, nil
}

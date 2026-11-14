// Package ddns keeps Cloudflare DNS records pointed at the current public IP.
//
// Flow per config: discover the public IP (>= 2 providers that must agree, never a single source)
// -> validate the family and reject non-public ranges -> read the Cloudflare record by its id ->
// check that it still has the configured type and name -> update only if it differs -> persist the
// state and a ddns_sync_history row. Transient failures retry with exponential backoff (1 s ->
// 5 min, jittered) without delaying other configs. The Controller never resolves a record by name:
// zone and record are chosen from the Cloudflare lookups and stored by id (REQ-API-020).
//
// API tokens are sealed with internal/secrets (AAD "ddns_configs.api_token"), travel only in the
// Authorization header of requests to Cloudflare, and appear in no error, log line or response.
//
// Files: discover.go (public IP + quorum), cloudflare.go (REST client), repo*.go (storage),
// service.go (CRUD, lookups, one sync), syncer.go (the periodic job).
package ddns

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/thanhtunguet/homecore/internal/storage"
)

// RecordType is a DNS record type managed by DDNS.
type RecordType string

const (
	RecordA    RecordType = "A"
	RecordAAAA RecordType = "AAAA"
)

// Valid reports whether t is a supported record type.
func (t RecordType) Valid() bool { return t == RecordA || t == RecordAAAA }

// IPDiscoverer returns the current public address for the requested family.
type IPDiscoverer interface {
	PublicIP(ctx context.Context, family RecordType) (netip.Addr, error)
}

// Zone is a Cloudflare zone.
type Zone struct {
	ID   string
	Name string
}

// Record is an A or AAAA record of a zone.
type Record struct {
	ID      string
	Name    string
	Type    RecordType
	Content string
	Proxied bool
	TTL     int // 1 = automatic
}

// RecordUpdate is what a sync changes: the content, plus the desired proxied and ttl. Name and type
// are never sent, so a PATCH cannot rename or retype a record.
type RecordUpdate struct {
	Content netip.Addr
	Proxied bool
	TTL     int
}

// DNSProvider is the Cloudflare surface DDNS needs. Records are addressed by id only. The token is
// a per-call argument because every configuration has its own.
type DNSProvider interface {
	ListZones(ctx context.Context, token string) ([]Zone, error)
	ListRecords(ctx context.Context, token, zoneID string) ([]Record, error)
	GetRecord(ctx context.Context, token, zoneID, recordID string) (Record, error)
	UpdateRecord(ctx context.Context, token, zoneID, recordID string, u RecordUpdate) (Record, error)
}

// Errors the HTTP layer maps to statuses.
var (
	// ErrNotFound: no such DDNS configuration (404).
	ErrNotFound = errors.New("ddns configuration not found")
	// ErrDuplicate: another configuration already manages this record (400, the spec has no 409).
	ErrDuplicate = errors.New("another DDNS configuration already manages this record")
	// ErrRecordMismatch: the record fetched by id no longer has the configured type or name. The
	// syncer never updates it: it is not the record the user chose.
	ErrRecordMismatch = errors.New("the Cloudflare record does not match the configured type and name")
)

// ValidationError is a rejected request (400).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// permanentError marks a failure that retrying with backoff cannot fix (bad token, wrong record):
// the syncer waits for the next normal interval instead of hammering Cloudflare.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// IsTransient reports whether err is worth retrying soon: network failures, timeouts, 5xx, rate
// limits and public-IP quorum failures. Authentication errors and record mismatches are not.
func IsTransient(err error) bool {
	var perm *permanentError
	if errors.As(err, &perm) {
		return false
	}
	var cf *CloudflareError
	if errors.As(err, &cf) {
		return cf.Transient()
	}
	return true
}

// redact removes every occurrence of secret from s.
func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

// PruneSyncHistory deletes at most limit ddns_sync_history rows older than cutoff (the retention
// job's DDNS target).
func PruneSyncHistory(ctx context.Context, db *storage.DB, cutoff time.Time, limit int) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM ddns_sync_history WHERE id IN
		(SELECT id FROM ddns_sync_history WHERE at < ? LIMIT ?)`, storage.Millis(cutoff), limit)
	if err != nil {
		return 0, fmt.Errorf("prune ddns sync history: %w", err)
	}
	return res.RowsAffected()
}

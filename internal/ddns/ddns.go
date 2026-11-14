// Package ddns keeps Cloudflare DNS records pointed at the current public IP.
//
// Flow per config: discover public IP (>= 2 providers, quorum) → validate v4/v6 → compare with
// last known → read Cloudflare record → update only if different → persist state and
// ddns_sync_history. Transient failures retry with exponential backoff (1s → 5m, jittered).
// API tokens are sealed with internal/secrets and never returned by the API.
//
// TODO(phase-4): IPDiscoverer, Cloudflare client (net/http, no SDK), Syncer job, repository.
package ddns

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/thanhtunguet/homecore/internal/storage"
)

// RecordType is a DNS record type managed by DDNS.
type RecordType string

const (
	RecordA    RecordType = "A"
	RecordAAAA RecordType = "AAAA"
)

// IPDiscoverer returns the current public address for the requested family.
type IPDiscoverer interface {
	PublicIP(ctx context.Context, family RecordType) (netip.Addr, error)
}

// DNSProvider reads and updates one DNS record.
type DNSProvider interface {
	GetRecord(ctx context.Context, zoneID, name string, typ RecordType) (id string, content netip.Addr, err error)
	UpsertRecord(ctx context.Context, zoneID, recordID, name string, typ RecordType, ip netip.Addr, ttl int, proxied bool) (id string, err error)
}

// PruneSyncHistory deletes at most limit ddns_sync_history rows older than cutoff (the retention
// job's DDNS target). The syncer that writes the table arrives in Phase 4.
func PruneSyncHistory(ctx context.Context, db *storage.DB, cutoff time.Time, limit int) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM ddns_sync_history WHERE id IN
		(SELECT id FROM ddns_sync_history WHERE at < ? LIMIT ?)`, storage.Millis(cutoff), limit)
	if err != nil {
		return 0, fmt.Errorf("prune ddns sync history: %w", err)
	}
	return res.RowsAffected()
}

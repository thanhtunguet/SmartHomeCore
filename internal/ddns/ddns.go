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
	"net/netip"
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

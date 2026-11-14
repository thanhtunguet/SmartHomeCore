package ddns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/secrets"
)

// TokenAAD binds a sealed token to its column (REQ-SEC-002).
const TokenAAD = "ddns_configs.api_token"

// Limits and defaults, from the DdnsConfig* schemas in api/openapi.yaml.
const (
	MinInterval     = 60 * time.Second
	MaxInterval     = 24 * time.Hour
	DefaultInterval = 300 * time.Second
	MaxTTL          = 86400
	maxNameLen      = 100
	maxRecordName   = 253
	maxTokenLen     = 512
	maxIDLen        = 64
)

// ServiceConfig configures a Service.
type ServiceConfig struct {
	Repo   Repository
	Sealer *secrets.Sealer
	DNS    DNSProvider
	IP     IPDiscoverer
	Now    func() time.Time // nil = time.Now
	Log    *slog.Logger     // nil = slog.Default()
}

// Service implements DDNS configuration management, the Cloudflare lookups for the form dropdowns
// and the one-record sync that both the periodic Syncer and POST /ddns/{id}/sync use.
type Service struct {
	repo   Repository
	sealer *secrets.Sealer
	dns    DNSProvider
	ip     IPDiscoverer
	now    func() time.Time
	log    *slog.Logger
	locks  keyedMutex
}

// NewService returns a Service.
func NewService(c ServiceConfig) *Service {
	s := &Service{repo: c.Repo, sealer: c.Sealer, dns: c.DNS, ip: c.IP, now: c.Now, log: c.Log}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s
}

// ---- configuration CRUD ---------------------------------------------------------------------------

// CreateInput is a new configuration. Zone and record come from the Cloudflare lookups; the
// Controller never resolves a record by name.
type CreateInput struct {
	Name, ZoneID, RecordID, RecordName string
	RecordType                         RecordType
	Proxied                            *bool
	TTL                                *int
	IntervalSeconds                    *int
	Enabled                            *bool
	APIToken                           string
}

// UpdateInput is a partial update; nil fields are unchanged.
type UpdateInput struct {
	Name, ZoneID, RecordID, RecordName *string
	RecordType                         *RecordType
	Proxied                            *bool
	TTL                                *int
	IntervalSeconds                    *int
	Enabled                            *bool
	APIToken                           *string
}

// IsEmpty reports whether nothing is to be changed.
func (u UpdateInput) IsEmpty() bool {
	return u.Name == nil && u.ZoneID == nil && u.RecordID == nil && u.RecordName == nil && u.RecordType == nil &&
		u.Proxied == nil && u.TTL == nil && u.IntervalSeconds == nil && u.Enabled == nil && u.APIToken == nil
}

// List returns every configuration ordered by name.
func (s *Service) List(ctx context.Context) ([]Config, error) { return s.repo.List(ctx) }

// Get returns one configuration (ErrNotFound).
func (s *Service) Get(ctx context.Context, id string) (Config, error) {
	st, err := s.repo.Get(ctx, id)
	return st.Config, err
}

// Create validates, optionally verifies the record with Cloudflare (see verifyTarget), seals the
// token and stores the configuration.
func (s *Service) Create(ctx context.Context, in CreateInput) (Config, error) {
	now := s.now()
	c := Config{
		ID: uuid7(), Name: strings.TrimSpace(in.Name), ZoneID: strings.TrimSpace(in.ZoneID),
		RecordID: strings.TrimSpace(in.RecordID), RecordName: normalizeName(in.RecordName), RecordType: in.RecordType,
		Proxied: deref(in.Proxied, false), TTL: deref(in.TTL, 1), Enabled: deref(in.Enabled, true),
		Interval: time.Duration(deref(in.IntervalSeconds, int(DefaultInterval/time.Second))) * time.Second,
		HasToken: true, CreatedAt: now, UpdatedAt: now,
	}
	token, err := cleanToken(in.APIToken)
	if err != nil {
		return Config{}, err
	}
	if err := validateConfig(&c); err != nil {
		return Config{}, err
	}
	if err := s.verifyTarget(ctx, token, c); err != nil {
		return Config{}, err
	}
	sealed, err := s.sealer.Seal([]byte(token), TokenAAD)
	if err != nil {
		return Config{}, fmt.Errorf("seal token: %w", err)
	}
	if err := s.repo.Create(ctx, Stored{Config: c, TokenSealed: sealed}); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Update applies a partial update. Rules (REQ-DDNS-005):
//   - changing zone_id, record_name or record_type requires record_id in the same request (400):
//     the record is always one the user picked from the lookup, never derived from a name;
//   - a new token or a changed target is verified with Cloudflare (verifyTarget);
//   - a changed target clears the observed state of the old record.
func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (Config, error) {
	st, err := s.repo.Get(ctx, id)
	if err != nil {
		return Config{}, err
	}
	if in.IsEmpty() {
		return st.Config, nil
	}
	old := st.Config
	c := old
	if in.Name != nil {
		c.Name = strings.TrimSpace(*in.Name)
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if in.Proxied != nil {
		c.Proxied = *in.Proxied
	}
	if in.TTL != nil {
		c.TTL = *in.TTL
	}
	if in.IntervalSeconds != nil {
		c.Interval = time.Duration(*in.IntervalSeconds) * time.Second
	}
	if in.ZoneID != nil {
		c.ZoneID = strings.TrimSpace(*in.ZoneID)
	}
	if in.RecordID != nil {
		c.RecordID = strings.TrimSpace(*in.RecordID)
	}
	if in.RecordName != nil {
		c.RecordName = normalizeName(*in.RecordName)
	}
	if in.RecordType != nil {
		c.RecordType = *in.RecordType
	}
	if (c.ZoneID != old.ZoneID || c.RecordName != old.RecordName || c.RecordType != old.RecordType) && in.RecordID == nil {
		return Config{}, invalid("record_id is required when zone_id, record_name or record_type changes: choose the record from the Cloudflare lookup")
	}
	if err := validateConfig(&c); err != nil {
		return Config{}, err
	}

	targetChanged := c.ZoneID != old.ZoneID || c.RecordID != old.RecordID || c.RecordName != old.RecordName || c.RecordType != old.RecordType
	token, sealed := "", st.TokenSealed
	if in.APIToken != nil {
		if token, err = cleanToken(*in.APIToken); err != nil {
			return Config{}, err
		}
		if sealed, err = s.sealer.Seal([]byte(token), TokenAAD); err != nil {
			return Config{}, fmt.Errorf("seal token: %w", err)
		}
	}
	if in.APIToken != nil || targetChanged {
		if token == "" {
			if token, err = s.openToken(st.TokenSealed); err != nil {
				return Config{}, err
			}
		}
		if err := s.verifyTarget(ctx, token, c); err != nil {
			return Config{}, err
		}
	}

	// updated_at is the version a racing sync is checked against, so it must always move.
	c.UpdatedAt = s.now()
	if !c.UpdatedAt.After(old.UpdatedAt) {
		c.UpdatedAt = old.UpdatedAt.Add(time.Millisecond)
	}
	c.HasToken = true
	if err := s.repo.Update(ctx, Stored{Config: c, TokenSealed: sealed}, targetChanged); err != nil {
		return Config{}, err
	}
	return s.Get(ctx, id)
}

// Delete removes a configuration and, by cascade, its history.
func (s *Service) Delete(ctx context.Context, id string) error { return s.repo.Delete(ctx, id) }

func validateConfig(c *Config) error {
	switch {
	case c.Name == "" || len([]rune(c.Name)) > maxNameLen:
		return invalid("name: must be 1 to %d characters", maxNameLen)
	case !ValidID(c.ZoneID):
		return invalid("zone_id: must be a Cloudflare zone id chosen from the zone lookup")
	case !ValidID(c.RecordID) || len(c.RecordID) > maxIDLen:
		return invalid("record_id: must be a Cloudflare record id chosen from the record lookup")
	case c.RecordName == "" || len(c.RecordName) > maxRecordName:
		return invalid("record_name: must be 1 to %d characters", maxRecordName)
	case !c.RecordType.Valid():
		return invalid("record_type: must be A or AAAA")
	case c.TTL < 1 || c.TTL > MaxTTL:
		return invalid("ttl: must be between 1 and %d (1 = automatic)", MaxTTL)
	case c.Interval < MinInterval || c.Interval > MaxInterval:
		return invalid("interval_seconds: must be between %d and %d", int(MinInterval.Seconds()), int(MaxInterval.Seconds()))
	}
	if c.Proxied {
		c.TTL = 1 // Cloudflare ignores the ttl of a proxied record; storing it would show a lie
	}
	return nil
}

// cleanToken trims the pasted token and rejects values that cannot be an API token or would be
// refused as an HTTP header value.
func cleanToken(raw string) (string, error) {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "", invalid("api_token: required")
	}
	if len(t) > maxTokenLen {
		return "", invalid("api_token: too long")
	}
	for _, r := range t {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r > unicode.MaxASCII {
			return "", invalid("api_token: contains characters a Cloudflare token cannot have")
		}
	}
	return t, nil
}

// normalizeName lower-cases a record name and drops a trailing dot, the form Cloudflare returns.
func normalizeName(n string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(n), "."))
}

func deref[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

func uuid7() string {
	id, err := uuid.NewV7()
	if err != nil { // entropy failure: not recoverable here
		panic(fmt.Sprintf("ddns: uuid: %v", err))
	}
	return id.String()
}

func (s *Service) openToken(sealed []byte) (string, error) {
	b, err := s.sealer.Open(sealed, TokenAAD)
	if err != nil {
		return "", fmt.Errorf("open stored DDNS token: %w", err)
	}
	return string(b), nil
}

// verifyTarget asks Cloudflare whether the record exists in the zone and has the configured type
// and name, so a wrong token or a stale record id is reported when the form is saved and not by the
// first sync. It rejects (400) only what Cloudflare definitely refuses: token rejected, record not
// found or not visible, type or name mismatch. When Cloudflare cannot answer (network failure,
// 5xx, rate limit) the configuration is saved anyway and the first sync reports the problem, so an
// outage of Cloudflare does not block editing local settings.
func (s *Service) verifyTarget(ctx context.Context, token string, c Config) error {
	rec, err := s.dns.GetRecord(ctx, token, c.ZoneID, c.RecordID)
	if err != nil {
		var cf *CloudflareError
		switch {
		case errors.As(err, &cf) && cf.AuthFailure():
			return invalid("Cloudflare rejected the API token: %s", cf.Message)
		case errors.As(err, &cf) && cf.NotFound():
			return invalid("Cloudflare does not have this record in this zone, or the token cannot see it: %s", cf.Message)
		case errors.As(err, &cf) && !cf.Transient():
			return invalid("Cloudflare refused the record lookup: %s", cf.Message)
		case ctx.Err() != nil:
			return ctx.Err()
		}
		s.log.Warn("ddns: could not verify the record with Cloudflare, saving anyway", "zone_id", c.ZoneID, "err", err)
		return nil
	}
	if rec.Type != c.RecordType || normalizeName(rec.Name) != c.RecordName {
		return invalid("the Cloudflare record %s (%s) is not %s (%s): choose the record again", rec.Name, rec.Type, c.RecordName, c.RecordType)
	}
	return nil
}

// ---- Cloudflare lookups ---------------------------------------------------------------------------

// Credentials selects the token of a lookup: exactly one of Token (typed into the form, not stored)
// or ConfigID (the sealed token of an existing configuration).
type Credentials struct {
	Token    string
	ConfigID string
}

func (s *Service) lookupToken(ctx context.Context, cr Credentials) (string, error) {
	switch {
	case cr.Token != "" && cr.ConfigID != "":
		return "", invalid("give either api_token or config_id, not both")
	case cr.Token == "" && cr.ConfigID == "":
		return "", invalid("give api_token or config_id")
	case cr.ConfigID != "":
		st, err := s.repo.Get(ctx, cr.ConfigID)
		if err != nil {
			return "", err
		}
		return s.openToken(st.TokenSealed)
	}
	return cleanToken(cr.Token)
}

// LookupZones lists the zones the credentials can see, ordered by name. Nothing is stored. A failed
// Cloudflare call is a *CloudflareError.
func (s *Service) LookupZones(ctx context.Context, cr Credentials) ([]Zone, error) {
	token, err := s.lookupToken(ctx, cr)
	if err != nil {
		return nil, err
	}
	return s.dns.ListZones(ctx, token)
}

// LookupRecords lists the A and AAAA records of a zone, ordered by name.
func (s *Service) LookupRecords(ctx context.Context, zoneID string, cr Credentials) ([]Record, error) {
	token, err := s.lookupToken(ctx, cr)
	if err != nil {
		return nil, err
	}
	if !ValidID(zoneID) {
		return nil, invalid("zone_id: not a Cloudflare zone id")
	}
	return s.dns.ListRecords(ctx, token, zoneID)
}

// ---- sync -----------------------------------------------------------------------------------------

// SyncResult is the outcome of one sync of one configuration.
type SyncResult struct {
	ConfigID string
	Result   Result
	IP       string // detected public address, when discovery succeeded
	Error    string // failed only
	At       time.Time
	// Cause is the underlying error of a failed sync, for IsTransient and logs. Not shown to users.
	Cause error
}

// Sync runs the sync of one configuration now: discover the public address, read the record by id,
// verify it is still the configured type and name, update it only if it differs, and persist the
// outcome and a history row. It is serialised per configuration with the periodic Syncer. A failed
// sync is a SyncResult with Result failed, not an error; the error is for ErrNotFound, a cancelled
// context and storage failures.
func (s *Service) Sync(ctx context.Context, id string) (SyncResult, error) {
	unlock := s.locks.lock(id)
	defer unlock()

	st, err := s.repo.Get(ctx, id)
	if err != nil {
		return SyncResult{}, err
	}
	cfg := st.Config
	out := Outcome{At: s.now()}
	token, cause := s.openToken(st.TokenSealed)
	if cause == nil {
		cause = s.syncRecord(ctx, token, cfg, &out)
	}
	if cause != nil {
		if ctx.Err() != nil {
			return SyncResult{}, ctx.Err() // cancelled (shutdown or client gone): not a sync result
		}
		out.Result = ResultFailed
		out.Error = truncate(redact(cause.Error(), token), 500)
	}

	// The record may have been updated at Cloudflare even if the request was cancelled meanwhile:
	// save regardless of ctx.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	applied, err := s.repo.SaveOutcome(saveCtx, id, cfg.UpdatedAt, out)
	if err != nil {
		return SyncResult{}, err
	}
	if !applied {
		s.log.Info("ddns: configuration changed during a sync, result not stored", "config_id", id)
	}
	return SyncResult{ConfigID: id, Result: out.Result, IP: out.IP, Error: out.Error, At: out.At, Cause: cause}, nil
}

// syncRecord does the work of one sync and fills out; a nil error sets out.Result.
func (s *Service) syncRecord(ctx context.Context, token string, cfg Config, out *Outcome) error {
	if cfg.RecordID == "" {
		return &permanentError{errors.New("no Cloudflare record is selected for this configuration: edit it and choose the record")}
	}
	ip, err := s.ip.PublicIP(ctx, cfg.RecordType)
	if err != nil {
		return err
	}
	out.IP = ip.String()

	rec, err := s.dns.GetRecord(ctx, token, cfg.ZoneID, cfg.RecordID)
	if err != nil {
		return err
	}
	out.DNSValue = rec.Content
	// The record is addressed by id; make sure it is still the one the user chose. A mismatch
	// (record replaced, retyped or renamed in Cloudflare) is never "fixed" by writing to it.
	if rec.Type != cfg.RecordType || normalizeName(rec.Name) != cfg.RecordName {
		return &permanentError{fmt.Errorf("%w: found %s (%s), expected %s (%s)", ErrRecordMismatch,
			rec.Name, rec.Type, cfg.RecordName, cfg.RecordType)}
	}

	want := RecordUpdate{Content: ip, Proxied: cfg.Proxied, TTL: cfg.TTL}
	if cur, ok := parseContent(rec.Content); ok && cur == ip && rec.Proxied == want.Proxied && rec.TTL == want.TTL {
		out.Result = ResultUnchanged
		return nil
	}
	updated, err := s.dns.UpdateRecord(ctx, token, cfg.ZoneID, cfg.RecordID, want)
	if err != nil {
		return err
	}
	out.Result = ResultUpdated
	out.DNSValue = updated.Content
	return nil
}

// keyedMutex serialises work per key and forgets idle keys.
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*keyLock
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

func (k *keyedMutex) lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*keyLock{}
	}
	l := k.m[key]
	if l == nil {
		l = &keyLock{}
		k.m[key] = l
	}
	l.refs++
	k.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		k.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}

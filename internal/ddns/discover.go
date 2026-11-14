package ddns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Discovery limits and defaults.
const (
	// DefaultProviderTimeout bounds each provider request (connect, TLS and body).
	DefaultProviderTimeout = 5 * time.Second
	// DefaultCacheTTL is how long a successful discovery is shared by the configurations of one cycle.
	DefaultCacheTTL = 10 * time.Second
	// MinAgreeing is the quorum: this many providers must return the same address, and no other
	// address may have as many answers.
	MinAgreeing = 2

	maxProviderBody = 128 // a bare address is at most 45 bytes; anything longer is not one
	maxRedirects    = 3
)

// DiscoveryConfig configures a Discoverer.
type DiscoveryConfig struct {
	// Providers are URLs that answer a plain GET with the caller's public address as bare text.
	// At least two, on different hosts. The providers learn this Controller's public address;
	// that is inherent to asking "what is my IP".
	Providers []string
	// AllowHTTP accepts http:// providers (development and tests only).
	AllowHTTP bool
	// Timeout per provider request; 0 = DefaultProviderTimeout.
	Timeout time.Duration
	// CacheTTL of a successful result per family; 0 = DefaultCacheTTL, negative disables caching.
	CacheTTL time.Duration
	// Now is the clock for the cache; nil = time.Now.
	Now func() time.Time
	// Transport replaces the family-pinned HTTP transports (tests). With it the requested family
	// is no longer forced at the socket, only checked on the answer.
	Transport http.RoundTripper
}

// Discoverer finds the public address by asking several independent providers and requiring them
// to agree. It never returns an address from a single source.
type Discoverer struct {
	providers []provider
	timeout   time.Duration
	cacheTTL  time.Duration
	now       func() time.Time
	clients   map[RecordType]*http.Client

	mu       sync.Mutex
	cache    map[RecordType]cacheEntry
	inflight map[RecordType]*flight
}

type provider struct {
	url  string
	host string // the only part of a provider that is ever logged or returned: URLs may carry keys
}

type cacheEntry struct {
	addr netip.Addr
	at   time.Time
}

type flight struct {
	done chan struct{}
	addr netip.Addr
	err  error
}

var _ IPDiscoverer = (*Discoverer)(nil)

// NewDiscoverer validates cfg and returns a Discoverer.
func NewDiscoverer(cfg DiscoveryConfig) (*Discoverer, error) {
	d := &Discoverer{
		timeout: cfg.Timeout, cacheTTL: cfg.CacheTTL, now: cfg.Now,
		cache: map[RecordType]cacheEntry{}, inflight: map[RecordType]*flight{},
	}
	if d.timeout <= 0 {
		d.timeout = DefaultProviderTimeout
	}
	if d.cacheTTL == 0 {
		d.cacheTTL = DefaultCacheTTL
	}
	if d.now == nil {
		d.now = time.Now
	}
	if len(cfg.Providers) < MinAgreeing {
		return nil, fmt.Errorf("ddns: at least %d public-IP providers are required, got %d", MinAgreeing, len(cfg.Providers))
	}
	hosts := map[string]bool{}
	for _, raw := range cfg.Providers {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil {
			return nil, fmt.Errorf("ddns: invalid public-IP provider URL (host %q)", hostOf(raw))
		}
		if u.Scheme != "https" && !(cfg.AllowHTTP && u.Scheme == "http") {
			return nil, fmt.Errorf("ddns: public-IP provider %q must use https", u.Hostname())
		}
		h := strings.ToLower(u.Hostname())
		if hosts[h] {
			return nil, fmt.Errorf("ddns: public-IP providers must be on different hosts (%q twice)", h)
		}
		hosts[h] = true
		d.providers = append(d.providers, provider{url: raw, host: h})
	}
	d.clients = map[RecordType]*http.Client{
		RecordA:    d.newClient(RecordA, cfg.Transport),
		RecordAAAA: d.newClient(RecordAAAA, cfg.Transport),
	}
	return d, nil
}

// hostOf extracts a host for an error message from a URL that failed to parse.
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Hostname()
	}
	return ""
}

func (d *Discoverer) newClient(family RecordType, rt http.RoundTripper) *http.Client {
	if rt == nil {
		network := "tcp4"
		if family == RecordAAAA {
			network = "tcp6"
		}
		dialer := &net.Dialer{Timeout: d.timeout}
		rt = &http.Transport{
			Proxy: nil, // a proxy would make the providers report the proxy's address
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			},
			TLSHandshakeTimeout:   d.timeout,
			ResponseHeaderTimeout: d.timeout,
			DisableKeepAlives:     true,
		}
	}
	return &http.Client{Transport: rt, CheckRedirect: sameHostRedirect}
}

// sameHostRedirect follows a few redirects within one host and never downgrades to http: a provider
// must not be able to bounce the Controller to another server.
func sameHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return errors.New("too many redirects")
	}
	first := via[0]
	if !strings.EqualFold(req.URL.Hostname(), first.URL.Hostname()) {
		return errors.New("redirect to another host refused")
	}
	if first.URL.Scheme == "https" && req.URL.Scheme != "https" {
		return errors.New("redirect to http refused")
	}
	return nil
}

// PublicIP returns the public address of the requested family. Successful results are cached for
// CacheTTL and concurrent callers share one discovery, so several configurations in one sync cycle
// ask the providers once per family.
func (d *Discoverer) PublicIP(ctx context.Context, family RecordType) (netip.Addr, error) {
	if !family.Valid() {
		return netip.Addr{}, fmt.Errorf("ddns: unsupported record type %q", family)
	}
	for {
		d.mu.Lock()
		if e, ok := d.cache[family]; ok && d.cacheTTL > 0 && d.now().Sub(e.at) < d.cacheTTL {
			d.mu.Unlock()
			return e.addr, nil
		}
		if f, ok := d.inflight[family]; ok {
			d.mu.Unlock()
			select {
			case <-f.done:
			case <-ctx.Done():
				return netip.Addr{}, ctx.Err()
			}
			// The leader's own cancellation is not this caller's failure: look again.
			if f.err != nil && (errors.Is(f.err, context.Canceled) || errors.Is(f.err, context.DeadlineExceeded)) && ctx.Err() == nil {
				continue
			}
			return f.addr, f.err
		}
		f := &flight{done: make(chan struct{})}
		d.inflight[family] = f
		d.mu.Unlock()

		f.addr, f.err = d.discover(ctx, family)

		d.mu.Lock()
		delete(d.inflight, family)
		if f.err == nil {
			d.cache[family] = cacheEntry{addr: f.addr, at: d.now()}
		}
		d.mu.Unlock()
		close(f.done)
		return f.addr, f.err
	}
}

// answer is one provider's outcome.
type answer struct {
	host string
	addr netip.Addr
	err  error
}

func (d *Discoverer) discover(ctx context.Context, family RecordType) (netip.Addr, error) {
	answers := make([]answer, len(d.providers))
	var wg sync.WaitGroup
	for i, p := range d.providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr, err := d.ask(ctx, d.clients[family], p, family)
			answers[i] = answer{host: p.host, addr: addr, err: err}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return netip.Addr{}, err
	}
	return decide(family, answers)
}

// ask queries one provider and validates its answer.
func (d *Discoverer) ask(ctx context.Context, client *http.Client, p provider, family RecordType) (netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, http.NoBody)
	if err != nil {
		return netip.Addr{}, errors.New("invalid request")
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", "homecore-ddns")
	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, errors.New(describeTransportError(ctx, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderBody+1))
	if err != nil {
		return netip.Addr{}, errors.New(describeTransportError(ctx, err))
	}
	if len(body) > maxProviderBody {
		return netip.Addr{}, errors.New("response too large")
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(string(body)))
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, errors.New("answer is not an IP address")
	}
	addr = addr.Unmap()
	if err := checkFamilyAndRange(addr, family); err != nil {
		return netip.Addr{}, err
	}
	return addr, nil
}

// describeTransportError gives a short reason without the request URL, which net/http would
// include and which may carry an API key.
func describeTransportError(ctx context.Context, err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), ctx.Err() != nil, errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case strings.Contains(err.Error(), "redirect"):
		return "redirect refused"
	}
	return "connection failed"
}

// decide applies the quorum rule to the providers' answers: the address returned by the most
// providers wins, provided at least MinAgreeing of them returned it and no other address ties it.
// Pure function (table-tested).
func decide(family RecordType, answers []answer) (netip.Addr, error) {
	votes := map[netip.Addr]int{}
	answered := 0
	for _, a := range answers {
		if a.err == nil {
			votes[a.addr]++
			answered++
		}
	}
	var best netip.Addr
	bestN, second := 0, 0
	for addr, n := range votes {
		switch {
		case n > bestN:
			best, second, bestN = addr, bestN, n
		case n > second:
			second = n
		}
	}
	if bestN >= MinAgreeing && bestN > second {
		return best, nil
	}
	return netip.Addr{}, &DiscoveryError{Family: family, Answered: answered, Total: len(answers), Detail: summarize(answers)}
}

func summarize(answers []answer) string {
	parts := make([]string, len(answers))
	for i, a := range answers {
		if a.err != nil {
			parts[i] = a.host + ": " + a.err.Error()
		} else {
			parts[i] = a.host + ": " + a.addr.String()
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// DiscoveryError: the providers did not produce an address that enough of them agree on. It is
// transient (providers recover), so the syncer retries with backoff.
type DiscoveryError struct {
	Family   RecordType
	Answered int
	Total    int
	Detail   string
}

func (e *DiscoveryError) Error() string {
	ip := "IPv4"
	if e.Family == RecordAAAA {
		ip = "IPv6"
	}
	return fmt.Sprintf("public %s address not confirmed: %d of %d providers gave a usable answer and at least %d must agree (%s)",
		ip, e.Answered, e.Total, MinAgreeing, e.Detail)
}

// nonPublic lists ranges that are never a valid public DDNS target and that net/netip does not
// classify itself (it covers private, loopback, link-local, multicast and unspecified).
var nonPublic = mustPrefixes(
	"0.0.0.0/8",       // "this network"
	"100.64.0.0/10",   // carrier-grade NAT (RFC 6598)
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"240.0.0.0/4",     // reserved, includes the broadcast address
	"2001:db8::/32",   // documentation
	"2001::/32",       // Teredo
	"64:ff9b::/96",    // NAT64
	"100::/64",        // discard-only
	"2002::/16",       // 6to4
)

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// checkFamilyAndRange requires an A record to get an IPv4 and an AAAA record an IPv6 address, both
// publicly routable. An IPv4-mapped IPv6 address is unmapped by the caller first, so it counts as
// IPv4 (and fails for AAAA).
func checkFamilyAndRange(addr netip.Addr, family RecordType) error {
	switch {
	case family == RecordA && !addr.Is4():
		return errors.New("not an IPv4 address")
	case family == RecordAAAA && !addr.Is6():
		return errors.New("not an IPv6 address")
	}
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		addr.IsMulticast() || addr.IsUnspecified() {
		return errors.New("not a public address")
	}
	for _, p := range nonPublic {
		if p.Contains(addr) {
			return errors.New("not a public address")
		}
	}
	return nil
}

package ddns

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func ans(host, ip string) answer {
	if ip == "" {
		return answer{host: host, err: errors.New("timeout")}
	}
	return answer{host: host, addr: netip.MustParseAddr(ip)}
}

// REQ-DDNS-001: at least two providers must agree, and no other address may tie them; never one source.
func TestQuorumDecision(t *testing.T) {
	cases := []struct {
		name    string
		answers []answer
		want    string // "" = must fail
	}{
		{"two of two agree", []answer{ans("a", "8.8.8.8"), ans("b", "8.8.8.8")}, "8.8.8.8"},
		{"two of two disagree", []answer{ans("a", "8.8.8.8"), ans("b", "1.1.1.1")}, ""},
		{"a single answer is never enough", []answer{ans("a", "8.8.8.8"), ans("b", "")}, ""},
		{"nobody answers", []answer{ans("a", ""), ans("b", "")}, ""},
		{"three agree", []answer{ans("a", "8.8.8.8"), ans("b", "8.8.8.8"), ans("c", "8.8.8.8")}, "8.8.8.8"},
		{"two agree, one outlier", []answer{ans("a", "8.8.8.8"), ans("b", "8.8.8.8"), ans("c", "1.1.1.1")}, "8.8.8.8"},
		{"two agree, one down", []answer{ans("a", "8.8.8.8"), ans("b", ""), ans("c", "8.8.8.8")}, "8.8.8.8"},
		{"three all different", []answer{ans("a", "8.8.8.8"), ans("b", "1.1.1.1"), ans("c", "9.9.9.9")}, ""},
		{"one answer of three", []answer{ans("a", ""), ans("b", "8.8.8.8"), ans("c", "")}, ""},
		{"two against two is a tie", []answer{ans("a", "8.8.8.8"), ans("b", "8.8.8.8"), ans("c", "1.1.1.1"), ans("d", "1.1.1.1")}, ""},
		{"three against one", []answer{ans("a", "8.8.8.8"), ans("b", "8.8.8.8"), ans("c", "8.8.8.8"), ans("d", "1.1.1.1")}, "8.8.8.8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decide(RecordA, tc.answers)
			if tc.want == "" {
				var de *DiscoveryError
				if err == nil || !errors.As(err, &de) || !IsTransient(err) {
					t.Fatalf("got %v, %v; want a transient DiscoveryError", got, err)
				}
				return
			}
			if err != nil || got.String() != tc.want {
				t.Fatalf("got %v, %v; want %s", got, err, tc.want)
			}
		})
	}
}

// REQ-DDNS-001: the family is checked and only public addresses are accepted.
func TestFamilyAndRangeChecks(t *testing.T) {
	ok4 := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "100.63.255.255", "100.128.0.1", "198.20.0.1"}
	bad4 := []string{"10.1.2.3", "172.16.0.1", "192.168.1.1", "127.0.0.1", "169.254.1.1", "100.64.0.1", "100.127.255.254",
		"0.0.0.0", "0.1.2.3", "224.0.0.1", "255.255.255.255", "240.0.0.1", "192.0.2.1", "198.51.100.7", "203.0.113.9",
		"198.18.0.1", "192.0.0.8"}
	ok6 := []string{"2606:4700:4700::1111", "2001:4860:4860::8888"}
	bad6 := []string{"::1", "::", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1", "2001:db8::1", "64:ff9b::808:808", "2002:808:808::1"}
	for _, s := range ok4 {
		if err := checkFamilyAndRange(netip.MustParseAddr(s), RecordA); err != nil {
			t.Errorf("A %s rejected: %v", s, err)
		}
		if err := checkFamilyAndRange(netip.MustParseAddr(s), RecordAAAA); err == nil {
			t.Errorf("AAAA accepted IPv4 %s", s)
		}
	}
	for _, s := range bad4 {
		if err := checkFamilyAndRange(netip.MustParseAddr(s), RecordA); err == nil {
			t.Errorf("A accepted %s", s)
		}
	}
	for _, s := range ok6 {
		if err := checkFamilyAndRange(netip.MustParseAddr(s), RecordAAAA); err != nil {
			t.Errorf("AAAA %s rejected: %v", s, err)
		}
		if err := checkFamilyAndRange(netip.MustParseAddr(s), RecordA); err == nil {
			t.Errorf("A accepted IPv6 %s", s)
		}
	}
	for _, s := range bad6 {
		if err := checkFamilyAndRange(netip.MustParseAddr(s), RecordAAAA); err == nil {
			t.Errorf("AAAA accepted %s", s)
		}
	}
}

// fakeNet answers provider requests by host without sockets.
type fakeNet struct {
	mu    sync.Mutex
	calls map[string]int
	reply map[string]func() (int, string)
	delay time.Duration
}

func (f *fakeNet) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[req.URL.Host]++
	fn := f.reply[req.URL.Host]
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	if fn == nil {
		return nil, errors.New("dial tcp: connection refused (" + req.URL.String() + ")")
	}
	code, body := fn()
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
}

func (f *fakeNet) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

func reply(body string) func() (int, string) { return func() (int, string) { return 200, body } }

func newDisc(t *testing.T, f *fakeNet, hosts ...string) (*Discoverer, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	var urls []string
	for _, h := range hosts {
		urls = append(urls, "https://"+h+"/ip?key=SECRET-KEY")
	}
	d, err := NewDiscoverer(DiscoveryConfig{Providers: urls, Transport: f, Now: func() time.Time { return now }, CacheTTL: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return d, &now
}

func TestDiscovererAgreementAndValidation(t *testing.T) {
	ctx := context.Background()
	t.Run("agree on IPv4 with whitespace and a trailing newline", func(t *testing.T) {
		f := &fakeNet{reply: map[string]func() (int, string){"a.example": reply("8.8.8.8\n"), "b.example": reply(" 8.8.8.8 ")}}
		d, _ := newDisc(t, f, "a.example", "b.example")
		ip, err := d.PublicIP(ctx, RecordA)
		if err != nil || ip.String() != "8.8.8.8" {
			t.Fatalf("%v %v", ip, err)
		}
	})
	t.Run("one provider down is not enough", func(t *testing.T) {
		f := &fakeNet{reply: map[string]func() (int, string){"a.example": reply("8.8.8.8")}}
		d, _ := newDisc(t, f, "a.example", "b.example")
		if _, err := d.PublicIP(ctx, RecordA); err == nil {
			t.Fatal("a single source was accepted")
		}
	})
	t.Run("error text has hosts only, never the provider URL or key", func(t *testing.T) {
		f := &fakeNet{reply: map[string]func() (int, string){"a.example": reply("8.8.8.8")}}
		d, _ := newDisc(t, f, "a.example", "b.example")
		_, err := d.PublicIP(ctx, RecordA)
		if err == nil || strings.Contains(err.Error(), "SECRET-KEY") || strings.Contains(err.Error(), "https://") || !strings.Contains(err.Error(), "b.example: connection failed") {
			t.Fatalf("error text: %v", err)
		}
	})
	t.Run("disagreeing providers fail the cycle", func(t *testing.T) {
		f := &fakeNet{reply: map[string]func() (int, string){"a.example": reply("8.8.8.8"), "b.example": reply("1.1.1.1")}}
		d, _ := newDisc(t, f, "a.example", "b.example")
		_, err := d.PublicIP(ctx, RecordA)
		if err == nil || !strings.Contains(err.Error(), "at least 2 must agree") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("an IPv6 answer is not an A record address", func(t *testing.T) {
		f := &fakeNet{reply: map[string]func() (int, string){"a.example": reply("2606:4700:4700::1111"), "b.example": reply("2606:4700:4700::1111")}}
		d, _ := newDisc(t, f, "a.example", "b.example")
		if _, err := d.PublicIP(ctx, RecordA); err == nil || !strings.Contains(err.Error(), "not an IPv4 address") {
			t.Fatalf("got %v", err)
		}
		if ip, err := d.PublicIP(ctx, RecordAAAA); err != nil || ip.String() != "2606:4700:4700::1111" {
			t.Fatalf("AAAA: %v %v", ip, err)
		}
	})
	t.Run("IPv4-mapped IPv6 counts as IPv4", func(t *testing.T) {
		f := &fakeNet{reply: map[string]func() (int, string){"a.example": reply("::ffff:8.8.8.8"), "b.example": reply("8.8.8.8")}}
		d, _ := newDisc(t, f, "a.example", "b.example")
		if ip, err := d.PublicIP(ctx, RecordA); err != nil || ip.String() != "8.8.8.8" {
			t.Fatalf("%v %v", ip, err)
		}
		if _, err := d.PublicIP(ctx, RecordAAAA); err == nil {
			t.Fatal("a mapped IPv4 address was accepted for AAAA")
		}
	})
	for name, body := range map[string]string{
		"private":       "192.168.1.5",
		"cgnat":         "100.64.3.3",
		"loopback":      "127.0.0.1",
		"link local":    "169.254.0.5",
		"html":          "<html>8.8.8.8</html>",
		"empty":         "",
		"zone":          "fe80::1%eth0",
		"two addresses": "8.8.8.8 1.1.1.1",
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			f := &fakeNet{reply: map[string]func() (int, string){"a.example": reply(body), "b.example": reply(body)}}
			d, _ := newDisc(t, f, "a.example", "b.example")
			if ip, err := d.PublicIP(ctx, RecordA); err == nil {
				t.Fatalf("accepted %q as %v", body, ip)
			}
		})
	}
	t.Run("non-200 and oversize answers", func(t *testing.T) {
		f := &fakeNet{reply: map[string]func() (int, string){
			"a.example": func() (int, string) { return 503, "8.8.8.8" },
			"b.example": reply(strings.Repeat("8", 1<<20)),
		}}
		d, _ := newDisc(t, f, "a.example", "b.example")
		_, err := d.PublicIP(ctx, RecordA)
		if err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "response too large") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("unsupported family", func(t *testing.T) {
		d, _ := newDisc(t, &fakeNet{}, "a.example", "b.example")
		if _, err := d.PublicIP(ctx, "CNAME"); err == nil {
			t.Fatal("accepted CNAME")
		}
	})
}

// REQ-DDNS-001: several configurations of one cycle share one discovery per family.
func TestDiscovererCachesAndSharesInflightRequests(t *testing.T) {
	ctx := context.Background()
	f := &fakeNet{reply: map[string]func() (int, string){"a.example": reply("8.8.8.8"), "b.example": reply("8.8.8.8")}, delay: 30 * time.Millisecond}
	d, now := newDisc(t, f, "a.example", "b.example")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ip, err := d.PublicIP(ctx, RecordA); err != nil || ip.String() != "8.8.8.8" {
				t.Errorf("%v %v", ip, err)
			}
		}()
	}
	wg.Wait()
	if n := f.total(); n != 2 {
		t.Fatalf("8 concurrent callers made %d provider requests, want 2 (one per provider)", n)
	}
	if _, err := d.PublicIP(ctx, RecordA); err != nil || f.total() != 2 {
		t.Fatalf("a cached answer re-asked the providers: %d", f.total())
	}
	if _, err := d.PublicIP(ctx, RecordAAAA); err == nil { // other family: own discovery (and 8.8.8.8 is not IPv6)
		t.Fatal("AAAA must not reuse the IPv4 answer")
	}
	if f.total() != 4 {
		t.Fatalf("AAAA discovery made %d requests in total", f.total())
	}
	*now = now.Add(11 * time.Second)
	if _, err := d.PublicIP(ctx, RecordA); err != nil || f.total() != 6 {
		t.Fatalf("after the TTL the providers must be asked again: %d", f.total())
	}
}

func TestDiscovererDoesNotCacheFailures(t *testing.T) {
	ctx := context.Background()
	var healthy atomic.Bool
	f := &fakeNet{reply: map[string]func() (int, string){
		"a.example": func() (int, string) {
			if healthy.Load() {
				return 200, "8.8.8.8"
			}
			return 500, ""
		},
		"b.example": reply("8.8.8.8"),
	}}
	d, _ := newDisc(t, f, "a.example", "b.example")
	if _, err := d.PublicIP(ctx, RecordA); err == nil {
		t.Fatal("expected failure")
	}
	healthy.Store(true)
	if ip, err := d.PublicIP(ctx, RecordA); err != nil || ip.String() != "8.8.8.8" {
		t.Fatalf("recovery: %v %v", ip, err)
	}
}

func TestDiscovererCancelledCallerDoesNotPoisonOthers(t *testing.T) {
	f := &fakeNet{reply: map[string]func() (int, string){"a.example": reply("8.8.8.8"), "b.example": reply("8.8.8.8")}, delay: 100 * time.Millisecond}
	d, _ := newDisc(t, f, "a.example", "b.example")
	leaderCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := d.PublicIP(leaderCtx, RecordA); done <- err }()
	time.Sleep(20 * time.Millisecond)
	waiter := make(chan error, 1)
	go func() { _, err := d.PublicIP(context.Background(), RecordA); waiter <- err }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("the cancelled leader returned no error")
	}
	if err := <-waiter; err != nil {
		t.Fatalf("a caller with a live context failed because the leader was cancelled: %v", err)
	}
}

func TestDiscovererTimesOutSlowProviders(t *testing.T) {
	f := &fakeNet{reply: map[string]func() (int, string){"a.example": reply("8.8.8.8"), "b.example": reply("8.8.8.8")}, delay: 5 * time.Second}
	d, err := NewDiscoverer(DiscoveryConfig{Providers: []string{"https://a.example", "https://b.example"}, Transport: f, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = d.PublicIP(context.Background(), RecordA)
	if err == nil || !strings.Contains(err.Error(), "timeout") || time.Since(start) > 2*time.Second {
		t.Fatalf("got %v after %v", err, time.Since(start))
	}
}

func TestNewDiscovererValidatesProviders(t *testing.T) {
	cases := map[string]struct {
		urls  []string
		allow bool
	}{
		"none":        {nil, false},
		"one":         {[]string{"https://a.example"}, false},
		"http":        {[]string{"http://a.example", "https://b.example"}, false},
		"same host":   {[]string{"https://a.example/x", "https://A.example/y"}, false},
		"credentials": {[]string{"https://u:p@a.example", "https://b.example"}, false},
		"no host":     {[]string{"https://", "https://b.example"}, false},
		"ftp":         {[]string{"ftp://a.example", "https://b.example"}, true},
	}
	for name, tc := range cases {
		if _, err := NewDiscoverer(DiscoveryConfig{Providers: tc.urls, AllowHTTP: tc.allow}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := NewDiscoverer(DiscoveryConfig{Providers: []string{"http://127.0.0.1:1", "http://127.0.0.2:1"}, AllowHTTP: true}); err != nil {
		t.Errorf("explicit http: %v", err)
	}
}

// REQ-DDNS-001 over real sockets: redirects stay on the provider's host, the body is bounded.
func TestDiscovererOverRealHTTP(t *testing.T) {
	ctx := context.Background()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "8.8.8.8") }))
	defer other.Close()
	var hits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); _, _ = io.WriteString(w, "8.8.8.8\n") })
	mux.HandleFunc("/same-host", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusFound) })
	mux.HandleFunc("/other-host", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) })
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	mux.HandleFunc("/huge", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, strings.Repeat("1", 1<<20)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	// A second host name for the same listener: "localhost" vs "127.0.0.1".
	altURL := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	try := func(path string) (netip.Addr, error) {
		d, err := NewDiscoverer(DiscoveryConfig{Providers: []string{srv.URL + path, altURL + path}, AllowHTTP: true, CacheTTL: -1})
		if err != nil {
			t.Fatal(err)
		}
		return d.PublicIP(ctx, RecordA)
	}
	if ip, err := try("/ok"); err != nil || ip.String() != "8.8.8.8" {
		t.Fatalf("plain: %v %v", ip, err)
	}
	if ip, err := try("/same-host"); err != nil || ip.String() != "8.8.8.8" {
		t.Fatalf("same-host redirect: %v %v", ip, err)
	}
	if _, err := try("/other-host"); err == nil || !strings.Contains(err.Error(), "redirect refused") {
		t.Fatalf("a redirect to another host was followed: %v", err)
	}
	if _, err := try("/loop"); err == nil {
		t.Fatal("a redirect loop was followed")
	}
	if _, err := try("/huge"); err == nil || !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("an unbounded body was accepted: %v", err)
	}
}

package portforward

import (
	"strings"
	"testing"
)

func TestCleanListenAddress(t *testing.T) {
	ok := map[string]string{
		"":                "0.0.0.0",
		" 0.0.0.0 ":       "0.0.0.0",
		"192.168.1.10":    "192.168.1.10",
		"::ffff:10.0.0.1": "10.0.0.1",
		"::":              "::",
		"FE80::1":         "fe80::1",
	}
	for in, want := range ok {
		got, err := cleanListenAddress(in)
		if err != nil || got != want {
			t.Errorf("cleanListenAddress(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"localhost", "10.0.0", "300.1.1.1", "fe80::1%eth0", "224.0.0.1", "10.0.0.1:80", "example.com"} {
		if got, err := cleanListenAddress(in); err == nil {
			t.Errorf("cleanListenAddress(%q) = %q, want an error", in, got)
		}
	}
}

func TestCleanTargetHost(t *testing.T) {
	ok := map[string]string{
		"192.168.1.20":    "192.168.1.20",
		"NAS.local":       "nas.local",
		"nas.local.":      "nas.local",
		"my_host-1":       "my_host-1",
		"2001:db8::1":     "2001:db8::1",
		"::ffff:10.0.0.9": "10.0.0.9",
	}
	for in, want := range ok {
		got, err := cleanTargetHost(in)
		if err != nil || got != want {
			t.Errorf("cleanTargetHost(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	long := strings.Repeat("a", 64) + ".com"
	for _, in := range []string{"", "  ", "a b", "http://x", "x/y", "-x.com", "x-.com", "a..b", long, strings.Repeat("a.", 130), "[::1]", "fe80::1%eth0", "x:80"} {
		if got, err := cleanTargetHost(in); err == nil {
			t.Errorf("cleanTargetHost(%q) = %q, want an error", in, got)
		}
	}
}

func TestSelfLoop(t *testing.T) {
	cases := []struct {
		listen, target string
		lp, tp         int
		want           bool
	}{
		{"0.0.0.0", "127.0.0.1", 8080, 8080, true},
		{"0.0.0.0", "localhost", 8080, 8080, true},
		{"0.0.0.0", "0.0.0.0", 8080, 8080, true},
		{"192.168.1.5", "192.168.1.5", 8080, 8080, true},
		{"192.168.1.5", "127.0.0.1", 8080, 8080, false}, // loopback is not covered by a specific address
		{"0.0.0.0", "127.0.0.1", 8080, 9090, false},     // other port
		{"0.0.0.0", "192.168.1.20", 8080, 8080, false},  // another host, same port: the usual case
		{"::", "::1", 22, 22, true},
	}
	for _, c := range cases {
		r := Rule{ListenAddress: c.listen, TargetHost: c.target, ListenPort: c.lp, TargetPort: c.tp}
		if got := selfLoop(r); got != c.want {
			t.Errorf("selfLoop(%s:%d -> %s:%d) = %v, want %v", c.listen, c.lp, c.target, c.tp, got, c.want)
		}
	}
}

func TestOverlaps(t *testing.T) {
	r := func(p Protocol, addr string, port int) Rule {
		return Rule{Protocol: p, ListenAddress: addr, ListenPort: port}
	}
	cases := []struct {
		a, b Rule
		want bool
	}{
		{r(TCP, "0.0.0.0", 80), r(TCP, "0.0.0.0", 80), true},
		{r(TCP, "0.0.0.0", 80), r(UDP, "0.0.0.0", 80), false}, // TCP and UDP ports are separate
		{r(TCP, "0.0.0.0", 80), r(Universal, "0.0.0.0", 80), true},
		{r(UDP, "10.0.0.1", 53), r(Universal, "10.0.0.1", 53), true},
		{r(TCP, "10.0.0.1", 80), r(TCP, "10.0.0.2", 80), false}, // different interfaces
		{r(TCP, "0.0.0.0", 80), r(TCP, "10.0.0.2", 80), true},   // the wildcard covers it
		{r(TCP, "10.0.0.1", 80), r(TCP, "10.0.0.1", 81), false},
	}
	for i, c := range cases {
		if got := overlaps(c.a, c.b); got != c.want || overlaps(c.b, c.a) != c.want {
			t.Errorf("case %d: overlaps = %v, want %v", i, got, c.want)
		}
	}
}

func TestNetworks(t *testing.T) {
	for addr, want := range map[string][2]string{
		"0.0.0.0":     {"tcp4", "udp4"}, // stays IPv4 only, as the user wrote it
		"192.168.1.5": {"tcp4", "udp4"},
		"::":          {"tcp", "udp"}, // dual stack
		"fe80::1":     {"tcp6", "udp6"},
	} {
		tcp, udp := networks(addr)
		if tcp != want[0] || udp != want[1] {
			t.Errorf("networks(%q) = %s, %s; want %v", addr, tcp, udp, want)
		}
	}
}

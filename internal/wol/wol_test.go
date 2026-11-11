package wol

import (
	"bytes"
	"net"
	"testing"
)

func TestMagicPacketLayout(t *testing.T) {
	mac, err := ParseMAC("AA-BB-CC-DD-EE-FF")
	if err != nil {
		t.Fatal(err)
	}
	p := MagicPacket(mac)
	if len(p) != 102 {
		t.Fatalf("len = %d, want 102", len(p))
	}
	if !bytes.Equal(p[:6], bytes.Repeat([]byte{0xFF}, 6)) {
		t.Fatal("header must be 6x 0xFF")
	}
	for i := range 16 {
		if !bytes.Equal(p[6+i*6:12+i*6], mac) {
			t.Fatalf("repetition %d mismatch", i)
		}
	}
}

func TestParseMAC(t *testing.T) {
	if got, err := NormalizeMAC("AA:BB:CC:DD:EE:FF"); err != nil || got != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("got %q %v", got, err)
	}
	for _, bad := range []string{"", "zz:bb:cc:dd:ee:ff", "aa:bb:cc:dd:ee", "00:00:00:00:fe:80:00:00:00:00:00:00:02:00:5e:10"} {
		if _, err := ParseMAC(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestDirectedBroadcast(t *testing.T) {
	_, n, _ := net.ParseCIDR("192.168.1.37/24")
	n.IP = net.ParseIP("192.168.1.37")
	if got := directedBroadcast(n).String(); got != "192.168.1.255" {
		t.Fatalf("got %s", got)
	}
	if got := BroadcastFor("192.168.1.20", "10.0.0.255"); got != "10.0.0.255" {
		t.Fatalf("override ignored: %s", got)
	}
}

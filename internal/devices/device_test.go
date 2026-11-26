package devices

import "testing"

// ProbeHost is always the IP address; the deprecated Hostname never takes over.
func TestProbeHostIgnoresTheDeprecatedHostname(t *testing.T) {
	for _, d := range []Device{
		{IPAddress: "192.168.100.12"},
		{IPAddress: "192.168.100.12", Hostname: "tungpt-mint"},
		{IPAddress: "fd00::1", Hostname: "nas.lan"},
	} {
		if got := d.ProbeHost(); got != d.IPAddress {
			t.Errorf("ProbeHost() = %q, want the IP %q (hostname %q)", got, d.IPAddress, d.Hostname)
		}
	}
}

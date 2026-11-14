package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thanhtunguet/homecore/internal/platform"
)

func TestMetricsLocalAddressesJSON(t *testing.T) {
	m := platform.Metrics{LocalIPs: []string{"10.0.0.2", "10.0.0.2"}, LocalAddresses: []platform.LocalAddress{
		{InterfaceName: "eth0", IPAddress: "10.0.0.2"}, {InterfaceName: "wlan0", IPAddress: "10.0.0.2"},
	}}
	body, err := json.Marshal(toAPIMetrics(m))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		LocalIPs       []string `json:"local_ips"`
		LocalAddresses []struct {
			InterfaceName string `json:"interface_name"`
			IPAddress     string `json:"ip_address"`
		} `json:"local_addresses"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.LocalIPs) != 2 || len(got.LocalAddresses) != 2 || got.LocalAddresses[0].InterfaceName != "eth0" || got.LocalAddresses[1].InterfaceName != "wlan0" || got.LocalAddresses[1].IPAddress != "10.0.0.2" {
		t.Fatalf("bad metrics: %s", body)
	}
	empty, err := json.Marshal(toAPIMetrics(platform.Metrics{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "local_addresses") {
		t.Fatalf("unexpected addresses: %s", empty)
	}
}

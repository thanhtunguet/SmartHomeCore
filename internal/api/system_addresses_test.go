package api

import (
	"context"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/platform"
)

type addressMetrics struct{}

func (addressMetrics) Collect(context.Context) (platform.Metrics, error) {
	return platform.Metrics{CollectedAt: time.Now(), Platform: "linux/amd64", LocalIPs: []string{"192.168.1.2"}, LocalAddresses: []platform.LocalAddress{{InterfaceName: "eth0", IPAddress: "192.168.1.2"}}}, nil
}

func TestSystemLocalAddressesOverHTTP(t *testing.T) {
	e := newDevEnv(t)
	e.metrics.set(addressMetrics{})
	m := decode[gen.SystemMetrics](t, ok(t, e.as("GET", "/api/v1/system/metrics", nil), 200))
	if m.LocalIps == nil || len(*m.LocalIps) != 1 || (*m.LocalIps)[0] != "192.168.1.2" || m.LocalAddresses == nil || len(*m.LocalAddresses) != 1 || (*m.LocalAddresses)[0].InterfaceName != "eth0" || (*m.LocalAddresses)[0].IpAddress != "192.168.1.2" {
		t.Fatalf("unexpected metrics: %+v", m)
	}
}

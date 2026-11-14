package system

import (
	"net"
	"reflect"
	"testing"

	"github.com/thanhtunguet/homecore/internal/platform"
)

func TestInterfaceAddressesPreserveNameAndFilter(t *testing.T) {
	var addrs []net.Addr
	for _, cidr := range []string{"192.168.1.4/24", "2001:db8::1/64", "127.0.0.1/8", "fe80::1/64", "224.0.0.1/4", "0.0.0.0/32"} {
		ip, network, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		network.IP = ip
		addrs = append(addrs, network)
	}
	got := interfaceAddresses("en0", addrs)
	want := []platform.LocalAddress{{InterfaceName: "en0", IPAddress: "192.168.1.4"}, {InterfaceName: "en0", IPAddress: "2001:db8::1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

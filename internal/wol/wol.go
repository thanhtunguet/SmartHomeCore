// Package wol builds and sends Wake-on-LAN magic packets.
//
// Sending only means "packet sent", never "device powered on"; the commands package verifies the
// outcome through the monitor (ADR-003).
package wol

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// DefaultPort is the conventional WOL UDP port.
const DefaultPort = 9

// ParseMAC parses and validates a 48-bit MAC address (":" / "-" / "." separators).
func ParseMAC(s string) (net.HardwareAddr, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("invalid MAC address %q", s)
	}
	if len(hw) != 6 {
		return nil, fmt.Errorf("MAC address %q must be 48-bit", s)
	}
	return hw, nil
}

// NormalizeMAC returns the canonical lower-case colon form, e.g. "aa:bb:cc:dd:ee:ff".
func NormalizeMAC(s string) (string, error) {
	hw, err := ParseMAC(s)
	if err != nil {
		return "", err
	}
	return hw.String(), nil
}

// MagicPacket returns 6×0xFF followed by the MAC repeated 16 times (102 bytes).
func MagicPacket(mac net.HardwareAddr) []byte {
	p := make([]byte, 0, 102)
	p = append(p, bytes.Repeat([]byte{0xFF}, 6)...)
	for range 16 {
		p = append(p, mac...)
	}
	return p
}

// BroadcastFor picks the broadcast address for a device: explicit override, else the directed
// broadcast of a local IPv4 interface whose subnet contains deviceIP, else 255.255.255.255.
// Directed broadcast avoids Android/multi-homed hosts sending limited broadcast out of the wrong
// interface.
func BroadcastFor(deviceIP, override string) string {
	if override != "" {
		return override
	}
	ip := net.ParseIP(deviceIP).To4()
	if ip != nil {
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, a := range addrs {
				ipn, ok := a.(*net.IPNet)
				if !ok || ipn.IP.To4() == nil || !ipn.Contains(ip) {
					continue
				}
				if b := directedBroadcast(ipn); b != nil {
					return b.String()
				}
			}
		}
	}
	return net.IPv4bcast.String()
}

func directedBroadcast(n *net.IPNet) net.IP {
	ip := n.IP.To4()
	mask := n.Mask
	if len(mask) == net.IPv6len {
		mask = mask[12:]
	}
	if ip == nil || len(mask) != net.IPv4len {
		return nil
	}
	b := make(net.IP, net.IPv4len)
	for i := range b {
		b[i] = ip[i] | ^mask[i]
	}
	return b
}

// Send transmits a magic packet for mac to broadcast:port over UDP.
func Send(ctx context.Context, mac net.HardwareAddr, broadcast string, port int) error {
	if len(mac) != 6 {
		return errors.New("MAC must be 48-bit")
	}
	if port <= 0 || port > 65535 {
		port = DefaultPort
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp4", net.JoinHostPort(broadcast, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("wol dial: %w", err)
	}
	defer conn.Close()
	if _, err := conn.Write(MagicPacket(mac)); err != nil {
		return fmt.Errorf("wol send: %w", err)
	}
	return nil
}

package commands

import (
	"context"
	"net"

	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/wol"
)

// WakeSender transmits a magic packet. wol.Send satisfies it through WakeSenderFunc.
type WakeSender interface {
	SendWake(ctx context.Context, mac net.HardwareAddr, broadcast string, port int) error
}

// WakeSenderFunc adapts a function (such as wol.Send) to WakeSender.
type WakeSenderFunc func(ctx context.Context, mac net.HardwareAddr, broadcast string, port int) error

// SendWake calls f.
func (f WakeSenderFunc) SendWake(ctx context.Context, mac net.HardwareAddr, broadcast string, port int) error {
	return f(ctx, mac, broadcast, port)
}

// WakeDispatcher is the Dispatcher of `wake` commands: one magic packet to the device's broadcast
// address (override, else the directed broadcast of the local subnet, else 255.255.255.255).
// Sending only means "packet left"; the monitor completes the command when the device is seen online.
type WakeDispatcher struct {
	Sender WakeSender // default: wol.Send
}

var _ Dispatcher = WakeDispatcher{}

// Precheck requires a MAC address.
func (WakeDispatcher) Precheck(d devices.Device) error {
	if d.MACAddress == "" {
		return &PreconditionError{Message: "the device has no mac_address; set one to enable Wake-on-LAN"}
	}
	return nil
}

// Dispatch sends the packet: `sent`, or `failed` with error_code wol_send_failed.
func (w WakeDispatcher) Dispatch(ctx context.Context, _ Command, d devices.Device) Result {
	mac, err := wol.ParseMAC(d.MACAddress)
	if err != nil {
		return Result{Status: StatusFailed, ErrorCode: ErrCodeWOLSend, Error: err.Error(), Attempts: 1}
	}
	send := w.Sender
	if send == nil {
		send = WakeSenderFunc(wol.Send)
	}
	broadcast := wol.BroadcastFor(d.IPAddress, d.WOLBroadcastAddress)
	if err := send.SendWake(ctx, mac, broadcast, d.WOLPort); err != nil {
		return Result{Status: StatusFailed, ErrorCode: ErrCodeWOLSend, Error: err.Error(), Attempts: 1}
	}
	return Result{Status: StatusSent, Attempts: 1}
}

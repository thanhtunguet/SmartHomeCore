package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/ddns"
	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/monitoring"
)

// The three hooks below connect the producers to Service.Notify. Each implements a small observer
// interface that the producing package defines, so ddns, commands and monitoring know nothing about
// alerts; internal/app wires them. All of them only build a message and queue it.

// IPHook alerts when a DDNS sync moves a record to a new public address (KindIPChanged).
type IPHook struct{ Svc *Service }

var _ ddns.IPObserver = IPHook{}

// IPChanged implements ddns.IPObserver.
func (h IPHook) IPChanged(_ context.Context, c ddns.IPChange) {
	old := c.OldIP
	if old == "" {
		old = "(empty)"
	}
	h.Svc.Notify(Message{
		Kind:  KindIPChanged,
		Title: "🌐 Public IP changed",
		Body:  fmt.Sprintf("%s (%s)\n%s → %s", c.RecordName, c.RecordType, old, c.NewIP),
	})
}

// CommandHook alerts when a wake or shutdown request was accepted (KindPowerCommand).
type CommandHook struct{ Svc *Service }

var _ commands.IssueObserver = CommandHook{}

// CommandIssued implements commands.IssueObserver.
func (h CommandHook) CommandIssued(ctx context.Context, c commands.Command, d devices.Device) {
	var title, outcome string
	switch {
	case c.Type == commands.TypeWake && c.Status == commands.StatusFailed:
		title, outcome = "❌ Wake failed: "+d.Name, "The Wake-on-LAN packet could not be sent."
	case c.Type == commands.TypeWake:
		title, outcome = "⚡ Wake requested: "+d.Name, "The Wake-on-LAN packet was sent."
	case c.Status == commands.StatusFailed:
		title, outcome = "❌ Shutdown failed: "+d.Name, "The agent did not accept the shutdown."
	default:
		title, outcome = "⏻ Shutdown requested: "+d.Name, "The agent accepted the shutdown."
	}
	body := outcome
	if c.Status == commands.StatusFailed && c.ErrorCode != "" {
		body += " (" + c.ErrorCode + ")"
	}
	if who := requester(ctx); who != "" {
		body += "\nBy " + who
	}
	h.Svc.Notify(Message{Kind: KindPowerCommand, Title: title, Body: body})
}

// requester names who asked, from the principal of the request ("" when there is none).
func requester(ctx context.Context) string {
	p, ok := auth.FromContext(ctx)
	if !ok || p.Username == "" {
		return ""
	}
	if p.KeyID != "" {
		return p.Username + " (service key)"
	}
	return p.Username
}

// ActiveCommands finds the in-flight command of a type for a device (commands.Repository).
type ActiveCommands interface {
	Active(ctx context.Context, deviceID string, t commands.Type) (commands.Command, error)
}

// StatusHook alerts when a device goes online or offline although no matching command is in
// progress: a wake explains "online", a shutdown explains "offline" (KindStatusChanged).
//
// Ignored: a device first seen (status `unknown`), and disabled devices (the owner switched
// monitoring off; a manual probe of one is the owner's own doing).
//
// The monitor calls it before the commands engine completes the command, so an active command is
// still visible here. A command that already timed out no longer explains a late change: that one
// is reported, which is what the owner wants to know.
type StatusHook struct {
	Svc      *Service
	Commands ActiveCommands
	Log      *slog.Logger // nil = slog.Default()
}

var _ monitoring.TransitionObserver = StatusHook{}

// DeviceTransitioned implements monitoring.TransitionObserver.
func (h StatusHook) DeviceTransitioned(ctx context.Context, t monitoring.Transition) {
	d := t.Device
	if !d.Enabled || t.From == devices.StatusUnknown || t.To == devices.StatusUnknown {
		return
	}
	var explains commands.Type
	var title, body string
	switch t.To {
	case devices.StatusOnline:
		explains = commands.TypeWake
		title = "🟢 " + d.Name + " is online"
		body = "It came online without a wake request from HomeCore."
	case devices.StatusOffline:
		explains = commands.TypeShutdown
		title = "🔴 " + d.Name + " went offline"
		body = "It stopped answering without a shutdown request from HomeCore: power loss, a crash, a local shutdown or a network problem."
	default:
		return
	}
	switch _, err := h.Commands.Active(ctx, d.ID, explains); {
	case err == nil:
		return // HomeCore did this
	case !errors.Is(err, commands.ErrNotFound):
		// Cannot tell: say it. A missing alert is worse than a redundant one.
		log := h.Log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("alerts: cannot look up the active command", "device", d.ID, "err", err)
	}
	if host := d.ProbeHost(); host != "" {
		body += "\n" + host + ":" + strconv.Itoa(d.MonitorPort)
	}
	h.Svc.Notify(Message{Kind: KindStatusChanged, Title: title, Body: body})
}

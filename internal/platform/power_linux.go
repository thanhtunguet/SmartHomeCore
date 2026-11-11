//go:build linux && !android

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

// systemdPower asks systemd-logind to power off/reboot. The agent runs as an unprivileged user;
// deploy/polkit/50-homecore-agent.rules grants exactly these two actions to that user.
type systemdPower struct{ log *slog.Logger }

func nativePower(log *slog.Logger) PowerController { return systemdPower{log: log} }

func (p systemdPower) PowerOff(ctx context.Context) error { return p.systemctl(ctx, "poweroff") }
func (p systemdPower) Reboot(ctx context.Context) error   { return p.systemctl(ctx, "reboot") }

func (p systemdPower) systemctl(ctx context.Context, verb string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Fixed binary and fixed argument: never interpolate request data here.
	out, err := exec.CommandContext(ctx, "systemctl", verb, "--no-wall").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", verb, err, out)
	}
	p.log.Info("power action issued", "action", verb)
	return nil
}

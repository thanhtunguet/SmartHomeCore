//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"syscall"

	"golang.org/x/sys/windows"
)

// shutdownMessage is the fixed text Windows shows for the action. No request data is ever put in it.
const shutdownMessage = "HomeCore: remote power action"

// windowsPower powers off / reboots through InitiateSystemShutdownExW. The Agent is expected to run
// elevated (Administrator, or a service running as LocalSystem): the process then holds
// SeShutdownPrivilege, which this adapter enables on its own token just before the call. Nothing
// else is requested: no shell, no command line, no arguments from the caller.
type windowsPower struct {
	log             *slog.Logger
	enablePrivilege func() error
	initiate        func(reboot bool) error
}

func nativePower(log *slog.Logger) PowerController {
	return windowsPower{log: log, enablePrivilege: enableShutdownPrivilege, initiate: initiateShutdown}
}

func (p windowsPower) PowerOff(ctx context.Context) error { return p.do(ctx, "poweroff", false) }
func (p windowsPower) Reboot(ctx context.Context) error   { return p.do(ctx, "reboot", true) }

func (p windowsPower) do(ctx context.Context, verb string, reboot bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.enablePrivilege(); err != nil {
		return fmt.Errorf("windows %s: enable shutdown privilege: %w", verb, err)
	}
	if err := p.initiate(reboot); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return fmt.Errorf("windows %s: %w (run the Agent as Administrator or as a service account that holds SeShutdownPrivilege)", verb, err)
		}
		return fmt.Errorf("windows %s: %w", verb, err)
	}
	p.log.Info("power action issued", "action", verb)
	return nil
}

// enableShutdownPrivilege turns SeShutdownPrivilege on in the current process token. If the token
// does not hold the privilege at all, AdjustTokenPrivileges still reports success (the
// ERROR_NOT_ALL_ASSIGNED detail is not surfaced by x/sys); the shutdown call then fails with
// ERROR_ACCESS_DENIED, which do() turns into an actionable message.
func enableShutdownPrivilege() error {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()

	name, err := syscall.UTF16PtrFromString("SeShutdownPrivilege")
	if err != nil {
		return err
	}
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &luid); err != nil {
		return fmt.Errorf("lookup privilege: %w", err)
	}
	privs := windows.Tokenprivileges{
		PrivilegeCount: 1,
		Privileges:     [1]windows.LUIDAndAttributes{{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}},
	}
	if err := windows.AdjustTokenPrivileges(token, false, &privs, 0, nil, nil); err != nil {
		return fmt.Errorf("adjust token privileges: %w", err)
	}
	return nil
}

// initiateShutdown shuts the local machine down now (timeout 0) and closes applications by force.
// A remote request cannot answer a "save changes?" dialog; without force an open dialog would keep
// the machine on and the command would only time out. Unsaved data in open applications is lost.
func initiateShutdown(reboot bool) error {
	msg, err := syscall.UTF16PtrFromString(shutdownMessage)
	if err != nil {
		return err
	}
	// MAJOR_OTHER | MINOR_OTHER | PLANNED: shows as a planned "Other" shutdown in the event log.
	const reason = windows.SHTDN_REASON_MAJOR_OTHER | windows.SHTDN_REASON_MINOR_OTHER | windows.SHTDN_REASON_FLAG_PLANNED
	return windows.InitiateSystemShutdownEx(nil, msg, 0, true, reboot, reason)
}

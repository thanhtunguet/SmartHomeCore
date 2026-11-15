package agentsetup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const shortcutFileName = "HomeCore Agent.lnk"

// ShortcutSpec describes the shortcut to create.
type ShortcutSpec struct {
	Path       string // the .lnk file to create
	Target     string // the homecore.exe to start
	Arguments  string // its command line
	WorkingDir string
}

// shortcutArguments is the shortcut's command line. The values are validated and contain no space or
// quote, so no quoting is needed.
func shortcutArguments(p Params) string { return strings.Join(p.Args(), " ") }

// MarkRunAsAdmin sets the "Run as administrator" bit (LinkFlags.RunAsUser, bit 13 at offset 0x14 of
// the ShellLinkHeader, [MS-SHLLINK] 2.1.1) so that opening the shortcut asks for elevation; the Agent
// needs it to shut the machine down. WScript.Shell cannot set this flag.
func MarkRunAsAdmin(lnk []byte) error {
	// HeaderSize is always 0x0000004C; below that the bytes are not a shell link.
	if len(lnk) < 0x4C || lnk[0] != 0x4C || lnk[1] != 0 || lnk[2] != 0 || lnk[3] != 0 {
		return errors.New("not a Windows shortcut file")
	}
	lnk[0x15] |= 0x20
	return nil
}

// createShortcut creates the .lnk through WScript.Shell (PowerShell is part of every supported
// Windows) and then sets "Run as administrator". Values travel in environment variables, never in
// the script text.
func createShortcut(spec ShortcutSpec) error {
	const script = `$s=(New-Object -ComObject WScript.Shell).CreateShortcut($env:HC_LNK);` +
		`$s.TargetPath=$env:HC_TARGET;$s.Arguments=$env:HC_ARGS;$s.WorkingDirectory=$env:HC_WD;` +
		`$s.Description='HomeCore Agent';$s.Save()`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(),
		"HC_LNK="+spec.Path, "HC_TARGET="+spec.Target, "HC_ARGS="+spec.Arguments, "HC_WD="+spec.WorkingDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create shortcut with PowerShell: %w: %s", err, strings.TrimSpace(string(out)))
	}
	b, err := os.ReadFile(spec.Path)
	if err != nil {
		return err
	}
	if err := MarkRunAsAdmin(b); err != nil {
		return err
	}
	return os.WriteFile(spec.Path, b, 0o644)
}

// WindowsNotes are the steps printed after the shortcut is created.
func WindowsNotes(p Params, exe, lnk string) string {
	port := p.Port()
	return fmt.Sprintf(`
Shortcut created: %[1]s
  It starts the binary with the Agent id and the Controller key, and asks for administrator rights
  (the Agent needs them to shut Windows down). Applications are closed by force on shutdown.

Start the Agent: double-click the shortcut and accept the UAC prompt. Keep the window open.

Start it automatically when you sign in (elevated PowerShell, once):

  $a = New-ScheduledTaskAction -Execute '%[2]s' -Argument '%[3]s'
  Register-ScheduledTask -TaskName 'HomeCore Agent' -Action $a -Trigger (New-ScheduledTaskTrigger -AtLogOn) -RunLevel Highest

Let the Controller reach it (elevated PowerShell, once; local subnet only):

  New-NetFirewallRule -DisplayName 'HomeCore Agent' -Direction Inbound -Action Allow -Protocol TCP -LocalPort %[4]d -RemoteAddress LocalSubnet -Profile Private

Check it from another machine on the LAN: http://<this PC's IP>:%[4]d/agent/v1/healthz
Never forward this port to the Internet. To change the id or the key, run this setup again.
`, lnk, psQuote(exe), shortcutArguments(p), port)
}

// psQuote escapes s for use inside a PowerShell single-quoted string.
func psQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }

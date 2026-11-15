package agentsetup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrNoCommand is returned when the user ends the input without pasting a command.
var ErrNoCommand = errors.New("no command entered")

// Options configure Run. The zero value of every field except Exe and Dir is a sensible default.
type Options struct {
	GOOS     string // default runtime.GOOS
	Exe      string // the running binary (os.Executable)
	Dir      string // where the files are written (the working directory)
	Shortcut func(ShortcutSpec) error
}

// Run asks for the command copied from the web UI, then writes the service files for this OS and
// prints how to install them. It does not start the Agent and installs nothing itself.
func Run(in io.Reader, out io.Writer, opt Options) error {
	if opt.GOOS == "" {
		opt.GOOS = runtime.GOOS
	}
	if opt.Shortcut == nil {
		opt.Shortcut = createShortcut
	}

	fmt.Fprint(out, `
HomeCore Agent setup
No Agent id was given. In the HomeCore web UI open the device, go to Remote Shutdown, copy the
command for this operating system and paste it here (Ctrl+C to cancel).

`)
	p, err := prompt(bufio.NewReader(in), out)
	if err != nil {
		return err
	}

	switch opt.GOOS {
	case "linux":
		return setupLinux(out, opt, p)
	case "windows":
		return setupWindows(out, opt, p)
	default:
		fmt.Fprintf(out, `
Service files are generated on Linux and Windows only. On %s run the Agent in the foreground, or
write a launchd/rc job yourself that runs:

  %s %s
`, opt.GOOS, opt.Exe, strings.Join(p.Args(), " "))
		return nil
	}
}

func prompt(r *bufio.Reader, out io.Writer) (Params, error) {
	for {
		fmt.Fprint(out, "Command> ")
		line, err := r.ReadString('\n')
		if strings.TrimSpace(line) == "" { // an empty line, or the input ended
			fmt.Fprintln(out)
			return Params{}, ErrNoCommand
		}
		p, perr := Parse(line)
		if perr == nil {
			return p, nil
		}
		fmt.Fprintf(out, "That command cannot be used: %v\nPaste it again, or press Enter on an empty line to cancel.\n\n", perr)
		if err != nil { // input ended after a bad line
			return Params{}, perr
		}
	}
}

func setupLinux(out io.Writer, opt Options, p Params) error {
	files := []struct {
		name, body string
	}{{unitFileName, RenderUnit(p)}, {rulesFileName, polkitRules}}
	for _, f := range files {
		if err := writeNew(filepath.Join(opt.Dir, f.name), []byte(f.body)); err != nil {
			return err
		}
	}
	fmt.Fprint(out, LinuxNotes(p, opt.Exe, opt.Dir))
	return nil
}

func setupWindows(out io.Writer, opt Options, p Params) error {
	lnk := filepath.Join(opt.Dir, shortcutFileName)
	if _, err := os.Stat(lnk); err == nil {
		return fmt.Errorf("%s already exists: remove or rename it, then run the setup again", lnk)
	}
	spec := ShortcutSpec{Path: lnk, Target: opt.Exe, Arguments: shortcutArguments(p), WorkingDir: filepath.Dir(opt.Exe)}
	if err := opt.Shortcut(spec); err != nil {
		return err
	}
	fmt.Fprint(out, WindowsNotes(p, opt.Exe, lnk))
	return nil
}

// writeNew writes a generated file. An existing file is left alone when it is identical and is an
// error otherwise: the working directory may be /etc/systemd/system, and a real unit must not be
// replaced silently.
func writeNew(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil {
		if bytes.Equal(old, data) {
			return nil
		}
		return fmt.Errorf("%s already exists and differs: remove or rename it, then run the setup again", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Package agentsetup turns the command copied from the web UI (Device > Remote Shutdown) into the
// files that run the Agent as a service: a systemd unit on Linux, a shortcut on Windows (ADR-019).
//
// The pasted text is never executed or copied into the output. It is split into words, only the
// documented options are accepted, every value is validated, and the files are rendered from the
// validated values, so nothing the user pastes can add a directive to a unit or an argument to a
// shortcut.
package agentsetup

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/config"
)

// DefaultListen is the Agent listen address when the command has no --agent-listen.
const DefaultListen = ":9750"

// Params are the validated Agent options carried by the command copied from the UI.
type Params struct {
	AgentID            string   // canonical lower-case UUID
	TrustedControllers []string // standard base64 Ed25519 public keys, at least one
	Listen             string   // host:port, never empty
}

// Args is the argument list that starts the Agent with these Params. The UI builds the same list
// (frontend/src/api/agentCommand.ts); both must stay identical.
func (p Params) Args() []string {
	args := []string{"--mode", "agent", "--agent-id", p.AgentID}
	for _, k := range p.TrustedControllers {
		args = append(args, "--trusted-controller", k)
	}
	return append(args, "--agent-listen", p.Listen)
}

// Port is the numeric port of Listen.
func (p Params) Port() int {
	_, port, _ := net.SplitHostPort(p.Listen)
	n, _ := strconv.Atoi(port)
	return n
}

var listenHost = regexp.MustCompile(`^[A-Za-z0-9.:\-\[\]]*$`)

// Parse reads a command line such as
//
//	./homecore --mode agent --agent-id <uuid> --trusted-controller <key> --agent-listen :9750
//
// Words before the first option (the program path, `sudo`, PowerShell's `&`) are ignored.
func Parse(line string) (Params, error) {
	words, err := split(line)
	if err != nil {
		return Params{}, err
	}
	start := 0
	for start < len(words) && !strings.HasPrefix(words[start], "--") {
		start++
	}
	if start == len(words) {
		return Params{}, errors.New("no options found: paste the whole command copied from the UI")
	}

	var p Params
	haveMode := false
	for i := start; i < len(words); i++ {
		w := words[i]
		if !strings.HasPrefix(w, "--") {
			return Params{}, fmt.Errorf("unexpected word %q: options look like --name value", w)
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(w, "--"), "=")
		switch name {
		case "mode", "agent-id", "trusted-controller", "agent-listen":
		default:
			return Params{}, fmt.Errorf("unsupported option --%s", name)
		}
		if !hasValue {
			if i+1 >= len(words) || strings.HasPrefix(words[i+1], "--") {
				return Params{}, fmt.Errorf("option --%s needs a value", name)
			}
			i++
			value = words[i]
		}
		switch name {
		case "mode":
			if value != "agent" {
				return Params{}, fmt.Errorf("--mode must be agent, got %q", value)
			}
			haveMode = true
		case "agent-id":
			if p.AgentID != "" {
				return Params{}, errors.New("--agent-id given twice")
			}
			id, err := uuid.Parse(value)
			if err != nil || id == uuid.Nil {
				return Params{}, fmt.Errorf("--agent-id %q is not a UUID", value)
			}
			p.AgentID = id.String()
		case "trusted-controller":
			if _, err := protocol.ParsePublicKey(value); err != nil {
				return Params{}, fmt.Errorf("--trusted-controller: %v", err)
			}
			p.TrustedControllers = append(p.TrustedControllers, value)
		case "agent-listen":
			if p.Listen != "" {
				return Params{}, errors.New("--agent-listen given twice")
			}
			if err := checkListen(value); err != nil {
				return Params{}, err
			}
			p.Listen = value
		}
	}
	switch {
	case !haveMode:
		return Params{}, errors.New("--mode agent is missing")
	case p.AgentID == "":
		return Params{}, errors.New("--agent-id is missing")
	case len(p.TrustedControllers) == 0:
		return Params{}, errors.New("--trusted-controller is missing (the Controller public key)")
	}
	if p.Listen == "" {
		p.Listen = DefaultListen
	}
	return p, nil
}

func checkListen(addr string) error {
	if err := config.ValidListen(addr); err != nil {
		return fmt.Errorf("--agent-listen: %v", err)
	}
	host, port, _ := net.SplitHostPort(addr)
	if !listenHost.MatchString(host) {
		return fmt.Errorf("--agent-listen: unsupported characters in host %q", host)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("--agent-listen: port must be a number from 1 to 65535, got %q", port)
	}
	return nil
}

// split breaks line into words. Single and double quotes group characters; there are no escape
// sequences, so Windows paths keep their backslashes.
func split(line string) ([]string, error) {
	var (
		words []string
		cur   strings.Builder
		in    bool
		quote rune
	)
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, in = r, true
		case unicode.IsSpace(r):
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if in {
		words = append(words, cur.String())
	}
	return words, nil
}

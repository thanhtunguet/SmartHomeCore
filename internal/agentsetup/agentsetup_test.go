package agentsetup

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
)

const testID = "0198a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

func testKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.EncodePublicKey(pub)
}

func TestParseAcceptsTheCommandsTheUIProduces(t *testing.T) {
	key := testKey(t)
	for name, line := range map[string]string{
		"linux":      "./homecore --mode agent --agent-id " + testID + " --trusted-controller " + key + " --agent-listen :9750",
		"windows":    `.\homecore.exe --mode agent --agent-id ` + testID + ` --trusted-controller ` + key + ` --agent-listen :9750`,
		"powershell": `& 'C:\Program Files\HomeCore\homecore.exe' --mode agent --agent-id ` + testID + ` --trusted-controller ` + key + ` --agent-listen :9750`,
		"sudo":       "sudo /usr/local/bin/homecore --mode=agent --agent-id=" + testID + " --trusted-controller=" + key + " --agent-listen=:9750\r\n",
		"quoted":     `homecore --mode agent --agent-id "` + testID + `" --trusted-controller '` + key + `' --agent-listen ":9750"`,
	} {
		p, err := Parse(line)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if p.AgentID != testID || len(p.TrustedControllers) != 1 || p.TrustedControllers[0] != key || p.Listen != ":9750" || p.Port() != 9750 {
			t.Errorf("%s: %+v", name, p)
		}
	}
}

func TestParseDefaultsTheListenAddressAndKeepsEveryKey(t *testing.T) {
	k1, k2 := testKey(t), testKey(t)
	p, err := Parse("homecore --mode agent --agent-id " + strings.ToUpper(testID) + " --trusted-controller " + k1 + " --trusted-controller " + k2)
	if err != nil {
		t.Fatal(err)
	}
	if p.AgentID != testID {
		t.Errorf("the id must be normalised to lower case, got %q", p.AgentID)
	}
	if p.Listen != DefaultListen || len(p.TrustedControllers) != 2 {
		t.Errorf("%+v", p)
	}
	// Args is what the UI builds: Parse(Args()) is the identity.
	back, err := Parse(strings.Join(p.Args(), " "))
	if err != nil || strings.Join(back.Args(), " ") != strings.Join(p.Args(), " ") {
		t.Errorf("round trip: %v %+v", err, back)
	}
}

func TestParseRejects(t *testing.T) {
	key := testKey(t)
	base := "--mode agent --agent-id " + testID + " --trusted-controller " + key
	for name, tc := range map[string]struct{ line, want string }{
		"empty":            {"", "no options"},
		"no options":       {"homecore", "no options"},
		"other mode":       {"--mode controller --agent-id " + testID + " --trusted-controller " + key, "must be agent"},
		"no mode":          {"--agent-id " + testID + " --trusted-controller " + key, "--mode agent is missing"},
		"no id":            {"--mode agent --trusted-controller " + key, "--agent-id is missing"},
		"no key":           {"--mode agent --agent-id " + testID, "--trusted-controller is missing"},
		"bad id":           {"--mode agent --agent-id nope --trusted-controller " + key, "not a UUID"},
		"nil id":           {"--mode agent --agent-id 00000000-0000-0000-0000-000000000000 --trusted-controller " + key, "not a UUID"},
		"bad key":          {"--mode agent --agent-id " + testID + " --trusted-controller AAAA", "trusted-controller"},
		"dup id":           {base + " --agent-id " + testID, "twice"},
		"bad listen":       {base + " --agent-listen 9750", "agent-listen"},
		"bad port":         {base + " --agent-listen :99999", "port"},
		"listen injection": {base + ` --agent-listen "x;rm:9750"`, "unsupported characters"},
		"unknown option":   {base + " --config /etc/passwd", "unsupported option --config"},
		"dry run":          {base + " --dry-run", "unsupported option"},
		"stray word":       {base + " && rm -rf /", "unexpected word"},
		"missing value":    {base + " --agent-listen", "needs a value"},
		"open quote":       {base + ` --agent-listen ":9750`, "unterminated"},
	} {
		if _, err := Parse(tc.line); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error containing %q, got %v", name, tc.want, err)
		}
	}
}

func TestUnitIsRenderedFromValidatedValuesOnly(t *testing.T) {
	key := testKey(t)
	// A newline smuggled inside a value must not become a second directive.
	if _, err := Parse("--mode agent --agent-id \"" + testID + "\nExecStartPre=/bin/evil\" --trusted-controller " + key); err == nil {
		t.Fatal("an id with a newline must be rejected")
	}
	p, err := Parse("homecore --mode agent --agent-id " + testID + " --trusted-controller " + key + " --agent-listen 192.168.1.5:9800")
	if err != nil {
		t.Fatal(err)
	}
	unit := RenderUnit(p)
	want := "ExecStart=/usr/local/bin/homecore --mode agent --agent-id " + testID + " --trusted-controller " + key + " --agent-listen 192.168.1.5:9800\n"
	if !strings.Contains(unit, want) {
		t.Fatalf("unit lacks %q:\n%s", want, unit)
	}
	for _, d := range []string{"User=homecore-agent", "ProtectSystem=strict", "NoNewPrivileges=yes", "WantedBy=multi-user.target"} {
		if !strings.Contains(unit, d) {
			t.Errorf("unit lacks %s", d)
		}
	}
	if strings.Contains(unit, "StateDirectory") || strings.Contains(unit, "HOMECORE_DATA_DIR") || strings.Contains(unit, "--config") {
		t.Errorf("a portable agent needs no state directory, data dir or config file:\n%s", unit)
	}
	if n := strings.Count(unit, "ExecStart="); n != 1 {
		t.Errorf("ExecStart appears %d times", n)
	}
}

func TestShippedFilesAndGeneratedFilesDoNotDrift(t *testing.T) {
	rules, err := os.ReadFile("../../deploy/polkit/50-homecore-agent.rules")
	if err != nil {
		t.Fatal(err)
	}
	if string(rules) != polkitRules {
		t.Error("polkitRules differs from deploy/polkit/50-homecore-agent.rules: update both")
	}
	shipped, err := os.ReadFile("../../deploy/systemd/homecore-agent.service")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(unitHardening, "\n") {
		if line != "" && !strings.Contains(string(shipped), line+"\n") {
			t.Errorf("hardening line %q is not in deploy/systemd/homecore-agent.service", line)
		}
	}
}

func TestMarkRunAsAdmin(t *testing.T) {
	lnk := make([]byte, 0x4C)
	lnk[0] = 0x4C
	lnk[0x14], lnk[0x15] = 0x81, 0x01 // some flags already set
	if err := MarkRunAsAdmin(lnk); err != nil {
		t.Fatal(err)
	}
	if lnk[0x14] != 0x81 || lnk[0x15] != 0x21 {
		t.Fatalf("flags = %02x %02x, want 81 21", lnk[0x14], lnk[0x15])
	}
	for _, bad := range [][]byte{nil, make([]byte, 0x4C), {0x4C, 0, 0, 0}} {
		if err := MarkRunAsAdmin(bad); err == nil {
			t.Errorf("%v must be rejected", bad)
		}
	}
}

func validLine(t *testing.T) string {
	return "./homecore --mode agent --agent-id " + testID + " --trusted-controller " + testKey(t) + " --agent-listen :9750\n"
}

func TestRunWritesUnitAndRulesOnLinux(t *testing.T) {
	dir := t.TempDir()
	var out strings.Builder
	err := Run(strings.NewReader(validLine(t)), &out, Options{GOOS: "linux", Exe: "/tmp/my dir/homecore", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(filepath.Join(dir, "homecore-agent.service"))
	if err != nil || !strings.Contains(string(unit), "--agent-id "+testID) {
		t.Fatalf("unit: %v\n%s", err, unit)
	}
	if rules, err := os.ReadFile(filepath.Join(dir, "50-homecore-agent.rules")); err != nil || string(rules) != polkitRules {
		t.Fatalf("rules: %v", err)
	}
	for _, want := range []string{"sudo systemctl enable --now homecore-agent", "'/tmp/my dir/homecore'", "/etc/polkit-1/rules.d/50-homecore-agent.rules", "127.0.0.1:9750/agent/v1/healthz"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("notes lack %q:\n%s", want, out.String())
		}
	}
}

func TestRunAsksAgainAfterABadCommandAndCancelsOnAnEmptyLine(t *testing.T) {
	dir := t.TempDir()
	var out strings.Builder
	in := "homecore --mode agent --agent-id nope\n" + validLine(t)
	if err := Run(strings.NewReader(in), &out, Options{GOOS: "linux", Exe: "/x/homecore", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "That command cannot be used") || !strings.Contains(out.String(), "not a UUID") {
		t.Errorf("no explanation of the bad command:\n%s", out.String())
	}

	for name, in := range map[string]string{"empty line": "\n", "eof": ""} {
		err := Run(strings.NewReader(in), &strings.Builder{}, Options{GOOS: "linux", Exe: "/x/homecore", Dir: t.TempDir()})
		if !errors.Is(err, ErrNoCommand) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := Run(strings.NewReader("homecore --bogus x"), &strings.Builder{}, Options{GOOS: "linux", Dir: t.TempDir()}); err == nil {
		t.Error("input that ends after a bad command must fail")
	}
}

func TestRunDoesNotReplaceADifferentExistingUnit(t *testing.T) {
	dir := t.TempDir()
	unit := filepath.Join(dir, "homecore-agent.service")
	if err := os.WriteFile(unit, []byte("[Service]\nExecStart=/real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(strings.NewReader(validLine(t)), &strings.Builder{}, Options{GOOS: "linux", Exe: "/x/homecore", Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(unit); !strings.Contains(string(b), "/real") {
		t.Fatal("the existing unit was overwritten")
	}
}

func TestRunCreatesTheShortcutOnWindows(t *testing.T) {
	dir := t.TempDir()
	var got ShortcutSpec
	var out strings.Builder
	err := Run(strings.NewReader(validLine(t)), &out, Options{
		GOOS: "windows", Exe: `C:\Tools\homecore.exe`, Dir: dir,
		Shortcut: func(s ShortcutSpec) error { got = s; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != filepath.Join(dir, "HomeCore Agent.lnk") || got.Target != `C:\Tools\homecore.exe` ||
		!strings.HasPrefix(got.Arguments, "--mode agent --agent-id "+testID+" --trusted-controller ") || !strings.HasSuffix(got.Arguments, "--agent-listen :9750") {
		t.Errorf("spec = %+v", got)
	}
	for _, want := range []string{"Register-ScheduledTask", "-RunLevel Highest", "New-NetFirewallRule", "-LocalPort 9750", "-RemoteAddress LocalSubnet", "UAC"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("notes lack %q:\n%s", want, out.String())
		}
	}
}

func TestRunOnOtherSystemsPrintsTheCommand(t *testing.T) {
	var out strings.Builder
	err := Run(strings.NewReader(validLine(t)), &out, Options{GOOS: "darwin", Exe: "/opt/homecore", Dir: t.TempDir()})
	if err != nil || !strings.Contains(out.String(), "/opt/homecore --mode agent --agent-id "+testID) {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

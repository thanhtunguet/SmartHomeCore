package portforward

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/storage"
)

const lo = "127.0.0.1"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// freePort returns a port that is free for both TCP and UDP right now.
func freePort(t *testing.T) int {
	t.Helper()
	for range 50 {
		ln, err := net.Listen("tcp4", lo+":0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		pc, uerr := net.ListenPacket("udp4", net.JoinHostPort(lo, strconv.Itoa(port)))
		_ = ln.Close()
		if uerr == nil {
			_ = pc.Close()
			return port
		}
	}
	t.Fatal("no free port")
	return 0
}

// tcpEcho serves an echo server and returns its port. It answers the bytes it reads, and once the
// client half-closes it answers "bye" and closes: the relay must carry both.
func tcpEcho(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", lo+":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// udpEcho serves a UDP server that answers every datagram with "echo:" + the datagram.
func udpEcho(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp4", lo+":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(append([]byte("echo:"), buf[:n]...), from)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func newRepo(t *testing.T) *SQLiteRepository {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "pf.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return NewSQLiteRepository(db)
}

// running starts svc.Run and returns a stop function that cancels it and waits for it to return.
func running(t *testing.T, svc *Service) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Run returned %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("Run did not return after cancel")
			}
		})
	}
	t.Cleanup(stop)
	// Run publishes its context first thing; wait for that so Create starts rules.
	waitFor(t, func() bool { svc.mu.Lock(); defer svc.mu.Unlock(); return svc.ctx != nil }, "Run to start")
	return stop
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newService(t *testing.T, repo Repository) *Service {
	return NewService(ServiceConfig{Repo: repo, RetryInterval: 50 * time.Millisecond, Log: quiet})
}

func addr(port int) string { return net.JoinHostPort(lo, strconv.Itoa(port)) }

func in(name string, p Protocol, listen, target int) CreateInput {
	return CreateInput{Name: name, Protocol: p, ListenAddress: lo, ListenPort: listen, TargetHost: lo, TargetPort: target}
}

func mustCreate(t *testing.T, svc *Service, c CreateInput) View {
	t.Helper()
	v, err := svc.Create(context.Background(), c)
	if err != nil {
		t.Fatalf("Create(%s): %v", c.Name, err)
	}
	return v
}

// roundTripTCP sends msg through the relay and returns the echo.
func roundTripTCP(t *testing.T, port int, msg string) string {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr(port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(buf)
}

func roundTripUDP(t *testing.T, c net.Conn, msg string) string {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("udp read: %v", err)
	}
	return string(buf[:n])
}

func refused(port int) bool {
	c, err := net.DialTimeout("tcp", addr(port), time.Second)
	if err == nil {
		_ = c.Close()
		return false
	}
	return true
}

func TestTCPRelayCarriesBothDirectionsAndHalfClose(t *testing.T) {
	svc := newService(t, newRepo(t))
	running(t, svc)
	listen := freePort(t)
	v := mustCreate(t, svc, in("ssh", TCP, listen, tcpEcho(t)))
	if v.Status != StatusRunning {
		t.Fatalf("status = %s (%s), want running", v.Status, v.LastError)
	}
	if got := roundTripTCP(t, listen, "hello"); got != "hello" {
		t.Fatalf("echo = %q", got)
	}

	// Half-close: the client finishes writing, the reply still comes back, then EOF.
	c, err := net.Dial("tcp", addr(listen))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = c.Write([]byte("last words"))
	_ = c.(*net.TCPConn).CloseWrite()
	all, err := io.ReadAll(c)
	if err != nil || string(all) != "last words" {
		t.Fatalf("after CloseWrite got %q, %v", all, err)
	}
}

func TestTCPRelayToADeadTargetClosesTheClient(t *testing.T) {
	svc := newService(t, newRepo(t))
	running(t, svc)
	listen := freePort(t)
	mustCreate(t, svc, in("dead", TCP, listen, freePort(t))) // nothing listens on the target
	c, err := net.Dial("tcp", addr(listen))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("read succeeded, want the connection closed")
	}
}

func TestUDPRelayKeepsClientsApart(t *testing.T) {
	svc := newService(t, newRepo(t))
	running(t, svc)
	listen := freePort(t)
	mustCreate(t, svc, in("dns", UDP, listen, udpEcho(t)))

	a, err := net.Dial("udp", addr(listen))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := net.Dial("udp", addr(listen))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got := roundTripUDP(t, a, "from-a"); got != "echo:from-a" {
		t.Errorf("a got %q", got)
	}
	if got := roundTripUDP(t, b, "from-b"); got != "echo:from-b" {
		t.Errorf("b got %q", got)
	}
	if got := roundTripUDP(t, a, "again"); got != "echo:again" { // same session reused
		t.Errorf("a got %q", got)
	}
}

func TestUniversalRelaysTCPAndUDPOnOnePort(t *testing.T) {
	svc := newService(t, newRepo(t))
	running(t, svc)
	listen := freePort(t)
	// TCP and UDP targets on different ports: universal sends each protocol to the same target port,
	// so use one port that both an echo servers can take.
	tp := freePort(t)
	tl, err := net.Listen("tcp4", addr(tp))
	if err != nil {
		t.Fatal(err)
	}
	defer tl.Close()
	go func() {
		for {
			c, err := tl.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	up, err := net.ListenPacket("udp4", addr(tp))
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := up.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = up.WriteTo(append([]byte("echo:"), buf[:n]...), from)
		}
	}()

	v := mustCreate(t, svc, in("both", Universal, listen, tp))
	if v.Status != StatusRunning {
		t.Fatalf("status = %s (%s)", v.Status, v.LastError)
	}
	if got := roundTripTCP(t, listen, "tcp!"); got != "tcp!" {
		t.Errorf("tcp echo = %q", got)
	}
	u, err := net.Dial("udp", addr(listen))
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	if got := roundTripUDP(t, u, "udp!"); got != "echo:udp!" {
		t.Errorf("udp echo = %q", got)
	}
}

func TestSwitchOffClosesTheListenerAndOnReopensIt(t *testing.T) {
	svc := newService(t, newRepo(t))
	running(t, svc)
	listen := freePort(t)
	v := mustCreate(t, svc, in("web", TCP, listen, tcpEcho(t)))
	ctx := context.Background()

	off, on := false, true
	if v, err := svc.Update(ctx, v.ID, UpdateInput{Enabled: &off}); err != nil || v.Status != StatusStopped || v.Enabled {
		t.Fatalf("switch off: %+v, %v", v, err)
	}
	if !refused(listen) {
		t.Fatal("port still accepts connections after switching off")
	}
	if v, err := svc.Update(ctx, v.ID, UpdateInput{Enabled: &on}); err != nil || v.Status != StatusRunning {
		t.Fatalf("switch on: %+v, %v", v, err)
	}
	if got := roundTripTCP(t, listen, "back"); got != "back" {
		t.Fatalf("echo = %q", got)
	}
}

func TestEnabledRulesComeBackAfterARestart(t *testing.T) {
	repo := newRepo(t)
	listenOn, listenOff := freePort(t), freePort(t)
	target := tcpEcho(t)

	first := newService(t, repo)
	stop := running(t, first)
	mustCreate(t, first, in("on", TCP, listenOn, target))
	off := false
	c := in("off", TCP, listenOff, target)
	c.Enabled = &off
	mustCreate(t, first, c)
	if got := roundTripTCP(t, listenOn, "x"); got != "x" {
		t.Fatal("first run does not relay")
	}
	stop() // the Controller goes down
	if !refused(listenOn) {
		t.Fatal("a stopped Controller still listens")
	}

	second := newService(t, repo) // a new process: same database, nothing in memory
	running(t, second)
	waitFor(t, func() bool { return !refused(listenOn) }, "the enabled rule to listen again")
	if got := roundTripTCP(t, listenOn, "again"); got != "again" {
		t.Fatalf("echo = %q", got)
	}
	if !refused(listenOff) {
		t.Fatal("a disabled rule was started")
	}
	views, err := second.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]Status{}
	for _, v := range views {
		status[v.Name] = v.Status
	}
	if status["on"] != StatusRunning || status["off"] != StatusStopped {
		t.Fatalf("statuses after restart = %v", status)
	}
}

func TestRulesDoNotListenBeforeRun(t *testing.T) {
	svc := newService(t, newRepo(t))
	listen := freePort(t)
	v := mustCreate(t, svc, in("early", TCP, listen, tcpEcho(t)))
	if v.Status != StatusStopped || !v.Enabled || !refused(listen) {
		t.Fatalf("before Run: status %s, enabled %v, refused %v", v.Status, v.Enabled, refused(listen))
	}
	running(t, svc)
	waitFor(t, func() bool { return !refused(listen) }, "the rule to start with Run")
}

func TestARuleThatCannotBindIsRetriedUntilThePortIsFree(t *testing.T) {
	svc := newService(t, newRepo(t))
	running(t, svc)
	listen := freePort(t)
	blocker, err := net.Listen("tcp4", addr(listen))
	if err != nil {
		t.Fatal(err)
	}

	v := mustCreate(t, svc, in("busy", TCP, listen, tcpEcho(t)))
	if v.Status != StatusError || v.LastError == "" || !v.Enabled {
		t.Fatalf("view = %+v, want an enabled rule in error with a reason", v)
	}
	_ = blocker.Close()
	waitFor(t, func() bool {
		got, err := svc.Get(context.Background(), v.ID)
		return err == nil && got.Status == StatusRunning && got.LastError == ""
	}, "the retry to bind the freed port")
	if got := roundTripTCP(t, listen, "ok"); got != "ok" {
		t.Fatalf("echo = %q", got)
	}
}

func TestAUniversalRuleThatHalfBindsLeavesNothingOpen(t *testing.T) {
	svc := newService(t, newRepo(t))
	running(t, svc)
	listen := freePort(t)
	blocker, err := net.ListenPacket("udp4", addr(listen)) // UDP is taken, TCP is free
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	v := mustCreate(t, svc, in("half", Universal, listen, tcpEcho(t)))
	if v.Status != StatusError {
		t.Fatalf("status = %s, want error", v.Status)
	}
	if !refused(listen) {
		t.Fatal("the TCP listener of a failed universal rule stayed open")
	}
}

func TestUpdateRestartsOnlyWhenTheRelayChanges(t *testing.T) {
	svc := newService(t, newRepo(t))
	running(t, svc)
	listen := freePort(t)
	t1, t2 := tcpEcho(t), tcpEcho(t)
	v := mustCreate(t, svc, in("move", TCP, listen, t1))
	ctx := context.Background()

	// An established connection survives a rename.
	c, err := net.Dial("tcp", addr(listen))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	name := "renamed"
	if _, err := svc.Update(ctx, v.ID, UpdateInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write([]byte("still"))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "still" {
		t.Fatalf("connection dropped by a rename: %q, %v", buf, err)
	}

	// A new target restarts the listener and drops the old connections.
	if _, err := svc.Update(ctx, v.ID, UpdateInput{TargetPort: &t2}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(buf); err == nil {
		t.Fatal("old connection survived a target change")
	}
	if got := roundTripTCP(t, listen, "new"); got != "new" {
		t.Fatalf("echo = %q", got)
	}
}

func TestDeleteClosesTheListenerAndConnections(t *testing.T) {
	svc := newService(t, newRepo(t))
	running(t, svc)
	listen := freePort(t)
	v := mustCreate(t, svc, in("gone", TCP, listen, tcpEcho(t)))
	c, err := net.Dial("tcp", addr(listen))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if err := svc.Delete(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
	if !refused(listen) {
		t.Fatal("deleted rule still listens")
	}
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("deleted rule kept a connection open")
	}
	if err := svc.Delete(context.Background(), v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

func TestStoppingTheServiceClosesEstablishedConnections(t *testing.T) {
	svc := newService(t, newRepo(t))
	stop := running(t, svc)
	listen := freePort(t)
	mustCreate(t, svc, in("held", TCP, listen, tcpEcho(t)))
	c, err := net.Dial("tcp", addr(listen))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	stop() // returns only once every goroutine of every rule has finished
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection survived shutdown")
	}
}

func TestOverlapIsRefusedOnlyBetweenEnabledRules(t *testing.T) {
	svc := newService(t, newRepo(t))
	running(t, svc)
	ctx := context.Background()
	listen := freePort(t)
	target := tcpEcho(t)
	first := mustCreate(t, svc, in("first", TCP, listen, target))

	var ce *ConflictError
	if _, err := svc.Create(ctx, in("second", TCP, listen, target)); !errors.As(err, &ce) || ce.With != "first" {
		t.Fatalf("overlapping create = %v, want a conflict with first", err)
	}
	if _, err := svc.Create(ctx, in("udp", UDP, listen, target)); err != nil { // TCP and UDP are separate
		t.Fatalf("same port, other protocol: %v", err)
	}

	off := false
	dis := in("parked", TCP, listen, target)
	dis.Enabled = &off
	parked := mustCreate(t, svc, dis) // a disabled rule reserves nothing
	on := true
	if _, err := svc.Update(ctx, parked.ID, UpdateInput{Enabled: &on}); !errors.As(err, &ce) {
		t.Fatalf("enabling an overlapping rule = %v, want a conflict", err)
	}
	if _, err := svc.Update(ctx, first.ID, UpdateInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if v, err := svc.Update(ctx, parked.ID, UpdateInput{Enabled: &on}); err != nil || v.Status != StatusRunning {
		t.Fatalf("after freeing the port: %+v, %v", v, err)
	}
}

func TestNamesAreUniqueIgnoringCase(t *testing.T) {
	svc := newService(t, newRepo(t))
	ctx := context.Background()
	off := false
	mk := func(name string, port int) CreateInput {
		c := in(name, TCP, port, 9)
		c.Enabled = &off
		return c
	}
	first := mustCreate(t, svc, mk("Plex", 32400))
	if _, err := svc.Create(ctx, mk("plex", 32401)); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate name = %v", err)
	}
	other := mustCreate(t, svc, mk("Other", 32402))
	n := "PLEX"
	if _, err := svc.Update(ctx, other.ID, UpdateInput{Name: &n}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("rename onto an existing name = %v", err)
	}
	same := "plex"
	if _, err := svc.Update(ctx, first.ID, UpdateInput{Name: &same}); err != nil {
		t.Fatalf("renaming a rule to its own name in another case: %v", err)
	}
}

func TestCreateAndUpdateValidation(t *testing.T) {
	svc := newService(t, newRepo(t))
	ctx := context.Background()
	off := false
	good := in("ok", TCP, 8080, 80)
	good.Enabled = &off

	bad := []CreateInput{
		{Name: "", Protocol: TCP, ListenPort: 80, TargetHost: "h", TargetPort: 80},
		{Name: "a\nb", Protocol: TCP, ListenPort: 80, TargetHost: "h", TargetPort: 80},
		{Name: "n", Protocol: "icmp", ListenPort: 80, TargetHost: "h", TargetPort: 80},
		{Name: "n", Protocol: TCP, ListenPort: 0, TargetHost: "h", TargetPort: 80},
		{Name: "n", Protocol: TCP, ListenPort: 80, TargetHost: "h", TargetPort: 70000},
		{Name: "n", Protocol: TCP, ListenAddress: "not-an-ip", ListenPort: 80, TargetHost: "h", TargetPort: 80},
		{Name: "n", Protocol: TCP, ListenPort: 80, TargetHost: "bad host", TargetPort: 80},
		{Name: "n", Protocol: TCP, ListenPort: 80, TargetHost: "127.0.0.1", TargetPort: 80}, // forwards to itself
	}
	for i, c := range bad {
		var ve *ValidationError
		if _, err := svc.Create(ctx, c); !errors.As(err, &ve) || len(ve.Problems) == 0 {
			t.Errorf("case %d: Create = %v, want a ValidationError", i, err)
		}
	}
	// One response lists every problem.
	var ve *ValidationError
	_, err := svc.Create(ctx, CreateInput{Name: "", Protocol: "x", ListenPort: 0, TargetHost: "", TargetPort: 0})
	if !errors.As(err, &ve) || len(ve.Problems) < 5 {
		t.Errorf("all-bad create = %v, want at least 5 problems", err)
	}

	v := mustCreate(t, svc, good)
	if v.ListenAddress != lo || v.Name != "ok" || v.Enabled {
		t.Fatalf("created %+v", v)
	}
	zero := 0
	if _, err := svc.Update(ctx, v.ID, UpdateInput{ListenPort: &zero}); !errors.As(err, &ve) {
		t.Errorf("update to port 0 = %v", err)
	}
	loop := "127.0.0.1"
	all := "0.0.0.0"
	port := 80
	if _, err := svc.Update(ctx, v.ID, UpdateInput{ListenAddress: &all, TargetHost: &loop, ListenPort: &port}); !errors.As(err, &ve) {
		t.Errorf("update into a self loop = %v", err)
	}
	if got, _ := svc.Get(ctx, v.ID); got.ListenPort != 8080 {
		t.Errorf("a refused update changed the rule: %+v", got)
	}
	if _, err := svc.Update(ctx, "0190c0de-0000-7000-8000-000000000001", UpdateInput{Enabled: &off}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update of an unknown rule = %v", err)
	}
	if got, err := svc.Update(ctx, v.ID, UpdateInput{}); err != nil || got.ID != v.ID {
		t.Errorf("empty update = %+v, %v", got, err)
	}
}

func TestDefaultsAreWildcardAndEnabled(t *testing.T) {
	svc := newService(t, newRepo(t))
	v := mustCreate(t, svc, CreateInput{Name: "d", Protocol: Universal, ListenPort: 4000, TargetHost: "NAS.local", TargetPort: 4000})
	if v.ListenAddress != "0.0.0.0" || !v.Enabled || v.TargetHost != "nas.local" {
		t.Fatalf("defaults: %+v", v)
	}
}

func TestUDPClientsGetOneSessionEach(t *testing.T) {
	// The idle timeout and the session limit are too large to wait out in a test; what is checked
	// here is the bookkeeping they rely on: one live session per client address.
	svc := newService(t, newRepo(t))
	running(t, svc)
	listen := freePort(t)
	mustCreate(t, svc, in("burst", UDP, listen, udpEcho(t)))
	var conns []net.Conn
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for i := range 20 {
		c, err := net.Dial("udp", addr(listen))
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		if got := roundTripUDP(t, c, "n"+strconv.Itoa(i)); got != "echo:n"+strconv.Itoa(i) {
			t.Fatalf("client %d got %q", i, got)
		}
	}
	svc.mu.Lock()
	var active int64
	for _, r := range svc.running {
		active += r.udpActive.Load()
	}
	svc.mu.Unlock()
	if active != 20 {
		t.Fatalf("active UDP sessions = %d, want 20", active)
	}
}

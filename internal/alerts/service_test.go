package alerts_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/alerts"
	"github.com/thanhtunguet/homecore/internal/alerts/alertstest"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

// scripted is an adapter the test controls: it records what it is sent and fails as told.
type scripted struct {
	mu    sync.Mutex
	sent  []alerts.Message
	calls int
	// errs are returned by the first len(errs) calls, then Send succeeds.
	errs []error
}

func (s *scripted) Send(_ context.Context, m alerts.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return err
	}
	s.sent = append(s.sent, m)
	return nil
}

func (s *scripted) Calls() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls }
func (s *scripted) Sent() []alerts.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]alerts.Message(nil), s.sent...)
}

type env struct {
	t    *testing.T
	svc  *alerts.Service
	db   *storage.DB
	tg   *alertstest.Telegram
	fake *scripted
}

// newEnv builds a Service on a real temp database with two adapters: "telegram" against the fake
// Bot API, and "fake", which is the scripted adapter above.
func newEnv(t *testing.T, mutate func(*alerts.ServiceConfig)) *env {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "alerts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	sealer, err := secrets.NewSealer(bytes.Repeat([]byte{7}, secrets.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, tg: alertstest.New(t, botToken), fake: &scripted{}}
	cfg := alerts.ServiceConfig{
		Repo: alerts.NewSQLiteRepository(db), Sealer: sealer,
		Adapters: map[string]alerts.Factory{
			alerts.TypeTelegram: alerts.TelegramFactory(e.tg.URL(), nil),
			"fake":              func(map[string]string, string) (alerts.Adapter, error) { return e.fake, nil },
		},
		RetryDelays: []time.Duration{time.Millisecond, time.Millisecond},
		Log:         slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	e.svc = alerts.NewService(cfg)
	return e
}

// run starts the delivery loop and stops it with the test.
func (e *env) run() {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = e.svc.Run(ctx) }()
	e.t.Cleanup(func() { cancel(); <-done })
}

func (e *env) telegram(name string, events ...alerts.Kind) alerts.Channel {
	e.t.Helper()
	c, err := e.svc.Create(context.Background(), alerts.CreateInput{
		Name: name, Type: alerts.TypeTelegram, Events: events,
		Settings: map[string]string{"target_user_id": target}, Secret: botToken,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) fakeChannel(name string, events ...alerts.Kind) alerts.Channel {
	e.t.Helper()
	c, err := e.svc.Create(context.Background(), alerts.CreateInput{
		Name: name, Type: "fake", Events: events, Settings: map[string]string{}, Secret: "s3cret",
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// REQ-ALERT-001: a Telegram channel is a name, the two adapter parameters and the events it wants.
func TestCreateChannel(t *testing.T) {
	e := newEnv(t, nil)
	c := e.telegram("  My phone ")
	if c.Name != "My phone" || c.Type != "telegram" || !c.Enabled || !c.HasSecret ||
		c.Settings["target_user_id"] != target {
		t.Fatalf("created: %+v", c)
	}
	if got, want := fmt.Sprint(c.Events), "[ip_changed power_command status_changed]"; got != want {
		t.Fatalf("a channel without events gets all of them: %s", got)
	}
	got, err := e.svc.Get(context.Background(), c.ID)
	if err != nil || got.Name != c.Name || !got.Subscribes(alerts.KindIPChanged) {
		t.Fatalf("get: %+v, %v", got, err)
	}
}

func TestEventsAreStoredInCanonicalOrder(t *testing.T) {
	e := newEnv(t, nil)
	c := e.telegram("a", alerts.KindStatusChanged, alerts.KindIPChanged)
	if got, want := fmt.Sprint(c.Events), "[ip_changed status_changed]"; got != want {
		t.Fatalf("events %s, want %s", got, want)
	}
}

// REQ-ALERT-006 and REQ-SEC-007: the bot token is sealed in the database and never read back.
func TestSecretIsSealedAtRest(t *testing.T) {
	e := newEnv(t, nil)
	c := e.telegram("a")
	var raw []byte
	if err := e.db.QueryRow(`SELECT secret_sealed FROM alert_channels WHERE id = ?`, c.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || bytes.Contains(raw, []byte(botToken)) || bytes.Contains(raw, []byte("AAH-")) {
		t.Fatalf("the stored secret is not sealed: %q", raw)
	}
	var settings string
	if err := e.db.QueryRow(`SELECT settings FROM alert_channels WHERE id = ?`, c.ID).Scan(&settings); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(settings, botToken) {
		t.Fatalf("settings hold the token: %s", settings)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t, nil)
	ok := alerts.CreateInput{Name: "n", Type: alerts.TypeTelegram, Settings: map[string]string{"target_user_id": target}, Secret: botToken}
	mut := func(f func(*alerts.CreateInput)) alerts.CreateInput { in := ok; f(&in); return in }
	cases := []struct {
		name string
		in   alerts.CreateInput
		want string
	}{
		{"empty name", mut(func(i *alerts.CreateInput) { i.Name = "  " }), "name"},
		{"long name", mut(func(i *alerts.CreateInput) { i.Name = strings.Repeat("x", 101) }), "name"},
		{"control character", mut(func(i *alerts.CreateInput) { i.Name = "a\nb" }), "control"},
		{"unknown type", mut(func(i *alerts.CreateInput) { i.Type = "smoke-signal" }), "unknown channel type"},
		{"unknown event", mut(func(i *alerts.CreateInput) { i.Events = []alerts.Kind{"boom"} }), "unknown event"},
		{"duplicate event", mut(func(i *alerts.CreateInput) { i.Events = []alerts.Kind{"ip_changed", "ip_changed"} }), "twice"},
		{"no events", mut(func(i *alerts.CreateInput) { i.Events = []alerts.Kind{} }), "at least one"},
		{"no secret", mut(func(i *alerts.CreateInput) { i.Secret = "" }), "secret"},
		{"bad token", mut(func(i *alerts.CreateInput) { i.Secret = "nope" }), "bot token"},
		{"bad target", mut(func(i *alerts.CreateInput) { i.Settings = map[string]string{"target_user_id": "x"} }), "numeric"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := e.svc.Create(context.Background(), c.in)
			var ve *alerts.ValidationError
			if !errors.As(err, &ve) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want a validation error mentioning %q", err, c.want)
			}
			if strings.Contains(err.Error(), c.in.Secret) && len(c.in.Secret) > 3 {
				t.Fatalf("the error echoes the secret: %v", err)
			}
		})
	}
	if list, _ := e.svc.List(context.Background()); len(list) != 0 {
		t.Fatalf("a rejected request stored something: %+v", list)
	}
}

func TestNamesAreUniqueIgnoringCase(t *testing.T) {
	e := newEnv(t, nil)
	e.telegram("Phone")
	_, err := e.svc.Create(context.Background(), alerts.CreateInput{
		Name: "phone", Type: alerts.TypeTelegram, Settings: map[string]string{"target_user_id": target}, Secret: botToken})
	if !errors.Is(err, alerts.ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
}

func TestUpdate(t *testing.T) {
	e := newEnv(t, nil)
	c := e.telegram("Phone")
	ctx := context.Background()

	off, name := false, "Dad's phone"
	events := []alerts.Kind{alerts.KindStatusChanged}
	up, err := e.svc.Update(ctx, c.ID, alerts.UpdateInput{Name: &name, Enabled: &off, Events: &events})
	if err != nil {
		t.Fatal(err)
	}
	if up.Name != name || up.Enabled || fmt.Sprint(up.Events) != "[status_changed]" || up.Settings["target_user_id"] != target ||
		!up.HasSecret || !up.UpdatedAt.After(c.UpdatedAt) && !up.UpdatedAt.Equal(c.UpdatedAt) {
		t.Fatalf("updated: %+v", up)
	}

	// New target, same stored token: the adapter checks the pair, so the secret is not needed.
	settings := map[string]string{"target_user_id": "99"}
	if up, err = e.svc.Update(ctx, c.ID, alerts.UpdateInput{Settings: &settings}); err != nil || up.Settings["target_user_id"] != "99" {
		t.Fatalf("settings update: %+v, %v", up, err)
	}
	// An invalid change is refused and leaves the channel as it was.
	bad := map[string]string{"target_user_id": "@name"}
	badSecret := "garbage"
	for _, in := range []alerts.UpdateInput{{Settings: &bad}, {Secret: &badSecret}} {
		if _, err := e.svc.Update(ctx, c.ID, in); err == nil || !errors.As(err, new(*alerts.ValidationError)) {
			t.Fatalf("update %+v: err = %v", in, err)
		}
	}
	if got, _ := e.svc.Get(ctx, c.ID); got.Settings["target_user_id"] != "99" {
		t.Fatalf("a refused update changed the channel: %+v", got)
	}
	// A new token is sealed and used.
	other := "555555555:BBB-another_token_0123456789abcdef"
	if _, err := e.svc.Update(ctx, c.ID, alerts.UpdateInput{Secret: &other}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Update(ctx, "00000000-0000-0000-0000-000000000000", alerts.UpdateInput{Name: &name}); !errors.Is(err, alerts.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	// An empty update is a no-op, not an error.
	if _, err := e.svc.Update(ctx, c.ID, alerts.UpdateInput{}); err != nil {
		t.Fatal(err)
	}
}

func TestDelete(t *testing.T) {
	e := newEnv(t, nil)
	c := e.telegram("a")
	if err := e.svc.Delete(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(context.Background(), c.ID); !errors.Is(err, alerts.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// REQ-ALERT-007: an alert goes to the enabled channels that subscribe to its event, in order.
func TestNotifyDeliversToSubscribedEnabledChannels(t *testing.T) {
	e := newEnv(t, nil)
	e.telegram("all")
	e.fakeChannel("ip only", alerts.KindIPChanged)
	e.fakeChannel("status only", alerts.KindStatusChanged)
	off := e.fakeChannel("disabled")
	f := false
	if _, err := e.svc.Update(context.Background(), off.ID, alerts.UpdateInput{Enabled: &f}); err != nil {
		t.Fatal(err)
	}
	e.run()

	e.svc.Notify(alerts.Message{Kind: alerts.KindIPChanged, Title: "one"})
	e.svc.Notify(alerts.Message{Kind: alerts.KindIPChanged, Title: "two", Body: "b"})

	msgs := e.tg.WaitFor(t, 2)
	if msgs[0].Text != "one" || msgs[1].Text != "two\nb" {
		t.Fatalf("telegram: %+v", msgs)
	}
	eventually(t, "the ip-only channel", func() bool { return len(e.fake.Sent()) == 2 })
	for _, m := range e.fake.Sent() {
		if m.Kind != alerts.KindIPChanged {
			t.Fatalf("a channel got an alert it did not subscribe to: %+v (disabled and status-only channels share this adapter)", m)
		}
	}
	if n := e.fake.Calls(); n != 2 {
		t.Fatalf("the shared scripted adapter was called %d times, want 2 (only 'ip only')", n)
	}
}

// REQ-ALERT-008: the outcome is kept on the channel; a temporary failure is retried, a refusal is not.
func TestDeliveryOutcomeAndRetries(t *testing.T) {
	ctx := context.Background()
	t.Run("temporary failures are retried", func(t *testing.T) {
		e := newEnv(t, nil)
		c := e.fakeChannel("a")
		e.fake.errs = []error{alerts.Temporary(errors.New("503")), alerts.Temporary(errors.New("503"))}
		e.run()
		e.svc.Notify(alerts.Message{Kind: alerts.KindIPChanged, Title: "x"})
		eventually(t, "delivery after two retries", func() bool { return len(e.fake.Sent()) == 1 })
		eventually(t, "the outcome", func() bool { got, _ := e.svc.Get(ctx, c.ID); return got.LastSentAt != nil })
		if got, _ := e.svc.Get(ctx, c.ID); e.fake.Calls() != 3 || got.LastError != "" || got.LastErrorAt != nil {
			t.Fatalf("calls=%d, channel=%+v", e.fake.Calls(), got)
		}
	})
	t.Run("a refusal is final and recorded, the next success clears it", func(t *testing.T) {
		e := newEnv(t, nil)
		c := e.fakeChannel("a")
		e.fake.errs = []error{errors.New("403 Forbidden: blocked by the user")}
		e.run()
		e.svc.Notify(alerts.Message{Kind: alerts.KindIPChanged, Title: "lost"})
		eventually(t, "the failure", func() bool { got, _ := e.svc.Get(ctx, c.ID); return got.LastError != "" })
		got, _ := e.svc.Get(ctx, c.ID)
		if e.fake.Calls() != 1 || !strings.Contains(got.LastError, "blocked by the user") || got.LastErrorAt == nil || got.LastSentAt != nil {
			t.Fatalf("calls=%d, channel=%+v", e.fake.Calls(), got)
		}
		e.svc.Notify(alerts.Message{Kind: alerts.KindIPChanged, Title: "ok"})
		eventually(t, "the success", func() bool { got, _ := e.svc.Get(ctx, c.ID); return got.LastSentAt != nil })
		if got, _ = e.svc.Get(ctx, c.ID); got.LastError != "" || got.LastErrorAt != nil {
			t.Fatalf("success did not clear the error: %+v", got)
		}
	})
	t.Run("giving up after the retries", func(t *testing.T) {
		e := newEnv(t, nil)
		c := e.fakeChannel("a")
		for range 5 {
			e.fake.errs = append(e.fake.errs, alerts.Temporary(errors.New("timeout")))
		}
		e.run()
		e.svc.Notify(alerts.Message{Kind: alerts.KindIPChanged, Title: "x"})
		eventually(t, "the failure", func() bool { got, _ := e.svc.Get(ctx, c.ID); return got.LastError != "" })
		if n := e.fake.Calls(); n != 3 {
			t.Fatalf("attempts = %d, want 3 (the first plus two retries)", n)
		}
	})
}

// One broken channel must not keep the others from their alert.
func TestOneFailingChannelDoesNotBlockAnother(t *testing.T) {
	e := newEnv(t, nil)
	e.fakeChannel("broken")
	e.fake.errs = []error{errors.New("boom")}
	e.telegram("fine")
	e.run()
	e.svc.Notify(alerts.Message{Kind: alerts.KindPowerCommand, Title: "x"})
	e.tg.WaitFor(t, 1)
}

// REQ-ALERT-007: Notify never blocks the producer; a full queue drops the alert.
func TestNotifyDropsWhenTheQueueIsFull(t *testing.T) {
	e := newEnv(t, func(c *alerts.ServiceConfig) { c.QueueSize = 2 })
	e.fakeChannel("a")
	done := make(chan struct{})
	go func() {
		for i := range 10 { // nobody consumes yet
			e.svc.Notify(alerts.Message{Kind: alerts.KindIPChanged, Title: fmt.Sprint(i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Notify blocked")
	}
	e.run()
	eventually(t, "the queued alerts", func() bool { return len(e.fake.Sent()) == 2 })
	if got := e.fake.Sent(); got[0].Title != "0" || got[1].Title != "1" {
		t.Fatalf("the oldest alerts should be the ones kept: %+v", got)
	}
}

// REQ-ALERT-009: a test message goes through even a disabled channel, once, and shows the reason when it fails.
func TestTestMessage(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	c := e.telegram("a")
	f := false
	if _, err := e.svc.Update(ctx, c.ID, alerts.UpdateInput{Enabled: &f}); err != nil {
		t.Fatal(err)
	}

	res, err := e.svc.Test(ctx, c.ID)
	if err != nil || res.Error != "" || res.ChannelID != c.ID {
		t.Fatalf("test: %+v, %v", res, err)
	}
	if msgs := e.tg.Messages(); len(msgs) != 1 || !strings.Contains(msgs[0].Text, "HomeCore test message") || !strings.Contains(msgs[0].Text, "a") {
		t.Fatalf("messages: %+v", msgs)
	}
	if got, _ := e.svc.Get(ctx, c.ID); got.LastSentAt == nil {
		t.Fatalf("the test did not record the delivery: %+v", got)
	}

	e.tg.FailWith(503, "Service Unavailable")
	before := e.tg.Requests()
	res, err = e.svc.Test(ctx, c.ID)
	if err != nil || !strings.Contains(res.Error, "Service Unavailable") {
		t.Fatalf("failed test: %+v, %v", res, err)
	}
	if n := e.tg.Requests() - before; n != 1 {
		t.Fatalf("a test is one attempt, made %d", n)
	}
	if got, _ := e.svc.Get(ctx, c.ID); !strings.Contains(got.LastError, "Service Unavailable") {
		t.Fatalf("the failure was not recorded: %+v", got)
	}
	if _, err := e.svc.Test(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, alerts.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

// The token never reaches what the service stores or returns, even when the adapter's error would carry it.
func TestFailedDeliveryNeverStoresTheToken(t *testing.T) {
	e := newEnv(t, nil)
	c := e.telegram("a")
	e.tg.FailWith(403, "Forbidden: "+botToken) // a server that echoes it
	res, _ := e.svc.Test(context.Background(), c.ID)
	if res.Error == "" || strings.Contains(res.Error, botToken) {
		t.Fatalf("test result: %q", res.Error)
	}
	got, _ := e.svc.Get(context.Background(), c.ID)
	if strings.Contains(got.LastError, botToken) {
		t.Fatalf("stored error: %q", got.LastError)
	}
}

func TestRunStopsWhenTheContextIsCancelled(t *testing.T) {
	e := newEnv(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.svc.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

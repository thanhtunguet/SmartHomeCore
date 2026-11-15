package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/secrets"
)

// SecretAAD binds a sealed secret to its column (REQ-SEC-002).
const SecretAAD = "alert_channels.secret"

// Limits and defaults.
const (
	maxNameLen      = 100
	maxSecretLen    = 512
	maxSettings     = 16
	maxSettingLen   = 256
	maxErrorLen     = 500
	DefaultQueue    = 64
	DefaultTimeout  = 15 * time.Second // one delivery attempt
	deliverySlack   = 5 * time.Second  // time to store the outcome of a delivery
	defaultRetryGap = 5 * time.Second
)

// ServiceConfig configures a Service.
type ServiceConfig struct {
	Repo   Repository
	Sealer *secrets.Sealer
	// Adapters maps an adapter name (TypeTelegram) to its Factory. A channel of an unknown type
	// cannot be created; one that already exists is reported as a failed delivery.
	Adapters map[string]Factory

	QueueSize int // alerts waiting for delivery; 0 = DefaultQueue. When full, new alerts are dropped.
	// RetryDelays are the waits before the 2nd, 3rd ... attempt at a delivery that failed with a
	// temporary error. nil = 1 s then 5 s; an empty non-nil slice = no retry.
	RetryDelays []time.Duration
	Timeout     time.Duration    // one delivery attempt; 0 = DefaultTimeout
	Now         func() time.Time // nil = time.Now
	Log         *slog.Logger     // nil = slog.Default()
}

// Service manages alert channels and delivers alerts to them.
//
// Producers call Notify, which only queues the alert; Run is the single consumer. Alerts are
// delivered in the order they were queued, to the channels in parallel. The queue is in memory: an
// alert not yet delivered when the Controller stops is lost (alerts are best effort and describe
// the moment; replaying them later would mislead).
type Service struct {
	repo     Repository
	sealer   *secrets.Sealer
	adapters map[string]Factory
	retry    []time.Duration
	timeout  time.Duration
	now      func() time.Time
	log      *slog.Logger
	queue    chan Message
}

// NewService returns a Service.
func NewService(c ServiceConfig) *Service {
	s := &Service{repo: c.Repo, sealer: c.Sealer, adapters: c.Adapters, retry: c.RetryDelays, timeout: c.Timeout,
		now: c.Now, log: c.Log}
	if s.retry == nil {
		s.retry = []time.Duration{time.Second, defaultRetryGap}
	}
	if s.timeout <= 0 {
		s.timeout = DefaultTimeout
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	n := c.QueueSize
	if n <= 0 {
		n = DefaultQueue
	}
	s.queue = make(chan Message, n)
	return s
}

// ---- channel CRUD -------------------------------------------------------------------------------

// CreateInput is a new channel.
type CreateInput struct {
	Name     string
	Type     string
	Enabled  *bool // nil = true
	Events   []Kind
	Settings map[string]string
	Secret   string
}

// UpdateInput is a partial update; nil fields are unchanged. The type never changes.
type UpdateInput struct {
	Name     *string
	Enabled  *bool
	Events   *[]Kind
	Settings *map[string]string
	Secret   *string
}

// IsEmpty reports whether nothing is to be changed.
func (u UpdateInput) IsEmpty() bool {
	return u.Name == nil && u.Enabled == nil && u.Events == nil && u.Settings == nil && u.Secret == nil
}

// Types lists the adapter names a channel can have, sorted.
func (s *Service) Types() []string {
	out := make([]string, 0, len(s.adapters))
	for t := range s.adapters {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// List returns every channel ordered by name.
func (s *Service) List(ctx context.Context) ([]Channel, error) { return s.repo.List(ctx) }

// Get returns one channel. ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (Channel, error) {
	st, err := s.repo.Get(ctx, id)
	return st.Channel, err
}

// Create validates and stores a channel. *ValidationError, ErrNameTaken.
func (s *Service) Create(ctx context.Context, in CreateInput) (Channel, error) {
	var problems []string
	name, err := cleanName(in.Name)
	if err != nil {
		problems = append(problems, err.Error())
	}
	events := in.Events
	if events == nil {
		events = AllKinds()
	}
	if err := checkEvents(events); err != nil {
		problems = append(problems, err.Error())
	}
	factory := s.adapters[in.Type]
	switch {
	case factory == nil:
		problems = append(problems, fmt.Sprintf("unknown channel type %q (known: %s)", in.Type, strings.Join(s.Types(), ", ")))
	case len(in.Secret) == 0 || len(in.Secret) > maxSecretLen:
		problems = append(problems, fmt.Sprintf("secret must be 1..%d characters", maxSecretLen))
	default:
		if err := checkSettings(in.Settings); err != nil {
			problems = append(problems, err.Error())
		} else if _, err := factory(in.Settings, in.Secret); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return Channel{}, &ValidationError{Problems: problems}
	}

	sealed, err := s.sealer.Seal([]byte(in.Secret), SecretAAD)
	if err != nil {
		return Channel{}, fmt.Errorf("seal alert secret: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Channel{}, fmt.Errorf("new alert channel id: %w", err)
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	c := Channel{
		ID: id.String(), Name: name, Type: in.Type, Enabled: in.Enabled == nil || *in.Enabled,
		Events: normalizeEvents(events), Settings: cloneSettings(in.Settings), HasSecret: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.Create(ctx, Stored{Channel: c, SecretSealed: sealed}); err != nil {
		return Channel{}, err
	}
	return c, nil
}

// Update applies a partial update. *ValidationError, ErrNotFound, ErrNameTaken.
func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (Channel, error) {
	st, err := s.repo.Get(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	if in.IsEmpty() {
		return st.Channel, nil
	}
	c := st.Channel
	var problems []string
	if in.Name != nil {
		if name, err := cleanName(*in.Name); err != nil {
			problems = append(problems, err.Error())
		} else {
			c.Name = name
		}
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if in.Events != nil {
		if err := checkEvents(*in.Events); err != nil {
			problems = append(problems, err.Error())
		} else {
			c.Events = normalizeEvents(*in.Events)
		}
	}
	if in.Settings != nil {
		c.Settings = cloneSettings(*in.Settings)
	}

	if in.Settings != nil || in.Secret != nil {
		secret := ""
		if in.Secret != nil {
			secret = *in.Secret
			if len(secret) == 0 || len(secret) > maxSecretLen {
				problems = append(problems, fmt.Sprintf("secret must be 1..%d characters", maxSecretLen))
			} else if sealed, err := s.sealer.Seal([]byte(secret), SecretAAD); err != nil {
				return Channel{}, fmt.Errorf("seal alert secret: %w", err)
			} else {
				st.SecretSealed = sealed
			}
		} else if secret, err = s.openSecret(st.SecretSealed); err != nil {
			return Channel{}, err
		}
		// The adapter validates settings and secret together, so a changed settings object is
		// checked against the stored secret and the other way round.
		if len(problems) == 0 {
			if err := checkSettings(c.Settings); err != nil {
				problems = append(problems, err.Error())
			} else if factory := s.adapters[c.Type]; factory == nil {
				problems = append(problems, fmt.Sprintf("channel type %q is no longer supported", c.Type))
			} else if _, err := factory(c.Settings, secret); err != nil {
				problems = append(problems, err.Error())
			}
		}
	}
	if len(problems) > 0 {
		return Channel{}, &ValidationError{Problems: problems}
	}

	c.UpdatedAt = s.now().UTC().Truncate(time.Millisecond)
	st.Channel = c
	if err := s.repo.Update(ctx, st); err != nil {
		return Channel{}, err
	}
	return s.Get(ctx, id)
}

// Delete removes a channel. ErrNotFound.
func (s *Service) Delete(ctx context.Context, id string) error { return s.repo.Delete(ctx, id) }

func (s *Service) openSecret(sealed []byte) (string, error) {
	b, err := s.sealer.Open(sealed, SecretAAD)
	if err != nil {
		return "", fmt.Errorf("open alert secret: %w", err)
	}
	return string(b), nil
}

func cleanName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || len([]rune(n)) > maxNameLen {
		return "", fmt.Errorf("name must be 1..%d characters", maxNameLen)
	}
	if strings.IndexFunc(n, unicode.IsControl) >= 0 {
		return "", errors.New("name must not contain control characters")
	}
	return n, nil
}

func checkEvents(events []Kind) error {
	if len(events) == 0 {
		return errors.New("events must name at least one event")
	}
	for i, k := range events {
		if !k.Valid() {
			return fmt.Errorf("unknown event %q (known: %v)", k, AllKinds())
		}
		if slices.Contains(events[:i], k) {
			return fmt.Errorf("event %q is listed twice", k)
		}
	}
	return nil
}

// normalizeEvents returns the events in the documented order, so the stored form is canonical.
func normalizeEvents(events []Kind) []Kind {
	out := make([]Kind, 0, len(events))
	for _, k := range AllKinds() {
		if slices.Contains(events, k) {
			out = append(out, k)
		}
	}
	return out
}

func checkSettings(m map[string]string) error {
	if len(m) > maxSettings {
		return fmt.Errorf("settings holds at most %d entries", maxSettings)
	}
	for k, v := range m {
		if len(k) > maxSettingLen || len(v) > maxSettingLen {
			return fmt.Errorf("settings keys and values are at most %d characters", maxSettingLen)
		}
	}
	return nil
}

func cloneSettings(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = strings.TrimSpace(v)
	}
	return out
}

// ---- delivery -------------------------------------------------------------------------------------

// Notify queues an alert for every enabled channel subscribed to m.Kind. It never blocks and never
// fails: if the queue is full the alert is dropped and logged. Safe to call before Run (alerts wait
// in the queue) and from any goroutine.
func (s *Service) Notify(m Message) {
	select {
	case s.queue <- m:
	default:
		s.log.Warn("alerts: queue full, alert dropped", "kind", m.Kind, "title", m.Title)
	}
}

// Run delivers queued alerts until ctx is cancelled. It always returns nil.
func (s *Service) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case m := <-s.queue:
			s.deliver(ctx, m)
		}
	}
}

// deliver sends m to every enabled channel subscribed to its kind, in parallel, and waits for all.
func (s *Service) deliver(ctx context.Context, m Message) {
	chans, err := s.repo.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("alerts: cannot list channels", "kind", m.Kind, "err", err)
		}
		return
	}
	var wg sync.WaitGroup
	for _, c := range chans {
		if !c.Enabled || !c.Subscribes(m.Kind) {
			continue
		}
		wg.Go(func() {
			defer func() {
				if p := recover(); p != nil {
					s.log.Error("alerts: adapter panicked", "channel", c.ID, "type", c.Type, "panic", p)
				}
			}()
			s.deliverTo(ctx, c.ID, m, true)
		})
	}
	wg.Wait()
}

// deliverTo sends m through one channel and stores the outcome on it. It returns the outcome (""
// = delivered); a delivery cut short by shutdown is neither stored nor reported.
func (s *Service) deliverTo(ctx context.Context, id string, m Message, retry bool) string {
	st, err := s.repo.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) && ctx.Err() == nil { // deleted since the list: nothing to do
			s.log.Warn("alerts: cannot load channel", "channel", id, "err", err)
		}
		return errText(err)
	}
	c := st.Channel
	var sendErr error
	if secret, err := s.openSecret(st.SecretSealed); err != nil {
		sendErr = err
	} else if factory := s.adapters[c.Type]; factory == nil {
		sendErr = fmt.Errorf("channel type %q is no longer supported", c.Type)
	} else if adapter, err := factory(c.Settings, secret); err != nil {
		sendErr = err
	} else {
		sendErr = s.send(ctx, adapter, m, retry)
	}
	if ctx.Err() != nil {
		return errText(sendErr)
	}

	out := ""
	if sendErr != nil {
		out = errText(sendErr)
		s.log.Warn("alerts: delivery failed", "channel", c.ID, "name", c.Name, "type", c.Type, "kind", m.Kind, "err", out)
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliverySlack)
	defer cancel()
	if err := s.repo.RecordDelivery(rctx, c.ID, Delivery{At: s.now().UTC().Truncate(time.Millisecond), Error: out}); err != nil {
		s.log.Warn("alerts: cannot record delivery", "channel", c.ID, "err", err)
	}
	return out
}

// send makes one attempt, plus the retries of s.retry while the failure is temporary.
func (s *Service) send(ctx context.Context, a Adapter, m Message, retry bool) error {
	var err error
	for attempt := 0; ; attempt++ {
		actx, cancel := context.WithTimeout(ctx, s.timeout)
		err = a.Send(actx, m)
		cancel()
		if err == nil || !retry || !IsTemporary(err) || attempt >= len(s.retry) {
			return err
		}
		select {
		case <-time.After(s.retry[attempt]):
		case <-ctx.Done():
			return err
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	t := err.Error()
	if r := []rune(t); len(r) > maxErrorLen {
		t = string(r[:maxErrorLen-1]) + "…"
	}
	return t
}

// TestResult is the outcome of a test message.
type TestResult struct {
	ChannelID string
	At        time.Time
	Error     string // '' = delivered
}

// Test sends a test message through one channel right now, whether or not it is enabled, with a
// single attempt, and stores the outcome on the channel. A delivery that fails is a TestResult with
// Error set, not an error; the error is for ErrNotFound and a cancelled context.
func (s *Service) Test(ctx context.Context, id string) (TestResult, error) {
	st, err := s.repo.Get(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	m := Message{
		Title: "✅ HomeCore test message",
		Body:  fmt.Sprintf("Alert channel “%s” works.", st.Channel.Name),
	}
	res := s.deliverTo(ctx, id, m, false)
	if err := ctx.Err(); err != nil {
		return TestResult{}, err
	}
	return TestResult{ChannelID: id, At: s.now().UTC(), Error: res}, nil
}

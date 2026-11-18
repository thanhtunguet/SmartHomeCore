package portforward

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DefaultRetryInterval is how often a rule that is enabled but not listening is tried again.
const DefaultRetryInterval = 15 * time.Second

// ServiceConfig configures a Service.
type ServiceConfig struct {
	Repo Repository
	// RetryInterval is the wait between attempts to start an enabled rule that is not listening
	// (port in use, interface not up yet). 0 = DefaultRetryInterval.
	RetryInterval time.Duration
	Now           func() time.Time // nil = time.Now
	Log           *slog.Logger     // nil = slog.Default()
}

// Service manages port forwarding rules and runs the enabled ones.
//
// Rules live in the database; the Service keeps one runner per enabled rule that is listening.
// Nothing listens before Run is called: a rule created earlier is started when Run begins.
type Service struct {
	repo  Repository
	retry time.Duration
	now   func() time.Time
	log   *slog.Logger

	// mu serialises every change (rule CRUD and starting/stopping runners), so the overlap check
	// and the bind that follows it cannot interleave with another request.
	mu      sync.Mutex
	ctx     context.Context // set by Run; nil = not running yet
	running map[string]*runner
	errs    map[string]string // why an enabled rule is not listening
}

// NewService returns a Service.
func NewService(c ServiceConfig) *Service {
	s := &Service{repo: c.Repo, retry: c.RetryInterval, now: c.Now, log: c.Log,
		running: map[string]*runner{}, errs: map[string]string{}}
	if s.retry <= 0 {
		s.retry = DefaultRetryInterval
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s
}

// CreateInput is a new rule.
type CreateInput struct {
	Name          string
	Protocol      Protocol
	ListenAddress string // "" = DefaultListenAddress
	ListenPort    int
	TargetHost    string
	TargetPort    int
	Enabled       *bool // nil = true
}

// UpdateInput is a partial update; nil fields are unchanged.
type UpdateInput struct {
	Name          *string
	Protocol      *Protocol
	ListenAddress *string
	ListenPort    *int
	TargetHost    *string
	TargetPort    *int
	Enabled       *bool
}

// IsEmpty reports whether nothing is to be changed.
func (u UpdateInput) IsEmpty() bool {
	return u.Name == nil && u.Protocol == nil && u.ListenAddress == nil && u.ListenPort == nil &&
		u.TargetHost == nil && u.TargetPort == nil && u.Enabled == nil
}

// Run starts every enabled rule and then keeps trying to start the ones that could not listen,
// until ctx is cancelled; on cancel it closes every listener and returns nil. A rule that cannot
// listen never stops the Controller: its reason is its `LastError`.
func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()

	s.startEnabled(ctx)
	t := time.NewTicker(s.retry)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.stopAll()
			return nil
		case <-t.C:
			s.startEnabled(ctx)
		}
	}
}

// startEnabled starts every enabled rule that is not listening.
func (s *Service) startEnabled(ctx context.Context) {
	rules, err := s.repo.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("port forwarding: cannot list rules", "err", err)
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rules {
		if r.Enabled && s.running[r.ID] == nil {
			s.apply(r)
		}
	}
}

func (s *Service) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range s.running {
		r.stop()
		delete(s.running, id)
	}
}

// apply makes the runner of rule match it. Caller holds mu. Before Run it does nothing.
func (s *Service) apply(rule Rule) {
	if s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	cur := s.running[rule.ID]
	if !rule.Enabled {
		if cur != nil {
			cur.stop()
			delete(s.running, rule.ID)
		}
		delete(s.errs, rule.ID)
		return
	}
	if cur != nil {
		if sameRelay(cur.rule, rule) {
			cur.rule = rule // a rename must not drop connections
			return
		}
		cur.stop()
		delete(s.running, rule.ID)
	}
	r, err := start(s.ctx, rule, s.log)
	if err != nil {
		s.errs[rule.ID] = err.Error()
		s.log.Warn("port forwarding cannot start, will retry", "rule", rule.Name, "retry_in", s.retry, "err", err)
		return
	}
	delete(s.errs, rule.ID)
	s.running[rule.ID] = r
}

// sameRelay reports whether two rules open the same sockets and relay to the same target.
func sameRelay(a, b Rule) bool {
	return a.Protocol == b.Protocol && a.ListenAddress == b.ListenAddress && a.ListenPort == b.ListenPort &&
		a.TargetHost == b.TargetHost && a.TargetPort == b.TargetPort
}

// view combines a rule with its runtime state. Caller holds mu.
func (s *Service) view(r Rule) View {
	v := View{Rule: r, Status: StatusStopped}
	switch {
	case !r.Enabled:
	case s.running[r.ID] != nil:
		v.Status = StatusRunning
	case s.errs[r.ID] != "":
		v.Status, v.LastError = StatusError, s.errs[r.ID]
	}
	return v
}

// List returns every rule ordered by name.
func (s *Service) List(ctx context.Context) ([]View, error) {
	rules, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]View, len(rules))
	for i, r := range rules {
		out[i] = s.view(r)
	}
	return out, nil
}

// Get returns one rule. ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (View, error) {
	r, err := s.repo.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view(r), nil
}

// Create validates and stores a rule and, when enabled, starts it. A rule that cannot listen is
// still created: its View is StatusError and it is retried. *ValidationError, *ConflictError,
// ErrNameTaken.
func (s *Service) Create(ctx context.Context, in CreateInput) (View, error) {
	var problems []string
	name, err := cleanName(in.Name)
	if err != nil {
		problems = append(problems, err.Error())
	}
	listen, err := cleanListenAddress(in.ListenAddress)
	if err != nil {
		problems = append(problems, err.Error())
	}
	host, err := cleanTargetHost(in.TargetHost)
	if err != nil {
		problems = append(problems, err.Error())
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	rule := Rule{Name: name, Protocol: in.Protocol, ListenAddress: listen, ListenPort: in.ListenPort,
		TargetHost: host, TargetPort: in.TargetPort, Enabled: in.Enabled == nil || *in.Enabled,
		CreatedAt: now, UpdatedAt: now}
	if problems = validate(rule, problems); len(problems) > 0 {
		return View{}, &ValidationError{Problems: problems}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return View{}, fmt.Errorf("new port forwarding rule id: %w", err)
	}
	rule.ID = id.String()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkConflict(ctx, rule); err != nil {
		return View{}, err
	}
	if err := s.repo.Create(ctx, rule); err != nil {
		return View{}, err
	}
	s.apply(rule)
	return s.view(rule), nil
}

// Update applies a partial update and brings the listeners in line with the result. A change of the
// listener or the target restarts a running rule; a rename or a switch of `enabled` alone does not
// touch anything else. *ValidationError, *ConflictError, ErrNotFound, ErrNameTaken.
func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, err := s.repo.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if in.IsEmpty() {
		return s.view(rule), nil
	}
	var problems []string
	if in.Name != nil {
		if rule.Name, err = cleanName(*in.Name); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if in.Protocol != nil {
		rule.Protocol = *in.Protocol
	}
	if in.ListenAddress != nil {
		if rule.ListenAddress, err = cleanListenAddress(*in.ListenAddress); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if in.ListenPort != nil {
		rule.ListenPort = *in.ListenPort
	}
	if in.TargetHost != nil {
		if rule.TargetHost, err = cleanTargetHost(*in.TargetHost); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if in.TargetPort != nil {
		rule.TargetPort = *in.TargetPort
	}
	if in.Enabled != nil {
		rule.Enabled = *in.Enabled
	}
	if problems = validate(rule, problems); len(problems) > 0 {
		return View{}, &ValidationError{Problems: problems}
	}
	if err := s.checkConflict(ctx, rule); err != nil {
		return View{}, err
	}
	rule.UpdatedAt = s.now().UTC().Truncate(time.Millisecond)
	if err := s.repo.Update(ctx, rule); err != nil {
		return View{}, err
	}
	s.apply(rule)
	return s.view(rule), nil
}

// Delete closes the rule's listeners and removes it. ErrNotFound.
func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	if r := s.running[id]; r != nil {
		r.stop()
		delete(s.running, id)
	}
	delete(s.errs, id)
	return nil
}

// checkConflict refuses an enabled rule that would bind the same socket as another enabled rule.
// A disabled rule reserves nothing. Caller holds mu.
func (s *Service) checkConflict(ctx context.Context, rule Rule) error {
	if !rule.Enabled {
		return nil
	}
	others, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	for _, o := range others {
		if o.ID != rule.ID && o.Enabled && overlaps(o, rule) {
			return &ConflictError{With: o.Name}
		}
	}
	return nil
}

package agentctl

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/devices"
)

// Health defaults.
const (
	DefaultHealthInterval    = time.Minute
	DefaultHealthConcurrency = 4
)

// HealthStore is what the health job needs (implemented by *devices.SQLiteRepository).
type HealthStore interface {
	ContactRecorder
	// BoundDevices lists the devices that have an agent binding.
	BoundDevices(ctx context.Context) ([]devices.Device, error)
}

// HealthConfig configures a Health job.
type HealthConfig struct {
	Store       HealthStore
	HTTP        *http.Client  // default NewHTTPClient()
	Interval    time.Duration // default DefaultHealthInterval
	Timeout     time.Duration // per request, default DefaultTimeout
	Concurrency int           // default DefaultHealthConcurrency
	Now         func() time.Time
	Log         *slog.Logger
	// Ticks replaces the interval timer (tests drive passes with a fake clock); nil = real ticker.
	Ticks <-chan time.Time
}

// Health keeps AgentBinding.status, version and last_ok_at current without any user action. Once
// per Interval, for every device with an agent binding:
//
//   - device currently `online`: GET /agent/v1/healthz (unauthenticated, protocol §6); the answer
//     must carry the bound agent_id, otherwise the agent is `unreachable` ("wrong agent");
//   - device `offline` and its agent still recorded `ready`: marked `unreachable` without any
//     request, because a machine that is off cannot run its agent;
//   - device `unknown`: left alone.
//
// Offline hosts are never contacted, so a house full of powered-off machines costs nothing.
type Health struct {
	cfg HealthConfig
	hc  *http.Client
	log *slog.Logger
}

// NewHealth returns a Health job.
func NewHealth(cfg HealthConfig) *Health {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultHealthInterval
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = DefaultHealthConcurrency
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	h := &Health{cfg: cfg, hc: cfg.HTTP, log: cfg.Log}
	if h.hc == nil {
		h.hc = NewHTTPClient()
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	return h
}

// Run does one pass at start and then one per Interval until ctx is cancelled. It starts no
// goroutine that outlives a pass, and a pass ends when ctx does.
func (h *Health) Run(ctx context.Context) error {
	h.Pass(ctx)
	ticks := h.cfg.Ticks
	if ticks == nil {
		t := time.NewTicker(h.cfg.Interval)
		defer t.Stop()
		ticks = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
			h.Pass(ctx)
		}
	}
}

// Pass checks every bound device once (bounded concurrency) and returns when all are done.
func (h *Health) Pass(ctx context.Context) {
	ds, err := h.cfg.Store.BoundDevices(ctx)
	if err != nil {
		if ctx.Err() == nil {
			h.log.Warn("agent health: listing bound devices failed", "err", err)
		}
		return
	}
	sem := make(chan struct{}, h.cfg.Concurrency)
	var wg sync.WaitGroup
	for _, d := range ds {
		if d.Agent == nil || ctx.Err() != nil {
			continue
		}
		switch d.Status {
		case devices.StatusOnline:
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				h.check(ctx, d)
			}()
		case devices.StatusOffline:
			if d.Agent.Status == devices.AgentReady {
				h.store(ctx, d, devices.AgentContact{OK: false, At: h.cfg.Now().UTC()}, "the device is offline")
			}
		}
	}
	wg.Wait()
}

type healthBody struct {
	Status  string `json:"status"`
	AgentID string `json:"agent_id"`
	Version string `json:"version"`
}

// check asks one agent for its health and records the outcome.
func (h *Health) check(ctx context.Context, d devices.Device) {
	ok, version, why := h.probe(ctx, d)
	if ctx.Err() != nil {
		return // shutting down: an aborted request says nothing about the agent
	}
	h.store(ctx, d, devices.AgentContact{OK: ok, Version: version, At: h.cfg.Now().UTC()}, why)
}

func (h *Health) probe(ctx context.Context, d devices.Device) (ok bool, version, why string) {
	target, err := agentURL(d, protocol.HealthPath)
	if err != nil {
		return false, "", err.Error()
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return false, "", err.Error()
	}
	r := do(ctx, h.hc, h.cfg.Timeout, req)
	if r.networkFailure() {
		_, msg := classify(r.Err, d, h.cfg.Timeout)
		return false, "", msg
	}
	var body healthBody
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &body) != nil || body.Status != "ok" {
		return false, "", "the service at " + hostPort(d) + " is not a healthy HomeCore agent"
	}
	if !strings.EqualFold(body.AgentID, d.Agent.AgentID) {
		return false, "", "wrong agent: the agent_id answering at " + hostPort(d) + " is not the bound one"
	}
	return true, clean(body.Version, 64), ""
}

// store records the contact and logs status changes (not every pass).
func (h *Health) store(ctx context.Context, d devices.Device, c devices.AgentContact, why string) {
	if err := h.cfg.Store.RecordAgentContact(ctx, d.ID, d.Agent.AgentID, c); err != nil {
		if ctx.Err() == nil {
			h.log.Warn("agent health: recording status failed", "device", d.ID, "err", err)
		}
		return
	}
	want := devices.AgentUnreachable
	if c.OK {
		want = devices.AgentReady
	}
	if d.Agent.Status != want {
		h.log.Info("agent status changed", "device", d.ID, "from", d.Agent.Status, "to", want, "why", why)
	}
}

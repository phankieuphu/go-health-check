// Package health provides configurable liveness and readiness checks.
//
// The core is framework-agnostic: CheckLiveness and CheckReadiness return a
// Report that can be served over net/http (see LivenessHandler and
// ReadinessHandler), gin (see the ginhealth package), gRPC, or anything else.
package health

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"time"
)

// Overall and per-check status values.
const (
	StatusOK       = "ok"
	StatusReady    = "ready"
	StatusNotReady = "not_ready"
	StatusDraining = "draining"
	StatusDown     = "down"
	StatusUp       = "up"
)

// CheckFunc probes one dependency. It should honor ctx, but a check that
// ignores ctx is still bounded by its timeout (it is abandoned, not awaited).
type CheckFunc func(ctx context.Context) error

// Check probes one dependency.
type Check struct {
	Name string
	// Critical checks gate readiness: if one fails, readiness reports
	// not_ready (HTTP 503). Non-critical checks are only reported, for
	// dependencies the service keeps working without (e.g. a cache).
	// Critical is ignored for liveness checks: every liveness check is critical.
	Critical bool
	// Timeout overrides the handler-wide timeout for this check. Zero means
	// use the handler default.
	Timeout time.Duration
	Func    CheckFunc
}

// Result is the outcome of one check.
type Result struct {
	Status     string `json:"status"`
	Critical   bool   `json:"critical"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// Report is the outcome of a liveness or readiness probe.
type Report struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Checks    map[string]Result `json:"checks,omitempty"`
	Info      map[string]any    `json:"info,omitempty"`
}

// Healthy reports whether the probe passed.
func (r Report) Healthy() bool {
	return r.Status == StatusOK || r.Status == StatusReady
}

// Handler runs registered checks. It is safe for concurrent use, and checks
// can be added or removed at runtime.
type Handler struct {
	cfg config

	mu        sync.RWMutex
	readiness map[string]Check
	liveness  map[string]Check

	draining atomic.Bool

	cacheMu     sync.Mutex
	cached      Report
	cacheExpiry time.Time
}

// New builds a Handler. Invalid checks passed through options (empty name or
// nil Func) cause a panic, since they are programmer errors at startup; use
// AddReadinessCheck for checks registered at runtime.
func New(opts ...Option) *Handler {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	h := &Handler{
		cfg:       cfg,
		readiness: make(map[string]Check),
		liveness:  make(map[string]Check),
	}
	for _, c := range cfg.readinessChecks {
		if err := h.AddReadinessCheck(c); err != nil {
			panic(err)
		}
	}
	for _, c := range cfg.livenessChecks {
		if err := h.AddLivenessCheck(c); err != nil {
			panic(err)
		}
	}
	return h
}

// AddReadinessCheck registers a readiness check, replacing any existing
// check with the same name.
func (h *Handler) AddReadinessCheck(c Check) error {
	return h.add(h.readiness, c)
}

// AddLivenessCheck registers a liveness check, replacing any existing check
// with the same name. Liveness checks must only cover the process itself
// (deadlocks, goroutine leaks), never external dependencies: a database
// outage would otherwise make the orchestrator restart every replica, which
// fixes nothing.
func (h *Handler) AddLivenessCheck(c Check) error {
	return h.add(h.liveness, c)
}

// RemoveReadinessCheck unregisters a readiness check by name.
func (h *Handler) RemoveReadinessCheck(name string) {
	h.mu.Lock()
	delete(h.readiness, name)
	h.mu.Unlock()
	h.invalidateCache()
}

// RemoveLivenessCheck unregisters a liveness check by name.
func (h *Handler) RemoveLivenessCheck(name string) {
	h.mu.Lock()
	delete(h.liveness, name)
	h.mu.Unlock()
}

func (h *Handler) add(set map[string]Check, c Check) error {
	if c.Name == "" {
		return errors.New("health: check name is empty")
	}
	if c.Func == nil {
		return fmt.Errorf("health: check %q has nil Func", c.Name)
	}
	h.mu.Lock()
	set[c.Name] = c
	h.mu.Unlock()
	h.invalidateCache()
	return nil
}

// SetDraining marks the service as shutting down (or not). While draining,
// readiness fails without running checks, so the load balancer stops sending
// traffic before the server stops accepting it. Liveness is unaffected.
func (h *Handler) SetDraining(draining bool) {
	h.draining.Store(draining)
}

// CheckLiveness runs every liveness check. With none registered it only
// reports that the process is up.
func (h *Handler) CheckLiveness(ctx context.Context) Report {
	h.mu.RLock()
	checks := snapshot(h.liveness)
	h.mu.RUnlock()

	results := h.run(ctx, checks)
	status := StatusOK
	for _, res := range results {
		if res.Status != StatusUp {
			status = StatusDown
		}
	}
	return Report{Status: status, Timestamp: time.Now(), Checks: results, Info: h.cfg.info}
}

// CheckReadiness runs every readiness check concurrently and reports
// not_ready if any critical one fails. With a cache TTL configured, results
// are reused within the TTL and concurrent callers share one run.
func (h *Handler) CheckReadiness(ctx context.Context) Report {
	if h.draining.Load() {
		return Report{Status: StatusDraining, Timestamp: time.Now(), Info: h.cfg.info}
	}
	if h.cfg.cacheTTL <= 0 {
		return h.checkReadiness(ctx)
	}

	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if time.Now().Before(h.cacheExpiry) {
		return h.cached
	}
	// The result is shared with other callers, so one caller hanging up
	// must not cancel it; per-check timeouts still bound the run.
	h.cached = h.checkReadiness(context.WithoutCancel(ctx))
	h.cacheExpiry = time.Now().Add(h.cfg.cacheTTL)
	return h.cached
}

func (h *Handler) checkReadiness(ctx context.Context) Report {
	h.mu.RLock()
	checks := snapshot(h.readiness)
	h.mu.RUnlock()

	results := h.run(ctx, checks)
	status := StatusReady
	for _, res := range results {
		if res.Critical && res.Status != StatusUp {
			status = StatusNotReady
		}
	}
	return Report{Status: status, Timestamp: time.Now(), Checks: results, Info: h.cfg.info}
}

func (h *Handler) invalidateCache() {
	h.cacheMu.Lock()
	h.cacheExpiry = time.Time{}
	h.cacheMu.Unlock()
}

func snapshot(set map[string]Check) []Check {
	checks := make([]Check, 0, len(set))
	for _, c := range set {
		checks = append(checks, c)
	}
	return checks
}

// run executes checks concurrently, each bounded by its own timeout.
func (h *Handler) run(ctx context.Context, checks []Check) map[string]Result {
	results := make(map[string]Result, len(checks))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, check := range checks {
		wg.Go(func() {
			timeout := check.Timeout
			if timeout <= 0 {
				timeout = h.cfg.timeout
			}
			checkCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			start := time.Now()
			err := runCheck(checkCtx, check.Func)
			res := Result{
				Status:     StatusUp,
				Critical:   check.Critical,
				DurationMS: time.Since(start).Milliseconds(),
			}
			if err != nil {
				res.Status, res.Error = StatusDown, err.Error()
			}
			for _, observe := range h.cfg.observers {
				observe(check.Name, res, err)
			}

			mu.Lock()
			results[check.Name] = res
			mu.Unlock()
		})
	}
	wg.Wait()
	return results
}

// runCheck enforces ctx's deadline even on checks that ignore ctx; such a
// check is abandoned, not awaited. A panicking check is reported as failed.
func runCheck(ctx context.Context, check CheckFunc) error {
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("check panicked: %v", r)
			}
		}()
		done <- check(ctx)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// redacted returns a copy of r with error messages removed.
func redacted(r Report) Report {
	r.Checks = maps.Clone(r.Checks)
	for name, res := range r.Checks {
		if res.Error != "" {
			res.Error = "check failed"
			r.Checks[name] = res
		}
	}
	return r
}

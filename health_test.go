package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func ok(context.Context) error   { return nil }
func fail(context.Context) error { return errors.New("dial tcp 10.0.0.1:5432: refused") }

func TestReadiness(t *testing.T) {
	tests := []struct {
		name   string
		checks []Check
		want   string
	}{
		{"no checks", nil, StatusReady},
		{"all up", []Check{{Name: "db", Critical: true, Func: ok}}, StatusReady},
		{"non-critical down", []Check{{Name: "db", Critical: true, Func: ok}, {Name: "cache", Func: fail}}, StatusReady},
		{"critical down", []Check{{Name: "db", Critical: true, Func: fail}, {Name: "cache", Func: ok}}, StatusNotReady},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := New(WithChecks(tt.checks...)).CheckReadiness(context.Background())
			if report.Status != tt.want {
				t.Fatalf("status = %q, want %q", report.Status, tt.want)
			}
			if len(report.Checks) != len(tt.checks) {
				t.Fatalf("got %d results, want %d", len(report.Checks), len(tt.checks))
			}
		})
	}
}

func TestTimeoutAbandonsCheckIgnoringContext(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	h := New(WithTimeout(time.Hour), WithChecks(Check{
		Name: "hung", Critical: true, Timeout: 20 * time.Millisecond,
		Func: func(context.Context) error { <-block; return nil },
	}))

	start := time.Now()
	report := h.CheckReadiness(context.Background())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("readiness took %v, want per-check timeout to apply", elapsed)
	}
	if report.Status != StatusNotReady || report.Checks["hung"].Error != context.DeadlineExceeded.Error() {
		t.Fatalf("got %+v", report)
	}
}

func TestPanickingCheckFails(t *testing.T) {
	h := New(WithChecks(Check{Name: "boom", Critical: true, Func: func(context.Context) error { panic("bad") }}))
	if report := h.CheckReadiness(context.Background()); report.Status != StatusNotReady {
		t.Fatalf("status = %q, want not_ready", report.Status)
	}
}

func TestDraining(t *testing.T) {
	h := New(WithChecks(Check{Name: "db", Critical: true, Func: ok}))
	h.SetDraining(true)
	if got := h.CheckReadiness(context.Background()).Status; got != StatusDraining {
		t.Fatalf("readiness = %q, want draining", got)
	}
	if got := h.CheckLiveness(context.Background()).Status; got != StatusOK {
		t.Fatalf("liveness = %q, want ok while draining", got)
	}
}

func TestCacheTTL(t *testing.T) {
	var calls atomic.Int32
	h := New(WithCacheTTL(time.Hour), WithChecks(Check{Name: "db", Func: func(context.Context) error {
		calls.Add(1)
		return nil
	}}))
	for range 3 {
		h.CheckReadiness(context.Background())
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("check ran %d times, want 1", n)
	}

	// Changing the checks invalidates the cache.
	h.RemoveReadinessCheck("missing")
	h.CheckReadiness(context.Background())
	if n := calls.Load(); n != 2 {
		t.Fatalf("check ran %d times after invalidation, want 2", n)
	}
}

func TestRuntimeRegistration(t *testing.T) {
	h := New()
	if err := h.AddReadinessCheck(Check{Name: "db", Critical: true, Func: fail}); err != nil {
		t.Fatal(err)
	}
	if got := h.CheckReadiness(context.Background()).Status; got != StatusNotReady {
		t.Fatalf("status = %q, want not_ready", got)
	}
	h.RemoveReadinessCheck("db")
	if got := h.CheckReadiness(context.Background()).Status; got != StatusReady {
		t.Fatalf("status = %q, want ready", got)
	}
	if err := h.AddReadinessCheck(Check{Name: "", Func: ok}); err == nil {
		t.Fatal("want error for empty name")
	}
	if err := h.AddReadinessCheck(Check{Name: "x"}); err == nil {
		t.Fatal("want error for nil func")
	}
}

func TestLivenessChecks(t *testing.T) {
	h := New(WithLivenessChecks(Check{Name: "deadlock", Func: fail}))
	if got := h.CheckLiveness(context.Background()).Status; got != StatusDown {
		t.Fatalf("status = %q, want down", got)
	}
}

func TestObserver(t *testing.T) {
	var seen atomic.Int32
	h := New(
		WithChecks(Check{Name: "a", Func: ok}, Check{Name: "b", Func: fail}),
		WithObserver(func(string, Result, error) { seen.Add(1) }),
	)
	h.CheckReadiness(context.Background())
	if n := seen.Load(); n != 2 {
		t.Fatalf("observer called %d times, want 2", n)
	}
}

func TestHTTP(t *testing.T) {
	h := New(
		WithPaths("/live", "/ready"),
		WithErrorDetails(false),
		WithInfo(map[string]any{"version": "1.2.3"}),
		WithChecks(Check{Name: "db", Critical: true, Func: fail}),
	)
	mux := http.NewServeMux()
	h.RegisterMux(mux)

	tests := []struct {
		path string
		code int
	}{
		{"/live", http.StatusOK},
		{"/ready", http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if rec.Code != tt.code {
			t.Fatalf("%s: code = %d, want %d", tt.path, rec.Code, tt.code)
		}
		var report Report
		if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report.Info["version"] != "1.2.3" {
			t.Fatalf("%s: info = %v", tt.path, report.Info)
		}
		if res, ok := report.Checks["db"]; ok && res.Error != "check failed" {
			t.Fatalf("%s: error not redacted: %q", tt.path, res.Error)
		}
	}
}

func TestConfigOptions(t *testing.T) {
	cfg := Config{Timeout: time.Second, LivenessPath: "/l", HideErrors: true}
	h := New(cfg.Options()...)
	if h.LivenessPath() != "/l" || h.ReadinessPath() != DefaultReadinessPath {
		t.Fatalf("paths = %q, %q", h.LivenessPath(), h.ReadinessPath())
	}
	if h.cfg.timeout != time.Second || h.cfg.showErrors {
		t.Fatalf("cfg = %+v", h.cfg)
	}
}

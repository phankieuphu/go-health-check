package health

import (
	"maps"
	"time"
)

// Defaults used when the corresponding option is not set.
const (
	DefaultTimeout       = 2 * time.Second
	DefaultLivenessPath  = "/healthz"
	DefaultReadinessPath = "/readyz"
)

// Observer is called after every check run, e.g. to export metrics or log
// failures. err is the raw error (nil on success). It must be safe for
// concurrent use and should return quickly.
type Observer func(name string, res Result, err error)

// Option configures a Handler.
type Option func(*config)

type config struct {
	timeout         time.Duration
	cacheTTL        time.Duration
	livenessPath    string
	readinessPath   string
	showErrors      bool
	info            map[string]any
	observers       []Observer
	readinessChecks []Check
	livenessChecks  []Check
}

func defaultConfig() config {
	return config{
		timeout:       DefaultTimeout,
		livenessPath:  DefaultLivenessPath,
		readinessPath: DefaultReadinessPath,
		showErrors:    true,
	}
}

// WithTimeout sets the default per-check timeout, so a hung dependency makes
// readiness fail fast instead of stalling the probe. Check.Timeout overrides it.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithChecks registers readiness checks.
func WithChecks(checks ...Check) Option {
	return func(c *config) { c.readinessChecks = append(c.readinessChecks, checks...) }
}

// WithLivenessChecks registers liveness checks. See Handler.AddLivenessCheck
// for what belongs here.
func WithLivenessChecks(checks ...Check) Option {
	return func(c *config) { c.livenessChecks = append(c.livenessChecks, checks...) }
}

// WithCacheTTL reuses readiness results for d, so frequent probes from many
// sources don't hammer dependencies. Zero (the default) disables caching.
func WithCacheTTL(d time.Duration) Option {
	return func(c *config) { c.cacheTTL = d }
}

// WithPaths sets the routes used by RegisterMux and the framework adapters.
// Empty values keep the defaults.
func WithPaths(liveness, readiness string) Option {
	return func(c *config) {
		if liveness != "" {
			c.livenessPath = liveness
		}
		if readiness != "" {
			c.readinessPath = readiness
		}
	}
}

// WithErrorDetails controls whether check error messages appear in HTTP
// responses. Disable it when probes are reachable from outside, since errors
// can leak hostnames or credentials. Observers always receive the raw error.
func WithErrorDetails(show bool) Option {
	return func(c *config) { c.showErrors = show }
}

// WithInfo adds static metadata (version, commit, region...) to every report.
func WithInfo(info map[string]any) Option {
	return func(c *config) {
		if c.info == nil {
			c.info = make(map[string]any, len(info))
		}
		maps.Copy(c.info, info)
	}
}

// WithObserver adds a hook called after every check run.
func WithObserver(o Observer) Option {
	return func(c *config) { c.observers = append(c.observers, o) }
}

// Config is a plain-struct form of the options, for loading settings from a
// file or environment. Zero values keep the defaults.
type Config struct {
	Timeout       time.Duration  `json:"timeout" yaml:"timeout"`
	CacheTTL      time.Duration  `json:"cache_ttl" yaml:"cache_ttl"`
	LivenessPath  string         `json:"liveness_path" yaml:"liveness_path"`
	ReadinessPath string         `json:"readiness_path" yaml:"readiness_path"`
	HideErrors    bool           `json:"hide_errors" yaml:"hide_errors"`
	Info          map[string]any `json:"info" yaml:"info"`
}

// Options converts cfg to options, to combine with others in New.
func (cfg Config) Options() []Option {
	return []Option{
		WithTimeout(cfg.Timeout),
		WithCacheTTL(cfg.CacheTTL),
		WithPaths(cfg.LivenessPath, cfg.ReadinessPath),
		WithErrorDetails(!cfg.HideErrors),
		WithInfo(cfg.Info),
	}
}

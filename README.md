# go-health-check

Configurable liveness (`/healthz`) and readiness (`/readyz`) probes for Go services.

- Framework-agnostic core (`net/http`), plus a gin adapter (`ginhealth`)
- Critical vs. non-critical checks: only critical failures make readiness return 503
- Global and per-check timeouts; a check that ignores `ctx` is abandoned, not awaited
- Panicking checks are reported as failed instead of crashing the process
- Add/remove checks at runtime
- Graceful draining: `SetDraining(true)` fails readiness before shutdown
- Optional result caching (`WithCacheTTL`) so frequent probes don't hammer dependencies
- Observer hook for metrics and logging
- Error redaction for publicly reachable probes
- Built-in checks: HTTP, TCP, DNS, `PingContext` (`*sql.DB`, ...), goroutine limit

## Install

```sh
go get github.com/phankieuphu/go-health-check
```

## Usage

```go
h := health.New(
    health.WithTimeout(2*time.Second),
    health.WithChecks(
        health.Check{Name: "postgres", Critical: true, Func: checks.Ping(db)},
        health.Check{Name: "redis", Critical: false, Func: func(ctx context.Context) error {
            return rdb.Ping(ctx).Err()
        }},
        health.Check{Name: "billing-api", Critical: true, Timeout: 500 * time.Millisecond,
            Func: checks.HTTP("http://billing/healthz")},
    ),
)

// gin
ginhealth.RegisterRoutes(router, h)

// or net/http
h.RegisterMux(mux)
```

See [examples/basic](examples/basic/main.go) for a full service with graceful shutdown.

### Options

| Option | Default | Purpose |
|---|---|---|
| `WithTimeout(d)` | 2s | Default per-check timeout (`Check.Timeout` overrides it) |
| `WithChecks(...)` | none | Readiness checks |
| `WithLivenessChecks(...)` | none | Liveness checks, for process health only (deadlocks, leaks) |
| `WithCacheTTL(d)` | 0 (off) | Reuse readiness results; concurrent probes share one run |
| `WithPaths(live, ready)` | `/healthz`, `/readyz` | Routes used by `RegisterMux` and `ginhealth` |
| `WithErrorDetails(bool)` | true | Set false to hide error messages in responses |
| `WithInfo(map)` | none | Static metadata (version, commit) in every response |
| `WithObserver(fn)` | none | Called after each check, e.g. for Prometheus metrics |

### Config from a file or env

`health.Config` has `json`/`yaml` tags and converts to options:

```go
var cfg health.Config // fill it from YAML, JSON, envconfig, ...
h := health.New(append(cfg.Options(), health.WithChecks(myChecks...))...)
```

### Runtime changes

```go
h.AddReadinessCheck(health.Check{Name: "kafka", Func: kafkaCheck})
h.RemoveReadinessCheck("kafka")
h.SetDraining(true) // on SIGTERM, before srv.Shutdown
```

### Response

```json
{
  "status": "not_ready",
  "timestamp": "2026-10-05T10:00:00Z",
  "checks": {
    "postgres": {"status": "down", "critical": true, "error": "context deadline exceeded", "duration_ms": 2000},
    "redis":    {"status": "up", "critical": false, "duration_ms": 1}
  },
  "info": {"version": "1.0.0"}
}
```

Status codes: 200 for `ok`/`ready`, 503 for `down`/`not_ready`/`draining`.

## Liveness vs. readiness

Don't put dependency checks in liveness. If Postgres goes down, a failing liveness probe makes the orchestrator restart every replica, which fixes nothing. Dependencies belong in readiness, which only takes the pod out of the load balancer.

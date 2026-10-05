// Package checks provides ready-made health.CheckFunc implementations for
// common dependencies. Each returns a health.CheckFunc; wrap it in a
// health.Check to give it a name, criticality and timeout.
package checks

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"

	health "github.com/phankieuphu/go-health-check"
)

// Pinger is satisfied by *sql.DB, *sql.Conn, *pgxpool.Pool and similar clients.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Ping checks a client that implements PingContext, such as *sql.DB.
func Ping(p Pinger) health.CheckFunc {
	return p.PingContext
}

// HTTPOption configures HTTP.
type HTTPOption func(*httpCheck)

type httpCheck struct {
	client *http.Client
	method string
	header http.Header
	accept func(code int) bool
}

// WithClient sets the HTTP client (default http.DefaultClient).
func WithClient(c *http.Client) HTTPOption {
	return func(h *httpCheck) { h.client = c }
}

// WithMethod sets the request method (default GET).
func WithMethod(m string) HTTPOption {
	return func(h *httpCheck) { h.method = m }
}

// WithHeader adds a request header, e.g. for auth.
func WithHeader(key, value string) HTTPOption {
	return func(h *httpCheck) { h.header.Add(key, value) }
}

// WithStatusCodes replaces the default "any 2xx" acceptance rule.
func WithStatusCodes(codes ...int) HTTPOption {
	return func(h *httpCheck) {
		h.accept = func(code int) bool {
			for _, c := range codes {
				if c == code {
					return true
				}
			}
			return false
		}
	}
}

// HTTP checks that a request to url succeeds with an accepted status code.
func HTTP(url string, opts ...HTTPOption) health.CheckFunc {
	hc := &httpCheck{
		client: http.DefaultClient,
		method: http.MethodGet,
		header: make(http.Header),
		accept: func(code int) bool { return code >= 200 && code < 300 },
	}
	for _, opt := range opts {
		opt(hc)
	}
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, hc.method, url, nil)
		if err != nil {
			return err
		}
		req.Header = hc.header.Clone()
		resp, err := hc.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		if !hc.accept(resp.StatusCode) {
			return fmt.Errorf("unexpected status %d", resp.StatusCode)
		}
		return nil
	}
}

// TCP checks that addr ("host:port") accepts connections.
func TCP(addr string) health.CheckFunc {
	return func(ctx context.Context) error {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return conn.Close()
	}
}

// DNS checks that host resolves to at least one address.
func DNS(host string) health.CheckFunc {
	return func(ctx context.Context) error {
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return err
		}
		if len(addrs) == 0 {
			return fmt.Errorf("no addresses for %s", host)
		}
		return nil
	}
}

// GoroutineLimit fails when the process runs more than max goroutines. It is
// meant as a liveness check, to restart a process that is leaking goroutines.
func GoroutineLimit(max int) health.CheckFunc {
	return func(context.Context) error {
		if n := runtime.NumGoroutine(); n > max {
			return fmt.Errorf("%d goroutines exceeds limit %d", n, max)
		}
		return nil
	}
}

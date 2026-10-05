// Command basic shows a gin service wired with liveness and readiness checks
// and graceful draining on shutdown.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	health "github.com/phankieuphu/go-health-check"
	"github.com/phankieuphu/go-health-check/checks"
	"github.com/phankieuphu/go-health-check/ginhealth"
)

func main() {
	h := health.New(
		health.WithTimeout(2*time.Second),
		health.WithCacheTTL(time.Second),
		health.WithInfo(map[string]any{"service": "example", "version": "1.0.0"}),
		health.WithChecks(
			health.Check{Name: "upstream", Critical: true, Func: checks.HTTP("https://example.com")},
			health.Check{Name: "dns", Critical: false, Timeout: 500 * time.Millisecond, Func: checks.DNS("example.com")},
		),
		health.WithLivenessChecks(
			health.Check{Name: "goroutines", Func: checks.GoroutineLimit(10_000)},
		),
		health.WithObserver(func(name string, res health.Result, err error) {
			if err != nil {
				log.Printf("health check %s failed in %dms: %v", name, res.DurationMS, err)
			}
		}),
	)

	r := gin.New()
	ginhealth.RegisterRoutes(r, h)

	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	// Fail readiness first so the load balancer stops routing here, then
	// give it time to notice before closing the listener.
	h.SetDraining(true)
	time.Sleep(5 * time.Second)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

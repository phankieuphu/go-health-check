package ginhealth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	health "github.com/phankieuphu/go-health-check"
)

func TestRegisterRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := health.New(health.WithChecks(health.Check{
		Name: "db", Critical: true,
		Func: func(context.Context) error { return errors.New("down") },
	}))
	r := gin.New()
	RegisterRoutes(r, h)

	for path, want := range map[string]int{
		"/healthz": http.StatusOK,
		"/readyz":  http.StatusServiceUnavailable,
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: code = %d, want %d", path, rec.Code, want)
		}
	}
}

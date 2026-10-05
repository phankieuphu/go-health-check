// Package ginhealth mounts a health.Handler on a gin router.
package ginhealth

import (
	"github.com/gin-gonic/gin"

	health "github.com/phankieuphu/go-health-check"
)

// RegisterRoutes mounts liveness and readiness at the handler's configured
// paths (default /healthz and /readyz).
func RegisterRoutes(r gin.IRoutes, h *health.Handler) {
	r.GET(h.LivenessPath(), Liveness(h))
	r.GET(h.ReadinessPath(), Readiness(h))
}

// Liveness returns a gin handler for the liveness probe, for custom routing.
func Liveness(h *health.Handler) gin.HandlerFunc {
	return gin.WrapH(h.LivenessHandler())
}

// Readiness returns a gin handler for the readiness probe, for custom routing.
func Readiness(h *health.Handler) gin.HandlerFunc {
	return gin.WrapH(h.ReadinessHandler())
}

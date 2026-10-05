package health

import (
	"encoding/json"
	"net/http"
)

// LivenessPath is the configured liveness route.
func (h *Handler) LivenessPath() string { return h.cfg.livenessPath }

// ReadinessPath is the configured readiness route.
func (h *Handler) ReadinessPath() string { return h.cfg.readinessPath }

// LivenessHandler serves the liveness report: 200 when healthy, 503 otherwise.
func (h *Handler) LivenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.write(w, h.CheckLiveness(r.Context()))
	})
}

// ReadinessHandler serves the readiness report: 200 when ready, 503 otherwise.
func (h *Handler) ReadinessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.write(w, h.CheckReadiness(r.Context()))
	})
}

// RegisterMux mounts both handlers on mux at the configured paths.
func (h *Handler) RegisterMux(mux *http.ServeMux) {
	mux.Handle("GET "+h.cfg.livenessPath, h.LivenessHandler())
	mux.Handle("GET "+h.cfg.readinessPath, h.ReadinessHandler())
}

func (h *Handler) write(w http.ResponseWriter, report Report) {
	if !h.cfg.showErrors {
		report = redacted(report)
	}
	code := http.StatusOK
	if !report.Healthy() {
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(report)
}

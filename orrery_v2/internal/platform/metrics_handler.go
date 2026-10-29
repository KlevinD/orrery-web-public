package platform

import (
	"net/http"

	"orrery_v2/internal/platform/metrics"
)

func NewMetricsHandler(runtime *Runtime) http.Handler {
	if runtime == nil || runtime.MetricsRegistry == nil {
		return http.NotFoundHandler()
	}
	return metrics.NewHandler(runtime.MetricsRegistry.Gatherer)
}

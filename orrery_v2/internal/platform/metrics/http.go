package metrics

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"orrery_v2/internal/platform/clock"
)

type HTTPMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
	clock    clock.Clock
}

type routePatternKey struct{}

type HTTPOptions struct {
	Clock clock.Clock
}

func NewHandler(gatherer prometheus.Gatherer) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		ErrorHandling:     promhttp.HTTPErrorOnError,
	}))
	return mux
}

func NewHTTPMetrics() *HTTPMetrics {
	return NewHTTPMetricsWithOptions(HTTPOptions{})
}

func NewHTTPMetricsWithOptions(opts HTTPOptions) *HTTPMetrics {
	return &HTTPMetrics{
		clock: metricsClock(opts.Clock),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "orrery",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total HTTP requests served by the API server.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "orrery",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration observed by the API server.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"method", "route", "status"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "orrery",
			Subsystem: "http",
			Name:      "in_flight_requests",
			Help:      "Current number of in-flight HTTP requests served by the API server.",
		}),
	}
}

func (m *HTTPMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{m.requests, m.duration, m.inFlight}
}

func (m *HTTPMetrics) Middleware(next http.Handler) http.Handler {
	if m == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isHealthProbePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		m.inFlight.Inc()
		defer m.inFlight.Dec()

		start := m.now()
		ww := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)

		labels := prometheus.Labels{
			"method": r.Method,
			"route":  routePattern(r),
			"status": strconv.Itoa(ww.status),
		}
		m.requests.With(labels).Inc()
		m.duration.With(labels).Observe(m.now().Sub(start).Seconds())
	})
}

func (m *HTTPMetrics) now() time.Time {
	if m == nil || m.clock == nil {
		return time.Now().UTC()
	}
	return m.clock.Now().UTC()
}

func metricsClock(c clock.Clock) clock.Clock {
	if c != nil {
		return c
	}
	return clock.Real{}
}

func WithRoutePattern(r *http.Request, pattern string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), routePatternKey{}, pattern))
}

func routePattern(r *http.Request) string {
	if pattern, ok := r.Context().Value(routePatternKey{}).(string); ok && pattern != "" {
		return pattern
	}
	if r.URL != nil && r.URL.Path != "" {
		return r.URL.Path
	}
	return "unmatched"
}

func isHealthProbePath(path string) bool {
	switch path {
	case "/health", "/healthz", "/readyz":
		return true
	default:
		return false
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

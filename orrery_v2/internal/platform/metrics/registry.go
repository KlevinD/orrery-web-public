package metrics

import (
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

type RegistryOptions struct {
	Version      string
	Commit       string
	Databases    map[string]DBStatsProvider
	StatusGroups []StatusGroup
	HTTP         HTTPOptions
}

type Registry struct {
	Gatherer prometheus.Gatherer
	HTTP     *HTTPMetrics
}

func NewRegistry(opts RegistryOptions) *Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "orrery",
		Name:      "build_info",
		Help:      "Build information for the Orrery server binary.",
	}, []string{"version", "commit"})
	buildInfo.WithLabelValues(defaultLabel(opts.Version, "dev"), defaultLabel(opts.Commit, "unknown")).Set(1)
	reg.MustRegister(buildInfo)

	httpMetrics := NewHTTPMetricsWithOptions(opts.HTTP)
	reg.MustRegister(httpMetrics.Collectors()...)
	if len(opts.Databases) > 0 {
		reg.MustRegister(NewDBCollector(opts.Databases))
	}
	for _, group := range opts.StatusGroups {
		reg.MustRegister(NewStatusCollector(group))
	}

	return &Registry{Gatherer: reg, HTTP: httpMetrics}
}

func metricNamePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var builder strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			builder.WriteRune(r)
			continue
		}
		builder.WriteRune('_')
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "unknown"
	}
	return result
}

func fqMetricName(subsystem, name string) string {
	return fmt.Sprintf("orrery_%s_%s", metricNamePart(subsystem), metricNamePart(name))
}

func defaultLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

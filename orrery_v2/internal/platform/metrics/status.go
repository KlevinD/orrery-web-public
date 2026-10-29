package metrics

import (
	"context"
	"sort"

	"github.com/prometheus/client_golang/prometheus"
)

type StatusCountsProvider interface {
	StatusCounts(context.Context) (map[string]int64, error)
}

type StatusGroup struct {
	Subsystem string
	Name      string
	Help      string
	Providers map[string]StatusCountsProvider
}

type StatusCollector struct {
	group     StatusGroup
	desc      *prometheus.Desc
	errorDesc *prometheus.Desc
}

func NewStatusCollector(group StatusGroup) *StatusCollector {
	if group.Help == "" {
		group.Help = "Items by status."
	}
	name := fqMetricName(group.Subsystem, group.Name)
	return &StatusCollector{
		group:     group,
		desc:      prometheus.NewDesc(name, group.Help, []string{"role", "status"}, nil),
		errorDesc: prometheus.NewDesc(name+"_scrape_errors_total", "Total status collector scrape errors.", []string{"role"}, nil),
	}
}

func (c *StatusCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
	ch <- c.errorDesc
}

func (c *StatusCollector) Collect(ch chan<- prometheus.Metric) {
	roles := make([]string, 0, len(c.group.Providers))
	for role := range c.group.Providers {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		provider := c.group.Providers[role]
		if provider == nil {
			continue
		}
		counts, err := provider.StatusCounts(context.Background())
		if err != nil {
			ch <- prometheus.MustNewConstMetric(c.errorDesc, prometheus.CounterValue, 1, role)
			continue
		}
		statuses := make([]string, 0, len(counts))
		for status := range counts {
			statuses = append(statuses, status)
		}
		sort.Strings(statuses)
		for _, status := range statuses {
			ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(counts[status]), role, status)
		}
	}
}

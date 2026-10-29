package metrics

import (
	"database/sql"
	"sort"

	"github.com/prometheus/client_golang/prometheus"
)

type DBStatsProvider interface {
	Stats() sql.DBStats
}

type DBCollector struct {
	providers map[string]DBStatsProvider

	openConnections  *prometheus.Desc
	inUseConnections *prometheus.Desc
	idleConnections  *prometheus.Desc
	waitCount        *prometheus.Desc
	waitDuration     *prometheus.Desc
}

func NewDBCollector(providers map[string]DBStatsProvider) *DBCollector {
	copied := make(map[string]DBStatsProvider, len(providers))
	for role, provider := range providers {
		if role != "" && provider != nil {
			copied[role] = provider
		}
	}
	return &DBCollector{
		providers:        copied,
		openConnections:  prometheus.NewDesc("orrery_db_open_connections", "Open database connections by configured database role.", []string{"role"}, nil),
		inUseConnections: prometheus.NewDesc("orrery_db_in_use_connections", "In-use database connections by configured database role.", []string{"role"}, nil),
		idleConnections:  prometheus.NewDesc("orrery_db_idle_connections", "Idle database connections by configured database role.", []string{"role"}, nil),
		waitCount:        prometheus.NewDesc("orrery_db_wait_count_total", "Total waits for a database connection by configured database role.", []string{"role"}, nil),
		waitDuration:     prometheus.NewDesc("orrery_db_wait_duration_seconds_total", "Total time waiting for a database connection by configured database role.", []string{"role"}, nil),
	}
}

func (c *DBCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{c.openConnections, c.inUseConnections, c.idleConnections, c.waitCount, c.waitDuration} {
		ch <- desc
	}
}

func (c *DBCollector) Collect(ch chan<- prometheus.Metric) {
	roles := make([]string, 0, len(c.providers))
	for role := range c.providers {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		stats := c.providers[role].Stats()
		ch <- prometheus.MustNewConstMetric(c.openConnections, prometheus.GaugeValue, float64(stats.OpenConnections), role)
		ch <- prometheus.MustNewConstMetric(c.inUseConnections, prometheus.GaugeValue, float64(stats.InUse), role)
		ch <- prometheus.MustNewConstMetric(c.idleConnections, prometheus.GaugeValue, float64(stats.Idle), role)
		ch <- prometheus.MustNewConstMetric(c.waitCount, prometheus.CounterValue, float64(stats.WaitCount), role)
		ch <- prometheus.MustNewConstMetric(c.waitDuration, prometheus.CounterValue, stats.WaitDuration.Seconds(), role)
	}
}

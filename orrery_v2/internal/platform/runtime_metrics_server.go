package platform

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/metrics"
	"orrery_v2/internal/platform/tracing"
)

func (r *Runtime) startMetricsServer(cfg config.Config) error {
	if strings.TrimSpace(cfg.Metrics.Addr) == "" {
		return nil
	}
	server, errCh, err := startMetricsHTTPServer(cfg.Metrics.Addr, r.MetricsRegistry.Gatherer)
	if err != nil {
		return err
	}
	r.MetricsServer = server
	r.metricsErr = errCh
	return nil
}

func startMetricsHTTPServer(addr string, gatherer prometheus.Gatherer) (*http.Server, chan error, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("start metrics server: %w", err)
	}
	server := metrics.NewServer(addr, gatherer)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()
	return server, errCh, nil
}

func shutdownMetricsServer(server *http.Server, errCh chan error) error {
	if server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(ctx)
	if errCh == nil {
		return shutdownErr
	}
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) && shutdownErr == nil {
			return err
		}
	case <-time.After(50 * time.Millisecond):
	}
	return shutdownErr
}

func (r *Runtime) activeMetricsAddr() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.MetricsServer == nil {
		return ""
	}
	return r.MetricsServer.Addr
}

func (r *Runtime) metricOptions() metrics.RegistryOptions {
	opts := metrics.RegistryOptions{Databases: metricDatabases(r.Databases)}
	if r == nil {
		return opts
	}
	opts.HTTP = metrics.HTTPOptions{Clock: r.Clock}
	if r.Outbox != nil {
		opts.StatusGroups = append(opts.StatusGroups, metrics.StatusGroup{
			Subsystem: "outbox",
			Name:      "jobs",
			Help:      "Outbox jobs by status.",
			Providers: map[string]metrics.StatusCountsProvider{db.RoleJobs: r.Outbox},
		})
	}
	if r.Execution != nil && r.Execution.Queue != nil {
		opts.StatusGroups = append(opts.StatusGroups, metrics.StatusGroup{
			Subsystem: "execution",
			Name:      "jobs",
			Help:      "Execution queue jobs by status.",
			Providers: map[string]metrics.StatusCountsProvider{db.RoleJobs: r.Execution.Queue},
		})
	}
	opts.StatusGroups = append(opts.StatusGroups, r.metricStatusGroups...)
	return opts
}

func (r *Runtime) tracingOptions() tracing.SpanOptions {
	if r == nil {
		return tracing.SpanOptions{}
	}
	return tracing.SpanOptions{Clock: r.Clock, IDGenerator: r.IDGenerator}
}

func metricDatabases(registry *db.Registry) map[string]metrics.DBStatsProvider {
	providers := map[string]metrics.DBStatsProvider{}
	for _, role := range []string{db.RoleCore, db.RoleAudit, db.RoleEvents, db.RoleJobs, db.RoleRuntime, db.RoleObservability} {
		if store, ok := registry.Get(role); ok {
			providers[role] = store
		}
	}
	return providers
}

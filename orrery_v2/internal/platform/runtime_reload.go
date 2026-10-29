package platform

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/filestore"
	"orrery_v2/internal/platform/health"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/metrics"
	"orrery_v2/internal/platform/secret"
	"orrery_v2/internal/platform/tracing"
)

func (r *Runtime) ReloadFromFile(ctx context.Context, path string) error {
	if r == nil || r.ConfigManager == nil {
		return fmt.Errorf("runtime config manager is not configured")
	}
	if err := r.beginOperation(); err != nil {
		return err
	}
	defer r.endOperation()
	if err := r.ConfigManager.ReloadFromFile(ctx, path); err != nil {
		return err
	}
	current := r.ConfigManager.Current()
	if err := r.static.diff(current); err != nil {
		r.ConfigManager.RecordError(err)
	}
	r.mu.Lock()
	r.Config = current
	r.mu.Unlock()
	return nil
}

func (r *Runtime) ReloadComponents(ctx context.Context) error {
	if r == nil || r.ConfigManager == nil {
		return fmt.Errorf("runtime config manager is not configured")
	}
	if err := r.beginOperation(); err != nil {
		return err
	}
	defer r.endOperation()
	cfg := r.ConfigManager.Current()
	hot, err := r.buildHotReloadable(ctx, cfg)
	if err != nil {
		r.ConfigManager.RecordError(err)
		return err
	}
	r.applyHotReloadable(cfg, hot)
	if err := r.ConfigManager.LastError(); err != nil && !isRestartRequired(err) {
		r.ConfigManager.RecordError(nil)
	}
	return nil
}

func (r *Runtime) WatchYAML(ctx context.Context, path string, interval time.Duration) func() {
	if r == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = time.Second
	}
	watchCtx, cancel := context.WithCancel(ctx)
	r.lifecycleMu.RLock()
	defer r.lifecycleMu.RUnlock()
	if r.closed {
		cancel()
		return func() {}
	}
	r.watchMu.Lock()
	r.watchStops = append(r.watchStops, cancel)
	r.watchWG.Add(1)
	r.watchMu.Unlock()

	go func() {
		defer r.watchWG.Done()
		r.watchYAML(watchCtx, path, interval)
	}()
	return cancel
}

type hotReloadable struct {
	logger          logging.Logger
	closeLog        func() error
	secrets         secret.Provider
	store           filestore.Store
	health          *health.Checker
	metrics         *metrics.BoundedSink
	metricsRegistry *metrics.Registry
	metricsServer   *http.Server
	metricsErr      chan error
	traces          *tracing.BoundedRecorder
	sweeper         *execution.SweeperRunner
}

func (r *Runtime) buildHotReloadable(ctx context.Context, cfg config.Config) (hotReloadable, error) {
	logger, closeLog, err := openConfiguredLogger(cfg)
	if err != nil {
		return hotReloadable{}, fmt.Errorf("open configured logger: %w", err)
	}
	secrets := newSecretProvider(cfg)
	fileStore, err := filestore.OpenFromConfig(ctx, cfg.FileStore, filestore.Dependencies{
		Secrets:     secrets,
		Logger:      logger,
		Clock:       r.Clock,
		IDGenerator: r.IDGenerator,
	})
	if err != nil {
		_ = closeLog()
		return hotReloadable{}, fmt.Errorf("open filestore: %w", err)
	}
	hot := hotReloadable{
		logger:          logger,
		closeLog:        closeLog,
		secrets:         secrets,
		store:           fileStore,
		health:          r.newHealthChecker(cfg),
		metrics:         metrics.NewBoundedSinkWithWarnInterval(cfg.Metrics.BufferSize, cfg.Metrics.WarnInterval),
		metricsRegistry: metrics.NewRegistry(r.metricOptions()),
		traces:          tracing.NewBoundedRecorder(cfg.Tracing.BufferSize),
		sweeper:         newSweeper(cfg, logger),
	}
	metricsAddr := strings.TrimSpace(cfg.Metrics.Addr)
	currentMetricsAddr := r.activeMetricsAddr()
	if metricsAddr != "" && metricsAddr == currentMetricsAddr {
		hot.metricsRegistry = r.MetricsRegistry
	}
	if metricsAddr != "" && metricsAddr != currentMetricsAddr {
		server, errCh, err := startMetricsHTTPServer(metricsAddr, hot.metricsRegistry.Gatherer)
		if err != nil {
			_ = closeLog()
			return hotReloadable{}, err
		}
		hot.metricsServer = server
		hot.metricsErr = errCh
	}
	return hot, nil
}

func (r *Runtime) watchYAML(ctx context.Context, path string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastMod time.Time
	var lastSize int64
	if stat, err := os.Stat(path); err == nil {
		lastMod = stat.ModTime()
		lastSize = stat.Size()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stat, err := os.Stat(path)
			if err != nil {
				r.recordReloadError(ctx, err)
				continue
			}
			if stat.ModTime().Equal(lastMod) && stat.Size() == lastSize {
				continue
			}
			if err := r.ReloadFromFile(ctx, path); err != nil {
				r.recordReloadError(ctx, err)
				continue
			}
			lastMod = stat.ModTime()
			lastSize = stat.Size()
		}
	}
}

func (r *Runtime) recordReloadError(ctx context.Context, err error) {
	if r != nil && r.ConfigManager != nil {
		r.ConfigManager.RecordError(err)
	}
	if r != nil && r.Logger != nil {
		r.Logger.Warn(ctx, "runtime config reload failed",
			logging.String("operation", "runtime.config.watch"),
			logging.String("error_code", "config_reload_failed"),
			logging.String("error", err.Error()),
		)
	}
}

func (r *Runtime) applyHotReloadable(cfg config.Config, hot hotReloadable) {
	r.mu.Lock()
	oldCloseLog := r.closeLog
	oldMetricsServer := r.MetricsServer
	oldMetricsErr := r.metricsErr
	shouldStopOldMetrics := false
	r.Config = cfg
	logging.CarryObserver(r.Logger, hot.logger)
	r.Logger = hot.logger
	r.closeLog = hot.closeLog
	r.Secrets = hot.secrets
	r.FileStore = hot.store
	r.Artifacts = NewArtifactStore(r.FileStore)
	r.Health = hot.health
	r.Metrics = hot.metrics
	r.MetricRecorder = NewMetricRecorder(r.Metrics, hot.logger)
	r.MetricsRegistry = hot.metricsRegistry
	r.Traces = hot.traces
	r.TracingOptions = r.tracingOptions()
	r.Tracer = NewTracer(r.Traces, r.TracingOptions)
	if hot.metricsServer != nil {
		r.MetricsServer = hot.metricsServer
		r.metricsErr = hot.metricsErr
		shouldStopOldMetrics = true
	} else if strings.TrimSpace(cfg.Metrics.Addr) == "" {
		r.MetricsServer = nil
		r.metricsErr = nil
		shouldStopOldMetrics = oldMetricsServer != nil
	} else {
		oldMetricsServer = nil
		oldMetricsErr = nil
	}
	r.Sweeper = hot.sweeper
	attachRegisteredSweepers(r.Sweeper, r.sweepers)
	if r.UnitOfWork != nil {
		r.UnitOfWork.WithLogger(hot.logger)
	}
	if r.RoutedUOW != nil {
		r.RoutedUOW.WithLogger(hot.logger)
	}
	r.mu.Unlock()
	if oldCloseLog != nil {
		_ = oldCloseLog()
	}
	if shouldStopOldMetrics {
		_ = shutdownMetricsServer(oldMetricsServer, oldMetricsErr)
	}
}

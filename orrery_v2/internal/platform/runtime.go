package platform

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/filestore"
	"orrery_v2/internal/platform/health"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/lock"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/metrics"
	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/outbox"
	"orrery_v2/internal/platform/secret"
	"orrery_v2/internal/platform/tracing"
	"orrery_v2/internal/platform/uow"
)

type Runtime struct {
	Config          config.Config
	ConfigManager   *config.Manager
	Clock           clock.Clock
	Logger          logging.Logger
	IDGenerator     idgen.ErrorGenerator
	Databases       *db.Registry
	DBRouter        *db.Router
	UnitOfWork      *uow.UnitOfWork
	RoutedUOW       *uow.RoutedUnitOfWork
	Migrations      *migration.Coordinator
	EventStore      *eventstore.Store
	EventRecorder   *EventRecorder
	FileStore       filestore.Store
	Artifacts       *ArtifactStore
	Execution       *execution.Infra
	ExecutionQueue  *execution.QueueService
	ExecutionClaims *execution.ClaimsService
	ExecutionLive   *execution.LivenessService
	Lock            *lock.Store
	Locks           *LockManager
	Secrets         secret.Provider
	Outbox          *outbox.Store
	OutboxPublisher *OutboxPublisher
	Health          *health.Checker
	Metrics         *metrics.BoundedSink
	MetricRecorder  *MetricRecorder
	MetricsRelay    *metrics.BatchRelay
	MetricsRegistry *metrics.Registry
	MetricsServer   *http.Server
	Traces          *tracing.BoundedRecorder
	TracingOptions  tracing.SpanOptions
	Tracer          *Tracer
	Sweeper         *execution.SweeperRunner

	mu                 sync.RWMutex
	closeLog           func() error
	metricsErr         chan error
	metricWriters      []registeredMetricWriter
	metricsRelayClosed bool
	outboxHandlers     map[string]outbox.Handler
	outboxRelayCancel  func()
	outboxRelayDone    chan error
	executionHandlers  map[string]execution.Handler
	executionCancel    func()
	executionDone      chan error
	initializers       []registeredInitializer
	sweepers           []registeredSweeper
	sweeperCancel      func()
	sweeperDone        chan error
	workerStatus       map[string]workerStatus
	metricStatusGroups []metrics.StatusGroup
	readyChecks        []registeredReadyCheck
	static             staticRuntimeConfig

	lifecycleMu sync.RWMutex
	closed      bool
	watchMu     sync.Mutex
	watchStops  []func()
	watchWG     sync.WaitGroup
}

type RuntimeOptions struct {
	Clock       clock.Clock
	IDGenerator idgen.ErrorGenerator
}

func OpenFromYAML(ctx context.Context, path string) (*Runtime, error) {
	cfg, err := config.LoadYAML(path)
	if err != nil {
		return nil, err
	}
	return Open(ctx, cfg)
}

func OpenFromYAMLWithModules(ctx context.Context, path string, modules ...Module) (*Runtime, error) {
	cfg, err := config.LoadYAML(path)
	if err != nil {
		return nil, err
	}
	return OpenWithModules(ctx, cfg, modules...)
}

func Open(ctx context.Context, cfg config.Config) (*Runtime, error) {
	manager := config.NewManager(cfg)
	return OpenWithConfigManager(ctx, manager)
}

func OpenWithOptions(ctx context.Context, cfg config.Config, opts RuntimeOptions) (*Runtime, error) {
	manager := config.NewManager(cfg)
	return openWithConfigManagerAndModules(ctx, manager, nil, opts)
}

func OpenWithModules(ctx context.Context, cfg config.Config, modules ...Module) (*Runtime, error) {
	manager := config.NewManager(cfg)
	rt, err := openWithConfigManagerAndModules(ctx, manager, modules, RuntimeOptions{})
	if err != nil {
		return nil, err
	}
	return rt, nil
}

func OpenWithOptionsAndModules(ctx context.Context, cfg config.Config, opts RuntimeOptions, modules ...Module) (*Runtime, error) {
	manager := config.NewManager(cfg)
	return openWithConfigManagerAndModules(ctx, manager, modules, opts)
}

func OpenWithConfigManager(ctx context.Context, manager *config.Manager) (*Runtime, error) {
	return openWithConfigManagerAndModules(ctx, manager, nil, RuntimeOptions{})
}

func openWithConfigManagerAndModules(ctx context.Context, manager *config.Manager, modules []Module, opts RuntimeOptions) (*Runtime, error) {
	if manager == nil {
		return nil, fmt.Errorf("config manager is required")
	}
	cfg := manager.Current()
	platformClock := opts.Clock
	if platformClock == nil {
		platformClock = clock.Real{}
	}
	idGenerator := opts.IDGenerator
	if idGenerator == nil {
		idGenerator = idgen.NewRandom("orrery")
	}
	rt := &Runtime{
		Config:            cfg,
		ConfigManager:     manager,
		Clock:             platformClock,
		IDGenerator:       idGenerator,
		outboxHandlers:    map[string]outbox.Handler{},
		executionHandlers: map[string]execution.Handler{},
		workerStatus:      map[string]workerStatus{},
		static:            staticFromConfig(cfg),
	}
	var err error
	defer func() {
		if err != nil {
			_ = rt.Close()
		}
	}()

	rt.Databases, err = db.OpenRegistryFromURLs(ctx, cfg.DatabaseURLs)
	if err != nil {
		return nil, fmt.Errorf("open database registry: %w", err)
	}
	rt.DBRouter = db.NewRouter(rt.Databases)
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		return nil, fmt.Errorf("open core database: %w", err)
	}
	rt.UnitOfWork = uow.New(core).WithClock(rt.Clock).WithIDGenerator(rt.IDGenerator)
	rt.RoutedUOW = uow.NewRouted(rt.DBRouter)
	rt.Migrations = migration.NewCoordinatorWithOptions(rt.Databases, migration.RunnerOptions{Clock: rt.Clock})

	rt.Logger, rt.closeLog, err = openConfiguredLogger(cfg)
	if err != nil {
		return nil, fmt.Errorf("open configured logger: %w", err)
	}
	rt.UnitOfWork.WithLogger(rt.Logger)
	rt.RoutedUOW.WithLogger(rt.Logger)
	events, err := rt.Databases.Store(db.RoleEvents)
	if err != nil {
		return nil, fmt.Errorf("open event store database: %w", err)
	}
	rt.EventStore = eventstore.NewWithOptions(events, eventstore.StoreOptions{Clock: rt.Clock})
	rt.EventRecorder = NewEventRecorder(rt.EventStore, rt.IDGenerator)

	rt.Secrets = newSecretProvider(cfg)
	rt.FileStore, err = filestore.OpenFromConfig(ctx, cfg.FileStore, filestore.Dependencies{
		Secrets:     rt.Secrets,
		Logger:      rt.Logger,
		Clock:       rt.Clock,
		IDGenerator: rt.IDGenerator,
	})
	if err != nil {
		return nil, fmt.Errorf("open filestore: %w", err)
	}
	rt.Artifacts = NewArtifactStore(rt.FileStore)
	rt.Execution, err = rt.openExecution(ctx, cfg, rt.Logger)
	if err != nil {
		return nil, fmt.Errorf("open execution infra: %w", err)
	}
	rt.ExecutionQueue = execution.NewQueueServiceWithIDGenerator(rt.Execution.Queue, rt.Clock, rt.IDGenerator)
	rt.ExecutionClaims = execution.NewClaimsService(rt.Execution.Claims, rt.Clock, cfg.Queue.ClaimTTL)
	rt.ExecutionLive = execution.NewLivenessService(rt.Execution.Liveness, rt.Clock, cfg.Liveness.HeartbeatTTL)
	runtimeStore, err := rt.Databases.Store(db.RoleRuntime)
	if err != nil {
		return nil, fmt.Errorf("open runtime store: %w", err)
	}
	rt.Lock, err = lock.New(ctx, runtimeStore)
	if err != nil {
		return nil, fmt.Errorf("open lock store: %w", err)
	}
	rt.Locks = NewLockManager(rt.Lock, rt.Clock, cfg.Lock.DefaultTTL)
	jobs, err := rt.Databases.Store(db.RoleJobs)
	if err != nil {
		return nil, fmt.Errorf("open outbox store: %w", err)
	}
	rt.Outbox = outbox.NewWithOptions(jobs, outbox.StoreOptions{Clock: rt.Clock})
	rt.OutboxPublisher = NewOutboxPublisher(rt.Outbox, rt.Clock, rt.IDGenerator)
	rt.Health = rt.newHealthChecker(cfg)
	rt.Metrics = metrics.NewBoundedSinkWithWarnInterval(cfg.Metrics.BufferSize, cfg.Metrics.WarnInterval)
	rt.MetricRecorder = NewMetricRecorder(rt.Metrics, rt.Logger)
	rt.MetricsRegistry = metrics.NewRegistry(rt.metricOptions())
	rt.Traces = tracing.NewBoundedRecorder(cfg.Tracing.BufferSize)
	rt.TracingOptions = rt.tracingOptions()
	rt.Tracer = NewTracer(rt.Traces, rt.TracingOptions)
	rt.Sweeper = rt.Execution.Sweepers
	rt.registerPlatformHousekeeping()
	attachRegisteredSweepers(rt.Sweeper, rt.sweepers)
	if err = registerModules(ctx, rt, modules); err != nil {
		return nil, err
	}
	rt.MetricsRegistry = metrics.NewRegistry(rt.metricOptions())
	if err := rt.startMetricsServer(cfg); err != nil {
		return nil, err
	}
	return rt, nil
}

func (r *Runtime) RegisterOutboxHandler(topic string, handler outbox.Handler) error {
	if strings.TrimSpace(topic) == "" {
		return fmt.Errorf("outbox topic is required")
	}
	if handler == nil {
		return fmt.Errorf("outbox handler is required")
	}
	if err := r.beginOperation(); err != nil {
		return err
	}
	defer r.endOperation()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outboxHandlers == nil {
		r.outboxHandlers = map[string]outbox.Handler{}
	}
	if _, exists := r.outboxHandlers[topic]; exists {
		return fmt.Errorf("outbox handler already registered for topic %q", topic)
	}
	r.outboxHandlers[topic] = handler
	return nil
}

func (r *Runtime) RegisterOutboxDeliveryHandler(topic string, handler OutboxDeliveryHandler) error {
	if handler == nil {
		return r.RegisterOutboxHandler(topic, nil)
	}
	return r.RegisterOutboxHandler(topic, func(ctx context.Context, job outbox.Job) error {
		return handler(ctx, outboxDeliveryFromJob(job))
	})
}

func (r *Runtime) Database(role string) (Database, bool) {
	if r == nil || r.Databases == nil {
		return Database{}, false
	}
	store, ok := r.Databases.Get(role)
	if !ok || store == nil {
		return Database{}, false
	}
	return Database{store: store}, true
}

func (r *Runtime) RegisterExecutionHandler(topic string, handler execution.Handler) error {
	if strings.TrimSpace(topic) == "" {
		return fmt.Errorf("execution topic is required")
	}
	if handler == nil {
		return fmt.Errorf("execution handler is required")
	}
	if err := r.beginOperation(); err != nil {
		return err
	}
	defer r.endOperation()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.executionHandlers == nil {
		r.executionHandlers = map[string]execution.Handler{}
	}
	if r.executionCancel != nil {
		return fmt.Errorf("execution runner already started")
	}
	if _, exists := r.executionHandlers[topic]; exists {
		return fmt.Errorf("execution handler already registered for topic %q", topic)
	}
	r.executionHandlers[topic] = handler
	return nil
}

func (r *Runtime) RegisterExecutionDeliveryHandler(topic string, handler ExecutionDeliveryHandler) error {
	if handler == nil {
		return r.RegisterExecutionHandler(topic, nil)
	}
	return r.RegisterExecutionHandler(topic, func(ctx context.Context, job execution.Job) error {
		return handler(ctx, executionDeliveryFromContext(ctx, job))
	})
}

func (r *Runtime) RegisterSweeper(name string, sweeper Sweeper) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("sweeper name is required")
	}
	if sweeper == nil {
		return fmt.Errorf("sweeper %q is required", name)
	}
	if err := r.beginOperation(); err != nil {
		return err
	}
	defer r.endOperation()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sweeperCancel != nil {
		return fmt.Errorf("sweeper runner already started")
	}
	for _, registered := range r.sweepers {
		if registered.name == name {
			return fmt.Errorf("sweeper already registered for name %q", name)
		}
	}
	registered := registeredSweeper{name: name, sweeper: sweeper}
	r.sweepers = append(r.sweepers, registered)
	if r.Sweeper != nil {
		r.Sweeper.Register(registered)
	}
	return nil
}

func (r *Runtime) beginOperation() error {
	r.lifecycleMu.RLock()
	if r.closed {
		r.lifecycleMu.RUnlock()
		return fmt.Errorf("runtime is closed")
	}
	return nil
}

func (r *Runtime) endOperation() {
	r.lifecycleMu.RUnlock()
}

func (r *Runtime) newHealthChecker(cfg config.Config) *health.Checker {
	checker := health.NewChecker(nil, nil, cfg.ReadinessCacheTTL).
		WithClock(r.Clock).
		WithMigrationCoordinator(r.Migrations).
		WithFileStoreRoot(cfg.FileStore.Root).
		WithConfigManager(r.ConfigManager)
	checker.WithReadyCheck(r.workerReadyCheck)
	for _, registered := range r.readyChecks {
		checker.WithReadyCheck(registered.check)
	}
	for _, initializer := range r.initializers {
		checker.WithReadyCheck(r.initializerReadyCheck(initializer.name))
	}
	return checker
}

func openConfiguredLogger(cfg config.Config) (logging.Logger, func() error, error) {
	return logging.NewConfiguredLogger(logging.Config{
		Level:   cfg.Logging.Level,
		Console: cfg.Logging.Console,
		File:    cfg.Logging.File,
	}, nil)
}

func newSecretProvider(cfg config.Config) secret.Provider {
	providers := []secret.Provider{}
	if strings.TrimSpace(cfg.Secret.Root) != "" {
		providers = append(providers, secret.NewFileProvider(cfg.Secret.Root))
	}
	providers = append(providers, secret.NewEnvProvider("ORRERY_SECRET_"))
	return secret.NewCompositeProvider(providers...)
}

func newSweeper(cfg config.Config, logger logging.Logger) *execution.SweeperRunner {
	return execution.NewSweeperRunnerWithConfig(execution.SweeperConfig{
		BatchSize: cfg.Sweeper.BatchSize,
		Timeout:   cfg.Sweeper.Timeout,
		Logger:    logger,
	})
}

func (r *Runtime) openExecution(ctx context.Context, cfg config.Config, logger logging.Logger) (*execution.Infra, error) {
	jobs, err := r.Databases.Store(db.RoleJobs)
	if err != nil {
		return nil, err
	}
	runtimeStore, err := r.Databases.Store(db.RoleRuntime)
	if err != nil {
		return nil, err
	}
	return execution.Open(ctx, execution.Stores{Jobs: jobs, Runtime: runtimeStore}, execution.Config{
		Queue: execution.QueueConfig{
			ClaimTTL:       cfg.Queue.ClaimTTL,
			MaxAttempts:    cfg.Queue.MaxAttempts,
			SweepBatchSize: cfg.Queue.SweepBatchSize,
		},
		Sweeper: execution.SweeperConfig{
			BatchSize: cfg.Sweeper.BatchSize,
			Timeout:   cfg.Sweeper.Timeout,
			Logger:    logger,
		},
		Clock: r.Clock,
	})
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.lifecycleMu.Lock()
	r.closed = true
	r.lifecycleMu.Unlock()

	r.watchMu.Lock()
	stops := append([]func(){}, r.watchStops...)
	r.watchStops = nil
	r.watchMu.Unlock()
	for _, stop := range stops {
		stop()
	}
	r.watchWG.Wait()

	var firstErr error
	r.mu.Lock()
	sweeperCancel := r.sweeperCancel
	sweeperDone := r.sweeperDone
	r.sweeperCancel = nil
	r.sweeperDone = nil
	executionCancel := r.executionCancel
	executionDone := r.executionDone
	r.executionCancel = nil
	r.executionDone = nil
	outboxCancel := r.outboxRelayCancel
	outboxDone := r.outboxRelayDone
	r.outboxRelayCancel = nil
	r.outboxRelayDone = nil
	metricsRelay := r.MetricsRelay
	r.MetricsRelay = nil
	metricsRelayAlreadyClosed := r.metricsRelayClosed
	metricsServer := r.MetricsServer
	metricsErr := r.metricsErr
	r.MetricsServer = nil
	r.metricsErr = nil
	closeLog := r.closeLog
	r.closeLog = nil
	sweepers := append([]registeredSweeper(nil), r.sweepers...)
	r.mu.Unlock()
	if err := stopSweeperRunner(sweeperCancel, sweeperDone); err != nil && firstErr == nil {
		firstErr = err
	}
	for _, registered := range sweepers {
		if closer, ok := registered.sweeper.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := stopExecutionRunner(executionCancel, executionDone); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := stopOutboxRelay(outboxCancel, outboxDone); err != nil && firstErr == nil {
		firstErr = err
	}
	if metricsRelay != nil && !metricsRelayAlreadyClosed {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := metricsRelay.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		cancel()
	}
	if err := shutdownMetricsServer(metricsServer, metricsErr); err != nil && firstErr == nil {
		firstErr = err
	}
	if r.Databases != nil {
		if err := r.Databases.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if closeLog != nil {
		if err := closeLog(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

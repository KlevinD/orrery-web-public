package platform

import (
	"context"
	"fmt"

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

type Module interface {
	RegisterPlatform(context.Context, *ModuleRegistrar) error
}

type ModuleFunc func(context.Context, *ModuleRegistrar) error

func (fn ModuleFunc) RegisterPlatform(ctx context.Context, registrar *ModuleRegistrar) error {
	if fn == nil {
		return fmt.Errorf("platform module function is nil")
	}
	return fn(ctx, registrar)
}

type SweeperFunc func(context.Context) error

type Sweeper interface {
	Sweep(context.Context) error
}

func (fn SweeperFunc) Sweep(ctx context.Context) error {
	if fn == nil {
		return fmt.Errorf("sweeper function is nil")
	}
	return fn(ctx)
}

type ModuleRegistrar struct {
	manager    *config.Manager
	migrations *migration.Coordinator
	runtime    *Runtime
}

type registeredReadyCheck struct {
	name  string
	check health.ReadyCheck
}

type ReadyStatus struct {
	OK      bool
	Message string
}

type StatusCountsProbe func(context.Context) (map[string]int64, error)

func (p StatusCountsProbe) StatusCounts(ctx context.Context) (map[string]int64, error) {
	if p == nil {
		return nil, fmt.Errorf("status counts probe is nil")
	}
	return p(ctx)
}

func (r *ModuleRegistrar) Config() config.Config {
	if r == nil || r.manager == nil {
		return config.Config{}
	}
	return r.manager.Current()
}

func (r *ModuleRegistrar) ConfigManager() *config.Manager {
	if r == nil {
		return nil
	}
	return r.manager
}

func (r *ModuleRegistrar) Clock() clock.Clock {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Clock
}

func (r *ModuleRegistrar) Logger() logging.Logger {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Logger
}

func (r *ModuleRegistrar) Databases() *db.Registry {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Databases
}

func (r *ModuleRegistrar) DBRouter() *db.Router {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.DBRouter
}

func (r *ModuleRegistrar) UnitOfWork() *uow.UnitOfWork {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.UnitOfWork
}

func (r *ModuleRegistrar) RoutedUOW() *uow.RoutedUnitOfWork {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.RoutedUOW
}

func (r *ModuleRegistrar) IDGenerator() idgen.ErrorGenerator {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.IDGenerator
}

func (r *ModuleRegistrar) Migrations() *migration.Coordinator {
	if r == nil {
		return nil
	}
	return r.migrations
}

func (r *ModuleRegistrar) EventStore() *eventstore.Store {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.EventStore
}

func (r *ModuleRegistrar) EventRecorder() *EventRecorder {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.EventRecorder
}

func (r *ModuleRegistrar) FileStore() filestore.Store {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.FileStore
}

func (r *ModuleRegistrar) Artifacts() *ArtifactStore {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Artifacts
}

func (r *ModuleRegistrar) Outbox() *outbox.Store {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Outbox
}

func (r *ModuleRegistrar) OutboxPublisher() *OutboxPublisher {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.OutboxPublisher
}

func (r *ModuleRegistrar) Execution() *execution.Infra {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Execution
}

func (r *ModuleRegistrar) ExecutionQueue() *execution.QueueService {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.ExecutionQueue
}

func (r *ModuleRegistrar) ExecutionClaims() *execution.ClaimsService {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.ExecutionClaims
}

func (r *ModuleRegistrar) ExecutionLiveness() *execution.LivenessService {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.ExecutionLive
}

func (r *ModuleRegistrar) Lock() *lock.Store {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Lock
}

func (r *ModuleRegistrar) Locks() *LockManager {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Locks
}

func (r *ModuleRegistrar) Secrets() secret.Provider {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Secrets
}

func (r *ModuleRegistrar) Health() *health.Checker {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Health
}

func (r *ModuleRegistrar) Metrics() *metrics.BoundedSink {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Metrics
}

func (r *ModuleRegistrar) MetricRecorder() *MetricRecorder {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.MetricRecorder
}

func (r *ModuleRegistrar) MetricsRegistry() *metrics.Registry {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.MetricsRegistry
}

func (r *ModuleRegistrar) RegisterMetricEventWriter(name string, writer MetricEventWriter) error {
	if r == nil || r.runtime == nil {
		return fmt.Errorf("platform runtime is not configured")
	}
	return r.runtime.RegisterMetricEventWriter(name, writer)
}

func (r *ModuleRegistrar) Traces() *tracing.BoundedRecorder {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Traces
}

func (r *ModuleRegistrar) Tracer() *Tracer {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Tracer
}

func (r *ModuleRegistrar) TracingOptions() tracing.SpanOptions {
	if r == nil || r.runtime == nil {
		return tracing.SpanOptions{}
	}
	return r.runtime.TracingOptions
}

func (r *ModuleRegistrar) RegisterConfigSection(name string, validator config.SectionValidator) error {
	if r == nil || r.manager == nil {
		return fmt.Errorf("platform config manager is not configured")
	}
	return r.manager.RegisterSection(name, validator)
}

func (r *ModuleRegistrar) RegisterConfigValidator(name string, validator ConfigValidator) error {
	if validator == nil {
		return fmt.Errorf("config validator %q is required", name)
	}
	return r.RegisterConfigSection(name, func(cfg config.Config) error {
		return validator(ConfigSection{name: name, config: cfg})
	})
}

func (r *ModuleRegistrar) RegisterConfigChangeHook(fn func(version int64)) error {
	if r == nil || r.manager == nil {
		return fmt.Errorf("platform config manager is not configured")
	}
	_, err := r.manager.SubscribeChange(fn)
	return err
}

func (r *ModuleRegistrar) GetSecret(ctx context.Context, name string) (secret.Value, error) {
	if r == nil || r.runtime == nil || r.runtime.Secrets == nil {
		return secret.Value{}, fmt.Errorf("platform secret provider is not configured")
	}
	return r.runtime.Secrets.Get(ctx, name)
}

func (r *ModuleRegistrar) ReadSecret(ctx context.Context, name string) (SecretValue, error) {
	value, err := r.GetSecret(ctx, name)
	if err != nil {
		return SecretValue{}, err
	}
	return SecretValue{value: value}, nil
}

func (r *ModuleRegistrar) RegisterMigration(role string, initializer migration.Migration) error {
	if r == nil || r.migrations == nil {
		return fmt.Errorf("platform migration coordinator is not configured")
	}
	if role != db.RoleCore && role != db.RoleAudit && role != db.RoleEvents && role != db.RoleJobs && role != db.RoleRuntime && role != db.RoleObservability {
		return fmt.Errorf("database role %q is not registered in platform database registry", role)
	}
	return r.migrations.RegisterInitializer(role, initializer)
}

func (r *ModuleRegistrar) RegisterSQLMigration(role, version, description string, statements ...string) error {
	return r.RegisterMigration(role, sqlMigration(version, description, statements))
}

func (r *ModuleRegistrar) RegisterInitializer(name string, initializer InitializerFunc) error {
	if r == nil || r.runtime == nil {
		return fmt.Errorf("platform runtime is not configured")
	}
	return r.runtime.RegisterInitializer(name, initializer)
}

func (r *ModuleRegistrar) RegisterOutboxHandler(topic string, handler outbox.Handler) error {
	if r == nil || r.runtime == nil {
		return fmt.Errorf("platform runtime is not configured")
	}
	return r.runtime.RegisterOutboxHandler(topic, handler)
}

func (r *ModuleRegistrar) RegisterOutboxDeliveryHandler(topic string, handler OutboxDeliveryHandler) error {
	if handler == nil {
		return r.RegisterOutboxHandler(topic, nil)
	}
	return r.RegisterOutboxHandler(topic, func(ctx context.Context, job outbox.Job) error {
		return handler(ctx, outboxDeliveryFromJob(job))
	})
}

func (r *ModuleRegistrar) RegisterExecutionHandler(topic string, handler execution.Handler) error {
	if r == nil || r.runtime == nil {
		return fmt.Errorf("platform runtime is not configured")
	}
	return r.runtime.RegisterExecutionHandler(topic, handler)
}

func (r *ModuleRegistrar) RegisterExecutionDeliveryHandler(topic string, handler ExecutionDeliveryHandler) error {
	if handler == nil {
		return r.RegisterExecutionHandler(topic, nil)
	}
	return r.RegisterExecutionHandler(topic, func(ctx context.Context, job execution.Job) error {
		return handler(ctx, executionDeliveryFromContext(ctx, job))
	})
}

func (r *ModuleRegistrar) RegisterSweeper(name string, sweeper Sweeper) error {
	if r == nil || r.runtime == nil {
		return fmt.Errorf("platform runtime is not configured")
	}
	return r.runtime.RegisterSweeper(name, sweeper)
}

func (r *ModuleRegistrar) RegisterReadyCheck(name string, check func(context.Context) health.Check) error {
	if r == nil || r.runtime == nil || r.runtime.Health == nil {
		return fmt.Errorf("platform health checker is not configured")
	}
	if name == "" {
		return fmt.Errorf("ready check name is required")
	}
	if check == nil {
		return fmt.Errorf("ready check %q is required", name)
	}
	registered := registeredReadyCheck{name: name, check: func(ctx context.Context) health.Check {
		result := check(ctx)
		if result.Name == "" {
			result.Name = name
		}
		return result
	}}
	r.runtime.readyChecks = append(r.runtime.readyChecks, registered)
	r.runtime.Health.WithReadyCheck(registered.check)
	return nil
}

func (r *ModuleRegistrar) RegisterReadyProbe(name string, probe func(context.Context) ReadyStatus) error {
	if probe == nil {
		return r.RegisterReadyCheck(name, nil)
	}
	return r.RegisterReadyCheck(name, func(ctx context.Context) health.Check {
		status := probe(ctx)
		return health.Check{Name: name, OK: status.OK, Message: status.Message}
	})
}

func (r *ModuleRegistrar) RegisterStatusMetrics(group metrics.StatusGroup) error {
	if r == nil || r.runtime == nil {
		return fmt.Errorf("platform runtime is not configured")
	}
	if group.Subsystem == "" {
		return fmt.Errorf("status metrics subsystem is required")
	}
	if group.Name == "" {
		return fmt.Errorf("status metrics name is required")
	}
	if len(group.Providers) == 0 {
		return fmt.Errorf("status metrics providers are required")
	}
	r.runtime.metricStatusGroups = append(r.runtime.metricStatusGroups, group)
	return nil
}

func (r *ModuleRegistrar) RegisterStatusProbe(subsystem, name, role string, probe StatusCountsProbe) error {
	if role == "" {
		return fmt.Errorf("status metrics role is required")
	}
	if probe == nil {
		return fmt.Errorf("status metrics provider %q is required", role)
	}
	return r.RegisterStatusMetrics(metrics.StatusGroup{
		Subsystem: subsystem,
		Name:      name,
		Providers: map[string]metrics.StatusCountsProvider{role: probe},
	})
}

func registerModules(ctx context.Context, rt *Runtime, modules []Module) error {
	if len(modules) == 0 {
		return nil
	}
	registrar := &ModuleRegistrar{manager: rt.ConfigManager, migrations: rt.Migrations, runtime: rt}
	for i, module := range modules {
		if module == nil {
			return fmt.Errorf("platform module %d is nil", i)
		}
		if err := module.RegisterPlatform(ctx, registrar); err != nil {
			return fmt.Errorf("register platform module %d: %w", i, err)
		}
	}
	return nil
}

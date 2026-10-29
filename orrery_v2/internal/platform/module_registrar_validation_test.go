package platform_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/health"
	"orrery_v2/internal/platform/metrics"
)

func TestModuleRegistrarRejectsInvalidHealthAndMetricsRegistrations(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	if err := os.WriteFile(configPath, []byte(`
paths:
  base_dir: .
database:
  urls:
    core: memory://module-invalid-core
    audit: memory://module-invalid-audit
    events: memory://module-invalid-events
    jobs: memory://module-invalid-jobs
    runtime: memory://module-invalid-runtime
    observability: memory://module-invalid-observability
filestore:
  backend: local
  root: ./artifacts
logging:
  console: false
  file: ./logs/platform.jsonl
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	for name, module := range map[string]platform.Module{
		"blank health check name": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterReadyCheck("", func(ctx context.Context) health.Check { return health.Check{OK: true} })
		}),
		"nil health check": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterReadyCheck("business.ready", nil)
		}),
		"blank metric subsystem": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterStatusMetrics(metrics.StatusGroup{Name: "workers", Providers: map[string]metrics.StatusCountsProvider{"core": staticStatusCounts{}}})
		}),
		"blank metric name": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterStatusMetrics(metrics.StatusGroup{Subsystem: "business", Providers: map[string]metrics.StatusCountsProvider{"core": staticStatusCounts{}}})
		}),
		"empty metric providers": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterStatusMetrics(metrics.StatusGroup{Subsystem: "business", Name: "workers"})
		}),
		"blank status probe role": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterStatusProbe("business", "workers", "", func(context.Context) (map[string]int64, error) {
				return map[string]int64{"ready": 1}, nil
			})
		}),
		"nil status probe": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterStatusProbe("business", "workers", "core", nil)
		}),
		"blank config validator name": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterConfigValidator("", func(platform.ConfigSection) error { return nil })
		}),
		"nil config validator": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterConfigValidator("business", nil)
		}),
		"blank sql migration role": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterSQLMigration("", "010_business_profiles", "create business profile table", `CREATE TABLE business_profiles (id TEXT PRIMARY KEY)`)
		}),
		"blank sql migration version": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterSQLMigration(platform.DatabaseRoleCore, "", "create business profile table", `CREATE TABLE business_profiles (id TEXT PRIMARY KEY)`)
		}),
		"blank execution topic": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterExecutionHandler("", func(context.Context, execution.Job) error { return nil })
		}),
		"nil execution handler": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterExecutionHandler("business.worker.sync", nil)
		}),
		"blank outbox delivery topic": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterOutboxDeliveryHandler("", func(context.Context, platform.OutboxDelivery) error { return nil })
		}),
		"nil outbox delivery handler": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterOutboxDeliveryHandler("business.profile.created", nil)
		}),
		"blank execution delivery topic": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterExecutionDeliveryHandler("", func(context.Context, platform.ExecutionDelivery) error { return nil })
		}),
		"nil execution delivery handler": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterExecutionDeliveryHandler("business.worker.sync", nil)
		}),
		"blank sweeper name": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterSweeper("", platform.SweeperFunc(func(context.Context) error { return nil }))
		}),
		"nil sweeper": platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
			return registry.RegisterSweeper("business.cleanup", nil)
		}),
	} {
		t.Run(name, func(t *testing.T) {
			rt, err := platform.OpenWithModules(ctx, cfg, module)
			if rt != nil {
				_ = rt.Close()
			}
			if err == nil {
				t.Fatalf("OpenWithModules returned nil error")
			}
		})
	}
}

type staticStatusCounts struct {
	counts map[string]int64
}

func (s staticStatusCounts) StatusCounts(context.Context) (map[string]int64, error) {
	counts := make(map[string]int64, len(s.counts))
	for key, value := range s.counts {
		counts[key] = value
	}
	return counts, nil
}

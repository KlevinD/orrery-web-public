package platform_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
)

func TestRuntimeRegistersBusinessModuleInfrastructureInOnePlace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	if err := os.WriteFile(configPath, []byte(`
paths:
  base_dir: .
database:
  urls:
    core: file://./data/core.db
    audit: file://./data/audit.db
    events: file://./data/events.db
    jobs: file://./data/jobs.db
    runtime: file://./data/runtime.db
    observability: file://./data/observability.db
filestore:
  backend: local
  root: ./artifacts
logging:
  console: false
  file: ./logs/platform.jsonl
business:
  enabled: true
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	processed := make(chan string, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		if err := registry.RegisterConfigValidator("business", func(section platform.ConfigSection) error {
			var businessConfig struct {
				Enabled bool `yaml:"enabled"`
			}
			if err := section.Decode(&businessConfig); err != nil {
				return err
			}
			if !businessConfig.Enabled {
				return section.Invalid("enabled must be true")
			}
			return nil
		}); err != nil {
			return err
		}
		if err := registry.RegisterSQLMigration(platform.DatabaseRoleCore, "010_business_profiles", "create business-owned profile table", `CREATE TABLE business_profiles (id TEXT PRIMARY KEY)`); err != nil {
			return err
		}
		return registry.RegisterOutboxDeliveryHandler("business.profile.created", func(ctx context.Context, delivery platform.OutboxDelivery) error {
			var payload struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
				return err
			}
			processed <- payload.ID
			return nil
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	if err := rt.Migrations.Up(ctx); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	coreDB, ok := rt.Database(platform.DatabaseRoleCore)
	if !ok {
		t.Fatalf("core database was not available through platform database facade")
	}
	if err := coreDB.Exec(ctx, `INSERT INTO business_profiles (id) VALUES (?)`, "profile-1"); err != nil {
		t.Fatalf("business migration table is not available: %v", err)
	}
	payload, _ := json.Marshal(map[string]string{"id": "profile-1"})
	outboxID, err := rt.OutboxPublisher.Publish(ctx, platform.OutboxMessage{
		Topic:   "business.profile.created",
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	status, ok := rt.OutboxPublisher.GetStatus(ctx, outboxID)
	if !ok {
		t.Fatalf("published outbox job %q was not stored", outboxID)
	}
	if status.Topic != "business.profile.created" || status.Status != platform.OutboxStatusPending {
		t.Fatalf("published outbox status = %+v", status)
	}
	if err := rt.StartOutboxRelay(ctx, platform.OutboxRelayRuntimeConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatalf("StartOutboxRelay returned error: %v", err)
	}
	select {
	case got := <-processed:
		if got != "profile-1" {
			t.Fatalf("processed profile = %q, want profile-1", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("business outbox handler was not registered through module registrar")
	}
}

func TestBusinessModuleGetsPlatformCapabilitiesFromRegistrar(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	capabilities := make(chan map[string]bool, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		capabilities <- map[string]bool{
			"config":           registry.Config().Environment == "test",
			"config_manager":   registry.ConfigManager() != nil,
			"clock":            registry.Clock() != nil && !registry.Clock().Now().IsZero(),
			"logger":           registry.Logger() != nil,
			"databases":        registry.Databases() != nil,
			"db_router":        registry.DBRouter() != nil,
			"unit_of_work":     registry.UnitOfWork() != nil,
			"routed_uow":       registry.RoutedUOW() != nil,
			"id_generator":     registry.IDGenerator() != nil,
			"migrations":       registry.Migrations() != nil,
			"eventstore":       registry.EventStore() != nil,
			"event_recorder":   registry.EventRecorder() != nil,
			"filestore":        registry.FileStore() != nil,
			"artifacts":        registry.Artifacts() != nil,
			"outbox":           registry.Outbox() != nil,
			"outbox_publisher": registry.OutboxPublisher() != nil,
			"execution":        registry.Execution() != nil,
			"execution_queue":  registry.ExecutionQueue() != nil,
			"execution_claims": registry.ExecutionClaims() != nil,
			"execution_live":   registry.ExecutionLiveness() != nil,
			"lock":             registry.Lock() != nil,
			"locks":            registry.Locks() != nil,
			"secrets":          registry.Secrets() != nil,
			"health":           registry.Health() != nil,
			"metrics":          registry.Metrics() != nil,
			"metric_recorder":  registry.MetricRecorder() != nil,
			"metrics_registry": registry.MetricsRegistry() != nil,
			"traces":           registry.Traces() != nil,
			"tracer":           registry.Tracer() != nil,
			"tracing_options":  registry.TracingOptions().Clock != nil && registry.TracingOptions().IDGenerator != nil,
		}
		return nil
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	select {
	case got := <-capabilities:
		for name, ok := range got {
			if !ok {
				t.Fatalf("module registrar capability %q was not available: %+v", name, got)
			}
		}
	default:
		t.Fatalf("module did not inspect registrar capabilities")
	}
}

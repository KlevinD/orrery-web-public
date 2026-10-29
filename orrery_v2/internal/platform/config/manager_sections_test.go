package config_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orrery_v2/internal/platform/config"
)

func TestManagerReloadPublishesExtensionSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, withExplicitTestDatabaseYAML(`
mission:
  max_parallel_tasks: 2
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	initial, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(initial)
	seen := make(chan int, 1)
	unsubscribe, err := manager.Subscribe(func(cfg config.Config) {
		var mission struct {
			MaxParallelTasks int `yaml:"max_parallel_tasks"`
		}
		if err := cfg.DecodeSection("mission", &mission); err != nil {
			t.Errorf("DecodeSection in subscriber: %v", err)
			return
		}
		seen <- mission.MaxParallelTasks
	})
	if err != nil {
		t.Fatalf("Subscribe returned error: %v", err)
	}
	defer unsubscribe()

	if err := os.WriteFile(path, withExplicitTestDatabaseYAML(`
mission:
  max_parallel_tasks: 9
`), 0o600); err != nil {
		t.Fatalf("rewrite yaml: %v", err)
	}
	if err := manager.ReloadFromFile(context.Background(), path); err != nil {
		t.Fatalf("ReloadFromFile: %v", err)
	}

	var mission struct {
		MaxParallelTasks int `yaml:"max_parallel_tasks"`
	}
	if err := manager.Current().DecodeSection("mission", &mission); err != nil {
		t.Fatalf("DecodeSection current: %v", err)
	}
	if mission.MaxParallelTasks != 9 {
		t.Fatalf("current mission = %+v, want max_parallel_tasks=9", mission)
	}
	select {
	case got := <-seen:
		if got != 9 {
			t.Fatalf("subscriber saw max_parallel_tasks=%d, want 9", got)
		}
	default:
		t.Fatalf("subscriber was not notified with extension section")
	}
}

func TestManagerRegistersPlatformSectionsOnInitialization(t *testing.T) {
	cfg, err := config.LoadYAML(explicitTestConfigPath(t))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	manager := config.NewManager(cfg)

	sections := manager.RegisteredSections()
	want := []string{"database", "device_delivery", "filestore", "health", "liveness", "lock", "logging", "metrics", "paths", "queue", "secret", "sweeper", "tracing"}
	if len(sections) != len(want) {
		t.Fatalf("RegisteredSections = %v, want %v", sections, want)
	}
	for i := range want {
		if sections[i] != want[i] {
			t.Fatalf("RegisteredSections = %v, want %v", sections, want)
		}
	}
}

func TestManagerRejectsReloadWhenPathsSectionIsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	writeExplicitTestDatabaseYAML(t, path, "environment: dev\n")
	initial, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(initial)

	writeExplicitTestDatabaseYAML(t, path, "paths:\n  base_dir: \"   \"\n")
	if err := manager.ReloadFromFile(context.Background(), path); err == nil {
		t.Fatalf("ReloadFromFile accepted blank paths.base_dir")
	}
	if manager.Current().BaseDir != filepath.Dir(path) {
		t.Fatalf("invalid paths reload changed BaseDir: %+v", manager.Current())
	}
	if manager.LastError() == nil {
		t.Fatalf("LastError was nil")
	}
}

func TestManagerRejectsReloadWhenRegisteredSectionValidatorFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, withExplicitTestDatabaseYAML(`
mission:
  max_parallel_tasks: 2
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	initial, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(initial)
	if err := manager.RegisterSection("mission", func(cfg config.Config) error {
		var mission struct {
			MaxParallelTasks int `yaml:"max_parallel_tasks"`
		}
		if err := cfg.DecodeSection("mission", &mission); err != nil {
			return err
		}
		if mission.MaxParallelTasks <= 0 {
			return config.SectionValidationError("mission", "max_parallel_tasks must be positive")
		}
		return nil
	}); err != nil {
		t.Fatalf("RegisterSection: %v", err)
	}

	seen := make(chan struct{}, 1)
	unsubscribe, err := manager.Subscribe(func(config.Config) {
		seen <- struct{}{}
	})
	if err != nil {
		t.Fatalf("Subscribe returned error: %v", err)
	}
	defer unsubscribe()

	if err := os.WriteFile(path, withExplicitTestDatabaseYAML(`
mission:
  max_parallel_tasks: 0
`), 0o600); err != nil {
		t.Fatalf("rewrite yaml: %v", err)
	}
	if err := manager.ReloadFromFile(context.Background(), path); err == nil {
		t.Fatalf("ReloadFromFile invalid registered section returned nil error")
	}
	if manager.Version() != 1 {
		t.Fatalf("Version = %d, want 1", manager.Version())
	}
	var mission struct {
		MaxParallelTasks int `yaml:"max_parallel_tasks"`
	}
	if err := manager.Current().DecodeSection("mission", &mission); err != nil {
		t.Fatalf("DecodeSection current: %v", err)
	}
	if mission.MaxParallelTasks != 2 {
		t.Fatalf("current mission = %+v, want last known-good value 2", mission)
	}
	if manager.LastError() == nil {
		t.Fatalf("LastError was nil")
	}
	select {
	case <-seen:
		t.Fatalf("subscriber was notified for rejected config")
	default:
	}
}

func TestManagerRejectsReloadWhenRegisteredPlatformSectionIsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, withExplicitTestDatabaseYAML(`
queue:
  claim_ttl: 10s
  max_attempts: 3
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	initial, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(initial)

	if err := os.WriteFile(path, withExplicitTestDatabaseYAML(`
queue:
  claim_ttl: 10s
  max_attempts: 0
`), 0o600); err != nil {
		t.Fatalf("rewrite yaml: %v", err)
	}
	if err := manager.ReloadFromFile(context.Background(), path); err == nil {
		t.Fatalf("ReloadFromFile accepted invalid registered queue section")
	}
	if manager.Current().Queue.MaxAttempts != 3 {
		t.Fatalf("current queue = %+v, want last known-good max_attempts=3", manager.Current().Queue)
	}
	if manager.LastError() == nil {
		t.Fatalf("LastError was nil")
	}
}

func TestManagerRejectsDatabaseSectionBlankRequiredRoleURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  urls:
    core: file://./core.db
    audit: file://./audit.db
    events: file://./events.db
    jobs: file://./jobs.db
    runtime: ""
    observability: file://./observability.db
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(path)
	if err == nil {
		t.Fatalf("LoadYAML returned nil error for blank required database role: %+v", cfg)
	}
	if !strings.Contains(err.Error(), "database.urls.runtime is required") {
		t.Fatalf("LoadYAML error = %v", err)
	}
}

func TestManagerAcceptsDatabaseSectionPostgresURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: postgres://localhost/core
  urls:
    core: postgres://localhost/core
    audit: file://./audit.db
    events: file://./events.db
    jobs: file://./jobs.db
    runtime: file://./runtime.db
    observability: file://./observability.db
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	manager := config.NewManager(cfg)

	if manager.LastError() != nil {
		t.Fatalf("LastError = %v, want nil", manager.LastError())
	}
}

func TestManagerRejectsDatabaseSectionUnsupportedURLScheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: mysql://localhost/core
  urls:
    core: mysql://localhost/core
    audit: file://./audit.db
    events: file://./events.db
    jobs: file://./jobs.db
    runtime: file://./runtime.db
    observability: file://./observability.db
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	manager := config.NewManager(cfg)

	if manager.LastError() == nil {
		t.Fatalf("LastError was nil for unsupported database URL scheme")
	}
}

func TestManagerRejectsS3BucketHostnameMisconfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, withExplicitTestDatabaseYAML(`
filestore:
  backend: s3
  s3:
    bucket: my-bucket.s3.us-east-1.amazonaws.com
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	manager := config.NewManager(cfg)

	if manager.LastError() == nil {
		t.Fatalf("LastError was nil for S3 bucket hostname misconfiguration")
	}
}

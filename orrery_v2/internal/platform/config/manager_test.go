package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/config"
)

func TestManagerProvidesPullBasedLookupFromCurrentSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
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
queue:
  claim_ttl: 15s
  max_attempts: 3
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(cfg)

	if got, ok := manager.String("queue.claim_ttl"); !ok || got != "15s" {
		t.Fatalf("queue.claim_ttl = %q ok=%v", got, ok)
	}
	if got, ok := manager.Int("queue.max_attempts"); !ok || got != 3 {
		t.Fatalf("queue.max_attempts = %d ok=%v", got, ok)
	}

	if err := os.WriteFile(path, []byte(`
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
queue:
  claim_ttl: 45s
  max_attempts: 7
`), 0o600); err != nil {
		t.Fatalf("rewrite yaml: %v", err)
	}
	if err := manager.ReloadFromFile(context.Background(), path); err != nil {
		t.Fatalf("ReloadFromFile: %v", err)
	}
	if got, ok := manager.Duration("queue.claim_ttl"); !ok || got != 45*time.Second {
		t.Fatalf("queue.claim_ttl duration = %v ok=%v", got, ok)
	}
	if got, ok := manager.Int("queue.max_attempts"); !ok || got != 7 {
		t.Fatalf("queue.max_attempts after reload = %d ok=%v", got, ok)
	}
}

func TestManagerDecodesSectionFromCurrentSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
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
worker:
  timeout: 5s
  max_parallel: 2
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(cfg)

	var worker struct {
		Timeout     string `yaml:"timeout"`
		MaxParallel int    `yaml:"max_parallel"`
	}
	if err := manager.DecodeSection("worker", &worker); err != nil {
		t.Fatalf("DecodeSection: %v", err)
	}
	if worker.Timeout != "5s" || worker.MaxParallel != 2 {
		t.Fatalf("worker = %+v", worker)
	}
}

func TestManagerReloadPublishesValidConfigAndKeepsVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	writeExplicitTestDatabaseYAML(t, path, "environment: dev\n")
	initial, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(initial)
	seen := make(chan string, 1)
	unsubscribe, err := manager.Subscribe(func(cfg config.Config) {
		seen <- cfg.Environment
	})
	if err != nil {
		t.Fatalf("Subscribe returned error: %v", err)
	}
	defer unsubscribe()

	writeExplicitTestDatabaseYAML(t, path, "environment: prod\n")
	if err := manager.ReloadFromFile(context.Background(), path); err != nil {
		t.Fatalf("ReloadFromFile: %v", err)
	}
	if manager.Current().Environment != "prod" {
		t.Fatalf("Current environment = %q", manager.Current().Environment)
	}
	if manager.Version() != 2 {
		t.Fatalf("Version = %d, want 2", manager.Version())
	}
	select {
	case got := <-seen:
		if got != "prod" {
			t.Fatalf("subscriber saw %q", got)
		}
	default:
		t.Fatalf("subscriber was not notified")
	}
}

func TestManagerNotifyChangeLetsConsumersPullCurrentSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	writeExplicitTestDatabaseYAML(t, path, "environment: dev\n")
	initial, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(initial)
	seen := make(chan string, 1)
	unsubscribe, err := manager.SubscribeChange(func(version int64) {
		if version != manager.Version() {
			t.Fatalf("version notification = %d, manager version = %d", version, manager.Version())
		}
		seen <- manager.Current().Environment
	})
	if err != nil {
		t.Fatalf("SubscribeChange returned error: %v", err)
	}
	defer unsubscribe()

	writeExplicitTestDatabaseYAML(t, path, "environment: prod\n")
	if err := manager.ReloadFromFile(context.Background(), path); err != nil {
		t.Fatalf("ReloadFromFile: %v", err)
	}

	select {
	case got := <-seen:
		if got != "prod" {
			t.Fatalf("change subscriber pulled environment %q, want prod", got)
		}
	default:
		t.Fatalf("change subscriber was not notified")
	}
}

func TestWatchYAMLReturnsClosableManagerAndStopsReloading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	writeExplicitTestDatabaseYAML(t, path, "environment: dev\n")

	manager, err := config.WatchYAML(context.Background(), path, 10*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("WatchYAML returned error: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	if err := manager.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	writeExplicitTestDatabaseYAML(t, path, "environment: prod\n")
	time.Sleep(50 * time.Millisecond)

	if got := manager.Current().Environment; got != "dev" {
		t.Fatalf("manager reloaded after Close: environment=%q", got)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

func TestManagerRejectsMutatingOperationsAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	writeExplicitTestDatabaseYAML(t, path, "environment: dev\n")
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(cfg)
	if err := manager.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	writeExplicitTestDatabaseYAML(t, path, "environment: prod\n")

	if err := manager.ReloadFromFile(context.Background(), path); err == nil || !strings.Contains(err.Error(), "config manager is closed") {
		t.Fatalf("ReloadFromFile after Close error = %v, want config manager is closed", err)
	}
	if _, err := manager.Subscribe(func(config.Config) {}); err == nil || !strings.Contains(err.Error(), "config manager is closed") {
		t.Fatalf("Subscribe after Close error = %v, want config manager is closed", err)
	}
	if err := manager.RegisterSection("closed_business", nil); err == nil || !strings.Contains(err.Error(), "config manager is closed") {
		t.Fatalf("RegisterSection after Close error = %v, want config manager is closed", err)
	}
	manager.RecordError(errors.New("late reload failure"))
	if err := manager.LastError(); err != nil {
		t.Fatalf("RecordError changed closed manager LastError: %v", err)
	}
	if got := manager.Current().Environment; got != "dev" {
		t.Fatalf("closed manager changed current config: environment=%q", got)
	}
}

func TestManagerReloadIsolatesSubscriberPanics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	writeExplicitTestDatabaseYAML(t, path, "environment: dev\n")
	initial, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(initial)
	if _, err := manager.Subscribe(func(config.Config) {
		panic("subscriber failed")
	}); err != nil {
		t.Fatalf("Subscribe panicking subscriber returned error: %v", err)
	}
	seen := make(chan string, 1)
	unsubscribe, err := manager.Subscribe(func(cfg config.Config) {
		seen <- cfg.Environment
	})
	if err != nil {
		t.Fatalf("Subscribe healthy subscriber returned error: %v", err)
	}
	defer unsubscribe()

	writeExplicitTestDatabaseYAML(t, path, "environment: prod\n")
	if err := manager.ReloadFromFile(context.Background(), path); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	if manager.Current().Environment != "prod" || manager.Version() != 2 {
		t.Fatalf("manager did not publish config: env=%s version=%d", manager.Current().Environment, manager.Version())
	}
	if manager.LastError() == nil {
		t.Fatalf("LastError was nil after subscriber panic")
	}
	select {
	case got := <-seen:
		if got != "prod" {
			t.Fatalf("healthy subscriber saw %q", got)
		}
	default:
		t.Fatalf("healthy subscriber was not notified after another subscriber panicked")
	}
}

func TestManagerRejectsNilSubscriber(t *testing.T) {
	cfg, err := config.LoadYAML(explicitTestConfigPath(t))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	manager := config.NewManager(cfg)

	_, err = manager.Subscribe(nil)
	if err == nil {
		t.Fatalf("Subscribe returned nil error for nil subscriber")
	}
	if manager.LastError() != nil {
		t.Fatalf("LastError = %v, want nil because registration failure is caller error", manager.LastError())
	}
}

func TestManagerCurrentReturnsIsolatedSnapshot(t *testing.T) {
	cfg, err := config.LoadYAML(explicitTestConfigPath(t))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	manager := config.NewManager(cfg)

	snapshot := manager.Current()
	snapshot.DatabaseURLs["events"] = "file://mutated/events.db"
	if database, ok := snapshot.Lookup("database"); ok {
		database.(map[string]any)["default_url"] = "file://mutated/core.db"
	}

	current := manager.Current()
	if current.DatabaseURLs["events"] == "file://mutated/events.db" {
		t.Fatalf("mutating Current snapshot changed manager DatabaseURLs: %+v", current.DatabaseURLs)
	}
	if got, _ := current.String("database.default_url"); got == "file://mutated/core.db" {
		t.Fatalf("mutating Current raw lookup map changed manager raw config")
	}
}

func TestManagerSubscribersReceiveIsolatedSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	writeExplicitTestDatabaseYAML(t, path, "environment: dev\n")
	initial, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(initial)
	if _, err := manager.Subscribe(func(cfg config.Config) {
		cfg.DatabaseURLs["events"] = "file://subscriber-mutated/events.db"
		if database, ok := cfg.Lookup("database"); ok {
			database.(map[string]any)["default_url"] = "file://subscriber-mutated/core.db"
		}
	}); err != nil {
		t.Fatalf("Subscribe returned error: %v", err)
	}

	writeExplicitTestDatabaseYAML(t, path, "environment: prod\n")
	if err := manager.ReloadFromFile(context.Background(), path); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}

	current := manager.Current()
	if current.DatabaseURLs["events"] == "file://subscriber-mutated/events.db" {
		t.Fatalf("subscriber mutated manager DatabaseURLs: %+v", current.DatabaseURLs)
	}
	if got, _ := current.String("database.default_url"); got == "file://subscriber-mutated/core.db" {
		t.Fatalf("subscriber mutated manager raw config")
	}
}

func TestManagerSectionValidatorsReceiveIsolatedSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	writeExplicitTestDatabaseYAML(t, path, "environment: dev\n")
	initial, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(initial)
	if err := manager.RegisterSection("mission", func(cfg config.Config) error {
		cfg.DatabaseURLs["events"] = "file://validator-mutated/events.db"
		if database, ok := cfg.Lookup("database"); ok {
			database.(map[string]any)["default_url"] = "file://validator-mutated/core.db"
		}
		return nil
	}); err != nil {
		t.Fatalf("RegisterSection returned error: %v", err)
	}
	current := manager.Current()
	if current.DatabaseURLs["events"] == "file://validator-mutated/events.db" {
		t.Fatalf("RegisterSection validator mutated current DatabaseURLs: %+v", current.DatabaseURLs)
	}
	if got, _ := current.String("database.default_url"); got == "file://validator-mutated/core.db" {
		t.Fatalf("RegisterSection validator mutated current raw config")
	}

	writeExplicitTestDatabaseYAML(t, path, "environment: prod\n")
	if err := manager.ReloadFromFile(context.Background(), path); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	current = manager.Current()
	if current.DatabaseURLs["events"] == "file://validator-mutated/events.db" {
		t.Fatalf("reload validator mutated published DatabaseURLs: %+v", current.DatabaseURLs)
	}
	if got, _ := current.String("database.default_url"); got == "file://validator-mutated/core.db" {
		t.Fatalf("reload validator mutated published raw config")
	}
}

func TestManagerRegisterSectionConvertsValidatorPanicToError(t *testing.T) {
	cfg, err := config.LoadYAML(explicitTestConfigPath(t))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	manager := config.NewManager(cfg)

	err = manager.RegisterSection("mission", func(config.Config) error {
		panic("validator failed")
	})
	if err == nil {
		t.Fatalf("RegisterSection returned nil error for validator panic")
	}
	if manager.LastError() == nil {
		t.Fatalf("LastError was nil after validator panic")
	}
	for _, section := range manager.RegisteredSections() {
		if section == "mission" {
			t.Fatalf("panicking section was registered")
		}
	}
}

func TestManagerReloadConvertsValidatorPanicToErrorAndKeepsLastKnownGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	writeExplicitTestDatabaseYAML(t, path, "environment: dev\n")
	initial, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(initial)
	if err := manager.RegisterSection("mission", func(cfg config.Config) error {
		if cfg.Environment == "prod" {
			panic("validator failed during reload")
		}
		return nil
	}); err != nil {
		t.Fatalf("RegisterSection returned error: %v", err)
	}

	writeExplicitTestDatabaseYAML(t, path, "environment: prod\n")
	if err := manager.ReloadFromFile(context.Background(), path); err == nil {
		t.Fatalf("ReloadFromFile returned nil error for validator panic")
	}
	if manager.Current().Environment != "dev" {
		t.Fatalf("reload panic changed current config: %+v", manager.Current())
	}
	if manager.Version() != 1 {
		t.Fatalf("Version = %d, want 1", manager.Version())
	}
	if manager.LastError() == nil {
		t.Fatalf("LastError was nil after reload validator panic")
	}
}

func TestManagerInvalidReloadKeepsLastKnownGoodConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	writeExplicitTestDatabaseYAML(t, path, "environment: dev\n")
	initial, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	manager := config.NewManager(initial)

	writeExplicitTestDatabaseYAML(t, path, "health:\n  readiness_cache_ttl: not-a-duration\n")
	if err := manager.ReloadFromFile(context.Background(), path); err == nil {
		t.Fatalf("ReloadFromFile invalid yaml returned nil error")
	}
	if manager.Current().Environment != "dev" {
		t.Fatalf("invalid reload changed config: %+v", manager.Current())
	}
	if manager.Version() != 1 {
		t.Fatalf("Version = %d, want 1", manager.Version())
	}
	if manager.LastError() == nil {
		t.Fatalf("LastError was nil")
	}
}

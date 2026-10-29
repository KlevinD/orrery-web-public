package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/config"
)

func TestLoadYAMLRequiresExplicitDatabaseConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte("environment: development\n"), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	_, err := config.LoadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "database configuration is required") {
		t.Fatalf("LoadYAML error = %v", err)
	}
}

func TestYAMLSplitDatabaseURLsMustBeComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	body := []byte(`
database:
  default_url: "postgres://orrery:secret@localhost:5432/orrery?sslmode=disable"
  urls:
    core: "postgres://orrery:secret@localhost:5432/orrery_core?sslmode=disable"
    events: "postgres://orrery:secret@localhost:5432/orrery_events?sslmode=disable"
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg, err := config.LoadYAML(path)
	if err == nil {
		t.Fatalf("LoadYAML returned nil error for incomplete split database URLs: %+v", cfg)
	}
	if !strings.Contains(err.Error(), "database.urls.audit is required") {
		t.Fatalf("LoadYAML error = %v", err)
	}
}

func TestYAMLDefaultDatabaseURLAppliesToAllRolesWhenSplitURLsAreUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	body := []byte(`
database:
  default_url: "postgres://orrery:secret@localhost:5432/orrery?sslmode=disable"
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://orrery:secret@localhost:5432/orrery?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	for _, role := range []string{"core", "audit", "events", "jobs", "runtime", "observability"} {
		if cfg.DatabaseURLs[role] != cfg.DatabaseURL {
			t.Fatalf("DatabaseURLs[%s] = %q, want default %q; all=%+v", role, cfg.DatabaseURLs[role], cfg.DatabaseURL, cfg.DatabaseURLs)
		}
	}
}

func TestLoadYAMLDoesNotBlankYAMLBaseDirWhenEnvUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	body := []byte(`
paths:
  base_dir: ./runtime
database:
  default_url: file://./data/core.db
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	wantBase := filepath.Join(filepath.Dir(path), "runtime")
	if cfg.BaseDir != wantBase {
		t.Fatalf("BaseDir = %q, want %q", cfg.BaseDir, wantBase)
	}
	if cfg.DatabaseURL != "file://"+filepath.Join(wantBase, "data", "core.db") {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}

func TestLoadYAMLPreservesPostgresDatabaseURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: postgres://orrery:secret@localhost:5432/orrery?sslmode=disable
  urls:
    core: postgres://orrery:secret@localhost:5432/orrery_core?sslmode=disable
    audit: postgresql://orrery:secret@localhost:5432/orrery_audit?sslmode=disable
    events: postgres://orrery:secret@localhost:5432/orrery_events?sslmode=disable
    jobs: postgres://orrery:secret@localhost:5432/orrery_jobs?sslmode=disable
    runtime: postgres://orrery:secret@localhost:5432/orrery_runtime?sslmode=disable
    observability: postgres://orrery:secret@localhost:5432/orrery_observability?sslmode=disable
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.DatabaseURL != "postgres://orrery:secret@localhost:5432/orrery?sslmode=disable" {
		t.Fatalf("DatabaseURL was rewritten: %q", cfg.DatabaseURL)
	}
	if cfg.DatabaseURLs["audit"] != "postgresql://orrery:secret@localhost:5432/orrery_audit?sslmode=disable" {
		t.Fatalf("audit URL was rewritten: %q", cfg.DatabaseURLs["audit"])
	}
}

func TestLoadYAMLIgnoresNonSecretProcessEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
environment: development
server:
  addr: 127.0.0.1:8080
database:
  default_url: memory://yaml
discussion:
  agent_id: yaml-agent
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	t.Setenv("ORRERY_ENV", "production")
	t.Setenv("ORRERY_API_ADDR", "0.0.0.0:9999")
	t.Setenv("ORRERY_DATABASE_URL", "memory://process")
	t.Setenv("ORRERY_DISCUSSION_AGENT_ID", "process-agent")

	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	if cfg.Environment != "development" || cfg.Server.Addr != "127.0.0.1:8080" || cfg.DatabaseURL != "memory://yaml" {
		t.Fatalf("config = %+v", cfg)
	}
	assertStringLookup(t, cfg, "discussion.agent_id", "yaml-agent")
}

func TestLoadYAMLUsesSharedFeishuApplicationIDFromEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: memory://shared-feishu-config
auth:
  feishu:
    app_id: yaml-app-id
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	t.Setenv("ORRERY_FEISHU_APP_ID", "shared-app-id")

	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	assertStringLookup(t, cfg, "auth.feishu.app_id", "shared-app-id")
}

func TestYAMLUpdatesTypedConfigAndDottedLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	body := []byte(`
environment: override
database:
  default_url: file://./override/core.db
  urls:
    core: file://./override/core.db
    audit: file://./override/audit.db
    events: file://./override/events.db
    jobs: file://./override/jobs.db
    runtime: file://./override/runtime.db
    observability: file://./override/observability.db
filestore:
  root: ./artifacts
health:
  readiness_cache_ttl: 4s
queue:
  claim_ttl: 10s
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	base := filepath.Dir(path)
	wantDatabaseURL := "file://" + filepath.Join(base, "override", "core.db")
	wantJobsURL := "file://" + filepath.Join(base, "override", "jobs.db")
	wantFileStoreRoot := filepath.Join(base, "artifacts")
	if cfg.Environment != "override" || cfg.DatabaseURL != wantDatabaseURL || cfg.DatabaseURLs["jobs"] != wantJobsURL || cfg.FileStoreRoot != wantFileStoreRoot || cfg.ReadinessCacheTTL != 4*time.Second {
		t.Fatalf("typed config did not apply precedence: %+v", cfg)
	}
	assertStringLookup(t, cfg, "environment", "override")
	assertStringLookup(t, cfg, "database.default_url", wantDatabaseURL)
	assertStringLookup(t, cfg, "database.urls.jobs", wantJobsURL)
	assertStringLookup(t, cfg, "filestore.root", wantFileStoreRoot)
	if got, ok := cfg.Duration("health.readiness_cache_ttl"); !ok || got != 4*time.Second {
		t.Fatalf("health.readiness_cache_ttl lookup = %v ok=%v, want 4s", got, ok)
	}
}

func TestMetricsConfigSupportsServerAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, withExplicitTestDatabaseYAML(`
metrics:
  addr: 127.0.0.1:0
  buffer_size: 32
  warn_interval: 3s
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	t.Setenv("ORRERY_METRICS_ADDR", "127.0.0.1:9090")
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Metrics.Addr != "127.0.0.1:0" {
		t.Fatalf("Metrics.Addr = %q, want YAML value", cfg.Metrics.Addr)
	}
	assertStringLookup(t, cfg, "metrics.addr", "127.0.0.1:0")
	if got, ok := cfg.Int("metrics.buffer_size"); !ok || got != 32 {
		t.Fatalf("metrics.buffer_size lookup = %d ok=%v", got, ok)
	}
}

func TestConfigPreservesAndDecodesExtensionSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	body := withExplicitTestDatabaseYAML(`
environment: dev
mission:
  max_parallel_tasks: 8
  planner: codex
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if got, ok := cfg.Int("mission.max_parallel_tasks"); !ok || got != 8 {
		t.Fatalf("mission.max_parallel_tasks lookup = %d ok=%v, want 8", got, ok)
	}
	var mission struct {
		MaxParallelTasks int    `yaml:"max_parallel_tasks"`
		Planner          string `yaml:"planner"`
	}
	if err := cfg.DecodeSection("mission", &mission); err != nil {
		t.Fatalf("DecodeSection returned error: %v", err)
	}
	if mission.MaxParallelTasks != 8 || mission.Planner != "codex" {
		t.Fatalf("mission section = %+v", mission)
	}
}

func TestLoadYAMLAppliesExplicitBaseDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), "runtime-root")
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	body := []byte(`
paths:
  base_dir: ` + base + `
database:
  default_url: file://./data/core.db
  urls:
    core: file://./data/core.db
    audit: file://./data/audit.db
    events: file://./data/events.db
    jobs: file://./data/jobs.db
    runtime: file://./data/runtime.db
    observability: file://./data/observability.db
filestore:
  root: ./artifacts
logging:
  file: ./logs/platform.jsonl
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.BaseDir != base {
		t.Fatalf("BaseDir = %q, want %q", cfg.BaseDir, base)
	}
	assertStringLookup(t, cfg, "paths.base_dir", base)
	assertStringLookup(t, cfg, "database.default_url", "file://"+filepath.Join(base, "data", "core.db"))
	assertStringLookup(t, cfg, "filestore.root", filepath.Join(base, "artifacts"))
	assertStringLookup(t, cfg, "logging.file", filepath.Join(base, "logs", "platform.jsonl"))
	assertStringLookup(t, cfg, "secret.root", filepath.Join(base, "secrets"))
}

func assertStringLookup(t *testing.T, cfg config.Config, path, want string) {
	t.Helper()
	got, ok := cfg.String(path)
	if !ok || got != want {
		t.Fatalf("%s lookup = %q ok=%v, want %q", path, got, ok, want)
	}
}

func TestConfigLookupSupportsDottedTypedAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: file://./data/core.db
  urls:
    core: file://./data/core.db
    audit: file://./data/audit.db
    events: file://./data/events.db
    jobs: file://./data/jobs.db
    runtime: file://./data/runtime.db
    observability: file://./data/observability.db
queue:
  claim_ttl: 45s
  max_attempts: 5
logging:
  console: true
sweeper:
  timeout: 5s
  batch_size: 50
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	wantEventsURL := "file://" + filepath.Join(filepath.Dir(path), "data", "events.db")
	if got, ok := cfg.String("database.urls.events"); !ok || got != wantEventsURL {
		t.Fatalf("database.urls.events = %q ok=%v", got, ok)
	}
	if got, ok := cfg.Duration("queue.claim_ttl"); !ok || got != 45*time.Second {
		t.Fatalf("queue.claim_ttl = %v ok=%v", got, ok)
	}
	if got, ok := cfg.Int("queue.max_attempts"); !ok || got != 5 {
		t.Fatalf("queue.max_attempts = %d ok=%v", got, ok)
	}
	if got, ok := cfg.Bool("logging.console"); !ok || !got {
		t.Fatalf("logging.console = %v ok=%v", got, ok)
	}
	if got, ok := cfg.Duration("sweeper.timeout"); !ok || got != 5*time.Second {
		t.Fatalf("sweeper.timeout = %v ok=%v", got, ok)
	}
	if got, ok := cfg.Int("sweeper.batch_size"); !ok || got != 50 {
		t.Fatalf("sweeper.batch_size = %d ok=%v", got, ok)
	}
	if got, ok := cfg.DatabaseURLFor("events"); !ok || got != wantEventsURL {
		t.Fatalf("DatabaseURLFor(events) = %q ok=%v", got, ok)
	}
	if _, ok := cfg.Lookup("database.urls.missing"); ok {
		t.Fatalf("missing lookup returned ok")
	}
}

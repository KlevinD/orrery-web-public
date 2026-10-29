package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"orrery_v2/internal/platform/config"
)

func TestLoadYAMLPopulatesNestedProductionConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	body := []byte(`
environment: production
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
  backend: local
  root: ./data/artifacts
  base_url: http://localhost:8080
  s3:
    bucket: orrery-artifacts
    region: us-east-1
    endpoint_url: http://localhost:9000
    cdn_domain: cdn.example.com
logging:
  level: warn
  console: true
  file: ./data/logs/platform.jsonl
health:
  readiness_cache_ttl: 3s
queue:
  claim_ttl: 45s
  max_attempts: 5
  sweep_batch_size: 250
  worker_concurrency: 7
mission:
  agent:
    timeout: 75m
lock:
  default_ttl: 20s
liveness:
  heartbeat_ttl: 2m
metrics:
  buffer_size: 2048
  warn_interval: 30s
tracing:
  buffer_size: 64
sweeper:
  timeout: 5s
  batch_size: 50
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("Load YAML returned error: %v", err)
	}

	if cfg.Environment != "production" {
		t.Fatalf("Environment = %q, want production", cfg.Environment)
	}
	base := filepath.Dir(path)
	wantCoreURL := "file://" + filepath.Join(base, "data", "core.db")
	wantAuditURL := "file://" + filepath.Join(base, "data", "audit.db")
	wantEventsURL := "file://" + filepath.Join(base, "data", "events.db")
	wantFileStoreRoot := filepath.Join(base, "data", "artifacts")
	wantLogFile := filepath.Join(base, "data", "logs", "platform.jsonl")
	if cfg.DatabaseURL != wantCoreURL || cfg.DatabaseURLs["audit"] != wantAuditURL || cfg.DatabaseURLs["events"] != wantEventsURL {
		t.Fatalf("database config = %+v default=%s", cfg.DatabaseURLs, cfg.DatabaseURL)
	}
	if cfg.FileStoreRoot != wantFileStoreRoot {
		t.Fatalf("FileStoreRoot = %q", cfg.FileStoreRoot)
	}
	if cfg.FileStore.Backend != "local" || cfg.FileStore.BaseURL != "http://localhost:8080" || cfg.FileStore.S3.Bucket != "orrery-artifacts" || cfg.FileStore.S3.EndpointURL != "http://localhost:9000" {
		t.Fatalf("FileStore = %+v", cfg.FileStore)
	}
	if cfg.Logging.Level != "warn" || !cfg.Logging.Console || cfg.Logging.File != wantLogFile {
		t.Fatalf("Logging = %+v", cfg.Logging)
	}
	if cfg.ReadinessCacheTTL != 3*time.Second {
		t.Fatalf("ReadinessCacheTTL = %v", cfg.ReadinessCacheTTL)
	}
	if cfg.Queue.ClaimTTL != 45*time.Second || cfg.Queue.MaxAttempts != 5 || cfg.Queue.SweepBatchSize != 250 || cfg.Queue.WorkerConcurrency != 7 {
		t.Fatalf("Queue = %+v", cfg.Queue)
	}
	if timeout, ok := cfg.Duration("mission.agent.timeout"); !ok || timeout != 75*time.Minute {
		t.Fatalf("mission.agent.timeout = %s ok=%v", timeout, ok)
	}
	if cfg.Lock.DefaultTTL != 20*time.Second || cfg.Liveness.HeartbeatTTL != 2*time.Minute {
		t.Fatalf("Lock/Liveness = %+v %+v", cfg.Lock, cfg.Liveness)
	}
	if cfg.Metrics.BufferSize != 2048 || cfg.Metrics.WarnInterval != 30*time.Second {
		t.Fatalf("Metrics = %+v", cfg.Metrics)
	}
	if cfg.Tracing.BufferSize != 64 {
		t.Fatalf("Tracing = %+v", cfg.Tracing)
	}
	if cfg.Sweeper.Timeout != 5*time.Second || cfg.Sweeper.BatchSize != 50 {
		t.Fatalf("Sweeper = %+v", cfg.Sweeper)
	}
}

func TestLoadYAMLPopulatesPeopleResponsibilityIntegration(t *testing.T) {
	path := writeConfigBody(t, string(withExplicitTestDatabaseYAML(`
integrations:
  people:
    enabled: true
    base_url: http://127.0.0.1:18083
    token_ref: secret://integrations/people/token
    people_tenant_id: tenant-preview
    orrery_tenant_id: system
    reconcile_interval: 15m
    planner_cron: 0 9 * * *
    planner_timezone: Asia/Shanghai
    agent_bindings:
      member-openclaw-main: main
    human_role_mappings:
      approval.reviewer: approval.reviewer
`)))
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	people := cfg.Integrations.People
	if !people.Enabled || people.BaseURL != "http://127.0.0.1:18083" || people.TokenRef != "secret://integrations/people/token" || people.PeopleTenantID != "tenant-preview" || people.OrreryTenantID != "system" || people.ReconcileInterval != 15*time.Minute || people.PlannerCron != "0 9 * * *" || people.PlannerTimezone != "Asia/Shanghai" || people.AgentBindings["member-openclaw-main"] != "main" || people.HumanRoleMappings["approval.reviewer"] != "approval.reviewer" {
		t.Fatalf("People integration = %+v", people)
	}
	assertStringLookup(t, cfg, "integrations.people.token_ref", "secret://integrations/people/token")
}

func TestLoadYAMLRejectsBlankPeopleHumanRoleMapping(t *testing.T) {
	path := writeConfigBody(t, string(withExplicitTestDatabaseYAML(`
integrations:
  people:
    enabled: true
    base_url: http://127.0.0.1:18083
    token_ref: secret://integrations/people/token
    people_tenant_id: tenant-preview
    orrery_tenant_id: system
    reconcile_interval: 15m
    planner_cron: 0 9 * * *
    planner_timezone: Asia/Shanghai
    agent_bindings:
      member-openclaw-main: main
    human_role_mappings:
      approval.reviewer: ""
`)))
	_, err := config.LoadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "integrations.people.human_role_mappings") {
		t.Fatalf("LoadYAML error = %v, want blank human role mapping rejection", err)
	}
}

func TestLoadYAMLRejectsIncompleteEnabledPeopleResponsibilityIntegration(t *testing.T) {
	path := writeConfigBody(t, string(withExplicitTestDatabaseYAML(`
integrations:
  people:
    enabled: true
    base_url: http://127.0.0.1:18083
    people_tenant_id: tenant-preview
    orrery_tenant_id: system
    reconcile_interval: 15m
    planner_cron: 0 9 * * *
    planner_timezone: Asia/Shanghai
    agent_bindings:
      member-openclaw-main: main
`)))
	_, err := config.LoadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "integrations.people.token_ref") {
		t.Fatalf("LoadYAML error = %v, want missing token_ref", err)
	}
}

func TestLoadYAMLRejectsUnknownFieldsInPlatformSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	body := withExplicitTestDatabaseYAML(`
logging:
  levle: info
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	_, err := config.LoadYAML(path)
	if err == nil {
		t.Fatal("LoadYAML accepted unknown logging field")
	}
	if !strings.Contains(err.Error(), "levle") || !strings.Contains(err.Error(), "field") {
		t.Fatalf("LoadYAML error = %v, want unknown field detail", err)
	}
}

func TestLoadYAMLDevelopmentSelfApprovalOverrideRequiresExplicitLocalMode(t *testing.T) {
	developmentPath := writeConfigBody(t, string(withExplicitTestDatabaseYAML("environment: development\nauth:\n  local:\n    enabled: true\ndevelopment:\n  allow_owner_self_approval: true\n")))
	development, err := config.LoadYAML(developmentPath)
	if err != nil || !development.Development.AllowOwnerSelfApproval {
		t.Fatalf("development override = %+v err=%v", development.Development, err)
	}

	saasPath := writeConfigBody(t, string(withExplicitTestDatabaseYAML("environment: production\nauth:\n  local:\n    enabled: false\ndevelopment:\n  allow_owner_self_approval: true\n")))
	_, err = config.LoadYAML(saasPath)
	if err == nil || !strings.Contains(err.Error(), "allow_owner_self_approval") {
		t.Fatalf("production override error = %v", err)
	}
}

func TestLoadYAMLRejectsMultipleDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	body := append(withExplicitTestDatabaseYAML("environment: development\n"), []byte("---\nenvironment: production\n")...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	_, err := config.LoadYAML(path)
	if err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("LoadYAML error = %v, want multiple document rejection", err)
	}
}

func TestProductionSampleConfigLoadsPostgresURLs(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "test-password")
	path := filepath.Join("..", "..", "..", "config", "orrery.production.yaml")
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	if cfg.Environment != "production" {
		t.Fatalf("Environment = %q, want production", cfg.Environment)
	}
	wantCore := "postgres://orrery:test-password@orrery-postgres:5432/orrery_core?sslmode=disable"
	if cfg.DatabaseURL != wantCore || cfg.DatabaseURLs["core"] != wantCore {
		t.Fatalf("core database config = default %q urls=%+v", cfg.DatabaseURL, cfg.DatabaseURLs)
	}
	for _, role := range []string{"audit", "events", "jobs", "runtime", "observability"} {
		if got := cfg.DatabaseURLs[role]; !strings.HasPrefix(got, "postgres://") {
			t.Fatalf("DatabaseURLs[%s] = %q, want postgres URL; all=%+v", role, got, cfg.DatabaseURLs)
		}
	}
}

func TestDevelopmentSampleConfigLoadsPostgresURLs(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "orrery")
	path := filepath.Join("..", "..", "..", "config", "orrery.development.yaml")
	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	if cfg.Environment != "development" {
		t.Fatalf("Environment = %q, want development", cfg.Environment)
	}
	if cfg.DeviceDelivery.PublicOrigin != "" || cfg.DeviceDelivery.DefaultTenantID != "global" || cfg.DeviceDelivery.MaxArtifactBytes != 10*1024*1024 {
		t.Fatalf("DeviceDelivery = %+v", cfg.DeviceDelivery)
	}
	wantBase := filepath.Clean(filepath.Join(filepath.Dir(path), "..", ".dev"))
	wantCore := "postgres://orrery:orrery@127.0.0.1:5433/orrery?sslmode=disable"
	if cfg.BaseDir != wantBase {
		t.Fatalf("BaseDir = %q, want %q", cfg.BaseDir, wantBase)
	}
	if cfg.DatabaseURL != wantCore || cfg.DatabaseURLs["core"] != wantCore {
		t.Fatalf("core database config = default %q urls=%+v", cfg.DatabaseURL, cfg.DatabaseURLs)
	}
	for _, role := range []string{"audit", "events", "jobs", "runtime", "observability"} {
		if got := cfg.DatabaseURLs[role]; got != wantCore {
			t.Fatalf("DatabaseURLs[%s] = %q, want shared local postgres URL %q; all=%+v", role, got, wantCore, cfg.DatabaseURLs)
		}
	}
}

func TestFeishuDirectorySyncConfigurationLoadsAsStagingOnly(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "orrery")
	body := strings.Replace(
		readTrackedConfig(t, "orrery.development.yaml"),
		"    directory_sync_enabled: false",
		"    directory_sync_enabled: true",
		1,
	)
	cfg, err := config.LoadYAML(writeConfigBody(t, body))
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	if enabled, ok := cfg.Bool("auth.feishu.directory_sync_enabled"); !ok || !enabled {
		t.Fatalf("directory_sync_enabled = %t, present=%t", enabled, ok)
	}
}

func TestDeviceDeliveryProfileValidation(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "test-password")
	development := readTrackedConfig(t, "orrery.development.yaml")
	tests := []struct {
		name        string
		body        string
		wantMessage string
	}{
		{
			name: "loopback HTTP is allowed in development",
			body: strings.Replace(development, `  public_origin: ""`, `  public_origin: http://127.0.0.1:52021`, 1),
		},
		{
			name:        "remote HTTP is rejected",
			body:        strings.Replace(development, `  public_origin: ""`, `  public_origin: http://orrery.example.com`, 1),
			wantMessage: "must use HTTPS",
		},
		{
			name:        "origin path is rejected",
			body:        strings.Replace(development, `  public_origin: ""`, `  public_origin: https://orrery.example.com/device`, 1),
			wantMessage: "without credentials, path, query, or fragment",
		},
		{
			name:        "empty tenant is rejected",
			body:        strings.Replace(development, `  default_tenant_id: global`, `  default_tenant_id: ""`, 1),
			wantMessage: "default_tenant_id is required",
		},
		{
			name:        "oversized artifact limit is rejected",
			body:        strings.Replace(development, `  max_artifact_bytes: 10485760`, `  max_artifact_bytes: 104857601`, 1),
			wantMessage: "max_artifact_bytes must be between",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := config.LoadYAML(writeConfigBody(t, test.body))
			if test.wantMessage == "" {
				if err != nil {
					t.Fatalf("LoadYAML returned error: %v", err)
				}
				if cfg.DeviceDelivery.PublicOrigin != "http://127.0.0.1:52021" {
					t.Fatalf("public origin = %q", cfg.DeviceDelivery.PublicOrigin)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("LoadYAML error = %v, want %q", err, test.wantMessage)
			}
		})
	}
}

func TestProductionDeviceDeliveryRequiresHTTPSPublicOrigin(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "test-password")
	body := strings.Replace(
		readTrackedConfig(t, "orrery.production.yaml"),
		"  public_origin: https://orrery.example.com",
		`  public_origin: ""`,
		1,
	)
	_, err := config.LoadYAML(writeConfigBody(t, body))
	if err == nil || !strings.Contains(err.Error(), "public_origin is required in production") {
		t.Fatalf("LoadYAML error = %v, want production public origin rejection", err)
	}
}

func TestTrackedProfileConfigsUseTheSameTopLevelSchema(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "test-password")
	paths := []string{
		filepath.Join("..", "..", "..", "config", "orrery.development.yaml"),
		filepath.Join("..", "..", "..", "config", "orrery.wsl-development.yaml"),
		filepath.Join("..", "..", "..", "config", "orrery.production.yaml"),
		filepath.Join("..", "..", "..", "config", "orrery.desktop.yaml"),
	}
	var wantKeys []string
	for _, path := range paths {
		if _, err := config.LoadYAML(path); err != nil {
			t.Fatalf("LoadYAML(%s) returned error: %v", path, err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(raw, &document); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		keys := make([]string, 0, len(document))
		for key := range document {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		if wantKeys == nil {
			wantKeys = keys
			continue
		}
		if !reflect.DeepEqual(keys, wantKeys) {
			t.Fatalf("top-level keys for %s = %v, want %v", path, keys, wantKeys)
		}
	}
}

func TestProfileConfigRejectsUnknownTopLevelSection(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "test-password")
	body := readTrackedConfig(t, "orrery.development.yaml") + "\nserver_typo: {}\n"
	_, err := config.LoadYAML(writeConfigBody(t, body))
	if err == nil || !strings.Contains(err.Error(), "server_typo") {
		t.Fatalf("LoadYAML error = %v, want unknown top-level section", err)
	}
}

func TestProfileConfigRejectsUnknownNestedField(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "test-password")
	body := strings.Replace(readTrackedConfig(t, "orrery.development.yaml"), "  level: info", "  levle: info", 1)
	_, err := config.LoadYAML(writeConfigBody(t, body))
	if err == nil || !strings.Contains(err.Error(), "levle") {
		t.Fatalf("LoadYAML error = %v, want unknown nested field", err)
	}
}

func TestProfileConfigRejectsMissingRequiredNestedField(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "test-password")
	body := strings.Replace(readTrackedConfig(t, "orrery.development.yaml"), "  level: info", "", 1)
	_, err := config.LoadYAML(writeConfigBody(t, body))
	if err == nil || !strings.Contains(err.Error(), "logging.level is required") {
		t.Fatalf("LoadYAML error = %v, want missing nested field", err)
	}
}

func TestPostgresProfileRequiresDatabasePassword(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "")
	_, err := config.LoadYAML(filepath.Join("..", "..", "..", "config", "orrery.production.yaml"))
	if err == nil || !strings.Contains(err.Error(), "ORRERY_DATABASE_PASSWORD") {
		t.Fatalf("LoadYAML error = %v, want missing database password", err)
	}
}

func TestProductionProfilePinsDefaultPiRuntimeBinding(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "test-password")
	cfg, err := config.LoadYAML(filepath.Join("..", "..", "..", "config", "orrery.production.yaml"))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	kind, _ := cfg.String("agent_defaults.runtime_kind")
	configRef, _ := cfg.String("agent_defaults.runtime_config_ref")
	if kind != "pi" || configRef != "runtime-pi-orrery-native" {
		t.Fatalf("production default runtime = kind %q config ref %q", kind, configRef)
	}
}

func TestProfileRejectsInlineDatabasePasswordWithoutLeakingIt(t *testing.T) {
	const marker = "inline-secret-marker"
	t.Setenv("ORRERY_DATABASE_PASSWORD", "injected-password")
	body := strings.Replace(readTrackedConfig(t, "orrery.production.yaml"), "postgres://orrery@", "postgres://orrery:"+marker+"@", 1)
	_, err := config.LoadYAML(writeConfigBody(t, body))
	if err == nil || !strings.Contains(err.Error(), "must not contain an inline password") {
		t.Fatalf("LoadYAML error = %v, want inline password rejection", err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("LoadYAML error leaked database password: %v", err)
	}
}

func TestProfileMustMatchEnvironment(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "test-password")
	body := strings.Replace(readTrackedConfig(t, "orrery.production.yaml"), "environment: production", "environment: development", 1)
	_, err := config.LoadYAML(writeConfigBody(t, body))
	if err == nil || !strings.Contains(err.Error(), "does not match environment") {
		t.Fatalf("LoadYAML error = %v, want profile mismatch", err)
	}
}

func TestProductionProfileRejectsNonPostgresDefaultURL(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "test-password")
	body := strings.Replace(
		readTrackedConfig(t, "orrery.production.yaml"),
		`default_url: "postgres://orrery@orrery-postgres:5432/orrery_core?sslmode=disable"`,
		`default_url: "file://./data/core.db"`,
		1,
	)
	_, err := config.LoadYAML(writeConfigBody(t, body))
	if err == nil || !strings.Contains(err.Error(), "database.default_url must use PostgreSQL") {
		t.Fatalf("LoadYAML error = %v, want production default URL rejection", err)
	}
}

func TestProductionProfileAcceptsEmbeddedDatabaseAndDisabledPageIndexBootstrap(t *testing.T) {
	body := readTrackedConfig(t, "orrery.desktop.yaml")
	body = strings.Replace(body, "profile: development", "profile: production", 1)
	body = strings.Replace(body, "environment: development", "environment: production", 1)
	body = strings.Replace(body, `  public_origin: ""`, `  public_origin: https://orrery.example.com`, 1)
	body = strings.Replace(body, `  password_env: ""`, "  password_env: \"\"\n  allow_embedded_in_production: true", 1)
	body = strings.Replace(body, "  pageindex:\n", "  pageindex:\n    enabled: false\n", 1)

	cfg, err := config.LoadYAML(writeConfigBody(t, body))
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	if !cfg.AllowEmbeddedProductionDatabase || !strings.HasPrefix(cfg.DatabaseURL, "file://") {
		t.Fatalf("embedded production database config = %+v", cfg)
	}
}

func TestProfileRejectsUnknownDatabaseRole(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "test-password")
	body := strings.Replace(
		strings.ReplaceAll(readTrackedConfig(t, "orrery.development.yaml"), "\r\n", "\n"),
		`    observability: "postgres://orrery@127.0.0.1:5433/orrery?sslmode=disable"`,
		`    observability: "postgres://orrery@127.0.0.1:5433/orrery?sslmode=disable"
    typo: "postgres://orrery@127.0.0.1:5433/orrery?sslmode=disable"`,
		1,
	)
	_, err := config.LoadYAML(writeConfigBody(t, body))
	if err == nil || !strings.Contains(err.Error(), "database.urls.typo") {
		t.Fatalf("LoadYAML error = %v, want unknown role rejection", err)
	}
}

func TestDevelopmentProfileLoadsPeopleResponsibilityIntegration(t *testing.T) {
	t.Setenv("ORRERY_DATABASE_PASSWORD", "test-password")
	body := strings.Replace(
		strings.ReplaceAll(readTrackedConfig(t, "orrery.development.yaml"), "\r\n", "\n"),
		"  people:\n    enabled: false",
		`  people:
    enabled: true
    base_url: http://orrery-responsibility-people:18083
    token_ref: secret://integrations/people/token
    people_tenant_id: tenant-preview
    orrery_tenant_id: system
    reconcile_interval: 7m
    planner_cron: "0 9 * * *"
    planner_timezone: Asia/Shanghai
    agent_bindings:
      member-openclaw-marlon-brando: legal-orchestrator-agent`,
		1,
	)
	cfg, err := config.LoadYAML(writeConfigBody(t, body))
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	people := cfg.Integrations.People
	if !people.Enabled || people.BaseURL != "http://orrery-responsibility-people:18083" ||
		people.TokenRef != "secret://integrations/people/token" || people.PeopleTenantID != "tenant-preview" ||
		people.OrreryTenantID != "system" || people.ReconcileInterval != 7*time.Minute ||
		people.AgentBindings["member-openclaw-marlon-brando"] != "legal-orchestrator-agent" {
		t.Fatalf("People integration = %+v", people)
	}
}

func readTrackedConfig(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", name))
	if err != nil {
		t.Fatalf("read tracked config %s: %v", name, err)
	}
	return string(raw)
}

func writeConfigBody(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestYAMLBaseDirResolvesLocalRelativePaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-root")
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	body := []byte(`
paths:
  base_dir: ` + root + `
database:
  default_url: file://./data/core.db
  urls:
    core: file://./data/core.db
    audit: file://./data/audit.db
    events: sqlite://./data/events.db
    jobs: memory://jobs
    runtime: file://./data/runtime.db
    observability: file://./data/observability.db
filestore:
  root: ./artifacts
logging:
  file: ./logs/platform.jsonl
secret:
  root: ./secrets
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	wantCore := "file://" + filepath.Join(root, "data", "core.db")
	wantEvents := "sqlite://" + filepath.Join(root, "data", "events.db")
	if cfg.BaseDir != root {
		t.Fatalf("BaseDir = %q, want %q", cfg.BaseDir, root)
	}
	if cfg.DatabaseURL != wantCore || cfg.DatabaseURLs["core"] != wantCore || cfg.DatabaseURLs["events"] != wantEvents {
		t.Fatalf("database paths not resolved: default=%q urls=%+v", cfg.DatabaseURL, cfg.DatabaseURLs)
	}
	if cfg.DatabaseURLs["jobs"] != "memory://jobs" {
		t.Fatalf("memory URL was rewritten: %q", cfg.DatabaseURLs["jobs"])
	}
	if cfg.FileStoreRoot != filepath.Join(root, "artifacts") || cfg.FileStore.Root != filepath.Join(root, "artifacts") {
		t.Fatalf("filestore root = %q / %+v, want base-dir resolved", cfg.FileStoreRoot, cfg.FileStore)
	}
	if cfg.Logging.File != filepath.Join(root, "logs", "platform.jsonl") {
		t.Fatalf("logging file = %q", cfg.Logging.File)
	}
	if cfg.Secret.Root != filepath.Join(root, "secrets") {
		t.Fatalf("secret root = %q, want base-dir resolved", cfg.Secret.Root)
	}
	assertStringLookup(t, cfg, "paths.base_dir", root)
	assertStringLookup(t, cfg, "database.default_url", wantCore)
	assertStringLookup(t, cfg, "filestore.root", filepath.Join(root, "artifacts"))
	assertStringLookup(t, cfg, "logging.file", filepath.Join(root, "logs", "platform.jsonl"))
	assertStringLookup(t, cfg, "secret.root", filepath.Join(root, "secrets"))
}

func TestYAMLRelativeBaseDirIsResolvedFromConfigFileDirectory(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	path := filepath.Join(configDir, "orrery.yaml")
	body := []byte(`
paths:
  base_dir: ./runtime
database:
  default_url: file://./data/core.db
filestore:
  root: ./artifacts
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg, err := config.LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	wantBase := filepath.Join(configDir, "runtime")
	if cfg.BaseDir != wantBase {
		t.Fatalf("BaseDir = %q, want %q", cfg.BaseDir, wantBase)
	}
	assertStringLookup(t, cfg, "database.default_url", "file://"+filepath.Join(wantBase, "data", "core.db"))
	assertStringLookup(t, cfg, "filestore.root", filepath.Join(wantBase, "artifacts"))
}

func TestYAMLWithoutBaseDirUsesConfigFileDirectoryAsBaseDir(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	path := filepath.Join(configDir, "orrery.yaml")
	body := []byte(`
database:
  default_url: file://./data/core.db
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
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	if cfg.BaseDir != configDir {
		t.Fatalf("BaseDir = %q, want config file dir %q", cfg.BaseDir, configDir)
	}
	assertStringLookup(t, cfg, "database.default_url", "file://"+filepath.Join(configDir, "data", "core.db"))
	assertStringLookup(t, cfg, "filestore.root", filepath.Join(configDir, "artifacts"))
	assertStringLookup(t, cfg, "logging.file", filepath.Join(configDir, "logs", "platform.jsonl"))
}

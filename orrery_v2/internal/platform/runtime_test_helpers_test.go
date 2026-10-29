package platform_test

import (
	"os"
	"testing"
	"time"

	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
)

func writeRuntimeYAML(t *testing.T, path, baseDir, filestoreRoot, logFile, readinessTTL string) {
	t.Helper()
	writeRuntimeYAMLWithDatabaseRoot(t, path, baseDir, "./data", filestoreRoot, logFile, readinessTTL)
}

func writeRuntimeYAMLWithMetrics(t *testing.T, path, baseDir, filestoreRoot, logFile, readinessTTL, metricsAddr string) {
	t.Helper()
	writeRuntimeYAMLWithDatabaseRoot(t, path, baseDir, "./data", filestoreRoot, logFile, readinessTTL)
	appendRuntimeYAML(t, path, "\nmetrics:\n  addr: "+metricsAddr+"\n")
}

func writeRuntimeYAMLWithDatabaseRoot(t *testing.T, path, baseDir, databaseRoot, filestoreRoot, logFile, readinessTTL string) {
	t.Helper()
	raw := `
environment: test
paths:
  base_dir: ` + baseDir + `
database:
  urls:
    core: file://` + databaseRoot + `/core.db
    audit: file://` + databaseRoot + `/audit.db
    events: file://` + databaseRoot + `/events.db
    jobs: file://` + databaseRoot + `/jobs.db
    runtime: file://` + databaseRoot + `/runtime.db
    observability: file://` + databaseRoot + `/observability.db
filestore:
  backend: local
  root: ` + filestoreRoot + `
logging:
  console: false
  file: ` + logFile + `
  level: debug
health:
  readiness_cache_ttl: ` + readinessTTL + `
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
}

func appendRuntimeYAML(t *testing.T, path string, body string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read runtime yaml: %v", err)
	}
	raw = append(raw, []byte(body)...)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("append runtime yaml: %v", err)
	}
}

func platformConfigFromYAML(path string) (config.Config, error) {
	return config.LoadYAML(path)
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition was not met within %s", timeout)
}

func mustStore(t *testing.T, registry *db.Registry, role string) *db.Store {
	t.Helper()
	store, err := registry.Store(role)
	if err != nil {
		t.Fatalf("lookup %s database: %v", role, err)
	}
	return store
}

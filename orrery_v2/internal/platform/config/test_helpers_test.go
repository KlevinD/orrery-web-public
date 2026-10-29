package config_test

import (
	"os"
	"strings"
	"testing"
)

func explicitTestConfigPath(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/orrery.yaml"
	writeExplicitTestDatabaseYAML(t, path, "")
	return path
}

func withExplicitTestDatabaseYAML(body string) []byte {
	const database = `
database:
  default_url: memory://core
  urls:
    core: memory://core
    audit: memory://audit
    events: memory://events
    jobs: memory://jobs
    runtime: memory://runtime
    observability: memory://observability
`
	return []byte(database + strings.TrimLeft(body, "\r\n"))
}

func writeExplicitTestDatabaseYAML(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, withExplicitTestDatabaseYAML(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
}

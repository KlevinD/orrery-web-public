// Package testpg connects regression tests only to an explicitly isolated
// PostgreSQL schema. It never creates a local database.
package testpg

import (
	"context"
	"net/url"
	"os"
	"regexp"
	"testing"

	"orrery_v2/internal/platform/db"
)

func URL(t testing.TB) string {
	t.Helper()
	raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	parsed, err := url.Parse(raw)
	if err != nil || !regexp.MustCompile(`^orrery_test_stall_[0-9a-f]{32}$`).MatchString(parsed.Query().Get("search_path")) {
		t.Fatal("stall regression tests require an isolated orrery_test_stall schema")
	}
	return raw
}

func Open(t testing.TB) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverPostgres, URL: URL(t)})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close PostgreSQL: %v", err)
		}
	})
	return store
}

func Registry(t testing.TB) *db.Registry {
	t.Helper()
	registry, err := db.OpenRegistry(context.Background(), db.RegistryConfig{Databases: map[string]db.Config{db.RoleCore: {Driver: db.DriverPostgres, URL: URL(t)}}})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL registry: %v", err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Errorf("close PostgreSQL registry: %v", err)
		}
	})
	return registry
}

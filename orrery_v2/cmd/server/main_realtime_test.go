package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestProductionHandlerWiresRealtimeEndpointAndOutboxMigration(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", persistentProductionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	jobs, err := db.Open(context.Background(), db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "jobs.db")})
	if err != nil {
		t.Fatalf("open jobs db: %v", err)
	}
	defer jobs.Close()
	exists, err := jobs.TableExists(context.Background(), "realtime_outbox")
	if err != nil {
		t.Fatalf("check realtime_outbox table: %v", err)
	}
	if !exists {
		t.Fatalf("realtime_outbox table was not created in jobs database")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/realtime/connect", nil)
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "discussion.read")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, req)

	if rec.Code == http.StatusNotFound || rec.Code == http.StatusServiceUnavailable {
		t.Fatalf("realtime endpoint status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestProductionHandlerStartsWithPostgresDatabase(t *testing.T) {
	databaseURL := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("set ORRERY_TEST_POSTGRES_URL to run PostgreSQL production handler smoke")
	}
	configURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal("invalid PostgreSQL test URL")
	}
	if configURL.User != nil {
		if password, present := configURL.User.Password(); present {
			t.Setenv("ORRERY_DATABASE_PASSWORD", password)
			configURL.User = url.User(configURL.User.Username())
		}
	}
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	t.Setenv("ORRERY_CONFIG", "")
	setTestConfigOverride(t, "database.password_env", "ORRERY_DATABASE_PASSWORD")
	setTestConfigOverride(t, "database.default_url", configURL.String())
	selectTestServerConfig(t)

	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production postgres: %v", err)
	}
	defer cleanup()

	jobs, err := db.Open(context.Background(), db.Config{Driver: db.DriverPostgres, URL: databaseURL})
	if err != nil {
		t.Fatalf("open postgres jobs db: %v", err)
	}
	defer jobs.Close()
	exists, err := jobs.TableExists(context.Background(), "realtime_outbox")
	if err != nil {
		t.Fatalf("check postgres realtime_outbox table: %v", err)
	}
	if !exists {
		t.Fatalf("postgres realtime_outbox table was not created")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/realtime/connect", nil)
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "discussion.read")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, req)

	if rec.Code == http.StatusNotFound || rec.Code == http.StatusServiceUnavailable {
		t.Fatalf("postgres realtime endpoint status = %d body = %s", rec.Code, rec.Body.String())
	}
}

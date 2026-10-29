package scmconnection

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestPlatformRepositoryRejectsInconsistentConnectionIdentity(t *testing.T) {
	ctx := context.Background()
	repo, router, closeEnv := newRepositoryEnvironment(t)
	defer closeEnv()

	connection := Connection{TenantID: "tenant-b", ConnectionID: "connection-1", Version: 1}
	payload, err := json.Marshal(connection)
	if err != nil {
		t.Fatalf("marshal connection: %v", err)
	}
	if err := router.PutRecord(ctx, connectionTable, "tenant-a/connection-1", db.Record{
		"tenant_id": "tenant-a", "connection_id": "connection-1", "version": int64(1), "payload": string(payload),
	}); err != nil {
		t.Fatalf("put corrupt connection: %v", err)
	}

	if _, err := repo.Get(ctx, "tenant-a", "connection-1"); !errors.Is(err, ErrDependency) {
		t.Fatalf("Get error = %v, want ErrDependency", err)
	}
}

func TestPlatformRepositoryRejectsConnectionRecordUnderWrongKey(t *testing.T) {
	ctx := context.Background()
	repo, router, closeEnv := newRepositoryEnvironment(t)
	defer closeEnv()
	connection := Connection{TenantID: "tenant-a", ConnectionID: "connection-1", Version: 1}
	if err := router.PutRecord(ctx, connectionTable, "tenant-a/connection-other", connectionRecord(connection)); err != nil {
		t.Fatalf("put misplaced connection: %v", err)
	}

	if _, _, err := repo.ListCatalog(ctx, "", 10); !errors.Is(err, ErrDependency) {
		t.Fatalf("ListCatalog error = %v, want ErrDependency", err)
	}
}

func TestPlatformRepositoryRejectsConnectionColumnDrift(t *testing.T) {
	ctx := context.Background()
	repo, router, closeEnv := newRepositoryEnvironment(t)
	defer closeEnv()
	connection := Connection{
		TenantID: "tenant-a", ConnectionID: "connection-1", OwnerID: "owner-1",
		Provider: ProviderGenericGit, Status: StatusActive, Version: 2,
	}
	record := connectionRecord(connection)
	record["owner_id"] = "other-owner"
	if err := router.PutRecord(ctx, connectionTable, "tenant-a/connection-1", record); err != nil {
		t.Fatalf("put drifted connection: %v", err)
	}
	if _, err := repo.Get(ctx, "tenant-a", "connection-1"); !errors.Is(err, ErrDependency) {
		t.Fatalf("Get error = %v, want ErrDependency", err)
	}
}

func TestPlatformRepositoryRejectsUnsafePendingConnectionEffect(t *testing.T) {
	ctx := context.Background()
	repo, router, closeEnv := newRepositoryEnvironment(t)
	defer closeEnv()
	now := time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC)
	connection := Connection{
		TenantID: "tenant-a", ConnectionID: "connection-1", OwnerID: "owner-1", Scope: ScopePlatform,
		Provider: ProviderManagedLocal, BaseURL: filepath.Clean(t.TempDir()), Status: StatusActive,
		CreatedAt: now, UpdatedAt: now, Version: 1,
		PendingEffects: []PendingEffect{{EffectID: "unsafe-effect", Kind: "shell.execute"}},
	}
	if err := router.PutRecord(ctx, connectionTable, "tenant-a/connection-1", connectionRecord(connection)); err != nil {
		t.Fatalf("put connection: %v", err)
	}
	if _, err := repo.Get(ctx, "tenant-a", "connection-1"); !errors.Is(err, ErrDependency) {
		t.Fatalf("Get error = %v, want ErrDependency", err)
	}
}

func newRepositoryEnvironment(t *testing.T) (*PlatformRepository, *db.Router, func()) {
	t.Helper()
	registry, err := db.OpenRegistry(context.Background(), db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "scm-connection-repository-test"},
	}})
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	router := db.NewRouter(registry)
	return NewPlatformRepository(router), router, func() { _ = registry.Close() }
}

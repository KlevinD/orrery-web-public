package developmentproject

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestPlatformRepositoryRejectsInconsistentProjectIdentity(t *testing.T) {
	ctx := context.Background()
	repo, router, closeEnv := newRepositoryEnvironment(t)
	defer closeEnv()

	project := Project{TenantID: "tenant-b", DevelopmentProjectID: "project-1", Version: 1}
	payload, err := json.Marshal(project)
	if err != nil {
		t.Fatalf("marshal project: %v", err)
	}
	if err := router.PutRecord(ctx, projectTable, "tenant-a/project-1", db.Record{
		"tenant_id": "tenant-a", "development_project_id": "project-1", "version": int64(1), "payload": string(payload),
	}); err != nil {
		t.Fatalf("put corrupt project: %v", err)
	}

	if _, err := repo.Get(ctx, "tenant-a", "project-1"); !errors.Is(err, ErrDependency) {
		t.Fatalf("Get error = %v, want ErrDependency", err)
	}
}

func TestPlatformRepositoryRejectsProjectRecordUnderWrongKey(t *testing.T) {
	ctx := context.Background()
	repo, router, closeEnv := newRepositoryEnvironment(t)
	defer closeEnv()
	project := Project{TenantID: "tenant-a", DevelopmentProjectID: "project-1", Version: 1}
	if err := router.PutRecord(ctx, projectTable, "tenant-a/project-other", projectRecord(project)); err != nil {
		t.Fatalf("put misplaced project: %v", err)
	}

	if _, _, err := repo.ListCatalog(ctx, "", 10); !errors.Is(err, ErrDependency) {
		t.Fatalf("ListCatalog error = %v, want ErrDependency", err)
	}
}

func TestPlatformRepositoryRejectsProjectColumnDrift(t *testing.T) {
	ctx := context.Background()
	repo, router, closeEnv := newRepositoryEnvironment(t)
	defer closeEnv()
	project := Project{
		TenantID: "tenant-a", DevelopmentProjectID: "project-1", Status: StatusActive,
		CreatedBy: "owner-1", Version: 2,
	}
	record := projectRecord(project)
	record["version"] = int64(1)
	if err := router.PutRecord(ctx, projectTable, "tenant-a/project-1", record); err != nil {
		t.Fatalf("put drifted project: %v", err)
	}
	if _, err := repo.Get(ctx, "tenant-a", "project-1"); !errors.Is(err, ErrDependency) {
		t.Fatalf("Get error = %v, want ErrDependency", err)
	}
}

func TestPlatformRepositoryRejectsUnsafePendingProjectEffect(t *testing.T) {
	ctx := context.Background()
	repo, router, closeEnv := newRepositoryEnvironment(t)
	defer closeEnv()
	now := time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC)
	project := Project{
		TenantID: "tenant-a", DevelopmentProjectID: "project-1", Name: "Project", Status: StatusActive,
		DefaultAutomationMode: AutomationManual, MaxConcurrentRuns: 1, ExecutionPolicy: ExecutionPolicy{NetworkMode: NetworkNone},
		CreatedBy: "owner-1", CreatedAt: now, UpdatedAt: now, Version: 1,
		Members:        []Member{{MemberID: "member-1", SubjectKind: "user", SubjectID: "owner-1", Role: MemberRoleOwner, CreatedBy: "owner-1", CreatedAt: now, UpdatedAt: now, Version: 1}},
		PendingEffects: []PendingEffect{{EffectID: "unsafe-effect", Kind: "shell.execute"}},
	}
	if err := router.PutRecord(ctx, projectTable, "tenant-a/project-1", projectRecord(project)); err != nil {
		t.Fatalf("put project: %v", err)
	}
	if _, err := repo.Get(ctx, "tenant-a", "project-1"); !errors.Is(err, ErrDependency) {
		t.Fatalf("Get error = %v, want ErrDependency", err)
	}
}

func newRepositoryEnvironment(t *testing.T) (*PlatformRepository, *db.Router, func()) {
	t.Helper()
	registry, err := db.OpenRegistry(context.Background(), db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "development-project-repository-test"},
	}})
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	router := db.NewRouter(registry)
	return NewPlatformRepository(router), router, func() { _ = registry.Close() }
}

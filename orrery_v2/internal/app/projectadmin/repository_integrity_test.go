package projectadmin

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestPlatformRepositoryRejectsProjectRecordIdentityDrift(t *testing.T) {
	ctx := context.Background()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "project-record-integrity"},
	}})
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	defer registry.Close()
	router := db.NewRouter(registry)
	repo := NewPlatformRepository(router)
	now := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)

	corrupt := projectRecord(Project{TenantID: "global", ProjectID: "different-project", Title: "Corrupt", Status: ProjectStatusActive, Version: 1, CreatedAt: now, UpdatedAt: now})
	if err := router.PutRecord(ctx, projectTable, projectKey("global", "project-1"), corrupt); err != nil {
		t.Fatalf("seed corrupt record: %v", err)
	}
	if _, err := repo.GetProject(ctx, "global", "project-1"); !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("GetProject error = %v, want ErrServiceDependency", err)
	}
	if _, err := repo.ListProjects(ctx, "global"); !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("ListProjects error = %v, want ErrServiceDependency", err)
	}
}

func TestPlatformRepositoryRejectsAmbiguousProjectTenantSegment(t *testing.T) {
	repo := NewMemoryRepository()
	project := Project{TenantID: "tenant/ambiguous", ProjectID: "project-1", Version: 1}
	if err := repo.SaveProject(context.Background(), project); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("SaveProject error = %v, want ErrInvalidRequest", err)
	}
}

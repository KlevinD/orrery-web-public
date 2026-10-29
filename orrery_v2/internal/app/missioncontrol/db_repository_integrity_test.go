package missioncontrol

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/db"
)

func TestDBProjectionRepositoryRejectsMissionRecordIdentityDrift(t *testing.T) {
	ctx := context.Background()
	repo, router, closeRepo := newIntegrityProjectionRepository(t)
	defer closeRepo()

	model := integrityReadModel("tenant-a", "different-mission")
	payload, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal model: %v", err)
	}
	if err := router.PutRecord(ctx, projectionTable, "tenant-a/mission-1", db.Record{
		"tenant_id": "tenant-a", "mission_id": "mission-1", "version": 1, "payload": string(payload),
	}); err != nil {
		t.Fatalf("seed corrupt projection: %v", err)
	}

	if _, err := repo.GetMissionControlForTenant(ctx, "tenant-a", missioncore.MissionID("mission-1")); !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("GetMissionControlForTenant error = %v, want ErrServiceDependency", err)
	}
	if _, err := repo.ListMissionControlsForTenant(ctx, "tenant-a"); !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("ListMissionControlsForTenant error = %v, want ErrServiceDependency", err)
	}
}

func TestDBProjectionRepositoryRejectsNonGlobalLegacyKey(t *testing.T) {
	ctx := context.Background()
	repo, router, closeRepo := newIntegrityProjectionRepository(t)
	defer closeRepo()

	model := integrityReadModel("tenant-a", "mission-1")
	payload, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal model: %v", err)
	}
	if err := router.PutRecord(ctx, projectionTable, "mission-1", db.Record{
		"tenant_id": "tenant-a", "mission_id": "mission-1", "version": 1, "payload": string(payload),
	}); err != nil {
		t.Fatalf("seed corrupt projection: %v", err)
	}

	if _, _, err := repo.ListMissionCatalog(ctx, "", 10); !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("ListMissionCatalog error = %v, want ErrServiceDependency", err)
	}
}

func TestDBProjectionRepositoryListsLightweightMissionCatalogEntries(t *testing.T) {
	ctx := context.Background()
	repo, router, closeRepo := newIntegrityProjectionRepository(t)
	defer closeRepo()

	legacy := integrityReadModel("global", "mission-legacy")
	legacy.Mission.Title = "Legacy mission"
	legacy.UpdatedAt = time.Unix(10, 0).UTC()
	legacyPayload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.PutRecord(ctx, projectionTable, "mission-legacy", db.Record{
		"tenant_id": "global", "mission_id": "mission-legacy", "version": 1, "payload": string(legacyPayload),
	}); err != nil {
		t.Fatal(err)
	}
	next := integrityReadModel("tenant-a", "mission-next")
	next.Mission.Title = "Next mission"
	next.UpdatedAt = time.Unix(20, 0).UTC()
	if err := repo.SaveForTenant(ctx, "tenant-a", next); err != nil {
		t.Fatal(err)
	}

	first, hasMore, err := repo.ListMissionCatalogEntries(ctx, "", 1)
	if err != nil {
		t.Fatalf("ListMissionCatalogEntries first page: %v", err)
	}
	if !hasMore || len(first) != 1 || first[0].Seek != "mission-legacy" || first[0].TenantID != "global" || first[0].MissionID != "mission-legacy" || first[0].Title != "Legacy mission" || !first[0].UpdatedAt.Equal(legacy.UpdatedAt) {
		t.Fatalf("first page = %+v, has_more=%v", first, hasMore)
	}
	second, hasMore, err := repo.ListMissionCatalogEntries(ctx, first[0].Seek, 1)
	if err != nil {
		t.Fatalf("ListMissionCatalogEntries second page: %v", err)
	}
	if hasMore || len(second) != 1 || second[0].Seek != "tenant-a/mission-next" || second[0].TenantID != "tenant-a" || second[0].MissionID != "mission-next" || second[0].Title != "Next mission" || !second[0].UpdatedAt.Equal(next.UpdatedAt) {
		t.Fatalf("second page = %+v, has_more=%v", second, hasMore)
	}
}

func TestDBProjectionRepositoryRejectsAmbiguousTenantSegment(t *testing.T) {
	repo, _, closeRepo := newIntegrityProjectionRepository(t)
	defer closeRepo()
	if err := repo.SaveForTenant(context.Background(), "tenant/ambiguous", integrityReadModel("tenant/ambiguous", "mission-1")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("SaveForTenant error = %v, want ErrInvalidRequest", err)
	}
}

func TestDBProjectionRepositoryRejectsConflictingAndStaleProjectionWrites(t *testing.T) {
	ctx := context.Background()
	repo, _, closeRepo := newIntegrityProjectionRepository(t)
	defer closeRepo()
	first := integrityReadModel("tenant-a", "mission-cas")
	first.Mission.Title = "Original"
	if err := repo.SaveForTenant(ctx, "tenant-a", first); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveForTenant(ctx, "tenant-a", first); err != nil {
		t.Fatalf("idempotent save: %v", err)
	}
	conflicting := first
	conflicting.Mission.Title = "Lost update"
	if err := repo.SaveForTenant(ctx, "tenant-a", conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-version conflict error = %v, want ErrConflict", err)
	}
	next := first
	next.Mission.Title = "Committed update"
	next.Checkpoint.Version++
	if err := repo.SaveForTenant(ctx, "tenant-a", next); err != nil {
		t.Fatalf("next-version save: %v", err)
	}
	if err := repo.SaveForTenant(ctx, "tenant-a", first); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale projection error = %v, want ErrConflict", err)
	}
}

func TestDBProjectionRepositoryRepairsRuntimeProjectionWithSameCheckpointCAS(t *testing.T) {
	ctx := context.Background()
	repo, _, closeRepo := newIntegrityProjectionRepository(t)
	defer closeRepo()
	model := integrityReadModel("tenant-a", "mission-runtime-repair")
	model.RuntimeSessions = []projection.RuntimeSessionView{{RunID: "run-1", Status: projection.RuntimeSessionRunning}}
	if err := repo.SaveForTenant(ctx, "tenant-a", model); err != nil {
		t.Fatal(err)
	}

	expected, err := repo.GetMissionControlForTenant(ctx, "tenant-a", "mission-runtime-repair")
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := cloneMemoryProjection(expected)
	if err != nil {
		t.Fatal(err)
	}
	replacement.RuntimeSessions[0].Status = projection.RuntimeSessionCompleted
	swapped, err := repo.repairRuntimeProjection(ctx, "tenant-a", expected, replacement)
	if err != nil || !swapped {
		t.Fatalf("repairRuntimeProjection swapped=%v err=%v", swapped, err)
	}

	loaded, err := repo.GetMissionControlForTenant(ctx, "tenant-a", "mission-runtime-repair")
	if err != nil || loaded.Checkpoint.Version != expected.Checkpoint.Version || loaded.RuntimeSessions[0].Status != projection.RuntimeSessionCompleted {
		t.Fatalf("repaired projection = %+v err=%v", loaded, err)
	}
	staleReplacement, err := cloneMemoryProjection(expected)
	if err != nil {
		t.Fatal(err)
	}
	staleReplacement.RuntimeSessions[0].Status = projection.RuntimeSessionFailed
	swapped, err = repo.repairRuntimeProjection(ctx, "tenant-a", expected, staleReplacement)
	if err != nil || swapped {
		t.Fatalf("stale repairRuntimeProjection swapped=%v err=%v", swapped, err)
	}
}

func TestMemoryProjectionRepositoryMatchesProductionCASAndCopySemantics(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryProjectionRepository()
	first := integrityReadModel("tenant-a", "mission-memory-cas")
	first.Mission.Title = "Original"
	first.TaskBoard.Tasks = []projection.TaskCardView{{TaskID: "task-1", Goal: "Original task"}}
	if err := repo.SaveForTenant(ctx, "tenant-a", first); err != nil {
		t.Fatal(err)
	}

	first.Mission.Title = "Caller mutation"
	first.TaskBoard.Tasks[0].Goal = "Caller mutation"
	stored, err := repo.GetMissionControlForTenant(ctx, "tenant-a", "mission-memory-cas")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Mission.Title != "Original" || stored.TaskBoard.Tasks[0].Goal != "Original task" {
		t.Fatalf("stored projection was mutated through caller alias: %+v", stored)
	}
	stored.Mission.Title = "Read mutation"
	reloaded, err := repo.GetMissionControlForTenant(ctx, "tenant-a", "mission-memory-cas")
	if err != nil || reloaded.Mission.Title != "Original" {
		t.Fatalf("stored projection was mutated through read alias: %+v err=%v", reloaded, err)
	}

	conflicting := reloaded
	conflicting.Mission.Title = "Lost update"
	if err := repo.SaveForTenant(ctx, "tenant-a", conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-version conflict error = %v, want ErrConflict", err)
	}
	next := reloaded
	next.Mission.Title = "Committed update"
	next.Checkpoint.Version++
	if err := repo.SaveForTenant(ctx, "tenant-a", next); err != nil {
		t.Fatalf("next-version save: %v", err)
	}
	if err := repo.SaveForTenant(ctx, "tenant-a", reloaded); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale projection error = %v, want ErrConflict", err)
	}
}

func integrityReadModel(tenantID, missionID string) projection.ReadModel {
	id := missioncore.MissionID(missionID)
	return projection.ReadModel{
		TenantID:   tenantID,
		Mission:    projection.MissionView{TenantID: tenantID, MissionID: id, Status: projection.MissionStatusActive},
		Checkpoint: projection.Checkpoint{MissionID: id, Version: 1},
	}
}

func newIntegrityProjectionRepository(t *testing.T) (*DBProjectionRepository, *db.Router, func()) {
	t.Helper()
	registry, err := db.OpenRegistry(context.Background(), db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "mission-projection-record-integrity"},
	}})
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	router := db.NewRouter(registry)
	return NewDBProjectionRepository(router), router, func() { _ = registry.Close() }
}

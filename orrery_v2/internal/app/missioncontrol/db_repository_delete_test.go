package missioncontrol

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestDBProjectionDeleteRejectsLateWriterAcrossReopen(t *testing.T) {
	ctx := context.Background()
	cfg := db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverFile, URL: "file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "mission-delete.db"))},
	}}
	open := func() (*DBProjectionRepository, func()) {
		t.Helper()
		registry, err := db.OpenRegistry(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return NewDBProjectionRepository(db.NewRouter(registry)), func() { _ = registry.Close() }
	}
	repo, closeRepo := open()
	model := integrityReadModel("tenant-a", "mission-delete")
	if err := repo.SaveForTenant(ctx, "tenant-a", model); err != nil {
		t.Fatal(err)
	}
	// A runtime projection worker has read the Mission before its owner deletes it.
	late, err := repo.GetMissionControlForTenant(ctx, "tenant-a", model.Mission.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteForTenant(ctx, "tenant-a", model.Mission.MissionID); err != nil {
		t.Fatal(err)
	}
	closeRepo()
	repo, closeRepo = open()
	defer closeRepo()
	late.Checkpoint.Version++
	if err := repo.SaveForTenant(ctx, "tenant-a", late); !errors.Is(err, ErrProjectionNotFound) {
		t.Fatalf("late write after delete = %v, want ErrProjectionNotFound", err)
	}
	if _, err := repo.GetMissionControlForTenant(ctx, "tenant-a", model.Mission.MissionID); !errors.Is(err, ErrProjectionNotFound) {
		t.Fatalf("deleted Mission reappeared: %v", err)
	}
	entries, _, err := repo.ListMissionCatalogEntries(ctx, "", 10)
	if err != nil || len(entries) != 0 {
		t.Fatalf("deleted Mission catalog = %+v, error = %v", entries, err)
	}
	// A deletion in one tenant must not reserve the same Mission ID in another.
	other := integrityReadModel("tenant-b", model.Mission.MissionID.String())
	if err := repo.SaveForTenant(ctx, "tenant-b", other); err != nil {
		t.Fatalf("other tenant create: %v", err)
	}
	// Deleting an unknown ID must not prevent a later legitimate first creation.
	if err := repo.DeleteForTenant(ctx, "tenant-a", "new-mission"); !errors.Is(err, ErrProjectionNotFound) {
		t.Fatalf("delete unknown Mission = %v", err)
	}
	if err := repo.SaveForTenant(ctx, "tenant-a", integrityReadModel("tenant-a", "new-mission")); err != nil {
		t.Fatalf("new Mission create: %v", err)
	}
}

func TestDBProjectionDeleteFencesGlobalLegacyAndCanonicalRecords(t *testing.T) {
	for _, canonical := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "both"}[canonical], func(t *testing.T) {
			ctx := context.Background()
			repo, router, closeRepo := newIntegrityProjectionRepository(t)
			defer closeRepo()
			model := integrityReadModel("global", "mission-legacy-delete")
			payload, err := json.Marshal(model)
			if err != nil {
				t.Fatal(err)
			}
			if err := router.PutRecord(ctx, projectionTable, model.Mission.MissionID.String(), db.Record{
				"tenant_id": "global", "mission_id": model.Mission.MissionID.String(), "version": 1, "payload": string(payload),
			}); err != nil {
				t.Fatal(err)
			}
			if canonical {
				if err := repo.Save(ctx, model); err != nil {
					t.Fatal(err)
				}
			}
			if err := repo.Delete(ctx, model.Mission.MissionID); err != nil {
				t.Fatal(err)
			}
			model.Checkpoint.Version++
			if err := repo.Save(ctx, model); !errors.Is(err, ErrProjectionNotFound) {
				t.Fatalf("legacy late write = %v", err)
			}
			if _, err := repo.GetMissionControl(ctx, model.Mission.MissionID); !errors.Is(err, ErrProjectionNotFound) {
				t.Fatalf("legacy record survived delete: %v", err)
			}
			models, err := repo.ListMissionControls(ctx)
			if err != nil || len(models) != 0 {
				t.Fatalf("deleted legacy Mission list = %+v, error = %v", models, err)
			}
		})
	}
}

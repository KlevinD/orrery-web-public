package main

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/testkit"
)

func TestMigrateLegacyTenantRecordKeysMovesProjectsAndMissionProjections(t *testing.T) {
	ctx := context.Background()
	env := testkit.NewInitializedTestRuntime(t)

	project := map[string]any{"project_id": "project-legacy", "title": "Legacy"}
	mission := map[string]any{"mission_id": "mission-legacy", "payload": `{"mission":{"mission_id":"mission-legacy"}}`}
	projectWithSlash := map[string]any{"project_id": "portfolio/project-legacy", "title": "Legacy slash ID"}
	missionWithSlash := map[string]any{"mission_id": "portfolio/mission-legacy", "payload": `{"mission":{"mission_id":"portfolio/mission-legacy"}}`}
	if err := env.DBRouter.PutRecord(ctx, "core.projects", "project-legacy", project); err != nil {
		t.Fatalf("put legacy project: %v", err)
	}
	if err := env.DBRouter.PutRecord(ctx, "core.mission_control_projection", "mission-legacy", mission); err != nil {
		t.Fatalf("put legacy mission projection: %v", err)
	}
	if err := env.DBRouter.PutRecord(ctx, "core.projects", "portfolio/project-legacy", projectWithSlash); err != nil {
		t.Fatalf("put legacy slash project: %v", err)
	}
	if err := env.DBRouter.PutRecord(ctx, "core.mission_control_projection", "portfolio/mission-legacy", missionWithSlash); err != nil {
		t.Fatalf("put legacy slash mission projection: %v", err)
	}

	if err := migrateLegacyTenantRecordKeys(ctx, env.DBRouter); err != nil {
		t.Fatalf("migrate legacy keys: %v", err)
	}
	for _, record := range []struct {
		table  string
		oldKey string
		newKey string
	}{
		{table: "core.projects", oldKey: "project-legacy", newKey: "global/project-legacy"},
		{table: "core.mission_control_projection", oldKey: "mission-legacy", newKey: "global/mission-legacy"},
		{table: "core.projects", oldKey: "portfolio/project-legacy", newKey: "global/portfolio/project-legacy"},
		{table: "core.mission_control_projection", oldKey: "portfolio/mission-legacy", newKey: "global/portfolio/mission-legacy"},
	} {
		if _, found, err := env.DBRouter.GetRecord(ctx, record.table, record.oldKey); err != nil || found {
			t.Fatalf("legacy record %s/%s remains: found=%v err=%v", record.table, record.oldKey, found, err)
		}
		migrated, found, err := env.DBRouter.GetRecord(ctx, record.table, record.newKey)
		if err != nil || !found {
			t.Fatalf("migrated record %s/%s: found=%v err=%v", record.table, record.newKey, found, err)
		}
		if migrated["tenant_id"] != "global" {
			t.Fatalf("migrated tenant = %#v", migrated["tenant_id"])
		}
	}
	if err := migrateLegacyTenantRecordKeys(ctx, env.DBRouter); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}

func TestMigrateLegacyTenantRecordKeysFailsClosedOnCollision(t *testing.T) {
	ctx := context.Background()
	env := testkit.NewInitializedTestRuntime(t)
	if err := env.DBRouter.PutRecord(ctx, "core.projects", "project-collision", map[string]any{"project_id": "project-collision"}); err != nil {
		t.Fatalf("put legacy project: %v", err)
	}
	if err := env.DBRouter.PutRecord(ctx, "core.projects", "global/project-collision", map[string]any{"tenant_id": "global", "project_id": "project-collision", "title": "New"}); err != nil {
		t.Fatalf("put tenant project: %v", err)
	}

	err := migrateLegacyTenantRecordKeys(ctx, env.DBRouter)
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("migration error = %v", err)
	}
	if _, found, getErr := env.DBRouter.GetRecord(ctx, "core.projects", "project-collision"); getErr != nil || !found {
		t.Fatalf("legacy project was removed on collision: found=%v err=%v", found, getErr)
	}
}

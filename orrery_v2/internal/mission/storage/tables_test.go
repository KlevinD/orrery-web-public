package storage_test

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
)

func TestCoreTablesDeclareMissionRepositoryTables(t *testing.T) {
	tables := storage.CoreTables()
	seen := map[string]storage.TableSpec{}
	for _, table := range tables {
		if table.LogicalName == "" || table.Role != platform.DatabaseRoleCore {
			t.Fatalf("invalid table spec: %+v", table)
		}
		if len(table.KeyFields) == 0 {
			t.Fatalf("table %s has no key fields", table.LogicalName)
		}
		if _, ok := seen[table.LogicalName]; ok {
			t.Fatalf("duplicate table spec for %s", table.LogicalName)
		}
		seen[table.LogicalName] = table
	}
	for _, want := range []string{
		storage.TeamRecords,
		storage.TeamAssignments,
		storage.TeamStaffingRequests,
		storage.TeamStaffingDecisions,
		storage.TeamStaffingAudits,
		storage.TaskRecords,
		storage.TaskSubmissions,
		storage.TaskSubmissionIdempotency,
		storage.TaskDeliverableSubmissions,
		storage.TaskEvidenceSubmissions,
		storage.TaskReworkRequests,
		storage.TaskQAResults,
		storage.TaskQAIdempotency,
		storage.TaskAcceptanceReports,
		storage.DiscussionGroups,
		storage.DiscussionMembers,
		storage.DiscussionMessages,
		storage.DiscussionMessageIdempotency,
		storage.DiscussionInboxItems,
	} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("missing mission storage table %s", want)
		}
	}
}

func TestRegisterMissionStorageAddsInitializer(t *testing.T) {
	ctx := context.Background()
	module := platform.ModuleFunc(storage.RegisterMissionStorage)
	rt := testkit.NewInitializedTestRuntime(t, module)
	checks := rt.Health.Ready(ctx).Checks
	var saw bool
	for _, check := range checks {
		if check.Name == "initializer.mission.storage.core_tables" {
			saw = true
			if !check.OK {
				t.Fatalf("mission storage initializer not ready: %+v", check)
			}
		}
	}
	if !saw {
		t.Fatalf("mission storage initializer readiness check missing: %+v", checks)
	}
}

func TestStoragePutValidatesDeclaredKeyFields(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	t.Cleanup(env.Close)

	err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx platform.Tx) error {
		return storage.Put(ctx, tx, storage.TableDiscussionMessages, "message-1", db.Record{
			"group_id": "discussion-1",
			"payload":  "{}",
		})
	})
	if err == nil || !strings.Contains(err.Error(), "message_id") {
		t.Fatalf("storage.Put error = %v, want missing message_id key field", err)
	}
}

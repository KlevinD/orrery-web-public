package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func personalAssistantApprovalQueryFixture(t *testing.T) productionPersonalAssistantApprovalNotificationStore {
	t.Helper()
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	for _, sql := range []string{
		`CREATE TABLE hil_decision_requests (id TEXT, tenant_id TEXT, requester_kind TEXT, requester_id TEXT, action TEXT, action_type_id TEXT, purpose TEXT, risk_level TEXT, route_mode TEXT, payload_preview_json TEXT, created_at INTEGER, expires_at INTEGER, status TEXT)`,
		`CREATE TABLE hil_workflow_instances (id TEXT, tenant_id TEXT, decision_request_id TEXT)`,
		`CREATE TABLE hil_workflow_tasks (id TEXT, tenant_id TEXT, workflow_instance_id TEXT, status TEXT, current_assignee_kind TEXT, current_assignee_id TEXT, created_at INTEGER)`,
		`INSERT INTO hil_decision_requests VALUES ('decision', 'tenant-a', 'agent', 'agent-a', 'runtime.approval', 'tool_use', '', 'high', 'approval', '{"approval_assigned_to":"human-a"}', 1, NULL, 'pending')`,
	} {
		if _, err := core.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	return productionPersonalAssistantApprovalNotificationStore{store: core}
}

func TestPersonalAssistantApprovalStoreRechecksRecipientExpiryAndTenant(t *testing.T) {
	for _, scenario := range []string{"pending", "reassigned", "expired", "resolved", "other_tenant"} {
		t.Run(scenario, func(t *testing.T) {
			s := personalAssistantApprovalQueryFixture(t)
			ctx := context.Background()
			page, err := s.ListPending(ctx, 20, 0)
			if err != nil || len(page) != 1 {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			var update string
			switch scenario {
			case "reassigned":
				update = `UPDATE hil_decision_requests SET payload_preview_json = '{"approval_assigned_to":"human-b"}'`
			case "expired":
				update = `UPDATE hil_decision_requests SET expires_at = 1`
			case "resolved":
				update = `UPDATE hil_decision_requests SET status = 'resolved'`
			case "other_tenant":
				page[0].ApprovalTenant = "tenant-b"
			}
			if update != "" {
				if _, err := s.store.Exec(ctx, update); err != nil {
					t.Fatal(err)
				}
			}
			pending, err := s.IsPending(ctx, page[0])
			if err != nil || pending != (scenario == "pending") {
				t.Fatalf("pending=%v err=%v", pending, err)
			}
		})
	}
}

func TestPersonalAssistantApprovalStoreRetainsNonHumanRowsForPaging(t *testing.T) {
	s := personalAssistantApprovalQueryFixture(t)
	ctx := context.Background()
	for _, sql := range []string{
		`INSERT INTO hil_workflow_instances VALUES ('workflow', 'tenant-a', 'decision')`,
		`INSERT INTO hil_workflow_tasks VALUES ('agent-task', 'tenant-a', 'workflow', 'pending', 'agent', 'agent-a', 2)`,
		`INSERT INTO hil_workflow_tasks VALUES ('human-task', 'tenant-a', 'workflow', 'pending', 'human', 'human-a', 1)`,
	} {
		if _, err := s.store.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.ListPending(ctx, 1, 0)
	if err != nil || len(first) != 1 || first[0].HumanProfileID != "" {
		t.Fatalf("first page=%+v %v", first, err)
	}
	second, err := s.ListPending(ctx, 1, 1)
	if err != nil || len(second) != 1 || second[0].HumanProfileID != "human-a" {
		t.Fatalf("second page=%+v %v", second, err)
	}
	pending, err := s.IsPending(ctx, second[0])
	if err != nil || !pending {
		t.Fatalf("workflow pending=%v %v", pending, err)
	}
	if _, err := s.store.Exec(ctx, `UPDATE hil_decision_requests SET expires_at = ?`, time.Now().Add(-time.Minute).UnixNano()); err != nil {
		t.Fatal(err)
	}
	pending, err = s.IsPending(ctx, second[0])
	if err != nil || pending {
		t.Fatalf("expired workflow pending=%v %v", pending, err)
	}
}

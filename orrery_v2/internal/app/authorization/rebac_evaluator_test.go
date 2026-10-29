package authorization

import (
	"testing"
	"time"

	"orrery_v2/internal/app/rebac"
)

func TestReBACSCMConnectionDerivesUseAndManageFromOrganizationAdmin(t *testing.T) {
	const tenantID = "tenant-a"
	admin := rebac.Ref{Kind: "user", ID: "org-admin"}
	connection := rebac.Ref{Kind: "scm_connection", ID: "connection-1"}
	evaluator := newReBACRelationEvaluator([]rebac.RelationTuple{
		{TenantID: tenantID, Subject: admin, Relation: "admin", Object: rebac.Ref{Kind: "organization", ID: "org-1"}},
		{TenantID: tenantID, Subject: rebac.Ref{Kind: "organization", ID: "org-1"}, Relation: "org", Object: connection},
	}, time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC))

	for _, relation := range []string{"user", "manager"} {
		if !evaluator.check(tenantID, admin, relation, connection) {
			t.Fatalf("organization admin must have derived %q relation", relation)
		}
	}
	if evaluator.check("tenant-b", admin, "manager", connection) {
		t.Fatal("organization admin relation crossed tenant boundary")
	}
	if evaluator.check(tenantID, rebac.Ref{Kind: "user", ID: "member"}, "user", connection) {
		t.Fatal("organization member without admin relation was allowed")
	}
}

func TestReBACRelationEvaluatorAllowsMissionAutomationEditor(t *testing.T) {
	evaluator := newReBACRelationEvaluator([]rebac.RelationTuple{{
		TenantID: "tenant-1",
		Subject:  rebac.Ref{Kind: "system", ID: "responsibility-coordinator"},
		Relation: "automation",
		Object:   rebac.Ref{Kind: "mission", ID: "mission-1"},
	}}, time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC))

	if !evaluator.check("tenant-1", rebac.Ref{Kind: "system", ID: "responsibility-coordinator"}, "editor", rebac.Ref{Kind: "mission", ID: "mission-1"}) {
		t.Fatal("mission automation subject should inherit editor")
	}
	if evaluator.check("tenant-2", rebac.Ref{Kind: "system", ID: "responsibility-coordinator"}, "editor", rebac.Ref{Kind: "mission", ID: "mission-1"}) {
		t.Fatal("mission automation relation must not cross tenants")
	}
}

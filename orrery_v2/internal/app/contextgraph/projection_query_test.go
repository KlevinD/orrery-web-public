package contextgraph

import (
	"context"
	"errors"
	"testing"
)

func TestProjectionAuditCursorFiltersVisibilityAndPaginates(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	for _, audit := range []ProjectionAudit{
		{ID: "audit-b", TenantID: fixture.tenant, SourceEventID: "event-b", SourceEventType: "mission.updated", Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: fixture.allowed, ObservedAt: queryTestTime(20)},
		{ID: "audit-a", TenantID: fixture.tenant, SourceEventID: "event-a", SourceEventType: "mission.updated", Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: fixture.allowed, ObservedAt: queryTestTime(20)},
		{ID: "audit-replayed", TenantID: fixture.tenant, SourceEventID: "event-replayed", SourceEventType: "mission.updated", Status: "replayed", ReasonCode: "missing_target", SourceAccessRef: fixture.allowed, ObservedAt: queryTestTime(30)},
		{ID: "audit-hidden", TenantID: fixture.tenant, SourceEventID: "event-hidden", SourceEventType: "mission.updated", Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: fixture.denied, ObservedAt: queryTestTime(40)},
		{ID: "audit-unresolved", TenantID: fixture.tenant, SourceEventID: "event-unresolved", SourceEventType: "mission.updated", Status: "skipped", ReasonCode: "missing_target", ObservedAt: queryTestTime(50)},
	} {
		if err := fixture.graph.SaveProjectionAudit(ctx, audit); err != nil {
			t.Fatalf("save projection audit %s: %v", audit.ID, err)
		}
	}

	page, err := fixture.graph.ListVisibleProjectionAudits(ctx, ProjectionAuditCursorQuery{
		TenantID: fixture.tenant, Visibility: fixture.visibility(), Status: "skipped", ReasonCode: "missing_target", Limit: 1,
	})
	if err != nil {
		t.Fatalf("ListVisibleProjectionAudits first page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "audit-a" || !page.HasMore {
		t.Fatalf("first projection audit page = %+v", page)
	}
	next, err := fixture.graph.ListVisibleProjectionAudits(ctx, ProjectionAuditCursorQuery{
		TenantID: fixture.tenant, Visibility: fixture.visibility(), Status: "skipped", ReasonCode: "missing_target", Limit: 1,
		After: ProjectionAuditSeek{ObservedAt: page.Items[0].ObservedAt, ID: page.Items[0].ID},
	})
	if err != nil {
		t.Fatalf("ListVisibleProjectionAudits second page: %v", err)
	}
	if len(next.Items) != 1 || next.Items[0].ID != "audit-b" || next.HasMore {
		t.Fatalf("second projection audit page = %+v", next)
	}

	refs, err := fixture.graph.ListProjectionAccessRefs(ctx, AccessRefQuery{TenantID: fixture.tenant, Limit: 10})
	if err != nil {
		t.Fatalf("ListProjectionAccessRefs: %v", err)
	}
	if len(refs.Items) != 2 || refs.HasMore || !sameAccessRef(refs.Items[0], fixture.allowed) || !sameAccessRef(refs.Items[1], fixture.denied) {
		t.Fatalf("projection access refs = %+v", refs)
	}
}

func TestProjectionAuditTenantDuplicateExactLookup(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	for _, audit := range []ProjectionAudit{
		{ID: "shared-audit", TenantID: fixture.tenant, SourceEventID: "event-a", SourceEventType: "mission.updated", Status: "skipped", Reason: "tenant a", SourceAccessRef: fixture.allowed, ObservedAt: queryTestTime(10)},
		{ID: "shared-audit", TenantID: "mission:scope-b", SourceEventID: "event-b", SourceEventType: "mission.updated", Status: "replayed", Reason: "tenant b", SourceAccessRef: fixture.denied, ObservedAt: queryTestTime(11)},
	} {
		if err := fixture.graph.SaveProjectionAudit(ctx, audit); err != nil {
			t.Fatalf("save duplicate projection audit: %v", err)
		}
	}

	first, err := fixture.graph.GetVisibleProjectionAudit(ctx, fixture.tenant, "shared-audit", fixture.visibility())
	if err != nil || first.Reason != "tenant a" {
		t.Fatalf("tenant a projection audit = %+v, err=%v", first, err)
	}
	second, err := fixture.graph.GetVisibleProjectionAudit(ctx, "mission:scope-b", "shared-audit", VisibilityConstraint{AllowedAccessRefs: []AccessRef{fixture.denied}})
	if err != nil || second.Reason != "tenant b" {
		t.Fatalf("tenant b projection audit = %+v, err=%v", second, err)
	}
	_, err = fixture.graph.GetVisibleProjectionAudit(ctx, fixture.tenant, "shared-audit", VisibilityConstraint{AllowedAccessRefs: []AccessRef{fixture.denied}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong visibility exact lookup error = %v", err)
	}
}

func TestProjectionAuditReplayHistoryCursorIsTenantScoped(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	for _, history := range []ProjectionAuditReplayHistory{
		{ID: "history-a", AuditID: "shared-audit", TenantID: fixture.tenant, SourceEventID: "event-a", SourceEventType: "mission.updated", Status: "failed", SourceAccessRef: fixture.allowed, AttemptedAt: queryTestTime(20)},
		{ID: "history-b", AuditID: "shared-audit", TenantID: fixture.tenant, SourceEventID: "event-a", SourceEventType: "mission.updated", Status: "replayed", SourceAccessRef: fixture.allowed, AttemptedAt: queryTestTime(20)},
		{ID: "history-hidden", AuditID: "shared-audit", TenantID: fixture.tenant, SourceEventID: "event-hidden", SourceEventType: "mission.updated", Status: "failed", SourceAccessRef: fixture.denied, AttemptedAt: queryTestTime(30)},
		{ID: "history-b", AuditID: "shared-audit", TenantID: "mission:scope-b", SourceEventID: "event-b", SourceEventType: "mission.updated", Status: "failed", SourceAccessRef: fixture.denied, AttemptedAt: queryTestTime(40)},
	} {
		if err := fixture.graph.SaveProjectionAuditReplayHistory(ctx, history); err != nil {
			t.Fatalf("save replay history %s/%s: %v", history.TenantID, history.ID, err)
		}
	}

	page, err := fixture.graph.ListVisibleProjectionReplayHistory(ctx, ProjectionReplayHistoryCursorQuery{
		TenantID: fixture.tenant, AuditID: "shared-audit", Visibility: fixture.visibility(), Limit: 1,
	})
	if err != nil {
		t.Fatalf("ListVisibleProjectionReplayHistory first page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "history-b" || !page.HasMore {
		t.Fatalf("first replay history page = %+v", page)
	}
	next, err := fixture.graph.ListVisibleProjectionReplayHistory(ctx, ProjectionReplayHistoryCursorQuery{
		TenantID: fixture.tenant, AuditID: "shared-audit", Visibility: fixture.visibility(), Limit: 1,
		After: ProjectionReplayHistorySeek{AttemptedAt: page.Items[0].AttemptedAt, ID: page.Items[0].ID},
	})
	if err != nil {
		t.Fatalf("ListVisibleProjectionReplayHistory second page: %v", err)
	}
	if len(next.Items) != 1 || next.Items[0].ID != "history-a" || next.HasMore {
		t.Fatalf("second replay history page = %+v", next)
	}
}

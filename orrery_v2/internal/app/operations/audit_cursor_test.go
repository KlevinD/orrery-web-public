package operations_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/testkit"
)

func TestAuditStoreCursorUsesStableSeekAndExactMetadataFilters(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	store := operations.NewAuditStore(env.DBRouter, env.Clock, env.IDGenerator)
	base := env.Clock.Now().UTC()
	for _, event := range []operations.AuditEventDTO{
		{AuditID: "audit-b", ActorID: "reviewer", EntityType: "ontology_metadata_proposal", EntityID: "proposal-1", Action: "ontology.review", Metadata: map[string]string{"tenant_id": "tenant-1"}},
		{AuditID: "audit-a", ActorID: "reviewer", EntityType: "ontology_metadata_proposal", EntityID: "proposal-1", Action: "ontology.review", Metadata: map[string]string{"tenant_id": "tenant-1"}},
		{AuditID: "audit-other-tenant", ActorID: "reviewer", EntityType: "ontology_metadata_proposal", EntityID: "proposal-1", Action: "ontology.review", Metadata: map[string]string{"tenant_id": "tenant-2"}},
	} {
		if err := store.RecordAuditEvent(ctx, event); err != nil {
			t.Fatalf("RecordAuditEvent(%s): %v", event.AuditID, err)
		}
	}
	env.Clock.Advance(time.Second)
	if err := store.RecordAuditEvent(ctx, operations.AuditEventDTO{
		AuditID: "audit-newest", ActorID: "reviewer", EntityType: "ontology_metadata_proposal", EntityID: "proposal-1",
		Action: "ontology.apply", Metadata: map[string]string{"tenant_id": "tenant-1"},
	}); err != nil {
		t.Fatalf("RecordAuditEvent newest: %v", err)
	}

	first, err := store.ListAuditEventCursorPage(ctx, operations.AuditEventCursorQuery{
		EntityType: "ontology_metadata_proposal", EntityID: "proposal-1", MetadataEquals: map[string]string{"tenant_id": "tenant-1"}, Limit: 2,
	})
	if err != nil {
		t.Fatalf("ListAuditEventCursorPage first: %v", err)
	}
	if !first.HasMore || len(first.Events) != 2 || first.Events[0].AuditID != "audit-newest" || first.Events[1].AuditID != "audit-a" {
		t.Fatalf("first cursor page = %+v", first)
	}
	if !first.Events[1].OccurredAt.Equal(base) {
		t.Fatalf("tied event time = %s, want %s", first.Events[1].OccurredAt, base)
	}
	last := first.Events[len(first.Events)-1]
	second, err := store.ListAuditEventCursorPage(ctx, operations.AuditEventCursorQuery{
		EntityType: "ontology_metadata_proposal", EntityID: "proposal-1", MetadataEquals: map[string]string{"tenant_id": "tenant-1"}, Limit: 2,
		After: operations.AuditEventSeek{OccurredAt: last.OccurredAt, AuditID: last.AuditID},
	})
	if err != nil {
		t.Fatalf("ListAuditEventCursorPage second: %v", err)
	}
	if second.HasMore || len(second.Events) != 1 || second.Events[0].AuditID != "audit-b" {
		t.Fatalf("second cursor page = %+v", second)
	}
}

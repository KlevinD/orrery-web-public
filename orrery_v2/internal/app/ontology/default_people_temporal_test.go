package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

func TestDefaultPeopleV2OntologyUsesAuthoritativeBusinessTime(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	for _, objectTypeID := range []string{
		"workforce_subject", "organization_unit", "job_profile", "position",
		"workforce_assignment", "reporting_relationship", "responsibility", "responsibility_binding",
	} {
		objectType, err := registry.GetObjectType(ctx, objectTypeID)
		if err != nil {
			t.Fatalf("GetObjectType(%s): %v", objectTypeID, err)
		}
		if objectType.OwnerDomain != "people" || objectType.TemporalMode != TemporalModeStateInterval {
			t.Fatalf("%s governance = owner %q temporal %q", objectTypeID, objectType.OwnerDomain, objectType.TemporalMode)
		}
		for _, property := range objectType.Properties {
			if property.TemporalMode != TemporalModeStateInterval {
				t.Fatalf("%s.%s temporal mode = %q", objectTypeID, property.PropertyID, property.TemporalMode)
			}
		}
	}

	rule, found := defaultMappingRuleByEventType("people.org_unit.v2.changed")
	if !found {
		t.Fatal("People v2 organization unit mapping rule is missing")
	}
	if rule.SourceDomain != "people" || rule.SourceRef != "people.outbox.v2" || rule.EventSchemaVersion != "2" {
		t.Fatalf("People v2 rule governance = %+v", rule)
	}
	projection, err := CompileMappingRuleProjection(rule)
	if err != nil {
		t.Fatalf("CompileMappingRuleProjection: %v", err)
	}

	validFrom := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	recordedAt := time.Date(2026, 8, 3, 9, 30, 0, 0, time.UTC)
	event := graphprotocol.DomainEvent{
		ID:            "people-org-event-2",
		TenantID:      "tenant-1",
		Type:          "people.org_unit.v2.changed",
		SchemaVersion: "2",
		StreamID:      "org_unit:engineering",
		Version:       2,
		OccurredAt:    recordedAt,
		PublishedBy:   graphprotocol.ActorRef{Kind: "service", ID: "people"},
		Payload: map[string]any{
			"Value": map[string]any{
				"ID": "engineering", "Code": "ENG", "Name": "Engineering",
				"ParentID": "company", "Status": "active", "Revision": float64(2),
			},
			"Metadata": map[string]any{
				"Revision":   float64(2),
				"Effective":  map[string]any{"Start": validFrom.Format(time.RFC3339Nano)},
				"RecordedAt": recordedAt.Format(time.RFC3339Nano),
				"Kind":       "business",
			},
		},
	}
	result, err := projection.Project(ctx, event)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	var object graphprotocol.ObjectAssertion
	var parentLink graphprotocol.LinkAssertion
	for _, assertion := range result.Assertions {
		switch typed := assertion.(type) {
		case graphprotocol.ObjectAssertion:
			object = typed
		case graphprotocol.LinkAssertion:
			parentLink = typed
		}
	}
	if object.Object.ID != "engineering" || object.Label != "Engineering" {
		t.Fatalf("organization assertion = %+v", object)
	}
	if object.Provenance.Temporal.Mode != string(TemporalModeStateInterval) ||
		!object.Provenance.Temporal.BusinessValidFrom.Equal(validFrom) ||
		!object.Provenance.Temporal.SourceRecordedAt.Equal(recordedAt) ||
		object.Provenance.Temporal.OrreryIngestedAt.IsZero() {
		t.Fatalf("organization temporal provenance = %+v", object.Provenance.Temporal)
	}
	if parentLink.Identity != "engineering" || parentLink.From.ID != "engineering" || parentLink.To.ID != "company" {
		t.Fatalf("organization parent link = %+v", parentLink)
	}
	report := graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry)).CheckResult(ctx, projection, event, result)
	if !report.OK {
		t.Fatalf("conformance report = %+v", report)
	}
}

func TestPeopleV2CatalogCreateFallsBackToEventTime(t *testing.T) {
	rule, found := defaultMappingRuleByEventType("people.subject.v2.changed")
	if !found {
		t.Fatal("People v2 subject mapping rule is missing")
	}
	projection, err := CompileMappingRuleProjection(rule)
	if err != nil {
		t.Fatalf("CompileMappingRuleProjection: %v", err)
	}
	occurredAt := time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC)
	result, err := projection.Project(context.Background(), graphprotocol.DomainEvent{
		ID: "subject-created-1", TenantID: "tenant-1", Type: rule.EventType, SchemaVersion: "2",
		OccurredAt: occurredAt, PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "people"},
		Payload: map[string]any{
			"id": "subject-1", "type": "human", "display_name": "Da Fu",
			"status": "active", "revision": float64(1),
		},
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	object := result.Assertions[0].(graphprotocol.ObjectAssertion)
	if !object.Provenance.Temporal.BusinessValidFrom.Equal(occurredAt) ||
		!object.Provenance.Temporal.SourceRecordedAt.Equal(occurredAt) {
		t.Fatalf("create event temporal fallback = %+v", object.Provenance.Temporal)
	}
}

func defaultMappingRuleByEventType(eventType string) (MappingRule, bool) {
	for _, rule := range defaultMappingRules() {
		if rule.EventType == eventType {
			return rule, true
		}
	}
	return MappingRule{}, false
}

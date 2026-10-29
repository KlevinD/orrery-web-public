package graphprotocol

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestConformanceGateAcceptsProjectionWithObjectLinkFactAndProvenance(t *testing.T) {
	projection := StaticProjection{
		SpecValue: ProjectionSpec{
			Name:           "mission.task.graph",
			Version:        "v1",
			SourceType:     "mission.task.updated",
			ObjectTypes:    []string{"mission", "task"},
			LinkTypes:      []string{"mission_has_task"},
			FactPredicates: []string{"status"},
		},
		ProjectFunc: func(context.Context, DomainEvent) (ProjectionResult, error) {
			return ProjectionResult{
				Cursor: ProjectionCursor{SourceID: "event-1", SourceVersion: 12},
				Assertions: []Assertion{
					ObjectAssertion{
						Object:       ObjectRef{TenantID: "tenant-1", Type: "mission", ID: "mission-1"},
						CanonicalKey: "mission:mission-1",
						Label:        "Launch",
						Provenance:   testProvenance(),
					},
					ObjectAssertion{
						Object:       ObjectRef{TenantID: "tenant-1", Type: "task", ID: "task-1"},
						CanonicalKey: "task:task-1",
						Label:        "Draft plan",
						Provenance:   testProvenance(),
					},
					LinkAssertion{
						LinkType:   "mission_has_task",
						From:       ObjectRef{TenantID: "tenant-1", Type: "mission", ID: "mission-1"},
						To:         ObjectRef{TenantID: "tenant-1", Type: "task", ID: "task-1"},
						Provenance: testProvenance(),
					},
					FactAssertion{
						Subject:    ObjectRef{TenantID: "tenant-1", Type: "task", ID: "task-1"},
						Predicate:  "status",
						Value:      "active",
						Provenance: testProvenance(),
					},
				},
			}, nil
		},
	}

	report := NewConformanceGate().Check(context.Background(), projection, DomainEvent{
		ID:          "event-1",
		TenantID:    "tenant-1",
		Type:        "mission.task.updated",
		StreamID:    "mission-1",
		Version:     12,
		OccurredAt:  time.Unix(10, 0).UTC(),
		Payload:     map[string]any{"task_id": "task-1"},
		PublishedBy: ActorRef{Kind: "service", ID: "mission"},
	})
	if !report.OK {
		t.Fatalf("conformance report should pass: %#v", report)
	}
	if !report.CoversObjectType("mission") || !report.CoversLinkType("mission_has_task") || !report.CoversFactPredicate("status") {
		t.Fatalf("coverage not recorded: %#v", report)
	}
}

func TestConformanceGateAcceptsExternalSourceEventProvenance(t *testing.T) {
	projection := StaticProjection{
		SpecValue: ProjectionSpec{Name: "people.subject.graph", Version: "v1", SourceType: "people.subject.v2.changed", ObjectTypes: []string{"person"}},
		ProjectFunc: func(context.Context, DomainEvent) (ProjectionResult, error) {
			provenance := testProvenance()
			provenance.SourceEventID = "people-event-1"
			provenance.SourceEventType = "people.subject.v2.changed"
			return ProjectionResult{
				Cursor: ProjectionCursor{SourceID: "people.v2:8:tenant-1:people-event-1", SourceVersion: 1},
				Assertions: []Assertion{ObjectAssertion{
					Object: ObjectRef{TenantID: "tenant-1", Type: "person", ID: "person-1"}, CanonicalKey: "person:person-1", Provenance: provenance,
				}},
			}, nil
		},
	}
	event := DomainEvent{
		ID: "people.v2:8:tenant-1:people-event-1", SourceEventID: "people-event-1",
		TenantID: "tenant-1", Type: "people.subject.v2.changed", Version: 1,
	}
	if report := NewConformanceGate().Check(context.Background(), projection, event); !report.OK {
		t.Fatalf("external source event provenance should pass: %#v", report)
	}
}

func TestConformanceGateRejectsMissingProvenanceAndCoverage(t *testing.T) {
	projection := StaticProjection{
		SpecValue: ProjectionSpec{
			Name:           "bad.graph",
			Version:        "v1",
			SourceType:     "mission.task.updated",
			ObjectTypes:    []string{"mission"},
			LinkTypes:      []string{"mission_has_task"},
			FactPredicates: []string{"status"},
		},
		ProjectFunc: func(context.Context, DomainEvent) (ProjectionResult, error) {
			return ProjectionResult{
				Cursor: ProjectionCursor{SourceID: "event-1", SourceVersion: 12},
				Assertions: []Assertion{
					ObjectAssertion{
						Object:       ObjectRef{TenantID: "tenant-1", Type: "mission", ID: "mission-1"},
						CanonicalKey: "mission:mission-1",
					},
				},
			}, nil
		},
	}

	report := NewConformanceGate().Check(context.Background(), projection, DomainEvent{
		ID:       "event-1",
		TenantID: "tenant-1",
		Type:     "mission.task.updated",
		Version:  12,
	})
	if report.OK {
		t.Fatalf("conformance report should fail")
	}
	if !report.HasIssue("missing_provenance") || !report.HasIssue("missing_link_coverage") || !report.HasIssue("missing_fact_coverage") {
		t.Fatalf("report missing expected issues: %#v", report)
	}
}

func TestConformanceGateChecksProjectionAgainstOntologyRegistry(t *testing.T) {
	ontology := &recordingOntologyRegistry{
		objectTypes: map[string]bool{"mission": true, "task": true},
		linkTypes:   map[string]bool{"mission_has_task": true},
	}
	projection := StaticProjection{
		SpecValue: ProjectionSpec{
			Name:        "mission.bad.graph",
			Version:     "v1",
			SourceType:  "mission.task.updated",
			ObjectTypes: []string{"mission", "unknown_object"},
			LinkTypes:   []string{"unknown_link"},
		},
		ProjectFunc: func(context.Context, DomainEvent) (ProjectionResult, error) {
			return ProjectionResult{
				Cursor: ProjectionCursor{SourceID: "event-1", SourceVersion: 12},
				Assertions: []Assertion{
					ObjectAssertion{
						Object:       ObjectRef{TenantID: "tenant-1", Type: "unknown_object", ID: "object-1"},
						CanonicalKey: "unknown:object-1",
						Provenance:   testProvenance(),
					},
					LinkAssertion{
						LinkType:   "unknown_link",
						From:       ObjectRef{TenantID: "tenant-1", Type: "mission", ID: "mission-1"},
						To:         ObjectRef{TenantID: "tenant-1", Type: "task", ID: "task-1"},
						Provenance: testProvenance(),
					},
				},
			}, nil
		},
	}

	report := NewConformanceGate(WithOntologyRegistry(ontology)).Check(context.Background(), projection, DomainEvent{
		ID:       "event-1",
		TenantID: "tenant-1",
		Type:     "mission.task.updated",
		Version:  12,
	})
	if report.OK {
		t.Fatalf("conformance report should fail")
	}
	if !report.HasIssue("unknown_object_type") || !report.HasIssue("unknown_link_type") {
		t.Fatalf("report missing ontology issues: %#v", report)
	}
}

func TestConformanceGateSurfacesProjectionErrors(t *testing.T) {
	projection := StaticProjection{
		SpecValue: ProjectionSpec{Name: "failing.graph", Version: "v1", SourceType: "event"},
		ProjectFunc: func(context.Context, DomainEvent) (ProjectionResult, error) {
			return ProjectionResult{}, errors.New("boom")
		},
	}

	report := NewConformanceGate().Check(context.Background(), projection, DomainEvent{ID: "event-1", TenantID: "tenant-1", Type: "event"})
	if report.OK || !report.HasIssue("projection_error") {
		t.Fatalf("report = %#v, want projection_error", report)
	}
	if !strings.Contains(report.Issues[0].Message, "boom") {
		t.Fatalf("projection error not surfaced: %#v", report.Issues)
	}
}

func TestConformanceGateRejectsMismatchedEventSchemaVersion(t *testing.T) {
	projection := StaticProjection{
		SpecValue: ProjectionSpec{
			Name: "versioned.graph", Version: "v1", SourceType: "mission.task.updated", SourceSchemaVersion: "v2",
		},
		ProjectFunc: func(context.Context, DomainEvent) (ProjectionResult, error) {
			return ProjectionResult{Cursor: ProjectionCursor{SourceID: "event-1", SourceVersion: 1}}, nil
		},
	}

	report := NewConformanceGate().Check(context.Background(), projection, DomainEvent{
		ID: "event-1", TenantID: "tenant-1", Type: "mission.task.updated", SchemaVersion: "v1", Version: 1,
	})
	if report.OK || !report.HasIssue("event_schema_version_mismatch") {
		t.Fatalf("report = %#v, want event_schema_version_mismatch", report)
	}
}

type recordingOntologyRegistry struct {
	objectTypes map[string]bool
	linkTypes   map[string]bool
}

func (r *recordingOntologyRegistry) HasObjectType(ctx context.Context, objectType string) (bool, error) {
	return r.objectTypes[objectType], nil
}

func (r *recordingOntologyRegistry) HasLinkType(ctx context.Context, linkType string) (bool, error) {
	return r.linkTypes[linkType], nil
}

func testProvenance() ProvenanceRef {
	return ProvenanceRef{
		SourceEventID:   "event-1",
		SourceEventType: "mission.task.updated",
		SourceSystem:    "orrery",
		Actor:           ActorRef{Kind: "service", ID: "mission"},
		ObservedAt:      time.Unix(10, 0).UTC(),
	}
}

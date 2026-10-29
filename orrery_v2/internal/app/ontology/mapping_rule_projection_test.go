package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

func TestCompileMappingRuleProjectsObjectFactAndLinkAssertions(t *testing.T) {
	ctx := context.Background()
	rule := MappingRule{
		ID:                 "mission.task.created",
		EventType:          "mission.task.created",
		EventSchemaVersion: "v1",
		ObjectTypes:        []string{"task"},
		LinkTypes:          []string{"mission_has_task"},
		Enabled:            true,
		Rule: map[string]any{
			"objects": []any{
				map[string]any{"type": "task", "id": "$.task_id", "canonical_key": "task:{task_id}", "label": "$.title"},
			},
			"properties": []any{
				map[string]any{"object_type": "task", "object_id": "$.task_id", "property_id": "title", "value": "$.title"},
			},
			"links": []any{
				map[string]any{"type": "mission_has_task", "from_type": "mission", "from_id": "$.mission_id", "to_type": "task", "to_id": "$.task_id"},
			},
		},
	}
	projection, err := CompileMappingRuleProjection(rule)
	if err != nil {
		t.Fatalf("CompileMappingRuleProjection: %v", err)
	}
	if projection.Spec().Name != "mission.task.created" || projection.Spec().SourceType != "mission.task.created" || projection.Spec().SourceSchemaVersion != "v1" {
		t.Fatalf("spec = %+v", projection.Spec())
	}

	event := graphprotocol.DomainEvent{
		ID:              "event-1",
		TenantID:        "tenant-1",
		Type:            "mission.task.created",
		SchemaVersion:   "v1",
		StreamID:        "mission:mission-1",
		Version:         7,
		OccurredAt:      time.Unix(20, 0).UTC(),
		SourceAccessRef: graphprotocol.AccessRef{Kind: "mission", ID: "mission-1", TenantID: "tenant-1"},
		Payload: map[string]any{
			"mission_id": "mission-1",
			"task_id":    "task-1",
			"title":      "Draft plan",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "mission"},
	}
	result, err := projection.Project(ctx, event)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if result.Cursor.SourceID != "event-1" || result.Cursor.SourceVersion != 7 {
		t.Fatalf("cursor = %+v", result.Cursor)
	}
	if len(result.Assertions) != 3 {
		t.Fatalf("assertions = %#v", result.Assertions)
	}
	object, ok := result.Assertions[0].(graphprotocol.ObjectAssertion)
	if !ok || object.Object.Type != "task" || object.Object.ID != "task-1" || object.CanonicalKey != "task:task-1" || object.Label != "Draft plan" {
		t.Fatalf("object assertion = %#v", result.Assertions[0])
	}
	if object.Provenance.SourceStreamID != event.StreamID || object.Provenance.SourceVersion != event.Version || object.Provenance.SourceAccessRef != event.SourceAccessRef {
		t.Fatalf("object provenance = %#v", object.Provenance)
	}
	fact, ok := result.Assertions[1].(graphprotocol.FactAssertion)
	if !ok || fact.Subject.Type != "task" || fact.Subject.ID != "task-1" || fact.Predicate != "title" || fact.Value != "Draft plan" {
		t.Fatalf("fact assertion = %#v", result.Assertions[1])
	}
	link, ok := result.Assertions[2].(graphprotocol.LinkAssertion)
	if !ok || link.LinkType != "mission_has_task" || link.From.Type != "mission" || link.From.ID != "mission-1" || link.To.Type != "task" || link.To.ID != "task-1" {
		t.Fatalf("link assertion = %#v", result.Assertions[2])
	}

	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	report := graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry)).Check(ctx, projection, event)
	if !report.OK {
		t.Fatalf("conformance report = %#v", report)
	}
}

func TestCompiledMappingRuleProjectionRejectsMissingPayloadField(t *testing.T) {
	projection, err := CompileMappingRuleProjection(MappingRule{
		ID:          "mission.created",
		EventType:   "mission.created",
		ObjectTypes: []string{"mission"},
		Enabled:     true,
		Rule: map[string]any{
			"objects": []any{
				map[string]any{"type": "mission", "id": "$.id", "canonical_key": "mission:{id}"},
			},
		},
	})
	if err != nil {
		t.Fatalf("CompileMappingRuleProjection: %v", err)
	}
	_, err = projection.Project(context.Background(), graphprotocol.DomainEvent{
		ID:       "event-1",
		TenantID: "tenant-1",
		Type:     "mission.created",
		Version:  1,
		Payload:  map[string]any{},
	})
	if err == nil {
		t.Fatalf("Project should reject missing payload field")
	}
}

func TestCompiledMappingRuleProjectionSkipsOptionalLinkWhenPayloadFieldMissing(t *testing.T) {
	projection, err := CompileMappingRuleProjection(MappingRule{
		ID:          "memory.record.accepted",
		EventType:   "memory.record.accepted",
		ObjectTypes: []string{"memory_record"},
		LinkTypes:   []string{"memory_cites_knowledge_document"},
		Enabled:     true,
		Rule: map[string]any{
			"objects": []any{
				map[string]any{"type": "memory_record", "id": "$.id", "canonical_key": "memory_record:{id}"},
			},
			"links": []any{
				map[string]any{"type": "memory_cites_knowledge_document", "from_type": "memory_record", "from_id": "$.id", "to_type": "knowledge_document", "to_id": "$.knowledge_document_id", "optional": true},
			},
		},
	})
	if err != nil {
		t.Fatalf("CompileMappingRuleProjection: %v", err)
	}

	result, err := projection.Project(context.Background(), graphprotocol.DomainEvent{
		ID:       "event-1",
		TenantID: "tenant-1",
		Type:     "memory.record.accepted",
		Version:  1,
		Payload: map[string]any{
			"id": "memory-1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
		OccurredAt:  time.Unix(30, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(result.Assertions) != 1 {
		t.Fatalf("assertions = %#v, want only memory object assertion", result.Assertions)
	}
	if object, ok := result.Assertions[0].(graphprotocol.ObjectAssertion); !ok || object.Object.Type != "memory_record" || object.Object.ID != "memory-1" {
		t.Fatalf("object assertion = %#v", result.Assertions[0])
	}
}

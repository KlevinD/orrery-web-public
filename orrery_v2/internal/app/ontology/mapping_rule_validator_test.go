package ontology

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryRegistryRejectsExecutableMappingRuleShape(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "mission",
		DisplayName:      "Mission",
		CanonicalKeyRule: "mission:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}

	err := registry.SaveMappingRule(ctx, MappingRule{
		ID:          "mission.created",
		EventType:   "mission.created",
		ObjectTypes: []string{"mission"},
		Rule: map[string]any{
			"script": "return event.payload",
		},
		Enabled: true,
	})
	if !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("SaveMappingRule error = %v, want ErrInvalidDefinition", err)
	}
}

func TestMemoryRegistryRejectsMappingRuleUnknownPropertyAssertion(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "mission",
		DisplayName:      "Mission",
		CanonicalKeyRule: "mission:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}

	err := registry.SaveMappingRule(ctx, MappingRule{
		ID:          "mission.created",
		EventType:   "mission.created",
		ObjectTypes: []string{"mission"},
		Rule: map[string]any{
			"properties": []any{
				map[string]any{
					"object_type": "mission",
					"object_id":   "$.id",
					"property_id": "status",
					"value":       "$.status",
				},
			},
		},
		Enabled: true,
	})
	if !errors.Is(err, ErrUnknownPropertyType) {
		t.Fatalf("SaveMappingRule error = %v, want ErrUnknownPropertyType", err)
	}
}

func TestMemoryRegistryAcceptsFactOnlyMappingRule(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "knowledge_document",
		DisplayName:      "Knowledge Document",
		CanonicalKeyRule: "knowledge_document:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	if err := registry.SavePropertyType(ctx, PropertyType{
		ID:           "knowledge_document.status",
		ObjectTypeID: "knowledge_document",
		PropertyID:   "status",
		ValueType:    ValueTypeString,
	}); err != nil {
		t.Fatalf("SavePropertyType: %v", err)
	}

	err := registry.SaveMappingRule(ctx, MappingRule{
		ID:        "knowledge.document.status.updated",
		EventType: "knowledge.document.status.updated",
		Rule: map[string]any{
			"properties": []any{
				map[string]any{
					"object_type": "knowledge_document",
					"object_id":   "$.id",
					"property_id": "status",
					"value":       "$.status",
				},
			},
		},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("SaveMappingRule fact-only rule: %v", err)
	}
}

func TestMemoryRegistryAcceptsOptionalPropertyMappingTemplate(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "contract",
		DisplayName:      "Contract",
		CanonicalKeyRule: "contract:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	for _, propertyType := range []PropertyType{
		{ID: "contract.title", ObjectTypeID: "contract", PropertyID: "title", ValueType: ValueTypeString},
		{ID: "contract.status", ObjectTypeID: "contract", PropertyID: "status", ValueType: ValueTypeString},
	} {
		if err := registry.SavePropertyType(ctx, propertyType); err != nil {
			t.Fatalf("SavePropertyType(%s): %v", propertyType.ID, err)
		}
	}

	if err := registry.SaveMappingRule(ctx, MappingRule{
		ID:          "legal.contract.created",
		EventType:   "legal.contract.created",
		ObjectTypes: []string{"contract"},
		Rule: map[string]any{
			"objects": []any{
				map[string]any{"type": "contract", "id": "$.id", "canonical_key": "contract:{id}"},
			},
			"properties": []any{
				map[string]any{"object_type": "contract", "object_id": "$.id", "property_id": "title", "value": "$.title"},
				map[string]any{"object_type": "contract", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
			},
		},
		Enabled: true,
	}); err != nil {
		t.Fatalf("SaveMappingRule optional property template: %v", err)
	}
}

func TestMemoryRegistryAcceptsDeclarativeMappingRuleTemplates(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := seedMissionTaskOntology(ctx, registry); err != nil {
		t.Fatalf("seed ontology: %v", err)
	}

	if err := registry.SavePropertyType(ctx, PropertyType{
		ID:           "task.title",
		ObjectTypeID: "task",
		PropertyID:   "title",
		ValueType:    ValueTypeString,
	}); err != nil {
		t.Fatalf("SavePropertyType task.title: %v", err)
	}

	err := registry.SaveMappingRule(ctx, MappingRule{
		ID:          "mission.task.created",
		EventType:   "mission.task.created",
		ObjectTypes: []string{"mission", "task"},
		LinkTypes:   []string{"mission_has_task"},
		Rule: map[string]any{
			"objects": []any{
				map[string]any{
					"type":          "mission",
					"id":            "$.mission_id",
					"canonical_key": "mission:{mission_id}",
				},
				map[string]any{
					"type":          "task",
					"id":            "$.task_id",
					"canonical_key": "task:{task_id}",
				},
			},
			"properties": []any{
				map[string]any{
					"object_type": "task",
					"object_id":   "$.task_id",
					"property_id": "title",
					"value":       "$.title",
				},
			},
			"links": []any{
				map[string]any{
					"type":        "mission_has_task",
					"from_type":   "mission",
					"from_id":     "$.mission_id",
					"to_type":     "task",
					"to_id":       "$.task_id",
					"evidence_id": "$.event_id",
				},
			},
		},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("SaveMappingRule declarative rule: %v", err)
	}
}

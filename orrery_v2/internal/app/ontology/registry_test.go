package ontology

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryRegistryRegistersObjectLinkPropertyAndMappingRule(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()

	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "mission",
		TenantScope:      TenantScopeTenant,
		DisplayName:      "Mission",
		CanonicalKeyRule: "mission:{id}",
		OwnerDomain:      "mission_control",
		Sensitivity:      "internal",
		Properties: []PropertyDefinition{{
			PropertyID: "title",
			ValueType:  ValueTypeString,
			Required:   true,
		}},
	}); err != nil {
		t.Fatalf("SaveObjectType mission: %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantScope:      TenantScopeTenant,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
		OwnerDomain:      "mission_control",
	}); err != nil {
		t.Fatalf("SaveObjectType task: %v", err)
	}
	if err := registry.SaveLinkType(ctx, LinkType{
		ID:               "mission_has_task",
		FromObjectType:   "mission",
		ToObjectType:     "task",
		Cardinality:      CardinalityOneToMany,
		EvidenceRequired: true,
		AllowedActions:   []string{"mission.plan"},
	}); err != nil {
		t.Fatalf("SaveLinkType: %v", err)
	}
	if err := registry.SavePropertyType(ctx, PropertyType{
		ID:              "mission.title",
		ObjectTypeID:    "mission",
		PropertyID:      "title",
		ValueType:       ValueTypeString,
		AuthoritySource: "mission_control",
		ConflictPolicy:  ConflictPolicyAuthorityWins,
		AllowedActions:  []string{"mission.plan"},
	}); err != nil {
		t.Fatalf("SavePropertyType: %v", err)
	}
	if err := registry.SaveMappingRule(ctx, MappingRule{
		ID:                 "mission.created",
		TenantID:           "tenant-1",
		EventType:          "mission.created",
		SourceDomain:       "mission_control",
		SourceRef:          "internal/mission/event",
		EventSchemaVersion: "v1",
		ObjectTypes:        []string{"mission", "task"},
		LinkTypes:          []string{"mission_has_task"},
		Enabled:            true,
		Rule: map[string]any{
			"objects": []any{
				map[string]any{"type": "mission", "id": "$.mission_id", "canonical_key": "mission:{mission_id}"},
				map[string]any{"type": "task", "id": "$.task_id", "canonical_key": "task:{task_id}"},
			},
			"properties": []any{
				map[string]any{"object_type": "mission", "object_id": "$.mission_id", "property_id": "title", "value": "$.title"},
			},
			"links": []any{
				map[string]any{"type": "mission_has_task", "from_type": "mission", "from_id": "$.mission_id", "to_type": "task", "to_id": "$.task_id"},
			},
		},
	}); err != nil {
		t.Fatalf("SaveMappingRule: %v", err)
	}

	mission, err := registry.GetObjectType(ctx, "mission")
	if err != nil {
		t.Fatalf("GetObjectType: %v", err)
	}
	if mission.CanonicalKeyRule != "mission:{id}" || len(mission.Properties) != 1 || mission.Properties[0].PropertyID != "title" {
		t.Fatalf("mission object type = %+v", mission)
	}
	link, err := registry.GetLinkType(ctx, "mission_has_task")
	if err != nil {
		t.Fatalf("GetLinkType: %v", err)
	}
	if link.FromObjectType != "mission" || link.ToObjectType != "task" || link.Cardinality != CardinalityOneToMany || len(link.AllowedActions) != 1 || link.AllowedActions[0] != "mission.plan" {
		t.Fatalf("link type = %+v", link)
	}
	property, err := registry.GetPropertyType(ctx, "mission.title")
	if err != nil {
		t.Fatalf("GetPropertyType: %v", err)
	}
	if property.AuthoritySource != "mission_control" || property.ConflictPolicy != ConflictPolicyAuthorityWins || len(property.AllowedActions) != 1 || property.AllowedActions[0] != "mission.plan" {
		t.Fatalf("property type = %+v", property)
	}
	rules, err := registry.ListMappingRules(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	if len(rules) != 1 || rules[0].EventType != "mission.created" {
		t.Fatalf("rules = %+v", rules)
	}
	if rules[0].SourceDomain != "mission_control" || rules[0].SourceRef != "internal/mission/event" || rules[0].EventSchemaVersion != "v1" {
		t.Fatalf("rule governance metadata = %+v", rules[0])
	}
	if rules[0].RuleHash == "" {
		t.Fatalf("rule hash should be generated")
	}

	if err := registry.SaveMappingRule(ctx, MappingRule{
		ID:                 "mission.created.copy",
		TenantID:           "tenant-1",
		EventType:          "mission.created.copy",
		SourceDomain:       "mission_control",
		SourceRef:          "internal/mission/event",
		EventSchemaVersion: "v1",
		ObjectTypes:        []string{"mission", "task"},
		LinkTypes:          []string{"mission_has_task"},
		Enabled:            true,
		Rule:               rules[0].Rule,
	}); err != nil {
		t.Fatalf("SaveMappingRule copy: %v", err)
	}
	copiedRules, err := registry.ListMappingRules(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListMappingRules after copy: %v", err)
	}
	var copied MappingRule
	for _, candidate := range copiedRules {
		if candidate.ID == "mission.created.copy" {
			copied = candidate
			break
		}
	}
	if copied.RuleHash != rules[0].RuleHash {
		t.Fatalf("copied rule hash = %q, want %q", copied.RuleHash, rules[0].RuleHash)
	}
}

func TestMemoryRegistryRejectsUnknownReferencedTypes(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()

	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "mission",
		DisplayName:      "Mission",
		TenantScope:      TenantScopeTenant,
		CanonicalKeyRule: "mission:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	if err := registry.SaveLinkType(ctx, LinkType{
		ID:             "mission_has_task",
		FromObjectType: "mission",
		ToObjectType:   "task",
	}); !errors.Is(err, ErrUnknownObjectType) {
		t.Fatalf("SaveLinkType error = %v, want ErrUnknownObjectType", err)
	}
	if err := registry.SavePropertyType(ctx, PropertyType{
		ID:           "task.title",
		ObjectTypeID: "task",
		PropertyID:   "title",
		ValueType:    ValueTypeString,
	}); !errors.Is(err, ErrUnknownObjectType) {
		t.Fatalf("SavePropertyType error = %v, want ErrUnknownObjectType", err)
	}
	if err := registry.SaveMappingRule(ctx, MappingRule{
		ID:          "mission.created",
		TenantID:    "tenant-1",
		EventType:   "mission.created",
		ObjectTypes: []string{"mission"},
		LinkTypes:   []string{"mission_has_task"},
	}); !errors.Is(err, ErrUnknownLinkType) {
		t.Fatalf("SaveMappingRule error = %v, want ErrUnknownLinkType", err)
	}
}

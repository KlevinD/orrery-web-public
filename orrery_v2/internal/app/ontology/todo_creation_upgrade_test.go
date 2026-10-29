package ontology

import (
	"context"
	"reflect"
	"testing"
)

func TestInstallDefaultsUpgradesTodoCreationPropertiesOnce(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	var legacy ObjectType
	for _, object := range defaultObjectTypes() {
		if object.ID == "todo" {
			legacy = object
		}
	}
	legacy.Properties = nil
	legacy.Properties = append(legacy.Properties, property("title", ValueTypeString, true), property("custom_property", ValueTypeString, false))
	legacy.DisplayName = "Custom task label"
	legacy.AllowedActions = []string{"operator.review"}
	if err := registry.SaveObjectType(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	before, err := registry.GetObjectType(ctx, "todo")
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	after, err := registry.GetObjectType(ctx, "todo")
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != before.Version+1 || after.DisplayName != before.DisplayName || !reflect.DeepEqual(after.AllowedActions, before.AllowedActions) {
		t.Fatalf("Todo upgrade changed custom governance or revision: before=%+v after=%+v", before, after)
	}
	if !reflect.DeepEqual(after.Properties[:len(before.Properties)], before.Properties) || len(after.Properties) != len(before.Properties)+3 {
		t.Fatalf("Todo upgrade replaced existing fields or added unrelated defaults: %+v", after.Properties)
	}
	fields := map[string]ValueType{}
	for _, field := range after.Properties {
		fields[field.PropertyID] = field.ValueType
	}
	if fields["created_at"] != ValueTypeDateTime || fields["creation_actor_id"] != ValueTypeString || fields["creation_source"] != ValueTypeString {
		t.Fatalf("missing Todo origin field types: %+v", fields)
	}
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	repeated, err := registry.GetObjectType(ctx, "todo")
	if err != nil || !reflect.DeepEqual(after, repeated) {
		t.Fatalf("repeat install changed Todo definition: before=%+v after=%+v err=%v", after, repeated, err)
	}
}

func TestTodoCreationUpgradePreservesTenantDefinition(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	current := ObjectType{ID: "todo", TenantID: "tenant-custom", DisplayName: "Tenant task", CanonicalKeyRule: "todo:{id}"}
	if err := registry.SaveObjectType(ctx, current); err != nil {
		t.Fatal(err)
	}
	before, err := registry.GetObjectType(ctx, "todo")
	if err != nil {
		t.Fatal(err)
	}
	desired := ObjectType{ID: "todo", Properties: []PropertyDefinition{property("created_at", ValueTypeDateTime, false)}}
	if err := upgradeTodoCreationProperties(ctx, registry, before, desired); err != nil {
		t.Fatal(err)
	}
	after, err := registry.GetObjectType(ctx, "todo")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("tenant definition changed: before=%+v after=%+v err=%v", before, after, err)
	}
}

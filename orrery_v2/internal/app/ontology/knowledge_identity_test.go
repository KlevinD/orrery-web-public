package ontology

import (
	"context"
	"slices"
	"testing"
)

func TestDefaultKnowledgeIdentityUpgradePreservesCustomization(t *testing.T) {
	ctx := context.Background()
	for _, customizedOwner := range []bool{false, true} {
		t.Run(map[bool]string{false: "managed", true: "custom_owner"}[customizedOwner], func(t *testing.T) {
			r := NewMemoryRegistry()
			old := knowledgeEntityObjectType()
			for i := range old.Properties {
				old.Properties[i].TemporalMode = TemporalModeStatic
			}
			old.Properties = slices.DeleteFunc(old.Properties, func(p PropertyDefinition) bool { return p.PropertyID == "identity_keys" })
			old.DisplayName = "Customized entity"
			old.AllowedActions = append(old.AllowedActions, "custom.entity.inspect")
			old.Properties = append(old.Properties, property("custom_value", ValueTypeString, false))
			if customizedOwner {
				old.OwnerDomain = "custom_domain"
			}
			if err := r.SaveObjectType(ctx, old); err != nil {
				t.Fatal(err)
			}
			before, _ := r.GetObjectType(ctx, old.ID)
			if err := InstallDefaultOntologyDefinitions(ctx, r); err != nil {
				t.Fatal(err)
			}
			got, _ := r.GetObjectType(ctx, old.ID)
			hasIdentity := slices.ContainsFunc(got.Properties, func(p PropertyDefinition) bool {
				return p.PropertyID == "identity_keys" && p.ValueType == ValueTypeArray
			})
			if hasIdentity == customizedOwner || got.DisplayName != old.DisplayName || !slices.Contains(got.AllowedActions, "custom.entity.inspect") || !slices.ContainsFunc(got.Properties, func(p PropertyDefinition) bool { return p.PropertyID == "custom_value" }) {
				t.Fatalf("unsafe additive schema upgrade: %+v", got)
			}
			if !customizedOwner && got.Version != before.Version+1 {
				t.Fatal("identity schema upgrade was not versioned")
			}
			for _, p := range got.Properties {
				if p.PropertyID == "amount" && ((p.TemporalMode == TemporalModeStateInterval) == customizedOwner) {
					t.Fatal("unsafe managed assertion time upgrade")
				}
				if p.PropertyID == "custom_value" && p.TemporalMode != TemporalModeStatic {
					t.Fatal("custom field time mode changed")
				}
			}
			if err := InstallDefaultOntologyDefinitions(ctx, r); err != nil {
				t.Fatal(err)
			}
			replayed, _ := r.GetObjectType(ctx, old.ID)
			if replayed.Version != got.Version {
				t.Fatal("identity schema upgrade is not idempotent")
			}
		})
	}
}

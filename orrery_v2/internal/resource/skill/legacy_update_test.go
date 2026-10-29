package skill

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestApplyLegacyManagedDefinitionRequiresKnownSource(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	definition := SkillDefinition{ID: "skill.review", Name: "Review", Version: "1.0.0", Enabled: true}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}
	definition.Description = "updated by catalog"
	if err := ApplyLegacyManagedDefinition(ctx, registry, definition, ManagedUpdateSourceDefaultCatalog); err != nil {
		t.Fatalf("managed update: %v", err)
	}
	updated, err := registry.Get(ctx, definition.ID)
	if err != nil || updated.Description != definition.Description {
		t.Fatalf("updated definition = %+v, err=%v", updated, err)
	}
	if err := ApplyLegacyManagedDefinition(ctx, registry, definition, ManagedUpdateSource("rsi")); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("unknown source error = %v, want ErrInvalidDefinition", err)
	}
}

func TestApplyLegacyManagedDefinitionRejectsRegistryWithoutMutableSurface(t *testing.T) {
	registry := readOnlySkillRegistry{definition: SkillDefinition{ID: "skill.review", Name: "Review", Version: "1.0.0", Enabled: true}}
	err := ApplyLegacyManagedDefinition(context.Background(), registry, registry.definition, ManagedUpdateSourceResourceAdmin)
	if !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("read-only registry error = %v, want ErrInvalidDefinition", err)
	}
}

type readOnlySkillRegistry struct {
	definition SkillDefinition
}

func (r readOnlySkillRegistry) Register(context.Context, SkillDefinition) error  { return nil }
func (r readOnlySkillRegistry) Disable(context.Context, agentcore.SkillID) error { return nil }
func (r readOnlySkillRegistry) Delete(context.Context, agentcore.SkillID) error  { return nil }
func (r readOnlySkillRegistry) IsDeleted(context.Context, agentcore.SkillID) (bool, error) {
	return false, nil
}
func (r readOnlySkillRegistry) Get(context.Context, agentcore.SkillID) (SkillDefinition, error) {
	return r.definition, nil
}
func (r readOnlySkillRegistry) List(context.Context) ([]SkillDefinition, error) {
	return []SkillDefinition{r.definition}, nil
}
func (r readOnlySkillRegistry) ListByTag(context.Context, string) ([]SkillDefinition, error) {
	return nil, nil
}

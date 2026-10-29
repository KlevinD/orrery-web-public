package skill

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestResolverReturnsResolvedSkillForMatchingVersionAndTags(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	recorder := &captureAuditRecorder{}
	definition := SkillDefinition{
		ID:           agentcore.SkillID("skill.tdd"),
		Name:         "test-driven-development",
		Version:      "2.0.1",
		Description:  "Use red green refactor workflow",
		TaskTypeTags: []string{"implementation", "bugfix"},
		RequiredTools: []agentcore.ToolRef{
			{ToolID: agentcore.ToolID("tool.go.test"), VersionConstraint: ">=1.0.0"},
		},
		Enabled: true,
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}
	resolver := NewResolver(registry, ResolverOptions{AuditRecorder: recorder})

	resolved, err := resolver.Resolve(ctx, ResolveRequest{
		Ref:          agentcore.SkillRef{SkillID: definition.ID, VersionConstraint: "^2.0.0"},
		TaskTypeTags: []string{"implementation"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.MatchedVersion != "2.0.1" || len(resolved.Definition.RequiredTools) != 1 {
		t.Fatalf("resolved skill mismatch: %+v", resolved)
	}
	if len(recorder.events) != 1 || recorder.events[0].Type != AuditSkillResolved {
		t.Fatalf("audit events = %+v", recorder.events)
	}
}

func TestResolverRejectsDisabledAndVersionMismatch(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	definition := SkillDefinition{
		ID:      agentcore.SkillID("skill.old"),
		Name:    "old",
		Version: "1.0.0",
		Enabled: true,
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}
	resolver := NewResolver(registry, ResolverOptions{})

	_, err := resolver.Resolve(ctx, ResolveRequest{
		Ref: agentcore.SkillRef{SkillID: definition.ID, VersionConstraint: ">=2.0.0"},
	})
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("Resolve version error = %v, want ErrVersionMismatch", err)
	}

	if err := registry.Disable(ctx, definition.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	_, err = resolver.Resolve(ctx, ResolveRequest{
		Ref: agentcore.SkillRef{SkillID: definition.ID, VersionConstraint: ">=1.0.0"},
	})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("Resolve disabled error = %v, want ErrDisabled", err)
	}
}

func TestResolverWarnsWhenTaskTagsDoNotMatch(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	definition := SkillDefinition{
		ID:           agentcore.SkillID("skill.prd"),
		Name:         "prd-drafter",
		Version:      "1.0.0",
		TaskTypeTags: []string{"writing"},
		Enabled:      true,
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}
	resolver := NewResolver(registry, ResolverOptions{})

	resolved, err := resolver.Resolve(ctx, ResolveRequest{
		Ref:          agentcore.SkillRef{SkillID: definition.ID, VersionConstraint: "1.0.0"},
		TaskTypeTags: []string{"implementation"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved.BindingWarnings) != 1 || resolved.BindingWarnings[0].Code != WarningTaskTagMismatch {
		t.Fatalf("warnings = %+v", resolved.BindingWarnings)
	}
}

func TestResolverCanResolveAnExplicitHistoricalVersion(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	base := SkillDefinition{ID: "skill.history", Name: "history", Version: "1.0.0", Description: "base", Enabled: true}
	candidate := base.Clone()
	candidate.Version = "2.0.0"
	candidate.Description = "candidate"
	if err := registry.Register(ctx, base); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatalf("register version: %v", err)
	}
	resolver := NewResolver(registry, ResolverOptions{})
	resolved, err := resolver.Resolve(ctx, ResolveRequest{Ref: agentcore.SkillRef{SkillID: base.ID, VersionConstraint: "2.0.0"}})
	if err != nil {
		t.Fatalf("resolve historical version: %v", err)
	}
	if resolved.MatchedVersion != "2.0.0" || resolved.Definition.Description != "candidate" {
		t.Fatalf("resolved = %+v", resolved)
	}
	active, err := registry.Get(ctx, base.ID)
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if active.Version != "1.0.0" {
		t.Fatalf("resolving candidate changed active version to %s", active.Version)
	}
}

type captureAuditRecorder struct {
	events []AuditEvent
}

func (r *captureAuditRecorder) RecordSkillAudit(ctx context.Context, event AuditEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.events = append(r.events, event)
	return nil
}

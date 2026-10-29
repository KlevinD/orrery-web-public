package graphprotocol

import (
	"context"
	"testing"
)

func TestProjectionRegistryRejectsProjectionWithUnknownOntologyTypes(t *testing.T) {
	ctx := context.Background()
	ontology := &recordingOntologyRegistry{
		objectTypes: map[string]bool{"mission": true},
		linkTypes:   map[string]bool{"mission_has_task": true},
	}
	registry := NewProjectionRegistry(NewConformanceGate(WithOntologyRegistry(ontology)))

	err := registry.Register(ctx, StaticProjection{
		SpecValue: ProjectionSpec{
			Name:        "mission.bad.graph",
			Version:     "v1",
			SourceType:  "mission.created",
			ObjectTypes: []string{"mission", "task"},
			LinkTypes:   []string{"unknown_link"},
		},
	})
	if err == nil {
		t.Fatalf("Register should reject unknown ontology types")
	}
	report, ok := AsConformanceError(err)
	if !ok {
		t.Fatalf("Register error = %T %v, want conformance error", err, err)
	}
	if !report.HasIssue("unknown_object_type") || !report.HasIssue("unknown_link_type") {
		t.Fatalf("report missing ontology issues: %#v", report)
	}
}

func TestProjectionRegistryRegistersAndListsBySourceType(t *testing.T) {
	ctx := context.Background()
	ontology := &recordingOntologyRegistry{
		objectTypes: map[string]bool{"mission": true},
		linkTypes:   map[string]bool{},
	}
	registry := NewProjectionRegistry(NewConformanceGate(WithOntologyRegistry(ontology)))

	projection := StaticProjection{
		SpecValue: ProjectionSpec{
			Name:        "mission.created.graph",
			Version:     "v1",
			SourceType:  "mission.created",
			ObjectTypes: []string{"mission"},
		},
	}
	if err := registry.Register(ctx, projection); err != nil {
		t.Fatalf("Register: %v", err)
	}
	registered, ok := registry.Get("mission.created.graph")
	if !ok {
		t.Fatalf("projection should be registered")
	}
	if registered.Spec().Name != "mission.created.graph" {
		t.Fatalf("registered projection = %+v", registered.Spec())
	}
	bySource := registry.ListBySourceType("mission.created")
	if len(bySource) != 1 || bySource[0].Spec().Name != "mission.created.graph" {
		t.Fatalf("ListBySourceType = %+v", bySource)
	}
}

func TestProjectionRegistryReplaceAllSwapsProjectionSet(t *testing.T) {
	ctx := context.Background()
	ontology := &recordingOntologyRegistry{
		objectTypes: map[string]bool{"mission": true, "task": true},
		linkTypes:   map[string]bool{},
	}
	registry := NewProjectionRegistry(NewConformanceGate(WithOntologyRegistry(ontology)))

	if err := registry.Register(ctx, StaticProjection{
		SpecValue: ProjectionSpec{
			Name:        "mission.created.graph",
			Version:     "v1",
			SourceType:  "mission.created",
			ObjectTypes: []string{"mission"},
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.ReplaceAll(ctx, []GraphProjection{StaticProjection{
		SpecValue: ProjectionSpec{
			Name:        "task.created.graph",
			Version:     "v1",
			SourceType:  "task.created",
			ObjectTypes: []string{"task"},
		},
	}}); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}

	if _, ok := registry.Get("mission.created.graph"); ok {
		t.Fatalf("old projection should be removed")
	}
	if bySource := registry.ListBySourceType("mission.created"); len(bySource) != 0 {
		t.Fatalf("old source index should be removed, got %+v", bySource)
	}
	if bySource := registry.ListBySourceType("task.created"); len(bySource) != 1 || bySource[0].Spec().Name != "task.created.graph" {
		t.Fatalf("new source index = %+v", bySource)
	}
}

func TestProjectionRegistryReplaceAllKeepsCurrentSetWhenValidationFails(t *testing.T) {
	ctx := context.Background()
	ontology := &recordingOntologyRegistry{
		objectTypes: map[string]bool{"mission": true},
		linkTypes:   map[string]bool{},
	}
	registry := NewProjectionRegistry(NewConformanceGate(WithOntologyRegistry(ontology)))

	if err := registry.Register(ctx, StaticProjection{
		SpecValue: ProjectionSpec{
			Name:        "mission.created.graph",
			Version:     "v1",
			SourceType:  "mission.created",
			ObjectTypes: []string{"mission"},
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.ReplaceAll(ctx, []GraphProjection{StaticProjection{
		SpecValue: ProjectionSpec{
			Name:        "bad.graph",
			Version:     "v1",
			SourceType:  "bad.created",
			ObjectTypes: []string{"unknown"},
		},
	}}); err == nil {
		t.Fatalf("ReplaceAll should reject invalid projection")
	}

	if _, ok := registry.Get("mission.created.graph"); !ok {
		t.Fatalf("current projection should remain after failed replace")
	}
	if bySource := registry.ListBySourceType("mission.created"); len(bySource) != 1 {
		t.Fatalf("current source index should remain, got %+v", bySource)
	}
}

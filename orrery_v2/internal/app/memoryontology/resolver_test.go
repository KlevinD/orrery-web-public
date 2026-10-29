package memoryontology

import (
	"testing"

	"orrery_v2/internal/app/contextgraph"
)

func TestProjectMentionsRequiresUniqueExactTenantMatch(t *testing.T) {
	objects := []contextgraph.Object{
		{ID: "project-1", TenantID: "tenant-1", ObjectType: "project", Label: "Orrery"},
		{ID: "project-2", TenantID: "tenant-1", ObjectType: "project", Label: "Orrery"},
		{ID: "project-other", TenantID: "tenant-2", ObjectType: "project", Label: "Unique Project"},
		{ID: "project-3", TenantID: "tenant-1", ObjectType: "project", Label: "Unique Project"},
	}
	extraction := Extraction{Mentions: []Mention{
		{Surface: "Orrery", ObjectType: "project", ObjectHint: "Orrery", Confidence: 0.99},
		{Surface: "Unique Project", ObjectType: "project", Confidence: 0.95},
		{Surface: "project-1", ObjectType: "project", ObjectHint: "project-1", Confidence: 0.89},
		{Surface: "Anything", ObjectType: "unsupported", Confidence: 1},
	}}

	got := ProjectMentions("tenant-1", extraction, objects)
	if got[0].Resolution != ResolutionAmbiguous {
		t.Fatalf("ambiguous resolution = %+v", got[0])
	}
	if got[1].Resolution != ResolutionResolved || got[1].ResolvedObjectID != "project-3" {
		t.Fatalf("unique resolution = %+v", got[1])
	}
	if got[2].Resolution != ResolutionLowConfidence {
		t.Fatalf("low-confidence resolution = %+v", got[2])
	}
	if got[3].Resolution != ResolutionInvalidType {
		t.Fatalf("invalid-type resolution = %+v", got[3])
	}
}

func TestProjectMentionsPrefersExactIDOverDuplicateLabels(t *testing.T) {
	objects := []contextgraph.Object{
		{ID: "agent-ding", TenantID: "tenant-1", ObjectType: "agent", Label: "Ding"},
		{ID: "agent-other", TenantID: "tenant-1", ObjectType: "agent", Label: "Ding"},
	}
	got := ProjectMentions("tenant-1", Extraction{Mentions: []Mention{{
		Surface: "Ding", ObjectType: "agent", ObjectHint: "agent-ding", Confidence: 0.99,
	}}}, objects)
	if len(got) != 1 || got[0].Resolution != ResolutionResolved || got[0].ResolvedObjectID != "agent-ding" {
		t.Fatalf("projection = %+v", got)
	}
}

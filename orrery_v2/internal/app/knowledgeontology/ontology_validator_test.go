package knowledgeontology

import (
	"context"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
)

type linkTypeRegistry struct {
	link ontology.LinkType
	err  error
}

func (r linkTypeRegistry) GetLinkType(context.Context, string) (ontology.LinkType, error) {
	return r.link, r.err
}

func TestOntologyLinkValidatorChecksRegisteredEndpointTypes(t *testing.T) {
	validator := NewOntologyLinkValidator(
		linkTypeRegistry{link: ontology.LinkType{ID: "project_uses_tool", FromObjectType: "project", ToObjectType: "tool"}},
		fakeSnapshot{objects: []contextgraph.Object{
			{ID: "project-1", TenantID: "tenant-1", ObjectType: "project"},
			{ID: "tool-1", TenantID: "tenant-1", ObjectType: "tool"},
		}},
	)

	if err := validator.ValidateKnowledgeLink(context.Background(), "tenant-1", "project_uses_tool", "project-1", "tool-1"); err != nil {
		t.Fatalf("ValidateKnowledgeLink: %v", err)
	}
	if err := validator.ValidateKnowledgeLink(context.Background(), "tenant-1", "project_uses_tool", "tool-1", "project-1"); err == nil {
		t.Fatal("expected reversed endpoint types to fail")
	}
	if err := validator.ValidateKnowledgeLinkTypes(context.Background(), "tenant-1", "project_uses_tool", "project", "tool"); err != nil {
		t.Fatalf("ValidateKnowledgeLinkTypes: %v", err)
	}
}

package knowledgeontology

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/ontology"
)

type LinkTypeRegistry interface {
	GetLinkType(ctx context.Context, id string) (ontology.LinkType, error)
}

type OntologyLinkValidator struct {
	registry LinkTypeRegistry
	graph    GraphSnapshot
}

func NewOntologyLinkValidator(registry LinkTypeRegistry, graph GraphSnapshot) *OntologyLinkValidator {
	return &OntologyLinkValidator{registry: registry, graph: graph}
}

func (v *OntologyLinkValidator) ValidateKnowledgeLink(ctx context.Context, tenantID, linkTypeID, fromObjectID, toObjectID string) error {
	if v == nil || v.registry == nil || v.graph == nil {
		return fmt.Errorf("ontology registry and graph are required")
	}
	objects, err := graphObjectsByID(ctx, v.graph, tenantID, []string{fromObjectID, toObjectID})
	if err != nil {
		return err
	}
	fromType, toType := "", ""
	for _, object := range objects {
		if object.ID == fromObjectID {
			fromType = object.ObjectType
		}
		if object.ID == toObjectID {
			toType = object.ObjectType
		}
	}
	if fromType == "" || toType == "" {
		return fmt.Errorf("ontology link endpoints must exist in tenant graph")
	}
	return v.ValidateKnowledgeLinkTypes(ctx, tenantID, linkTypeID, fromType, toType)
}

func (v *OntologyLinkValidator) ValidateKnowledgeLinkTypes(ctx context.Context, tenantID, linkTypeID, fromObjectType, toObjectType string) error {
	if v == nil || v.registry == nil {
		return fmt.Errorf("ontology registry is required")
	}
	linkType, err := v.registry.GetLinkType(ctx, strings.TrimSpace(linkTypeID))
	if err != nil {
		return err
	}
	if linkType.TenantScope == ontology.TenantScopeTenant && strings.TrimSpace(linkType.TenantID) != "" && strings.TrimSpace(linkType.TenantID) != strings.TrimSpace(tenantID) {
		return fmt.Errorf("ontology link type %s is not visible to tenant %s", linkType.ID, tenantID)
	}
	if !strings.EqualFold(fromObjectType, linkType.FromObjectType) || !strings.EqualFold(toObjectType, linkType.ToObjectType) {
		return fmt.Errorf("ontology link %s requires %s -> %s, got %s -> %s", linkType.ID, linkType.FromObjectType, linkType.ToObjectType, fromObjectType, toObjectType)
	}
	return nil
}

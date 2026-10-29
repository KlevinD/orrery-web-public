package main

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/knowledgeontology"
	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgestore "orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/platform/graphprotocol"
)

type knowledgeOntologyEventTenantResolver interface {
	ResolveEventTenant(context.Context, graphprotocol.DomainEvent) (string, bool, error)
}

func (s productionDocumentOntologySink) WithTenantResolver(resolver knowledgeOntologyEventTenantResolver) productionDocumentOntologySink {
	s.tenantResolver = resolver
	return s
}

func (s productionDocumentOntologySink) PublishKnowledgeBaseMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	return s.publishTrustedOntologyEvent(ctx, event, "knowledge_base")
}

type productionKnowledgeOntologyEventTenantResolver struct {
	repo          knowledgestore.Repository
	organizations knowledgeOntologyOrganizationCatalog
}

func newProductionKnowledgeOntologyEventTenantResolver(
	repo knowledgestore.Repository,
	organizations knowledgeOntologyOrganizationCatalog,
) *productionKnowledgeOntologyEventTenantResolver {
	return &productionKnowledgeOntologyEventTenantResolver{repo: repo, organizations: organizations}
}

func (r *productionKnowledgeOntologyEventTenantResolver) ResolveEventTenant(ctx context.Context, event graphprotocol.DomainEvent) (string, bool, error) {
	if r == nil || r.repo == nil {
		return "", false, fmt.Errorf("knowledge ontology event tenant resolver dependencies are required")
	}
	baseID, err := r.eventKnowledgeBaseID(ctx, event)
	if err != nil {
		return "", false, err
	}
	base, err := r.repo.GetKnowledgeBase(ctx, baseID)
	if err != nil {
		return "", false, fmt.Errorf("get knowledge base %s for ontology event: %w", baseID, err)
	}

	resolver := productionKnowledgeOntologyOwnerResolver{organizations: r.organizations}
	tenantID, _, supported, err := resolver.Resolve(ctx, base)
	if err != nil {
		return "", false, fmt.Errorf("resolve knowledge base %s ontology owner: %w", base.ID, err)
	}
	return tenantID, supported, nil
}

func (r *productionKnowledgeOntologyEventTenantResolver) eventKnowledgeBaseID(ctx context.Context, event graphprotocol.DomainEvent) (knowledgecore.KnowledgeBaseID, error) {
	if baseID := knowledgeOntologyEventPayloadString(event.Payload, "knowledge_base_id"); baseID != "" {
		return knowledgecore.KnowledgeBaseID(baseID), nil
	}
	if event.Type == "knowledge.base.created" {
		if baseID := knowledgeOntologyEventPayloadString(event.Payload, "id"); baseID != "" {
			return knowledgecore.KnowledgeBaseID(baseID), nil
		}
	}
	documentID := knowledgeOntologyEventPayloadString(event.Payload, "document_id")
	if documentID == "" && (event.Type == "knowledge.document.metadata.updated" || event.Type == "knowledge.document.status.updated") {
		documentID = knowledgeOntologyEventPayloadString(event.Payload, "id")
	}
	if documentID == "" {
		return "", fmt.Errorf("knowledge ontology event %s does not identify a knowledge base or document", event.ID)
	}
	document, err := r.repo.GetDocument(ctx, knowledgecore.KnowledgeDocumentID(documentID))
	if err != nil {
		return "", fmt.Errorf("get knowledge document %s for ontology event: %w", documentID, err)
	}
	if strings.TrimSpace(string(document.KnowledgeBaseID)) == "" {
		return "", fmt.Errorf("knowledge document %s has no knowledge base", documentID)
	}
	return document.KnowledgeBaseID, nil
}

func knowledgeOntologyEventPayloadString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

type knowledgeOntologyCatalogGraph interface {
	Snapshot(context.Context, string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error)
}

type knowledgeOntologyCatalogMetadataSink interface {
	PublishKnowledgeBaseMetadata(context.Context, graphprotocol.DomainEvent) error
	PublishKnowledgeDocumentMetadata(context.Context, graphprotocol.DomainEvent) error
}

type knowledgeOntologyCatalogProjector interface {
	EnsureKnowledgeBaseCatalog(context.Context, knowledgeontology.Binding) error
}

type productionKnowledgeOntologyCatalogProjector struct {
	repo  knowledgestore.Repository
	graph knowledgeOntologyCatalogGraph
	sink  knowledgeOntologyCatalogMetadataSink
}

func (p productionKnowledgeOntologyCatalogProjector) EnsureKnowledgeBaseCatalog(ctx context.Context, binding knowledgeontology.Binding) error {
	if p.repo == nil || p.graph == nil || p.sink == nil {
		return fmt.Errorf("knowledge ontology catalog projector dependencies are required")
	}
	baseID := knowledgecore.KnowledgeBaseID(strings.TrimSpace(binding.KnowledgeBaseID))
	base, err := p.repo.GetKnowledgeBase(ctx, baseID)
	if err != nil {
		return fmt.Errorf("get knowledge base %s for ontology catalog projection: %w", baseID, err)
	}
	if base.Status != knowledgecore.KnowledgeActive || !isProductionPageIndexKnowledgeBase(base) ||
		strings.TrimSpace(base.ProviderResourceID) != strings.TrimSpace(binding.FolderID) {
		return fmt.Errorf("knowledge base %s does not match its active PageIndex ontology binding", base.ID)
	}
	documents, err := p.repo.ListDocuments(ctx, knowledgecore.DocumentQuery{KnowledgeBaseID: base.ID})
	if err != nil {
		return fmt.Errorf("list knowledge base %s documents for ontology catalog projection: %w", base.ID, err)
	}
	objects, err := p.catalogObjects(ctx, binding.TenantID, base.ID, documents.Items)
	if err != nil {
		return fmt.Errorf("read knowledge base %s ontology catalog: %w", base.ID, err)
	}
	baseExists := false
	existing := make(map[string]struct{}, len(objects))
	for _, object := range objects {
		if object.TenantID != binding.TenantID {
			continue
		}
		if strings.EqualFold(object.ObjectType, "knowledge_base") && strings.TrimSpace(object.ID) == strings.TrimSpace(string(base.ID)) {
			baseExists = true
		}
		if strings.EqualFold(object.ObjectType, "knowledge_document") {
			existing[strings.TrimSpace(object.ID)] = struct{}{}
		}
	}
	if !baseExists {
		if err := p.sink.PublishKnowledgeBaseMetadata(ctx, knowledgeBaseOntologyInitializationEventForTenant(base, binding.TenantID)); err != nil {
			return fmt.Errorf("project knowledge base %s ontology catalog metadata: %w", base.ID, err)
		}
	}
	for _, document := range documents.Items {
		if document.KnowledgeBaseID != base.ID {
			return fmt.Errorf("knowledge document %s does not belong to knowledge base %s", document.ID, base.ID)
		}
		if _, ok := existing[strings.TrimSpace(string(document.ID))]; ok {
			continue
		}
		event := knowledgeDocumentOntologyInitializationEventForTenant(base, document, binding.TenantID)
		if err := p.sink.PublishKnowledgeDocumentMetadata(ctx, event); err != nil {
			return fmt.Errorf("project knowledge document %s ontology catalog metadata: %w", document.ID, err)
		}
		existing[strings.TrimSpace(string(document.ID))] = struct{}{}
	}
	return nil
}

func (p productionKnowledgeOntologyCatalogProjector) catalogObjects(ctx context.Context, tenantID string, baseID knowledgecore.KnowledgeBaseID, documents []knowledgecore.KnowledgeDocument) ([]contextgraph.Object, error) {
	if reader, ok := p.graph.(interface {
		ObjectsByID(context.Context, string, []string) ([]contextgraph.Object, error)
	}); ok {
		ids := []string{string(baseID)}
		for _, document := range documents {
			ids = append(ids, string(document.ID))
		}
		return reader.ObjectsByID(ctx, tenantID, ids)
	}
	objects, _, _, _, err := p.graph.Snapshot(ctx, tenantID)
	return objects, err
}

package knowledgeadmin

import (
	"context"
	"fmt"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/graphprotocol"
)

type OntologyDocumentMetadataSink struct {
	Runner *ontology.MappingRuleProjectionRunner
}

func (s OntologyDocumentMetadataSink) PublishKnowledgeDocumentMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	if s.Runner == nil {
		return fmt.Errorf("%w: ontology metadata runner is required", ErrServiceDependency)
	}
	_, err := s.Runner.ApplyEvent(ctx, event)
	return err
}

func (s OntologyDocumentMetadataSink) PublishKnowledgeDocumentStatus(ctx context.Context, event graphprotocol.DomainEvent) error {
	return s.PublishKnowledgeDocumentMetadata(ctx, event)
}

func (s OntologyDocumentMetadataSink) PublishKnowledgeChunkMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	return s.PublishKnowledgeDocumentMetadata(ctx, event)
}

func (s OntologyDocumentMetadataSink) PublishKnowledgePageMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	return s.PublishKnowledgeDocumentMetadata(ctx, event)
}

func (s OntologyDocumentMetadataSink) PublishKnowledgeAnchorMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	return s.PublishKnowledgeDocumentMetadata(ctx, event)
}

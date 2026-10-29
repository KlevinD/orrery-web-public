package knowledgeontology

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

type GraphWriter interface {
	SaveEvidence(ctx context.Context, evidence contextgraph.Evidence) error
	SaveLink(ctx context.Context, link contextgraph.Link) error
	DeleteLink(ctx context.Context, tenantID, linkID, sourceStreamID string) error
	DeleteEvidence(ctx context.Context, tenantID, evidenceID string, sourceRef contextgraph.Ref) error
}

type LinkTypeValidator interface {
	ValidateKnowledgeLink(ctx context.Context, tenantID, linkType, fromObjectID, toObjectID string) error
}

type resolvedLinkTypeValidator interface {
	ValidateKnowledgeLinkTypes(ctx context.Context, tenantID, linkType, fromObjectType, toObjectType string) error
}

type GraphApplier struct {
	writer    GraphWriter
	validator LinkTypeValidator
}

func NewGraphApplier(writer GraphWriter, validator LinkTypeValidator) *GraphApplier {
	return &GraphApplier{writer: writer, validator: validator}
}

func (a *GraphApplier) ApplyProjection(ctx context.Context, binding Binding, projection Projection) error {
	if a == nil || a.writer == nil || a.validator == nil {
		return fmt.Errorf("knowledge graph writer and link validator are required")
	}
	if projection.TenantID != binding.TenantID || projection.KnowledgeBaseID != binding.KnowledgeBaseID {
		return fmt.Errorf("projection does not match binding")
	}
	sourceStreamID := "knowledge_revision:" + projection.DocumentID
	desiredLinks := make(map[string]struct{})
	desiredEvidence := make(map[string]struct{})
	for _, subject := range projection.Subjects {
		if subject.Resolution != ResolutionResolved || subject.Status == AssertionUnresolved || strings.TrimSpace(subject.ObjectID) == "" {
			continue
		}
		if strings.TrimSpace(projection.KnowledgeDocumentObjectID) == "" {
			return fmt.Errorf("knowledge document %s is not projected into the ontology graph", projection.DocumentID)
		}
		linkType, ok := LinkTypeForMentionedObject(subject.Reference.Kind)
		if !ok {
			continue
		}
		if err := a.validateLink(ctx, projection.TenantID, linkType,
			"knowledge_document", projection.KnowledgeDocumentObjectID,
			subject.Reference.Kind, subject.ObjectID); err != nil {
			return err
		}
		identity := strings.Join([]string{
			projection.TenantID, projection.KnowledgeBaseID, projection.DocumentID,
			linkType, subject.ObjectID,
		}, "\x1f")
		evidenceID := stableID("knowledge-mention-evidence", strings.Join([]string{
			identity, fmt.Sprintf("%d", projection.Revision), subject.Evidence.Anchor,
		}, "\x1f"))
		linkID := stableID("knowledge-mention-link", identity)
		observedAt := projectionObservedAt(subject.Evidence.ObservedAt)
		if err := a.writer.SaveEvidence(ctx, contextgraph.Evidence{
			ID: evidenceID, TenantID: projection.TenantID,
			SourceRef:       knowledgeRevisionRef(projection),
			SourceAccessRef: binding.SourceAccessRef,
			Summary:         fmt.Sprintf("Knowledge document explicitly mentions %q as %s.", subject.Surface, subject.Reference.Kind),
			Confidence:      subject.Confidence, ObservedAt: observedAt,
		}); err != nil {
			return err
		}
		desiredEvidence[evidenceID] = struct{}{}
		if err := a.writer.SaveLink(ctx, contextgraph.Link{
			ID: linkID, TenantID: projection.TenantID, LinkType: linkType,
			FromObjectID: projection.KnowledgeDocumentObjectID, ToObjectID: subject.ObjectID,
			Properties: map[string]any{
				"knowledge_base_id":    projection.KnowledgeBaseID,
				"provider_document_id": projection.DocumentID,
				"revision":             projection.Revision,
				"content_hash":         projection.ContentHash,
				"surface":              subject.Surface,
				"anchor":               subject.Evidence.Anchor,
				"extraction_method":    subject.Evidence.ExtractionMethod,
			},
			Confidence: subject.Confidence, Status: "confirmed",
			Provenance:      knowledgeRevisionRef(projection),
			EvidenceRefs:    []contextgraph.Ref{{Kind: "evidence", ID: evidenceID}},
			SourceAccessRef: binding.SourceAccessRef,
			SourceStreamID:  sourceStreamID,
			SourceVersion:   int64(projection.Revision), UpdatedAt: observedAt,
		}); err != nil {
			return err
		}
		desiredLinks[linkID] = struct{}{}
	}
	for _, assertion := range projection.Assertions {
		if assertion.Status != AssertionConfirmed {
			continue
		}
		if err := a.validateLink(ctx, projection.TenantID, assertion.LinkType,
			assertion.From.Kind, assertion.FromObjectID, assertion.To.Kind, assertion.ToObjectID); err != nil {
			return err
		}
		evidenceID := stableID("knowledge-evidence", strings.Join([]string{
			projection.TenantID, projection.KnowledgeBaseID, projection.DocumentID,
			fmt.Sprintf("%d", projection.Revision), assertion.Evidence.Anchor,
		}, "\x1f"))
		observedAt := projectionObservedAt(assertion.Evidence.ObservedAt)
		evidence := contextgraph.Evidence{
			ID: evidenceID, TenantID: projection.TenantID,
			SourceRef:       knowledgeRevisionRef(projection),
			SourceAccessRef: binding.SourceAccessRef,
			Summary:         fmt.Sprintf("Knowledge assertion %s from %s revision %d at %s", assertion.LinkType, projection.DocumentID, projection.Revision, assertion.Evidence.Anchor),
			Confidence:      1, ObservedAt: observedAt,
		}
		if err := a.writer.SaveEvidence(ctx, evidence); err != nil {
			return err
		}
		desiredEvidence[evidenceID] = struct{}{}
		linkID := stableID("knowledge-link", strings.Join([]string{
			projection.TenantID, assertion.LinkType, assertion.FromObjectID,
			assertion.ToObjectID, projection.DocumentID, fmt.Sprintf("%d", projection.Revision),
		}, "\x1f"))
		link := contextgraph.Link{
			ID: linkID, TenantID: projection.TenantID, LinkType: assertion.LinkType,
			FromObjectID: assertion.FromObjectID, ToObjectID: assertion.ToObjectID,
			Properties: map[string]any{
				"knowledge_base_id": projection.KnowledgeBaseID,
				"document_id":       projection.DocumentID,
				"revision":          projection.Revision,
				"content_hash":      projection.ContentHash,
				"anchor":            assertion.Evidence.Anchor,
				"extraction_method": assertion.Evidence.ExtractionMethod,
			},
			Confidence: 1, Status: "confirmed",
			Provenance:      knowledgeRevisionRef(projection),
			EvidenceRefs:    []contextgraph.Ref{{Kind: "evidence", ID: evidenceID}},
			SourceAccessRef: binding.SourceAccessRef,
			SourceStreamID:  sourceStreamID,
			SourceVersion:   int64(projection.Revision), UpdatedAt: observedAt,
		}
		if err := a.writer.SaveLink(ctx, link); err != nil {
			return err
		}
		desiredLinks[linkID] = struct{}{}
	}
	for _, link := range projection.ExistingLinks {
		if link.TenantID != projection.TenantID || link.SourceStreamID != sourceStreamID {
			continue
		}
		if _, keep := desiredLinks[link.ID]; keep {
			continue
		}
		if err := a.writer.DeleteLink(ctx, projection.TenantID, link.ID, sourceStreamID); err != nil {
			return err
		}
	}
	for _, evidence := range projection.ExistingEvidence {
		if !knowledgeEvidenceBelongsToDocument(evidence, projection.TenantID, projection.DocumentID) {
			continue
		}
		if _, keep := desiredEvidence[evidence.ID]; keep {
			continue
		}
		if err := a.writer.DeleteEvidence(ctx, projection.TenantID, evidence.ID, evidence.SourceRef); err != nil {
			return err
		}
	}
	return nil
}

func (a *GraphApplier) DeleteDocumentProjection(ctx context.Context, binding Binding, documentID string, links []contextgraph.Link, evidence []contextgraph.Evidence) error {
	if a == nil || a.writer == nil {
		return fmt.Errorf("knowledge graph writer is required")
	}
	documentID = strings.TrimSpace(documentID)
	if strings.TrimSpace(binding.TenantID) == "" || documentID == "" {
		return fmt.Errorf("knowledge tenant and document are required")
	}
	sourceStreamID := "knowledge_revision:" + documentID
	for _, link := range links {
		if link.TenantID != binding.TenantID || link.SourceStreamID != sourceStreamID {
			continue
		}
		if err := a.writer.DeleteLink(ctx, binding.TenantID, link.ID, sourceStreamID); err != nil {
			return err
		}
	}
	for _, item := range evidence {
		if !knowledgeEvidenceBelongsToDocument(item, binding.TenantID, documentID) {
			continue
		}
		if err := a.writer.DeleteEvidence(ctx, binding.TenantID, item.ID, item.SourceRef); err != nil {
			return err
		}
	}
	return nil
}

func knowledgeEvidenceBelongsToDocument(evidence contextgraph.Evidence, tenantID, documentID string) bool {
	return evidence.TenantID == tenantID && evidence.SourceRef.Kind == "knowledge_revision" &&
		strings.HasPrefix(evidence.SourceRef.ID, strings.TrimSpace(documentID)+"@")
}

func (a *GraphApplier) validateLink(ctx context.Context, tenantID, linkType, fromType, fromID, toType, toID string) error {
	if validator, ok := a.validator.(resolvedLinkTypeValidator); ok {
		return validator.ValidateKnowledgeLinkTypes(ctx, tenantID, linkType, fromType, toType)
	}
	return a.validator.ValidateKnowledgeLink(ctx, tenantID, linkType, fromID, toID)
}

func knowledgeRevisionRef(projection Projection) contextgraph.Ref {
	return contextgraph.Ref{Kind: "knowledge_revision", ID: fmt.Sprintf("%s@%d", projection.DocumentID, projection.Revision)}
}

func projectionObservedAt(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func stableID(prefix, identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("%s:%x", prefix, digest[:])
}

package memoryontology

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	memcore "orrery_v2/internal/memory/core"
)

type GraphApplier struct {
	graph    GraphStore
	registry Registry
	now      func() time.Time
}

func NewGraphApplier(graph GraphStore, registry Registry, now func() time.Time) *GraphApplier {
	return &GraphApplier{graph: graph, registry: registry, now: now}
}

func (a *GraphApplier) Apply(ctx context.Context, tenantID, contentHash string, record memcore.MemoryRecord, projections []MentionProjection, objects []contextgraph.Object, existingLinks []contextgraph.Link) (int, error) {
	if a == nil || a.graph == nil || a.registry == nil {
		return 0, fmt.Errorf("memory ontology graph and registry are required")
	}
	memoryObject, ok := findObject(objects, record.ID, "memory_record")
	if !ok {
		return 0, fmt.Errorf("memory ontology source object %s is not projected", record.ID)
	}
	observedAt := record.UpdatedAt.UTC()
	if observedAt.IsZero() {
		observedAt = a.currentTime()
	}
	sourceVersion := observedAt.UnixNano()
	sourceStreamID := "memory_ontology:" + record.ID
	desiredLinks := make(map[string]struct{})
	linked := 0
	for index := range projections {
		projection := &projections[index]
		if projection.Resolution != ResolutionResolved {
			continue
		}
		target, ok := findObject(objects, projection.ResolvedObjectID, projection.Mention.ObjectType)
		if !ok {
			return linked, fmt.Errorf("resolved memory ontology target %s is missing", projection.ResolvedObjectID)
		}
		if err := a.validateLinkType(ctx, tenantID, projection.LinkType, memoryObject, target); err != nil {
			return linked, err
		}
		if err := a.graph.EnsureObjectReference(ctx, target); err != nil {
			return linked, fmt.Errorf("materialize memory ontology target reference %s: %w", target.ID, err)
		}
		identity := strings.Join([]string{tenantID, record.ID, projection.LinkType, target.ID}, "\x1f")
		projection.EvidenceID = stableID("memory-entity-evidence", identity)
		projection.LinkID = stableID("memory-entity-link", identity)
		if _, duplicate := desiredLinks[projection.LinkID]; duplicate {
			continue
		}
		desiredLinks[projection.LinkID] = struct{}{}
		evidence := contextgraph.Evidence{
			ID: projection.EvidenceID, TenantID: tenantID,
			SourceRef:       contextgraph.Ref{Kind: "memory_record", ID: record.ID},
			SourceAccessRef: memoryObject.OwnerAccessRef,
			AccessState:     memoryObject.AccessState,
			Summary:         fmt.Sprintf("Memory explicitly mentions %q as %s.", projection.Mention.Surface, projection.Mention.ObjectType),
			Confidence:      projection.Mention.Confidence,
			ObservedAt:      observedAt,
		}
		if err := a.graph.SaveEvidence(ctx, evidence); err != nil {
			return linked, err
		}
		link := contextgraph.Link{
			ID: projection.LinkID, TenantID: tenantID, LinkType: projection.LinkType,
			FromObjectID: record.ID, ToObjectID: target.ID,
			Properties: map[string]any{
				"surface":           projection.Mention.Surface,
				"object_type":       projection.Mention.ObjectType,
				"object_hint":       projection.Mention.ObjectHint,
				"anchor":            projection.Mention.Anchor,
				"content_hash":      contentHash,
				"extractor_version": ExtractorVersion,
				"extraction_method": "llm_entity_extraction_exact_resolution",
			},
			Confidence:      projection.Mention.Confidence,
			Status:          "confirmed",
			Provenance:      contextgraph.Ref{Kind: "memory_record", ID: record.ID},
			EvidenceRefs:    []contextgraph.Ref{{Kind: "evidence", ID: projection.EvidenceID}},
			SourceAccessRef: memoryObject.OwnerAccessRef,
			AccessState:     memoryObject.AccessState,
			SourceStreamID:  sourceStreamID,
			SourceVersion:   sourceVersion,
			UpdatedAt:       observedAt,
		}
		if err := a.graph.SaveLink(ctx, link); err != nil {
			return linked, err
		}
		linked++
	}
	for _, link := range existingLinks {
		if link.TenantID != tenantID || link.FromObjectID != record.ID || link.SourceStreamID != sourceStreamID {
			continue
		}
		if _, keep := desiredLinks[link.ID]; keep {
			continue
		}
		if err := a.graph.DeleteLink(ctx, tenantID, link.ID, sourceStreamID); err != nil {
			return linked, err
		}
	}
	return linked, nil
}

func (a *GraphApplier) validateLinkType(ctx context.Context, tenantID, linkTypeID string, from, to contextgraph.Object) error {
	linkType, err := a.registry.GetLinkType(ctx, linkTypeID)
	if err != nil {
		return err
	}
	if linkType.TenantScope == ontology.TenantScopeTenant && strings.TrimSpace(linkType.TenantID) != "" && strings.TrimSpace(linkType.TenantID) != strings.TrimSpace(tenantID) {
		return fmt.Errorf("ontology link type %s is not visible to tenant %s", linkType.ID, tenantID)
	}
	if !strings.EqualFold(from.ObjectType, linkType.FromObjectType) || !strings.EqualFold(to.ObjectType, linkType.ToObjectType) {
		return fmt.Errorf("ontology link %s requires %s -> %s, got %s -> %s", linkType.ID, linkType.FromObjectType, linkType.ToObjectType, from.ObjectType, to.ObjectType)
	}
	if !linkType.EvidenceRequired {
		return fmt.Errorf("memory ontology link %s must require evidence", linkType.ID)
	}
	return nil
}

func findObject(objects []contextgraph.Object, id, objectType string) (contextgraph.Object, bool) {
	for _, object := range objects {
		if object.ID == id && strings.EqualFold(object.ObjectType, objectType) {
			return object, true
		}
	}
	return contextgraph.Object{}, false
}

func stableID(prefix, identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("%s:%x", prefix, digest[:])
}

func (a *GraphApplier) currentTime() time.Time {
	if a.now != nil {
		return a.now().UTC()
	}
	return time.Now().UTC()
}

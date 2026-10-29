package main

import (
	"context"
	"fmt"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/platform/db"
)

type productionMemoryCitationEvidenceResolver struct {
	graph *contextgraph.PlatformStore
}

func newProductionMemoryCitationEvidenceResolver(core *db.Store) memoryadmin.CitationEvidenceResolver {
	if core == nil {
		return nil
	}
	return productionMemoryCitationEvidenceResolver{graph: contextgraph.NewPlatformStore(core)}
}

func (r productionMemoryCitationEvidenceResolver) ResolveCitationEvidence(ctx context.Context, query memoryadmin.CitationEvidenceQuery) ([]memoryadmin.CitationEvidenceDTO, error) {
	if r.graph == nil {
		return nil, fmt.Errorf("%w: context graph store is required", memoryadmin.ErrServiceDependency)
	}
	_, _, facts, _, err := r.graph.Snapshot(ctx, query.TenantID)
	if err != nil {
		return nil, err
	}
	factsBySubject := map[string]map[string]any{}
	for _, fact := range facts {
		key := fact.Subject.Kind + ":" + fact.Subject.ID
		if factsBySubject[key] == nil {
			factsBySubject[key] = map[string]any{}
		}
		factsBySubject[key][fact.Predicate] = fact.Value
	}
	out := make([]memoryadmin.CitationEvidenceDTO, 0, len(query.Refs))
	for _, ref := range query.Refs {
		kind, id, ok := citationEvidenceRef(ref)
		if !ok {
			continue
		}
		out = append(out, citationEvidenceDTO(kind, id, factsBySubject[kind+":"+id]))
	}
	return out, nil
}

func citationEvidenceRef(ref string) (string, string, bool) {
	for _, prefix := range []struct {
		value string
		kind  string
	}{
		{value: "knowledge_document:", kind: "knowledge_document"},
		{value: "knowledge_chunk:", kind: "knowledge_chunk"},
		{value: "knowledge_anchor:", kind: "knowledge_anchor"},
	} {
		if len(ref) > len(prefix.value) && ref[:len(prefix.value)] == prefix.value {
			return prefix.kind, ref[len(prefix.value):], true
		}
	}
	return "", "", false
}

func citationEvidenceDTO(kind string, id string, facts map[string]any) memoryadmin.CitationEvidenceDTO {
	dto := memoryadmin.CitationEvidenceDTO{Kind: kind, ID: id}
	if facts == nil {
		return dto
	}
	dto.Title = stringFact(facts, "title")
	dto.KnowledgeBaseID = stringFact(facts, "knowledge_base_id")
	dto.DocumentID = stringFact(facts, "document_id")
	dto.PageID = stringFact(facts, "page_id")
	dto.PageNumber = intFact(facts, "page_number")
	dto.Provider = stringFact(facts, "provider")
	dto.ProviderDocumentID = stringFact(facts, "provider_document_id")
	dto.ProviderNodeID = stringFact(facts, "provider_node_id")
	dto.HeadingPath = stringSliceFact(facts, "heading_path")
	dto.StructurePath = stringSliceFact(facts, "structure_path")
	dto.BBox = mapFact(facts, "bbox")
	dto.ImageRef = stringFact(facts, "image_ref")
	dto.SourceURI = stringFact(facts, "source_uri")
	dto.ContentHash = stringFact(facts, "content_hash")
	dto.SnippetHash = stringFact(facts, "snippet_hash")
	return dto
}

func stringFact(facts map[string]any, key string) string {
	value, _ := facts[key].(string)
	return value
}

func intFact(facts map[string]any, key string) int {
	switch value := facts[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func stringSliceFact(facts map[string]any, key string) []string {
	switch values := facts[key].(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, item := range values {
			if text, ok := item.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func mapFact(facts map[string]any, key string) map[string]any {
	value, ok := facts[key].(map[string]any)
	if !ok || len(value) == 0 {
		return nil
	}
	clone := make(map[string]any, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

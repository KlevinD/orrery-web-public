package knowledgeadmin

import (
	"context"
	"errors"
	"sort"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/retrievalevidence"
)

// Search remains a material search. Catalog metadata can identify a source,
// but cannot prove a remote snippet's revision or the truth of its assertions.
func (s *SearchService) annotateSearchEvidence(ctx context.Context, viewer ViewerRef, bases []core.KnowledgeBase, results []KnowledgeSearchResultDTO) ([]KnowledgeSearchResultDTO, error) {
	allowed := map[string]core.KnowledgeBase{}
	access := map[string]string{}
	used := map[string]bool{}
	for _, result := range results {
		used[result.KnowledgeBaseID] = true
	}
	for _, expected := range bases {
		if !used[string(expected.ID)] {
			continue
		}
		current, err := s.repo.GetKnowledgeBase(ctx, expected.ID)
		if err != nil {
			return nil, err
		}
		if retrievalevidence.Fingerprint(current) != retrievalevidence.Fingerprint(expected) || !s.policy.CanRead(ctx, viewer.AccessSubject(), current) {
			return nil, retrievalevidence.ErrInputChanged
		}
		bindings, err := s.repo.ListBindings(ctx, core.BindingQuery{KnowledgeBaseID: current.ID})
		if err != nil {
			return nil, err
		}
		sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
		allowed[string(current.ID)] = current
		access[string(current.ID)] = retrievalevidence.Fingerprint(struct {
			Base     core.KnowledgeBase
			Bindings []core.KnowledgeBinding
			Subject  AccessSubject
		}{current, bindings, viewer.AccessSubject()})
	}
	// PageIndex may return its document ID instead of Orrery's. Mapping is
	// bounded; an unmapped legacy document stays explicitly unversioned.
	catalogs := map[string][]core.KnowledgeDocument{}
	documents := map[string]*core.KnowledgeDocument{}
	out := make([]KnowledgeSearchResultDTO, 0, len(results))
	for _, result := range results {
		base, ok := allowed[result.KnowledgeBaseID]
		if !ok {
			continue
		}
		key := retrievalevidence.Fingerprint([]string{result.KnowledgeBaseID, result.DocumentID})
		doc, resolved := documents[key]
		if !resolved {
			current, err := s.repo.GetDocument(ctx, core.KnowledgeDocumentID(result.DocumentID))
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return nil, err
			}
			if err == nil {
				if current.KnowledgeBaseID != base.ID {
					continue
				}
				doc = &current
			} else if base.EffectiveProvider() == core.ProviderPageIndex {
				catalog, loaded := catalogs[result.KnowledgeBaseID]
				if !loaded {
					page, err := s.repo.ListDocuments(ctx, core.DocumentQuery{KnowledgeBaseID: base.ID, Limit: 501})
					if err != nil {
						return nil, err
					}
					catalog = page.Items
					if len(catalog) > 500 {
						catalog = catalog[:500]
					}
					catalogs[result.KnowledgeBaseID] = catalog
				}
				for _, candidate := range catalog {
					if candidate.KnowledgeBaseID == base.ID && candidate.ProviderDocumentID == result.DocumentID {
						if doc != nil {
							return nil, retrievalevidence.ErrInputChanged
						}
						value := candidate
						doc = &value
					}
				}
			}
			documents[key] = doc
		}
		if doc != nil && doc.Status == core.DocumentDeleting {
			continue
		}
		item := retrievalevidence.Item{Purpose: retrievalevidence.PurposeMaterial, TruthState: retrievalevidence.TruthUnknown,
			Source:      retrievalevidence.Source{Kind: "knowledge_document", ID: result.DocumentID, ContainerID: result.KnowledgeBaseID},
			SourceState: "unversioned", AccessHash: access[result.KnowledgeBaseID], CandidateHash: searchCandidateHash(result)}
		if doc != nil {
			item.Source.ID, item.Source.ProviderID = string(doc.ID), doc.ProviderDocumentID
			item.Source.Version = retrievalevidence.Fingerprint(*doc)
			item.Source.ContentHash, item.SourceState = doc.Checksum, "catalog_verified"
		}
		item = retrievalevidence.Seal(item, s.now())
		result.Evidence = &item
		out = append(out, result)
	}
	return out, nil
}

func searchCandidateHash(result KnowledgeSearchResultDTO) string {
	result.Score, result.Evidence = 0, nil
	return retrievalevidence.Fingerprint(result)
}

// A reranker is allowed to select and score offered items. It cannot replace
// their content, identities or host evidence, including by mutating input.
func restoreSearchCandidates(offered, ranked []KnowledgeSearchResultDTO) ([]KnowledgeSearchResultDTO, error) {
	byHash := map[string]KnowledgeSearchResultDTO{}
	for _, item := range offered {
		byHash[searchCandidateHash(item)] = item
	}
	out := make([]KnowledgeSearchResultDTO, 0, len(ranked))
	seen := map[string]bool{}
	for _, item := range ranked {
		hash := searchCandidateHash(item)
		original, ok := byHash[hash]
		if !ok {
			return nil, retrievalevidence.ErrInputChanged
		}
		if seen[hash] {
			continue
		}
		seen[hash] = true
		original.Score = item.Score
		out = append(out, original)
	}
	return out, nil
}

// Existing search supports completed provider results at the overall deadline.
// Keep that behavior while giving final authorization a separate short budget.
func searchValidationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
	}
	return context.WithCancel(ctx)
}

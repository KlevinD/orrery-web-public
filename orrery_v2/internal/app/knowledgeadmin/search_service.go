package knowledgeadmin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/ingest"
	"orrery_v2/internal/knowledge/provider"
	localprovider "orrery_v2/internal/knowledge/provider/local"
	"orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/knowledge/vector"
	"orrery_v2/internal/retrievalevidence"
)

type SearchService struct {
	now       func() time.Time
	repo      store.Repository
	vector    vector.Store
	embedding ingest.EmbeddingProvider
	reranker  SearchReranker
	policy    *AccessPolicy
	providers provider.Registry
}

func NewSearchService(repo store.Repository, vectorStore vector.Store, embedding ingest.EmbeddingProvider) *SearchService {
	return &SearchService{repo: repo, vector: vectorStore, embedding: embedding, reranker: scoreReranker{}, policy: NewAccessPolicy(repo), now: time.Now}
}

func (s *SearchService) WithClock(now func() time.Time) *SearchService {
	if now != nil {
		s.now = now
	}
	return s
}

func (s *SearchService) WithReranker(reranker SearchReranker) *SearchService {
	if reranker != nil {
		s.reranker = reranker
	}
	return s
}

func (s *SearchService) WithProviderRegistry(registry provider.Registry) *SearchService {
	s.providers = registry
	return s
}

func (s *SearchService) SearchKnowledge(ctx context.Context, viewer ViewerRef, cmd SearchKnowledgeCommand) (KnowledgeSearchPageDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeRead); err != nil {
		return KnowledgeSearchPageDTO{}, err
	}
	if s == nil || s.repo == nil {
		return KnowledgeSearchPageDTO{}, ErrServiceDependency
	}
	retrievalMode, err := provider.NormalizeRetrievalMode(string(cmd.RetrievalMode))
	if err != nil {
		return KnowledgeSearchPageDTO{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	ctx = withProviderRequestMetadata(ctx, viewer)
	bases, err := s.allowedBases(ctx, viewer, cmd)
	if err != nil {
		return KnowledgeSearchPageDTO{}, err
	}
	if len(bases) == 0 {
		return KnowledgeSearchPageDTO{Results: []KnowledgeSearchResultDTO{}}, nil
	}
	limit := cmd.Limit
	if limit <= 0 {
		limit = 8
	}
	results, executions, err := s.searchProviders(ctx, bases, cmd.Query, providerCandidateLimit(limit), retrievalMode)
	if err != nil {
		return KnowledgeSearchPageDTO{}, err
	}
	validationCtx, cancelValidation := searchValidationContext(ctx)
	results, err = s.annotateSearchEvidence(validationCtx, viewer, bases, results)
	cancelValidation()
	if err != nil {
		return KnowledgeSearchPageDTO{}, err
	}
	offered := cloneSearchResults(results)
	rankingCandidates := cloneSearchResults(results)
	for i := range rankingCandidates {
		// Check time is host observation metadata, not a new candidate version.
		// Keep shadow reranker cache keys stable for identical authorized inputs.
		if rankingCandidates[i].Evidence != nil {
			rankingCandidates[i].Evidence.CheckedAt = time.Time{}
		}
	}
	reranker := s.reranker
	if reranker == nil {
		reranker = scoreReranker{}
	}
	reranked, err := rerankKnowledgeResults(ctx, reranker, cmd.Query, rankingCandidates, limit)
	if err != nil {
		return KnowledgeSearchPageDTO{}, err
	}
	reranked, err = restoreSearchCandidates(offered, reranked)
	if err != nil {
		return KnowledgeSearchPageDTO{}, err
	}
	validationCtx, cancelValidation = searchValidationContext(ctx)
	reranked = limitSearchResults(reranked, limit)
	checked, err := s.annotateSearchEvidence(validationCtx, viewer, bases, reranked)
	cancelValidation()
	if err != nil {
		return KnowledgeSearchPageDTO{}, err
	}
	if len(checked) != len(reranked) {
		return KnowledgeSearchPageDTO{}, retrievalevidence.ErrInputChanged
	}
	for i := range checked {
		if checked[i].Evidence.InputHash != reranked[i].Evidence.InputHash {
			return KnowledgeSearchPageDTO{}, retrievalevidence.ErrInputChanged
		}
	}
	return KnowledgeSearchPageDTO{Results: checked, Executions: mapProviderSearchExecutions(executions)}, nil
}

func rerankKnowledgeResults(ctx context.Context, reranker SearchReranker, query string, results []KnowledgeSearchResultDTO, limit int) ([]KnowledgeSearchResultDTO, error) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return (scoreReranker{}).Rerank(context.Background(), query, results, limit)
	}
	return reranker.Rerank(ctx, query, results, limit)
}

func (s *SearchService) searchProviders(ctx context.Context, bases []core.KnowledgeBase, query string, candidateLimit int, retrievalMode provider.RetrievalMode) ([]KnowledgeSearchResultDTO, []provider.SearchExecution, error) {
	grouped := map[core.KnowledgeProvider][]core.KnowledgeBase{}
	for _, base := range bases {
		grouped[base.EffectiveProvider()] = append(grouped[base.EffectiveProvider()], base)
	}
	resolved := make(map[core.KnowledgeProvider]provider.Provider, len(grouped))
	for providerName := range grouped {
		kp := s.provider(providerName)
		if kp == nil {
			return nil, nil, ErrServiceDependency
		}
		resolved[providerName] = kp
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	results := make([]KnowledgeSearchResultDTO, 0)
	executions := make([]provider.SearchExecution, 0)
	var firstErr error
	succeeded := 0
	for providerName, group := range grouped {
		providerName := providerName
		group := append([]core.KnowledgeBase(nil), group...)
		kp := resolved[providerName]
		wg.Add(1)
		go func() {
			defer wg.Done()
			page, err := kp.Search(ctx, provider.SearchRequest{Query: query, Bases: group, Limit: candidateLimit, RetrievalMode: retrievalMode})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			succeeded++
			results = append(results, mapProviderSearchResults(page.Results)...)
			executions = append(executions, page.Executions...)
		}()
	}
	wg.Wait()
	if firstErr != nil && succeeded == 0 {
		return nil, nil, firstErr
	}
	return results, executions, nil
}

func providerCandidateLimit(finalLimit int) int {
	if finalLimit <= 0 {
		finalLimit = 8
	}
	candidateLimit := finalLimit * 4
	if candidateLimit < 20 {
		candidateLimit = 20
	}
	if candidateLimit > 50 && finalLimit <= 50 {
		return 50
	}
	if candidateLimit < finalLimit {
		return finalLimit
	}
	return candidateLimit
}

func (s *SearchService) allowedBases(ctx context.Context, viewer ViewerRef, cmd SearchKnowledgeCommand) ([]core.KnowledgeBase, error) {
	page, err := s.repo.ListKnowledgeBases(ctx, core.KnowledgeBaseQuery{Status: core.KnowledgeActive})
	if err != nil {
		return nil, err
	}
	idFilter := make(map[core.KnowledgeBaseID]bool, len(cmd.KnowledgeBaseIDs))
	for _, id := range cmd.KnowledgeBaseIDs {
		if id != "" {
			idFilter[core.KnowledgeBaseID(id)] = true
		}
	}
	out := make([]core.KnowledgeBase, 0, len(page.Items))
	for _, base := range page.Items {
		if len(idFilter) > 0 && !idFilter[base.ID] {
			continue
		}
		if cmd.OwnerFilter.OwnerType != "" && base.OwnerType != core.OwnerType(cmd.OwnerFilter.OwnerType) {
			continue
		}
		if cmd.OwnerFilter.OwnerID != "" && base.OwnerID != cmd.OwnerFilter.OwnerID {
			continue
		}
		if s.policy.CanRead(ctx, viewer.AccessSubject(), base) {
			out = append(out, base)
		}
	}
	return out, nil
}

func (s *SearchService) provider(name core.KnowledgeProvider) provider.Provider {
	if s == nil {
		return nil
	}
	if s.providers != nil {
		if kp := s.providers.Provider(name); kp != nil {
			return kp
		}
	}
	if name == core.ProviderLocal || name == "" {
		return localprovider.New(localprovider.Options{Repository: s.repo, Vector: s.vector, Embedding: s.embedding})
	}
	return nil
}

func mapProviderSearchResults(results []provider.SearchResult) []KnowledgeSearchResultDTO {
	out := make([]KnowledgeSearchResultDTO, 0, len(results))
	for _, result := range results {
		out = append(out, KnowledgeSearchResultDTO{
			KnowledgeBaseID: result.KnowledgeBaseID,
			DocumentID:      result.DocumentID,
			ChunkID:         result.ChunkID,
			Score:           result.Score,
			Snippet:         result.Snippet,
			HeadingPath:     append([]string(nil), result.HeadingPath...),
			DocumentName:    result.DocumentName,
		})
	}
	return out
}

func mapProviderSearchExecutions(executions []provider.SearchExecution) []KnowledgeSearchExecutionDTO {
	out := make([]KnowledgeSearchExecutionDTO, 0, len(executions))
	for _, execution := range executions {
		out = append(out, KnowledgeSearchExecutionDTO{
			Provider: string(execution.Provider), KnowledgeBaseID: execution.KnowledgeBaseID,
			RequestedMode: string(execution.RequestedMode), ActualMode: execution.ActualMode,
			FallbackReason: execution.FallbackReason,
		})
	}
	return out
}

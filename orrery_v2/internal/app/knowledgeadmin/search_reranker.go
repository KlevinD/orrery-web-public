package knowledgeadmin

import (
	"context"
	"orrery_v2/internal/retrievalevidence"
	"sort"
)

type SearchReranker interface {
	Rerank(ctx context.Context, query string, results []KnowledgeSearchResultDTO, limit int) ([]KnowledgeSearchResultDTO, error)
}

type scoreReranker struct{}

func (scoreReranker) Rerank(ctx context.Context, _ string, results []KnowledgeSearchResultDTO, limit int) ([]KnowledgeSearchResultDTO, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := cloneSearchResults(results)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].ChunkID < out[j].ChunkID
		}
		return out[i].Score > out[j].Score
	})
	return limitSearchResults(out, limit), nil
}

func cloneSearchResults(in []KnowledgeSearchResultDTO) []KnowledgeSearchResultDTO {
	if len(in) == 0 {
		return []KnowledgeSearchResultDTO{}
	}
	out := make([]KnowledgeSearchResultDTO, len(in))
	for i, result := range in {
		out[i] = result
		out[i].HeadingPath = append([]string(nil), result.HeadingPath...)
		if result.Evidence != nil {
			value := retrievalevidence.Clone([]retrievalevidence.Item{*result.Evidence})[0]
			out[i].Evidence = &value
		}
	}
	return out
}

func limitSearchResults(results []KnowledgeSearchResultDTO, limit int) []KnowledgeSearchResultDTO {
	if limit > 0 && len(results) > limit {
		return results[:limit]
	}
	return results
}

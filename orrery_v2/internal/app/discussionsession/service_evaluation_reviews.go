package discussionsession

import (
	"context"
	"encoding/json"
	"strings"

	toolstd "orrery_v2/internal/resource/tool/std"
)

func (s *Service) recordEvaluationReviewUsage(ctx context.Context, run RunRecord) error {
	if s == nil || s.evaluationReviews == nil {
		return nil
	}
	tokenConsumed := totalModelUsageTokens(run.Usage)
	if tokenConsumed <= 0 {
		return nil
	}
	for _, reviewID := range evaluationReviewIDsFromToolCalls(run.ToolCalls) {
		if err := s.evaluationReviews.RecordEvaluationReviewUsage(ctx, reviewID, tokenConsumed); err != nil {
			return err
		}
	}
	return nil
}

func evaluationReviewIDsFromToolCalls(calls []ToolCallDTO) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, call := range calls {
		if normalizeToolName(call.Name) != string(toolstd.EvaluationReview) {
			continue
		}
		add(call.Result["review_id"])
		var reviews []struct {
			ReviewID string `json:"review_id"`
		}
		if raw := strings.TrimSpace(call.Result["reviews"]); raw != "" && json.Unmarshal([]byte(raw), &reviews) == nil {
			for _, review := range reviews {
				add(review.ReviewID)
			}
		}
	}
	return ids
}

func totalModelUsageTokens(usage ModelUsageMap) int64 {
	var total int64
	for _, item := range usage {
		if item.TotalTokens > 0 {
			total += item.TotalTokens
			continue
		}
		total += item.InputTokens + item.OutputTokens + item.CacheReadTokens + item.CacheWriteTokens
	}
	return total
}

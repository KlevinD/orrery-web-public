package operations

import (
	"context"
	"fmt"
	"strings"
	"time"

	toolstd "orrery_v2/internal/resource/tool/std"
)

const (
	EvaluationToolModeAllChanged = "all_changed"
	EvaluationToolModeAgent      = "agent"
)

type EvaluationToolStore interface {
	ListEvaluationItems(ctx context.Context, query EvaluationQuery) (EvaluationPageDTO, error)
	UpsertEvaluationReview(ctx context.Context, review EvaluationReviewDTO) error
}

type EvaluationToolExecutorOptions struct {
	Store EvaluationToolStore
	Now   func() time.Time
}

type EvaluationToolExecutor struct {
	store EvaluationToolStore
	now   func() time.Time
}

type EvaluationToolRequest struct {
	PrincipalID        string
	AgentID            string
	Mode               string
	TargetAgentID      string
	EvaluatorAgentID   string
	Summary            string
	EstimatedHumanDays float64
	WindowDays         int
	IdempotencyKey     string
	ReviewInputs       []EvaluationToolReviewInput
}

type EvaluationToolReviewInput struct {
	AgentID            string
	Summary            string
	EstimatedHumanDays float64
}

type EvaluationToolResult struct {
	Output map[string]any
}

func NewEvaluationToolExecutor(opts EvaluationToolExecutorOptions) *EvaluationToolExecutor {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &EvaluationToolExecutor{store: opts.Store, now: now}
}

func (e *EvaluationToolExecutor) ExecuteEvaluationTool(ctx context.Context, toolID string, req EvaluationToolRequest) (EvaluationToolResult, error) {
	if e == nil || e.store == nil || e.now == nil {
		return EvaluationToolResult{}, ErrServiceDependency
	}
	if strings.TrimSpace(toolID) != string(toolstd.EvaluationReview) {
		return EvaluationToolResult{}, fmt.Errorf("%w: unsupported evaluation tool %q", ErrInvalidRequest, toolID)
	}
	if err := ctx.Err(); err != nil {
		return EvaluationToolResult{}, err
	}
	req = normalizeEvaluationToolRequest(req)
	if req.Mode != EvaluationToolModeAllChanged && req.Mode != EvaluationToolModeAgent {
		return EvaluationToolResult{}, fmt.Errorf("%w: unsupported evaluation mode %q", ErrInvalidRequest, req.Mode)
	}
	if req.Summary == "" {
		return EvaluationToolResult{}, fmt.Errorf("%w: summary is required", ErrInvalidRequest)
	}
	page, err := e.store.ListEvaluationItems(ctx, EvaluationQuery{EntityType: "agent"})
	if err != nil {
		return EvaluationToolResult{}, err
	}
	candidates, skipped := evaluationToolCandidates(page.Items, req)
	if req.Mode == EvaluationToolModeAgent && len(candidates) == 0 {
		return EvaluationToolResult{}, fmt.Errorf("%w: agent_id %q has no evaluation record", ErrInvalidRequest, req.TargetAgentID)
	}

	at := e.now().UTC()
	reviewInputs := evaluationToolReviewInputByAgent(req)
	written := make([]map[string]any, 0, len(candidates))
	for _, item := range candidates {
		input := reviewInputs[item.EntityID]
		review := EvaluationReviewDTO{
			ReviewID:           evaluationToolReviewID(req, item.EntityID, at),
			EntityType:         "agent",
			EntityID:           item.EntityID,
			ScopeKind:          "agent",
			ScopeID:            item.EntityID,
			EvaluatorAgentID:   firstNonBlankString(req.EvaluatorAgentID, req.AgentID, req.PrincipalID),
			WindowDays:         firstPositiveToolInt(req.WindowDays, 7),
			Summary:            firstNonBlankString(input.Summary, req.Summary),
			EstimatedHumanDays: firstPositiveToolFloat(input.EstimatedHumanDays, req.EstimatedHumanDays),
			CreatedAt:          at,
		}
		if err := e.store.UpsertEvaluationReview(ctx, review); err != nil {
			return EvaluationToolResult{}, err
		}
		written = append(written, map[string]any{
			"review_id":            review.ReviewID,
			"agent_id":             item.EntityID,
			"display_name":         item.DisplayName,
			"token_consumed":       item.TokenConsumed,
			"completed_tasks":      item.CompletedTasks,
			"estimated_human_days": review.EstimatedHumanDays,
			"summary":              review.Summary,
		})
	}

	return EvaluationToolResult{Output: map[string]any{
		"review_count":   len(written),
		"skipped_count":  len(skipped),
		"reviews":        written,
		"skipped_agents": skipped,
	}}, nil
}

func normalizeEvaluationToolRequest(req EvaluationToolRequest) EvaluationToolRequest {
	req.PrincipalID = strings.TrimSpace(req.PrincipalID)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.Mode = strings.TrimSpace(req.Mode)
	req.TargetAgentID = strings.TrimSpace(req.TargetAgentID)
	req.EvaluatorAgentID = strings.TrimSpace(req.EvaluatorAgentID)
	req.Summary = strings.TrimSpace(req.Summary)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.WindowDays < 0 {
		req.WindowDays = 0
	}
	if req.EstimatedHumanDays < 0 {
		req.EstimatedHumanDays = 0
	}
	out := make([]EvaluationToolReviewInput, 0, len(req.ReviewInputs))
	for _, input := range req.ReviewInputs {
		input.AgentID = strings.TrimSpace(input.AgentID)
		input.Summary = strings.TrimSpace(input.Summary)
		if input.EstimatedHumanDays < 0 {
			input.EstimatedHumanDays = 0
		}
		if input.AgentID == "" {
			continue
		}
		out = append(out, input)
	}
	req.ReviewInputs = out
	return req
}

func evaluationToolCandidates(items []EvaluationItemDTO, req EvaluationToolRequest) ([]EvaluationItemDTO, []string) {
	candidates := make([]EvaluationItemDTO, 0, len(items))
	skipped := make([]string, 0)
	for _, item := range items {
		if item.EntityType != "agent" || strings.TrimSpace(item.EntityID) == "" {
			continue
		}
		if req.Mode == EvaluationToolModeAgent {
			if item.EntityID == req.TargetAgentID {
				candidates = append(candidates, item)
			}
			continue
		}
		if evaluationItemHasRuntimeWorkSinceLastReview(item) {
			candidates = append(candidates, item)
			continue
		}
		skipped = append(skipped, item.EntityID)
	}
	return candidates, skipped
}

func evaluationItemHasRuntimeWorkSinceLastReview(item EvaluationItemDTO) bool {
	reviewedAt := item.LatestReview.CreatedAt
	for _, task := range item.TaskHistory {
		if task.TokenConsumed <= 0 {
			continue
		}
		if reviewedAt.IsZero() || task.Time.IsZero() || task.Time.After(reviewedAt) {
			return true
		}
	}
	return false
}

func evaluationToolReviewInputByAgent(req EvaluationToolRequest) map[string]EvaluationToolReviewInput {
	out := make(map[string]EvaluationToolReviewInput, len(req.ReviewInputs))
	for _, input := range req.ReviewInputs {
		out[input.AgentID] = input
	}
	return out
}

func evaluationToolReviewID(req EvaluationToolRequest, agentID string, at time.Time) string {
	base := strings.TrimSpace(req.IdempotencyKey)
	if base == "" {
		base = fmt.Sprintf("evaluation-review:%d", at.UnixNano())
	}
	return base + ":" + strings.TrimSpace(agentID)
}

func firstNonBlankString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstPositiveToolInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstPositiveToolFloat(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

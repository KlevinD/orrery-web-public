package operations_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/operations"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestEvaluationToolReviewsSpecificAgent(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := operations.NewMemoryEvaluationProjectionStore()
	if err := store.UpsertEvaluationItems(ctx, []operations.EvaluationItemDTO{evaluationToolItem("profile-agent-1", "档案 Agent", 3200, at.Add(-time.Hour))}); err != nil {
		t.Fatal(err)
	}
	executor := operations.NewEvaluationToolExecutor(operations.EvaluationToolExecutorOptions{
		Store: store,
		Now:   func() time.Time { return at },
	})

	result, err := executor.ExecuteEvaluationTool(ctx, string(toolstd.EvaluationReview), operations.EvaluationToolRequest{
		Mode:               "agent",
		TargetAgentID:      "profile-agent-1",
		EvaluatorAgentID:   "review-agent",
		Summary:            "结论：档案 Agent 近 7 天完成归档审核，交付稳定，建议继续使用。",
		EstimatedHumanDays: 1.8,
		WindowDays:         7,
		IdempotencyKey:     "review-specific-agent",
	})
	if err != nil {
		t.Fatalf("ExecuteEvaluationTool: %v", err)
	}
	if result.Output["review_count"] != 1 {
		t.Fatalf("result = %+v", result.Output)
	}

	page, err := store.ListEvaluationItems(ctx, operations.EvaluationQuery{EntityType: "agent", EntityID: "profile-agent-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %+v", page)
	}
	item := page.Items[0]
	if item.LatestReview.ReviewID != "review-specific-agent:profile-agent-1" ||
		item.LatestReview.EvaluatorAgentID != "review-agent" ||
		item.Investment.EstimatedHumanDays != 1.8 ||
		item.TokenConsumed != 3200 {
		t.Fatalf("reviewed item = %+v", item)
	}
}

func TestEvaluationToolAllChangedReviewsOnlyAgentsWithRuntimeWorkAfterLastReview(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := operations.NewMemoryEvaluationProjectionStore()
	if err := store.UpsertEvaluationItems(ctx, []operations.EvaluationItemDTO{
		evaluationToolItem("profile-agent-1", "档案 Agent", 3200, at.Add(-time.Hour)),
		evaluationToolItem("profile-agent-2", "需求 Agent", 1800, at.Add(-48*time.Hour)),
		evaluationToolItem("profile-agent-3", "风控 Agent", 0, at.Add(-30*time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertEvaluationReview(ctx, operations.EvaluationReviewDTO{
		ReviewID:           "existing-review-1",
		EntityType:         "agent",
		EntityID:           "profile-agent-1",
		Summary:            "旧评价",
		EstimatedHumanDays: 0.8,
		CreatedAt:          at.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertEvaluationReview(ctx, operations.EvaluationReviewDTO{
		ReviewID:           "existing-review-2",
		EntityType:         "agent",
		EntityID:           "profile-agent-2",
		Summary:            "刚评价过",
		EstimatedHumanDays: 0.5,
		CreatedAt:          at.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	executor := operations.NewEvaluationToolExecutor(operations.EvaluationToolExecutorOptions{
		Store: store,
		Now:   func() time.Time { return at },
	})

	result, err := executor.ExecuteEvaluationTool(ctx, string(toolstd.EvaluationReview), operations.EvaluationToolRequest{
		Mode:             "all_changed",
		EvaluatorAgentID: "review-agent",
		Summary:          "结论：本周期有实际任务执行的 Agent 已完成评价。",
		ReviewInputs: []operations.EvaluationToolReviewInput{{
			AgentID:            "profile-agent-1",
			Summary:            "结论：档案 Agent 本周期新增运行记录，产出约 1.6 人天。",
			EstimatedHumanDays: 1.6,
		}},
		WindowDays:     7,
		IdempotencyKey: "review-all-changed",
	})
	if err != nil {
		t.Fatalf("ExecuteEvaluationTool: %v", err)
	}
	if result.Output["review_count"] != 1 || result.Output["skipped_count"] != 2 {
		t.Fatalf("result = %+v", result.Output)
	}

	changed, err := store.ListEvaluationItems(ctx, operations.EvaluationQuery{EntityType: "agent", EntityID: "profile-agent-1"})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Items[0].LatestReview.ReviewID != "review-all-changed:profile-agent-1" || changed.Items[0].Investment.EstimatedHumanDays != 1.6 {
		t.Fatalf("changed item = %+v", changed.Items[0])
	}
	unchanged, err := store.ListEvaluationItems(ctx, operations.EvaluationQuery{EntityType: "agent", EntityID: "profile-agent-2"})
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Items[0].LatestReview.ReviewID != "existing-review-2" {
		t.Fatalf("unchanged item should keep previous review: %+v", unchanged.Items[0])
	}
	zeroToken, err := store.ListEvaluationItems(ctx, operations.EvaluationQuery{EntityType: "agent", EntityID: "profile-agent-3"})
	if err != nil {
		t.Fatal(err)
	}
	if zeroToken.Items[0].LatestReview.ReviewID != "" {
		t.Fatalf("zero-token item should not be reviewed: %+v", zeroToken.Items[0])
	}
}

func TestEvaluationToolStoreUpdatesReviewWithActualEvaluationTokenUsage(t *testing.T) {
	ctx := context.Background()
	store := operations.NewMemoryEvaluationProjectionStore()
	at := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	review := operations.EvaluationReviewDTO{
		ReviewID:           "review-1",
		EntityType:         "agent",
		EntityID:           "profile-agent-1",
		ScopeKind:          "agent",
		ScopeID:            "profile-agent-1",
		EvaluatorAgentID:   "review-agent",
		Summary:            "评估结论明确，建议继续优化低效节点。",
		EstimatedHumanDays: 1.5,
		CreatedAt:          at,
	}
	if err := store.UpsertEvaluationReview(ctx, review); err != nil {
		t.Fatalf("UpsertEvaluationReview: %v", err)
	}

	got, err := store.GetEvaluationReview(ctx, "review-1")
	if err != nil {
		t.Fatalf("GetEvaluationReview: %v", err)
	}
	got.EvaluationTokenConsumed = 1234
	if err := store.UpsertEvaluationReview(ctx, got); err != nil {
		t.Fatalf("UpsertEvaluationReview(update): %v", err)
	}

	page, err := store.ListEvaluationItems(ctx, operations.EvaluationQuery{EntityType: "agent", EntityID: "profile-agent-1"})
	if err != nil {
		t.Fatalf("ListEvaluationItems: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].LatestReview.ReviewID != "review-1" {
		t.Fatalf("items = %+v", page.Items)
	}
	if page.Items[0].LatestReview.EvaluationTokenConsumed != 1234 {
		t.Fatalf("evaluation token = %d", page.Items[0].LatestReview.EvaluationTokenConsumed)
	}
}

func evaluationToolItem(agentID, name string, tokens int64, taskAt time.Time) operations.EvaluationItemDTO {
	return operations.EvaluationItemDTO{
		EntityType:         "agent",
		EntityID:           agentID,
		DisplayName:        name,
		AssignedTasks:      1,
		CompletedTasks:     1,
		TokenConsumed:      tokens,
		QualityScore:       0.9,
		OnTimeRate:         1,
		LastActivityAt:     taskAt,
		TaskHistory:        []operations.EvaluationTaskHistoryDTO{{Goal: "完成档案审核", Responsibility: "核对材料并输出建议", Result: "通过", TokenConsumed: tokens, Time: taskAt}},
		Investment:         operations.EvaluationInvestmentDTO{TokenConsumed: tokens},
		Summary:            "结论：已有真实运行记录。",
		Capabilities:       []operations.EvaluationCapabilityDTO{{Name: "任务完成", Score: 90}},
		CollaborationScore: 0.8,
	}
}

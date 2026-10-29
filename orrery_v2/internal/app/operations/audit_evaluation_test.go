package operations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/testkit"
)

func TestQueryServiceListsAuditLogAndEvaluations(t *testing.T) {
	at := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)
	audit := &fakeAuditStore{events: []operations.AuditEventDTO{{
		AuditID:       "audit-1",
		ActorID:       "agent-1",
		ViewerID:      "user-1",
		MissionID:     "mission-1",
		EntityType:    "task",
		EntityID:      "task-1",
		Action:        "task.submit_result",
		Result:        "succeeded",
		Severity:      "info",
		CorrelationID: "corr-1",
		OccurredAt:    at,
	}}}
	repo := operations.NewMemoryMissionProjectionRepository()
	if err := repo.Save(context.Background(), evaluationReadModel(at)); err != nil {
		t.Fatalf("save projection: %v", err)
	}
	service := operations.NewQueryService(nil, nil)
	service.SetAuditStore(audit)
	service.SetMissionProjectionRepository(repo)

	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOperationsRead, operations.PermissionAuditRead}}
	auditPage, err := service.ListAuditLog(context.Background(), viewer, operations.AuditLogQuery{MissionID: "mission-1", ActorID: "agent-1"})
	if err != nil {
		t.Fatalf("ListAuditLog returned error: %v", err)
	}
	if len(auditPage.Events) != 1 || auditPage.Events[0].CorrelationID != "corr-1" {
		t.Fatalf("audit page = %+v", auditPage)
	}
	if audit.query.MissionID != "mission-1" || audit.query.ActorID != "agent-1" || audit.query.Limit != 100 {
		t.Fatalf("audit query = %+v", audit.query)
	}
	if _, err := service.ListAuditLog(context.Background(), viewer, operations.AuditLogQuery{Limit: 1000, Offset: -10}); err != nil {
		t.Fatalf("ListAuditLog with unsafe page bounds returned error: %v", err)
	}
	if audit.query.Limit != 500 || audit.query.Offset != 0 {
		t.Fatalf("bounded audit query = %+v", audit.query)
	}

	evaluations, err := service.ListEvaluations(context.Background(), viewer, operations.EvaluationQuery{MissionID: "mission-1", EntityType: "agent"})
	if err != nil {
		t.Fatalf("ListEvaluations returned error: %v", err)
	}
	if len(evaluations.Items) != 1 {
		t.Fatalf("evaluations = %+v", evaluations)
	}
	item := evaluations.Items[0]
	if item.EntityID != "profile-agent-1" || item.EntityType != "agent" {
		t.Fatalf("evaluation identity = %+v", item)
	}
	if item.CompletedTasks != 1 || item.ReworkCount != 1 || item.BlockerCount != 1 || item.QualityScore <= 0 {
		t.Fatalf("evaluation metrics = %+v", item)
	}
}

func TestQueryServiceListsEvaluationProjectionAcrossMissions(t *testing.T) {
	at := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)
	store := operations.NewMemoryEvaluationProjectionStore()
	if err := store.UpsertEvaluationItems(context.Background(), []operations.EvaluationItemDTO{
		{
			EntityType:           "agent",
			EntityID:             "agent-1",
			DisplayName:          "Ada",
			MissionID:            "mission-1",
			AssignedTasks:        2,
			CompletedTasks:       1,
			ReworkCount:          1,
			BlockerCount:         1,
			ResourceUsageCount:   3,
			DiscussionReplyCount: 2,
			QualityScore:         0.4,
			OnTimeRate:           0.5,
			CollaborationScore:   0.8,
			LastActivityAt:       at,
		},
		{
			EntityType:           "agent",
			EntityID:             "agent-1",
			DisplayName:          "Ada",
			MissionID:            "mission-2",
			AssignedTasks:        1,
			CompletedTasks:       1,
			ResourceUsageCount:   2,
			DiscussionReplyCount: 1,
			QualityScore:         1,
			OnTimeRate:           1,
			CollaborationScore:   1,
			LastActivityAt:       at.Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("upsert evaluations: %v", err)
	}
	service := operations.NewQueryService(nil, nil)
	service.SetEvaluationProjectionStore(store)

	page, err := service.ListEvaluations(context.Background(), operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOperationsRead}}, operations.EvaluationQuery{
		EntityType: "agent",
		EntityID:   "agent-1",
	})
	if err != nil {
		t.Fatalf("ListEvaluations returned error: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("evaluations = %+v", page)
	}
	item := page.Items[0]
	if item.MissionID != "" || item.AssignedTasks != 3 || item.CompletedTasks != 2 || item.ReworkCount != 1 || item.BlockerCount != 1 {
		t.Fatalf("aggregated task metrics = %+v", item)
	}
	if item.ResourceUsageCount != 5 || item.DiscussionReplyCount != 3 {
		t.Fatalf("aggregated analytics signals = %+v", item)
	}
	if item.QualityScore <= 0 || item.OnTimeRate <= 0 || item.CollaborationScore <= 0 {
		t.Fatalf("aggregated scores = %+v", item)
	}
}

func TestEvaluationProjectionStoreProjectsMissionSignals(t *testing.T) {
	at := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)
	model := evaluationReadModel(at)
	model.Discussion.Messages = []projection.MessageView{
		{MessageID: "message-1", AuthorID: "profile-agent-1", Body: "I can help", PostedAt: at.Add(time.Minute)},
		{MessageID: "message-2", AuthorID: "agent-2", Body: "Different author", PostedAt: at.Add(2 * time.Minute)},
	}
	model.Evidence.EvidenceItems = []projection.EvidenceItemView{
		{EvidenceID: "evidence-1", TaskID: "task-1", AgentID: "profile-agent-1", Kind: agentcore.EvidenceKindToolLog, ToolLogRef: agentcore.ToolLogRef{ID: "tool-log-1"}, SubmittedAt: at.Add(3 * time.Minute)},
		{EvidenceID: "evidence-2", TaskID: "task-2", AgentID: "profile-agent-1", Kind: "artifact", SubmittedAt: at.Add(4 * time.Minute)},
	}
	store := operations.NewMemoryEvaluationProjectionStore()

	if err := store.ProjectMission(context.Background(), model); err != nil {
		t.Fatalf("project mission: %v", err)
	}
	page, err := store.ListEvaluationItems(context.Background(), operations.EvaluationQuery{MissionID: "mission-1", EntityID: "profile-agent-1"})
	if err != nil {
		t.Fatalf("list projected evaluations: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("evaluations = %+v", page)
	}
	item := page.Items[0]
	if item.AssignedTasks != 2 || item.CompletedTasks != 1 || item.ReworkCount != 1 || item.BlockerCount != 1 {
		t.Fatalf("task and QA metrics = %+v", item)
	}
	if item.DiscussionReplyCount != 1 || item.ResourceUsageCount != 1 {
		t.Fatalf("discussion/resource metrics = %+v", item)
	}
	if !item.LastActivityAt.Equal(at.Add(4 * time.Minute)) {
		t.Fatalf("last activity = %s", item.LastActivityAt)
	}
}

func TestEvaluationProjectionFoldsScopedAgentInstanceSignalsIntoStableProfile(t *testing.T) {
	at := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)
	model := evaluationReadModel(at)
	model.Team.Members = []projection.TeamMemberView{{
		AgentID: "profile-agent-1", AgentProfileID: "profile-agent-1", AgentInstanceID: "profile-agent-1:mission-1", DisplayName: "Ada", MissionRole: "worker", Status: projection.TeamMemberJoined, JoinedAt: at,
	}}
	model.TaskBoard.Tasks = []projection.TaskCardView{
		{TaskID: "task-1", Assignee: "profile-agent-1", Status: projection.TaskStatusApproved, QAStatus: projection.QAStatusApproved, UpdatedAt: at},
		{TaskID: "task-2", Assignee: "profile-agent-1:mission-1", Status: projection.TaskStatusBlocked, BlockerRefs: []string{"blocker-1"}, UpdatedAt: at},
	}
	model.QA.ReworkRequests = []projection.ReworkRequestView{{TaskID: "task-2", QARef: "qa-2", Status: projection.ReworkStatusOpen, CreatedAt: at}}
	model.Blockers = []projection.BlockerView{{BlockerID: "blocker-1", TaskID: "task-2", RaisedBy: "profile-agent-1:mission-1", Summary: "Need access", RaisedAt: at}}
	model.Discussion.Messages = []projection.MessageView{
		{MessageID: "message-1", AuthorID: "profile-agent-1", Body: "I can help", PostedAt: at.Add(time.Minute)},
		{MessageID: "message-2", AuthorID: "profile-agent-1:mission-1", Body: "Runtime signal", PostedAt: at.Add(2 * time.Minute)},
	}
	model.Evidence.EvidenceItems = []projection.EvidenceItemView{
		{EvidenceID: "evidence-1", TaskID: "task-1", AgentID: "profile-agent-1", Kind: agentcore.EvidenceKindToolLog, ToolLogRef: agentcore.ToolLogRef{ID: "tool-log-1"}, SubmittedAt: at.Add(3 * time.Minute)},
		{EvidenceID: "evidence-2", TaskID: "task-2", AgentID: "profile-agent-1:mission-1", Kind: agentcore.EvidenceKindToolLog, ToolLogRef: agentcore.ToolLogRef{ID: "tool-log-2"}, SubmittedAt: at.Add(4 * time.Minute)},
	}
	store := operations.NewMemoryEvaluationProjectionStore()

	if err := store.ProjectMission(context.Background(), model); err != nil {
		t.Fatalf("project mission: %v", err)
	}
	page, err := store.ListEvaluationItems(context.Background(), operations.EvaluationQuery{MissionID: "mission-1", EntityID: "profile-agent-1"})
	if err != nil {
		t.Fatalf("list projected evaluations: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("evaluations = %+v", page)
	}
	item := page.Items[0]
	if item.EntityID != "profile-agent-1" || item.DisplayName != "Ada" {
		t.Fatalf("evaluation identity = %+v", item)
	}
	if item.AssignedTasks != 2 || item.CompletedTasks != 1 || item.ReworkCount != 1 || item.BlockerCount != 1 || item.DiscussionReplyCount != 2 || item.ResourceUsageCount != 2 {
		t.Fatalf("stable profile metrics should include scoped runtime signals: %+v", item)
	}
	scopedPage, err := store.ListEvaluationItems(context.Background(), operations.EvaluationQuery{MissionID: "mission-1", EntityID: "profile-agent-1:mission-1"})
	if err != nil {
		t.Fatalf("list scoped evaluations: %v", err)
	}
	if len(scopedPage.Items) != 0 {
		t.Fatalf("scoped runtime identity should not be projected as an agent: %+v", scopedPage)
	}
	allPage, err := store.ListEvaluationItems(context.Background(), operations.EvaluationQuery{MissionID: "mission-1", EntityType: "agent"})
	if err != nil {
		t.Fatalf("list all evaluations: %v", err)
	}
	if len(allPage.Items) != 1 || allPage.Items[0].EntityID != "profile-agent-1" {
		t.Fatalf("evaluation projection should contain only stable agent profiles: %+v", allPage)
	}
}

func TestEvaluationProjectionBuildsAgentResumeDetails(t *testing.T) {
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	model := evaluationReadModel(at)
	model.Mission.FinalGoal = "完成合同档案合规审核并输出归档建议"
	model.Team.Members[0].RoleName = "档案审核员"
	model.Team.Members[0].Department = "档案管理部"
	model.Team.Members[0].Position = "审档 Agent"
	model.TaskBoard.Tasks[0].Goal = "采购合同归档审核"
	model.TaskBoard.Tasks[0].WorkerPersonalGoalSummary = "核对合同、验收单与审批材料完整性"
	model.TaskBoard.Tasks[0].ExpectedDeliverables = []projection.ExpectedDeliverableView{{ID: "deliverable-1", Title: "归档审核结论"}}
	model.TaskBoard.Tasks[0].RequiredEvidence = []projection.RequiredEvidenceView{{ID: "evidence-1", Description: "合同签署页"}}
	model.TaskBoard.Tasks[1].Goal = "工程资料完整性校验"
	model.TaskBoard.Tasks[1].WorkerPersonalGoalSummary = "识别缺失材料和重复附件"
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-1", AgentID: "profile-agent-1", TaskID: "task-1", RuntimeKind: "codex", Status: projection.RuntimeSessionCompleted,
		StartedAt: at.Add(-3 * time.Minute), CompletedAt: at.Add(-1 * time.Minute), UpdatedAt: at.Add(-1 * time.Minute),
		Usage: map[string]agentcore.TokenUsage{"codex": {InputTokens: 2000, OutputTokens: 700, CacheReadTokens: 300}},
	}}
	model.Evidence.EvidenceItems = []projection.EvidenceItemView{{
		EvidenceID: "evidence-item-1", TaskID: "task-1", AgentID: "profile-agent-1", Kind: agentcore.EvidenceKindToolLog, SubmittedAt: at.Add(-90 * time.Second),
	}}
	model.Discussion.Messages = []projection.MessageView{{
		MessageID: "message-1", AuthorID: "profile-agent-1", AuthorType: "agent", Body: "已完成审核", PostedAt: at.Add(-80 * time.Second),
	}}

	store := operations.NewMemoryEvaluationProjectionStore()
	if err := store.ProjectMission(context.Background(), model); err != nil {
		t.Fatalf("project mission: %v", err)
	}
	page, err := store.ListEvaluationItems(context.Background(), operations.EvaluationQuery{EntityID: "profile-agent-1"})
	if err != nil {
		t.Fatalf("list projected evaluations: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("evaluations = %+v", page)
	}
	item := page.Items[0]
	if item.Department != "档案管理部" || item.RoleName != "档案审核员" || item.RuntimeKind != "codex" {
		t.Fatalf("agent metadata = %+v", item)
	}
	if item.EfficiencyScore <= 0 || item.StabilityScore <= 0 || item.AverageDurationSeconds <= 0 {
		t.Fatalf("derived scores/duration = %+v", item)
	}
	if !strings.HasPrefix(item.Summary, "结论：当前 Agent") {
		t.Fatalf("summary = %q", item.Summary)
	}
	if len(item.Capabilities) < 4 || item.Capabilities[0].Name == "" || item.Capabilities[0].Score <= 0 {
		t.Fatalf("capabilities = %+v", item.Capabilities)
	}
	if item.TokenConsumed != 3000 {
		t.Fatalf("token consumed should come from runtime usage, got %+v", item)
	}
	if item.Investment.TokenConsumed != 3000 || item.Investment.EstimatedHumanDays != 0 || item.Investment.ReturnMultiple < 0 {
		t.Fatalf("investment = %+v", item.Investment)
	}
	if len(item.TaskHistory) != 2 || item.TaskHistory[0].Goal == "" || item.TaskHistory[0].Responsibility == "" || item.TaskHistory[0].TokenConsumed != 3000 || item.TaskHistory[1].TokenConsumed != 0 {
		t.Fatalf("task history = %+v", item.TaskHistory)
	}
	if len(item.Trend) == 0 || len(item.Suggestions) == 0 || len(item.Advantages) == 0 || len(item.Risks) == 0 || len(item.Scenarios) == 0 {
		t.Fatalf("resume sections missing: trend=%+v suggestions=%+v advantages=%+v risks=%+v scenarios=%+v", item.Trend, item.Suggestions, item.Advantages, item.Risks, item.Scenarios)
	}
	if len(item.RepresentativeCases) == 0 || item.RepresentativeCases[0].Title == "" {
		t.Fatalf("representative cases = %+v", item.RepresentativeCases)
	}
}

func TestEvaluationProjectionUsesOnlyRealRuntimeTokenUsage(t *testing.T) {
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	model := evaluationReadModel(at)
	store := operations.NewMemoryEvaluationProjectionStore()

	if err := store.ProjectMission(context.Background(), model); err != nil {
		t.Fatalf("project mission: %v", err)
	}
	page, err := store.ListEvaluationItems(context.Background(), operations.EvaluationQuery{MissionID: "mission-1", EntityID: "profile-agent-1"})
	if err != nil {
		t.Fatalf("list projected evaluations: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("evaluations = %+v", page)
	}
	item := page.Items[0]
	if item.TokenConsumed != 0 || item.Investment.TokenConsumed != 0 {
		t.Fatalf("token usage must not be estimated when runtime usage is absent: %+v", item)
	}
	for _, task := range item.TaskHistory {
		if task.TokenConsumed != 0 {
			t.Fatalf("task token usage must not be estimated: %+v", item.TaskHistory)
		}
	}
}

func TestEvaluationReviewOverridesHumanDayValueEstimate(t *testing.T) {
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	model := evaluationReadModel(at)
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-1", AgentID: "profile-agent-1", TaskID: "task-1", RuntimeKind: "codex", Status: projection.RuntimeSessionCompleted,
		Usage:     map[string]agentcore.TokenUsage{"codex": {InputTokens: 500, OutputTokens: 200}},
		UpdatedAt: at,
	}}
	store := operations.NewMemoryEvaluationProjectionStore()
	if err := store.ProjectMission(context.Background(), model); err != nil {
		t.Fatalf("project mission: %v", err)
	}
	if err := store.UpsertEvaluationReview(context.Background(), operations.EvaluationReviewDTO{
		ReviewID:                "review-1",
		EntityType:              "agent",
		EntityID:                "profile-agent-1",
		ScopeKind:               "agent",
		ScopeID:                 "profile-agent-1",
		EvaluatorAgentID:        "review-agent",
		ConversationID:          "conversation-1",
		ScheduledRunID:          "scheduled-run-1",
		WindowDays:              7,
		Summary:                 "结论：该 Agent 近 7 天交付质量稳定，建议继续投入。",
		EstimatedHumanDays:      2.5,
		EvaluationTokenConsumed: 3200,
		CreatedAt:               at.Add(time.Hour),
	}); err != nil {
		t.Fatalf("upsert review: %v", err)
	}

	page, err := store.ListEvaluationItems(context.Background(), operations.EvaluationQuery{EntityType: "agent", EntityID: "profile-agent-1"})
	if err != nil {
		t.Fatalf("list projected evaluations: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("evaluations = %+v", page)
	}
	item := page.Items[0]
	if item.TokenConsumed != 700 || item.Investment.TokenConsumed != 700 {
		t.Fatalf("runtime token usage should stay real and separate from review token: %+v", item)
	}
	if item.Investment.EstimatedHumanDays != 2.5 || item.LatestReview.EvaluationTokenConsumed != 3200 {
		t.Fatalf("review estimate not applied: %+v", item)
	}
	if item.Summary != "结论：该 Agent 近 7 天交付质量稳定，建议继续投入。" {
		t.Fatalf("summary = %q", item.Summary)
	}
}

func TestEvaluationReviewCanCreateDiscussionEvaluationItem(t *testing.T) {
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store := operations.NewMemoryEvaluationProjectionStore()
	if err := store.UpsertEvaluationReview(context.Background(), operations.EvaluationReviewDTO{
		ReviewID:                "review-discussion-1",
		EntityType:              "discussion",
		EntityID:                "discussion-1",
		ScopeKind:               "discussion",
		ScopeID:                 "discussion-1",
		EvaluatorAgentID:        "review-agent",
		Summary:                 "结论：讨论组推进效率偏低，需要收敛任务拆分。",
		EstimatedHumanDays:      1.4,
		EvaluationTokenConsumed: 1800,
		CreatedAt:               at,
	}); err != nil {
		t.Fatalf("upsert review: %v", err)
	}

	page, err := store.ListEvaluationItems(context.Background(), operations.EvaluationQuery{EntityType: "discussion", EntityID: "discussion-1"})
	if err != nil {
		t.Fatalf("list projected evaluations: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].EntityType != "discussion" || page.Items[0].Investment.EstimatedHumanDays != 1.4 {
		t.Fatalf("discussion evaluation item = %+v", page)
	}
}

func TestEvaluationProjectionFoldsDistinctRuntimeAgentIDIntoProfileID(t *testing.T) {
	at := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)
	model := evaluationReadModel(at)
	model.Team.Members = []projection.TeamMemberView{{
		AgentID: "runtime-agent-1", AgentProfileID: "profile-agent-1", AgentInstanceID: "runtime-agent-1:mission-1", DisplayName: "档案 Agent", MissionRole: "worker", Status: projection.TeamMemberJoined, JoinedAt: at,
	}}
	model.TaskBoard.Tasks = []projection.TaskCardView{{
		TaskID: "task-1", Assignee: "runtime-agent-1", Status: projection.TaskStatusApproved, QAStatus: projection.QAStatusApproved, UpdatedAt: at,
	}}
	store := operations.NewMemoryEvaluationProjectionStore()

	if err := store.ProjectMission(context.Background(), model); err != nil {
		t.Fatalf("project mission: %v", err)
	}
	page, err := store.ListEvaluationItems(context.Background(), operations.EvaluationQuery{MissionID: "mission-1", EntityID: "profile-agent-1"})
	if err != nil {
		t.Fatalf("list projected evaluations: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].EntityID != "profile-agent-1" || page.Items[0].AssignedTasks != 1 {
		t.Fatalf("runtime agent id should fold into stable profile id: %+v", page)
	}
}

func TestEvaluationProjectionSkipsSignalsWithoutStableTeamProfile(t *testing.T) {
	at := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)
	model := evaluationReadModel(at)
	model.Team.Members = []projection.TeamMemberView{{
		AgentID: "runtime-agent", AgentInstanceID: "runtime-agent:mission-1", DisplayName: "Runtime Agent", MissionRole: "worker", Status: projection.TeamMemberJoined, JoinedAt: at,
	}}
	model.TaskBoard.Tasks = []projection.TaskCardView{
		{TaskID: "task-1", Assignee: "runtime-agent", Status: projection.TaskStatusApproved, QAStatus: projection.QAStatusApproved, UpdatedAt: at},
		{TaskID: "task-2", Assignee: "runtime-agent:mission-1", Status: projection.TaskStatusBlocked, BlockerRefs: []string{"blocker-1"}, UpdatedAt: at},
	}
	model.QA.ReworkRequests = []projection.ReworkRequestView{{TaskID: "task-2", QARef: "qa-2", Status: projection.ReworkStatusOpen, CreatedAt: at}}
	model.Blockers = []projection.BlockerView{{BlockerID: "blocker-1", TaskID: "task-2", RaisedBy: "runtime-agent:mission-1", Summary: "Need access", RaisedAt: at}}
	model.Discussion.Messages = []projection.MessageView{{MessageID: "message-1", AuthorID: "runtime-agent", Body: "Runtime signal", PostedAt: at.Add(time.Minute)}}
	model.Evidence.EvidenceItems = []projection.EvidenceItemView{{EvidenceID: "evidence-1", TaskID: "task-1", AgentID: "runtime-agent:mission-1", Kind: agentcore.EvidenceKindToolLog, ToolLogRef: agentcore.ToolLogRef{ID: "tool-log-1"}, SubmittedAt: at.Add(2 * time.Minute)}}
	store := operations.NewMemoryEvaluationProjectionStore()

	if err := store.ProjectMission(context.Background(), model); err != nil {
		t.Fatalf("project mission: %v", err)
	}
	page, err := store.ListEvaluationItems(context.Background(), operations.EvaluationQuery{MissionID: "mission-1", EntityType: "agent"})
	if err != nil {
		t.Fatalf("list projected evaluations: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("runtime signals without a stable profile should not create evaluation agents: %+v", page)
	}
}

func TestPlatformEvaluationProjectionStorePersistsAndAutoProjectsMissionSave(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	store := operations.NewPlatformEvaluationProjectionStore(env.DBRouter)
	repo := operations.NewAutoProjectingMissionProjectionRepository(operations.NewMemoryMissionProjectionRepository(), store)
	model := evaluationReadModel(time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC))

	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("save and project mission: %v", err)
	}

	reopened := operations.NewPlatformEvaluationProjectionStore(env.DBRouter)
	page, err := reopened.ListEvaluationItems(ctx, operations.EvaluationQuery{MissionID: "mission-1", EntityID: "profile-agent-1"})
	if err != nil {
		t.Fatalf("list persisted evaluations: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("evaluations = %+v", page)
	}
	item := page.Items[0]
	if item.AssignedTasks != 2 || item.CompletedTasks != 1 || item.ReworkCount != 1 || item.BlockerCount != 1 {
		t.Fatalf("persisted metrics = %+v", item)
	}
	if item.TokenConsumed != 0 || item.Investment.TokenConsumed != 0 || len(item.TaskHistory) == 0 || len(item.Capabilities) == 0 {
		t.Fatalf("persisted resume details = %+v", item)
	}
	if _, err := repo.GetMissionControl(ctx, missioncore.MissionID("mission-1")); err != nil {
		t.Fatalf("wrapped repository should still expose saved mission: %v", err)
	}
}

func TestPlatformEvaluationProjectionStoreUsesPostgresSafeRecordKeys(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	store := operations.NewPlatformEvaluationProjectionStore(env.DBRouter)

	if err := store.UpsertEvaluationItems(ctx, []operations.EvaluationItemDTO{{
		MissionID:            "mission-1",
		EntityType:           "agent",
		EntityID:             "profile-agent-1",
		DisplayName:          "Ada",
		AssignedTasks:        1,
		CompletedTasks:       1,
		QualityScore:         1,
		OnTimeRate:           1,
		CollaborationScore:   1,
		Investment:           operations.EvaluationInvestmentDTO{TokenConsumed: 42},
		LastActivityAt:       time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC),
		ResourceUsageCount:   1,
		DiscussionReplyCount: 1,
	}}); err != nil {
		t.Fatalf("upsert evaluation: %v", err)
	}

	_, ok := env.DBRouter.Get("observability.evaluation_projection", "mission-1|agent|profile-agent-1")
	if !ok {
		t.Fatalf("evaluation projection was not stored under a postgres-safe key")
	}
}

type fakeAuditStore struct {
	events []operations.AuditEventDTO
	query  operations.AuditLogQuery
}

func (f *fakeAuditStore) ListAuditEvents(ctx context.Context, query operations.AuditLogQuery) (operations.AuditEventPageDTO, error) {
	f.query = query
	return operations.AuditEventPageDTO{Events: f.events, Page: operations.PageDTO{Total: len(f.events), Returned: len(f.events)}}, nil
}

func evaluationReadModel(at time.Time) projection.ReadModel {
	return projection.ReadModel{
		Mission: projection.MissionView{MissionID: missioncore.MissionID("mission-1"), Title: "Evaluation", Status: projection.MissionStatusActive, CreatedAt: at},
		Team: projection.TeamView{Members: []projection.TeamMemberView{{
			AgentID: "profile-agent-1", AgentProfileID: "profile-agent-1", AgentInstanceID: "profile-agent-1", DisplayName: "Ada", RealName: "Ada Lovelace", MissionRole: "worker", Status: projection.TeamMemberJoined, JoinedAt: at,
		}}},
		TaskBoard: projection.TaskBoardView{Tasks: []projection.TaskCardView{
			{TaskID: "task-1", Assignee: "profile-agent-1", Status: projection.TaskStatusApproved, QAStatus: projection.QAStatusApproved, UpdatedAt: at},
			{TaskID: "task-2", Assignee: "profile-agent-1", Status: projection.TaskStatusReworkRequested, QAStatus: projection.QAStatusRejected, BlockerRefs: []string{"blocker-1"}, UpdatedAt: at},
		}},
		QA:         projection.QAView{ReworkRequests: []projection.ReworkRequestView{{TaskID: "task-2", QARef: "qa-2", Status: projection.ReworkStatusOpen, CreatedAt: at}}},
		Blockers:   []projection.BlockerView{{BlockerID: "blocker-1", TaskID: "task-2", RaisedBy: "profile-agent-1", Summary: "Need access", RaisedAt: at}},
		UpdatedAt:  at,
		Checkpoint: projection.Checkpoint{MissionID: "mission-1", Version: 1, UpdatedAt: at},
	}
}

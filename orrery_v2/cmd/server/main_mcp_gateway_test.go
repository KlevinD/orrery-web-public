package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memorydiaryapp "orrery_v2/internal/app/memorydiary"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	"orrery_v2/internal/platform/testkit"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestProductionMCPGatewayAuditRecorderPersistsCorrelationID(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	store := operations.NewAuditStore(env.DBRouter, env.Clock, env.IDGenerator)
	recorder := productionMCPGatewayAuditRecorder{store: store}

	err := recorder.RecordMCPToolAudit(ctx, mcptransport.AuditEvent{
		TenantID: "tenant-a", RunID: "agent-run-1", ToolID: "mcp.commerce.commerce_operation_call", PrincipalID: "agent-ops",
		ResponsibilityContextID: "context-1", ResponsibilityID: "responsibility-1", ResponsibilityPolicyID: "policy-1", ResponsibilityPolicyVersion: 4,
		Outcome: mcptransport.AuditOutcomeAllowed, CorrelationID: "corr-commerce-1", OccurredAt: env.Clock.Now(),
	})
	if err != nil {
		t.Fatalf("RecordMCPToolAudit: %v", err)
	}
	page, err := store.ListAuditEvents(ctx, operations.AuditLogQuery{Action: "mcp.tool.call"})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].CorrelationID != "corr-commerce-1" || page.Events[0].Metadata["agent_run_id"] != "agent-run-1" ||
		page.Events[0].Metadata["responsibility_context_id"] != "context-1" || page.Events[0].Metadata["responsibility_id"] != "responsibility-1" ||
		page.Events[0].Metadata["responsibility_policy_id"] != "policy-1" || page.Events[0].Metadata["responsibility_policy_version"] != "4" {
		t.Fatalf("audit events = %+v", page.Events)
	}
}

func TestProductionMCPGatewayPublishesSchedulerTools(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)) {
		if err := registry.Register(ctx, definition); err != nil {
			t.Fatalf("register %s: %v", definition.ID, err)
		}
	}
	skillExecutor := &serverTestSkillExecutor{}
	shellExecutor := &serverTestPersonalAssistantShellExecutor{}

	gateway := newProductionMCPGateway(productionMCPGatewayOptions{
		Registry:                       registry,
		SchedulerExecutor:              &serverTestSchedulerExecutor{},
		PersonalAssistantShellExecutor: shellExecutor,
		SkillExecutor:                  skillExecutor,
		Now:                            func() time.Time { return time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC) },
	})
	tools, err := gateway.ListTools(ctx, mcptransport.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	found := false
	for _, tool := range tools {
		if tool.ToolID == string(toolstd.ScheduledTaskRunNow) {
			found = true
			if tool.Source != mcptransport.ToolSourceInternal {
				t.Fatalf("scheduler tool source = %q", tool.Source)
			}
		}
	}
	if !found {
		t.Fatalf("scheduler run-now tool not published: %+v", tools)
	}
	foundShell := false
	for _, tool := range tools {
		if tool.ToolID == string(toolstd.PersonalAssistantShellExec) {
			foundShell = true
			if tool.Source != mcptransport.ToolSourceInternal {
				t.Fatalf("personal assistant shell source = %q", tool.Source)
			}
		}
	}
	if !foundShell {
		t.Fatalf("personal assistant shell tool not published: %+v", tools)
	}

	if _, err := gateway.CallTool(ctx, mcptransport.CallToolRequest{
		ToolID:      string(toolstd.SkillLoad),
		PrincipalID: "agent-test",
		Permissions: []string{toolstd.PermissionSkillRead},
		SkillBindings: []agentcore.RuntimeSkillBinding{{
			SkillID: "skill.tdd",
		}},
		Arguments: map[string]any{"skill_id": "skill.tdd"},
	}); err != nil {
		t.Fatalf("CallTool(load_skill): %v", err)
	}
	if skillExecutor.toolID != string(toolstd.SkillLoad) || skillExecutor.request.SkillID != "skill.tdd" {
		t.Fatalf("skill executor request = tool:%q req:%+v", skillExecutor.toolID, skillExecutor.request)
	}

	if _, err := gateway.CallTool(ctx, mcptransport.CallToolRequest{
		ToolID:      string(toolstd.PersonalAssistantShellExec),
		PrincipalID: "agent-test",
		Permissions: []string{toolstd.PermissionPersonalAssistantShell},
		Arguments: map[string]any{
			"command":        "pwd",
			"workspace_root": "D:/workspace",
		},
	}); err != nil {
		t.Fatalf("CallTool(personal shell): %v", err)
	}
	if shellExecutor.toolID != string(toolstd.PersonalAssistantShellExec) ||
		shellExecutor.request.Command != "pwd" ||
		shellExecutor.request.WorkspaceRoot != "D:/workspace" {
		t.Fatalf("shell executor request = tool:%q req:%+v", shellExecutor.toolID, shellExecutor.request)
	}
}

func TestProductionSchedulerToolCallPortPassesCreatorContextToGateway(t *testing.T) {
	ctx := context.Background()
	gateway := &serverTestMCPGatewayCaller{
		result: mcptransport.CallToolResult{Output: map[string]any{"todo_id": "todo-1"}},
	}
	port := productionSchedulerToolCallPort{gateway: gateway}

	result, err := port.CallTool(ctx, schedulerapp.ToolCallRequest{
		TenantID:       "tenant-a",
		ToolID:         "todo.create",
		PrincipalID:    "operator",
		ProfileID:      "human-operator",
		Permissions:    []string{"todo.write"},
		Arguments:      map[string]any{"title": "Ship scheduler tool call"},
		IdempotencyKey: "scheduled-run:run-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	if gateway.req.ToolID != "todo.create" ||
		gateway.req.TenantID != "tenant-a" ||
		gateway.req.PrincipalID != "operator" ||
		gateway.req.ProfileID != "human-operator" ||
		gateway.req.Permissions[0] != "todo.write" ||
		gateway.req.Arguments["title"] != "Ship scheduler tool call" {
		t.Fatalf("gateway request = %+v", gateway.req)
	}
	if gateway.req.Arguments["idempotency_key"] != "scheduled-run:run-1" {
		t.Fatalf("gateway idempotency arguments = %+v", gateway.req.Arguments)
	}
	if result.Output["todo_id"] != "todo-1" {
		t.Fatalf("result = %+v", result)
	}
}

func TestProductionSchedulerAgentTaskPortMaterializesMissionTaskResponsibility(t *testing.T) {
	ctx := context.Background()
	commands := &serverTestSchedulerMissionCommands{}
	port := productionSchedulerAgentTaskPort{missions: commands}

	receipt, err := port.StartAgentTask(ctx, schedulerapp.AgentTaskRequest{
		TenantID:         "tenant-a",
		RunID:            "run-scheduled-1",
		ScheduledTaskID:  "sched-1",
		Title:            "Direct agent run",
		Description:      "Create a durable scheduled responsibility",
		AgentID:          "agent-worker",
		TaskGoal:         "Summarize customer feedback",
		Prompt:           "Return ranked actions",
		PrincipalID:      "operator",
		CreatorProfileID: "human-operator",
		Permissions:      []string{"todo.write", "todo.write", "memory.read"},
		MaxAttempts:      3,
		RetryDelay:       "5m",
		ResultContract: schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{
			ID: "weekly-report", Kind: "report", PathPattern: "/reports/weekly-*.md", MinBytes: 128,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	expectedResponsibilityID := responsibility.AssignmentID("tenant-a", responsibility.WorkKindMissionTask, commands.task.TaskID.String())
	if receipt.CommandID != expectedResponsibilityID || receipt.AgentID != "agent-worker" || receipt.MissionID != "mission-scheduled-1" || receipt.TaskID != commands.task.TaskID.String() || receipt.ResponsibilityID != expectedResponsibilityID {
		t.Fatalf("receipt = %+v", receipt)
	}
	if commands.mission.MissionID != scheduledMissionID("tenant-a", "sched-1") || commands.mission.OriginKind != "scheduler" || commands.mission.OriginID != "sched-1" || commands.mission.InitialAgentProfileID != "agent-worker" {
		t.Fatalf("mission command = %+v", commands.mission)
	}
	if commands.task.SourceKind != schedulerAgentTaskRunSource || commands.task.SourceID != "sched-1" || commands.task.SourceStepID != "run-scheduled-1" || commands.task.AssigneeID != "agent-worker" {
		t.Fatalf("task command = %+v", commands.task)
	}
	if len(commands.task.ExecutionPermissionScopes) != 2 || commands.task.ExecutionPermissionScopes[0] != "todo.write" || commands.task.ExecutionPermissionScopes[1] != "memory.read" {
		t.Fatalf("permission scopes = %+v", commands.task.ExecutionPermissionScopes)
	}
	if len(commands.task.ExpectedDeliverables) != 1 || commands.task.ExpectedDeliverables[0].ID != "weekly-report" || len(commands.task.EvidenceRequirements) != 1 || commands.task.EvidenceRequirements[0].ID != "evidence-weekly-report" {
		t.Fatalf("scheduled result requirements = deliverables:%+v evidence:%+v", commands.task.ExpectedDeliverables, commands.task.EvidenceRequirements)
	}
	if commands.assign.TaskID != commands.task.TaskID || commands.assign.AssigneeID != "agent-worker" {
		t.Fatalf("assign command = %+v", commands.assign)
	}
}

func TestScheduledMissionResultRequirementsUseToolEvidenceWithoutArtifactContract(t *testing.T) {
	deliverables, evidence := scheduledMissionResultRequirements(schedulerapp.AgentTaskRequest{Title: "Governed Marketing workflow"}, "completed")
	if len(deliverables) != 1 || deliverables[0].ID != "deliverable-result" {
		t.Fatalf("deliverables = %+v", deliverables)
	}
	if len(evidence) != 1 || evidence[0].ID != "evidence-acceptance-1" || evidence[0].Kind != "tool_log" || !evidence[0].Required {
		t.Fatalf("no-contract task evidence = %+v", evidence)
	}
}

func TestProductionMCPGatewayRoutesEvaluationReviewTool(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)) {
		if err := registry.Register(ctx, definition); err != nil {
			t.Fatalf("register %s: %v", definition.ID, err)
		}
	}
	store := operations.NewMemoryEvaluationProjectionStore()
	at := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := store.UpsertEvaluationItems(ctx, []operations.EvaluationItemDTO{{
		EntityType:     "agent",
		EntityID:       "profile-agent-1",
		DisplayName:    "档案 Agent",
		CompletedTasks: 1,
		TokenConsumed:  2400,
		TaskHistory: []operations.EvaluationTaskHistoryDTO{{
			Goal:          "完成档案审核",
			Result:        "通过",
			TokenConsumed: 2400,
			Time:          at.Add(-time.Hour),
		}},
	}}); err != nil {
		t.Fatalf("seed evaluation item: %v", err)
	}

	gateway := newProductionMCPGateway(productionMCPGatewayOptions{
		Registry: registry,
		EvaluationExecutor: productionEvaluationToolExecutor{executor: operations.NewEvaluationToolExecutor(operations.EvaluationToolExecutorOptions{
			Store: store,
			Now:   func() time.Time { return at },
		})},
		Profiles: serverTestProfileStore{
			profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
				"review-agent": {
					ID:       "review-agent",
					Enabled:  true,
					ToolRefs: []agentcore.ToolRef{{ToolID: toolstd.SystemMCPServer}},
				},
			},
			page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
				AgentProfileID: "review-agent",
				Enabled:        true,
			}}},
		},
		Now: func() time.Time { return at },
	})

	result, err := gateway.CallTool(ctx, mcptransport.CallToolRequest{
		ToolID:      string(toolstd.EvaluationReview),
		PrincipalID: "review-agent",
		AgentID:     "review-agent",
		Permissions: []string{toolstd.PermissionEvaluationReview},
		Arguments: map[string]any{
			"mode":                 "agent",
			"agent_id":             "profile-agent-1",
			"summary":              "结论：档案 Agent 本周期完成审核任务，交付稳定。",
			"estimated_human_days": 1.5,
			"window_days":          7,
			"idempotency_key":      "review-prod-gateway",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(evaluation_review): %v", err)
	}
	if result.Output["review_count"] != 1 {
		t.Fatalf("result = %+v", result.Output)
	}
	page, err := store.ListEvaluationItems(ctx, operations.EvaluationQuery{EntityType: "agent", EntityID: "profile-agent-1"})
	if err != nil {
		t.Fatalf("list evaluation items: %v", err)
	}
	if len(page.Items) != 1 ||
		page.Items[0].LatestReview.ReviewID != "review-prod-gateway:profile-agent-1" ||
		page.Items[0].Investment.EstimatedHumanDays != 1.5 {
		t.Fatalf("reviewed item = %+v", page.Items)
	}
}

func TestProductionMCPGatewayRoutesMemoryDiaryWriteTool(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)) {
		if err := registry.Register(ctx, definition); err != nil {
			t.Fatalf("register %s: %v", definition.ID, err)
		}
	}
	store := memtestkit.NewFakeStore()
	if err := store.SaveProfile(ctx, memcore.MemoryProfile{
		ID:               "memory-profile-agent-1",
		OwnerRef:         memcore.OwnerRef{AgentID: "profile-agent-1"},
		MemoryKind:       agentcore.MemoryKindPersonal,
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate.agent_diary.profile-agent-1.run-1",
		SourceType:    memcore.SourceAgentRunDiary,
		SourceRef:     "run-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "profile-agent-1"},
		ProposedKind:  agentcore.MemoryKindPersonal,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "memory/2026-06-16.md",
		Summary:       "完成评估中心记忆查看页。",
		Narrative:     "Diary: memory/2026-06-16.md\nSummary: 完成评估中心记忆查看页。",
		Tags:          []string{memorydiaryapp.DiaryTagAgent, memorydiaryapp.DiaryTagDailyNote, memorydiaryapp.DiaryTagRunSource, "diary_date:2026-06-16", "diary_path:memory/2026-06-16.md"},
		CreatedBy:     "profile-agent-1",
		Status:        memcore.CandidateAccepted,
		CreatedAt:     time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	at := time.Date(2026, 6, 17, 2, 0, 0, 0, time.UTC)
	gateway := newProductionMCPGateway(productionMCPGatewayOptions{
		Registry: registry,
		MemoryDiaryExecutor: productionMemoryDiaryToolExecutor{executor: memorydiaryapp.NewToolExecutor(memorydiaryapp.ToolExecutorOptions{
			Repository: store,
			Enabled:    func(context.Context) (bool, error) { return true, nil },
			Now:        func() time.Time { return at },
		})},
		Profiles: serverTestProfileStore{
			profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
				"diary-agent": {
					ID:      "diary-agent",
					Enabled: true,
					ToolRefs: []agentcore.ToolRef{
						{ToolID: toolstd.SystemMCPServer},
						{ToolID: toolstd.MemoryDiaryWrite, PermissionScope: toolstd.PermissionMemoryDiaryWrite},
					},
				},
			},
			page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
				AgentProfileID: "diary-agent",
				Enabled:        true,
			}}},
		},
		Now: func() time.Time { return at },
	})

	result, err := gateway.CallTool(ctx, mcptransport.CallToolRequest{
		ToolID:      string(toolstd.MemoryDiaryWrite),
		PrincipalID: "diary-agent",
		AgentID:     "diary-agent",
		Permissions: []string{toolstd.PermissionMemoryDiaryWrite},
		Arguments: map[string]any{
			"mode":     "agent",
			"agent_id": "profile-agent-1",
			"date":     "2026-06-16",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(memory_diary_write): %v", err)
	}
	if result.Output["written_count"] != 1 {
		t.Fatalf("result = %+v", result.Output)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{Tags: []string{memorydiaryapp.DiaryTagSummary}})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 1 || records[0].Topic != "memory/2026-06-16.md" {
		t.Fatalf("records = %+v", records)
	}
}

type serverTestSchedulerExecutor struct{}

func (serverTestSchedulerExecutor) Execute(ctx context.Context, req schedulerapp.ToolExecutionRequest) (schedulerapp.ToolExecutionResult, error) {
	return schedulerapp.ToolExecutionResult{Output: map[string]any{"ok": true}}, nil
}

type serverTestMCPGatewayCaller struct {
	req    mcptransport.CallToolRequest
	result mcptransport.CallToolResult
	err    error
}

func (g *serverTestMCPGatewayCaller) CallTool(ctx context.Context, req mcptransport.CallToolRequest) (mcptransport.CallToolResult, error) {
	g.req = req
	if g.err != nil {
		return mcptransport.CallToolResult{}, g.err
	}
	return g.result, nil
}

type serverTestSkillExecutor struct {
	toolID  string
	request skillres.RuntimeSkillRequest
}

func (e *serverTestSkillExecutor) ExecuteSkillTool(ctx context.Context, toolID string, req skillres.RuntimeSkillRequest) (skillres.RuntimeToolResult, error) {
	e.toolID = toolID
	e.request = req
	return skillres.RuntimeToolResult{Output: map[string]any{"skill_id": req.SkillID}}, nil
}

type serverTestPersonalAssistantShellExecutor struct {
	toolID  string
	request mcptransport.PersonalAssistantShellRequest
}

func (e *serverTestPersonalAssistantShellExecutor) ExecutePersonalAssistantShell(ctx context.Context, toolID string, req mcptransport.PersonalAssistantShellRequest) (mcptransport.PersonalAssistantShellResult, error) {
	e.toolID = toolID
	e.request = req
	return mcptransport.PersonalAssistantShellResult{Output: map[string]any{"stdout": "shell ok"}}, nil
}

type serverTestSchedulerMissionCommands struct {
	mission missioncontrol.CreateMissionCommand
	task    missioncontrol.CreateTaskCommand
	assign  missioncontrol.AssignTaskCommand
}

func (s *serverTestSchedulerMissionCommands) CreateMission(_ context.Context, _ missioncontrol.ViewerRef, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	s.mission = cmd
	return missioncontrol.MissionDTO{MissionID: "mission-scheduled-1", Title: cmd.Title}, nil
}

func (s *serverTestSchedulerMissionCommands) SyncSchedulerMission(_ context.Context, _ missioncontrol.ViewerRef, cmd missioncontrol.SyncSchedulerMissionCommand) (missioncontrol.MissionDTO, error) {
	return missioncontrol.MissionDTO{MissionID: cmd.MissionID.String(), Title: cmd.Title, FinalGoal: cmd.FinalGoal, Status: cmd.ScheduledTaskStatus}, nil
}

func (s *serverTestSchedulerMissionCommands) CreateTask(_ context.Context, _ missioncontrol.ViewerRef, cmd missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	s.task = cmd
	return missioncontrol.TaskCardDTO{TaskID: cmd.TaskID.String(), Goal: cmd.Goal}, nil
}

func (s *serverTestSchedulerMissionCommands) AssignTask(_ context.Context, _ missioncontrol.ViewerRef, cmd missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	s.assign = cmd
	return missioncontrol.CommandReceiptDTO{MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(), Status: "accepted"}, nil
}

type serverTestProfileStore struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
	page     agentcore.AgentDirectoryPage
}

func (s serverTestProfileStore) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := s.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, mcptransport.ErrToolNotFound
	}
	return profile, nil
}

func (s serverTestProfileStore) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return s.page, nil
}

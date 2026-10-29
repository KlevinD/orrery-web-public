package discussionsession

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestServiceApplyRuntimeToolCallPersistsMissionContextForSequentialTools(t *testing.T) {
	repo := newMemoryRepository(t)
	seedRunningDiscussionRun(t, repo, "conv-1", "run-1", testTime)
	commands := &capturingMissionCommands{
		mission: missioncontrol.MissionDTO{MissionID: "mission-created", Title: "资产负债表可视化网页工具", Status: "active", CreatedAt: testTime},
		task:    missioncontrol.TaskCardDTO{TaskID: "task-root", UpdatedAt: testTime.Add(time.Minute)},
	}
	service := newTestService(ServiceOptions{
		Repository:     repo,
		MissionTooling: NewMissionToolApplier(commands),
		Clock:          fixedClock{now: testTime},
	})
	boundTools := []DiscussionAgentBoundTool{
		{ToolID: "mission_prepare"},
		{ToolID: "mission_task_create_root"},
	}

	prepared, links, err := service.ApplyRuntimeToolCall(
		context.Background(),
		"conv-1",
		"run-1",
		"lead-agent",
		"user-conv-1",
		"",
		RuntimeToolCallDTO("mission_prepare", "tool-prepare", map[string]any{
			"title":           "资产负债表可视化网页工具",
			"final_goal":      "根据上传的资产负债表 Excel 模板，生成一个网页工具",
			"idempotency_key": "balance-sheet-viz-prep-001",
		}),
		boundTools,
		testTime,
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Result["mission_id"] != "mission-created" || len(links) != 1 {
		t.Fatalf("prepared=%+v links=%+v", prepared, links)
	}
	afterPrepare := mustConversationView(t, repo, "conv-1")
	if len(afterPrepare.LinkedMissions) != 1 || afterPrepare.LinkedMissions[0].MissionID != "mission-created" {
		t.Fatalf("linked missions after prepare = %+v", afterPrepare.LinkedMissions)
	}
	if len(afterPrepare.Runs) != 1 || len(afterPrepare.Runs[0].ToolCalls) != 1 || afterPrepare.Runs[0].ToolCalls[0].Result["mission_id"] != "mission-created" {
		t.Fatalf("run tool calls after prepare = %+v", afterPrepare.Runs)
	}

	root, _, err := service.ApplyRuntimeToolCall(
		context.Background(),
		"conv-1",
		"run-1",
		"lead-agent",
		"user-conv-1",
		"",
		RuntimeToolCallDTO("mission_task_create_root", "tool-root", map[string]any{
			"goal":            "建立资产负债表可视化网页工具根任务",
			"idempotency_key": "balance-sheet-root-001",
		}),
		boundTools,
		testTime.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if root.Result["mission_id"] != "mission-created" || root.Result["task_id"] != "task-root" {
		t.Fatalf("root result = %+v", root)
	}
	if !commands.createTaskCalled || commands.createTaskCmd.MissionID.String() != "mission-created" {
		t.Fatalf("root task command = %+v", commands.createTaskCmd)
	}
	afterRoot := mustConversationView(t, repo, "conv-1")
	if len(afterRoot.Runs) != 1 || len(afterRoot.Runs[0].ToolCalls) != 2 || afterRoot.Runs[0].ToolCalls[1].Result["task_id"] != "task-root" {
		t.Fatalf("run tool calls after root = %+v", afterRoot.Runs)
	}
}

func TestServiceApplyRuntimeCoordinatorToolCarriesParentRunProvenance(t *testing.T) {
	repo := newMemoryRepository(t)
	seedRunningDiscussionRun(t, repo, "conv-runtime-plan", "responsibility-parent", testTime)
	commands := &capturingRunnableMissionCommands{capturingMissionCommands: &capturingMissionCommands{}}
	service := newTestService(ServiceOptions{
		Repository:     repo,
		MissionTooling: NewMissionToolApplier(commands),
		Clock:          fixedClock{now: testTime},
	})

	applied, _, err := service.ApplyRuntimeToolCall(
		context.Background(),
		"conv-runtime-plan",
		"responsibility-parent",
		"lead-agent",
		"user-conv-runtime-plan",
		"",
		RuntimeToolCallDTO("mission_coordinator_plan_parallel", "tool-runtime-plan", map[string]any{
			"mission_id":        "mission-1",
			"mission_task_id":   "task-root",
			"idempotency_key":   "runtime-plan-1",
			"worker_lanes_json": `[{"role":"evidence verifier","assignee_id":"agent-worker","goal":"Verify one evidence gap"}]`,
		}),
		[]DiscussionAgentBoundTool{{ToolID: "mission_coordinator_plan_parallel"}},
		testTime,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands.startCalls) != 1 || commands.startCalls[0].ParentRunID != "responsibility-parent" {
		t.Fatalf("runtime coordinator parent provenance = %+v", commands.startCalls)
	}
	if applied.Result["status"] != "accepted" || applied.Result["run_ids"] != "run-task-1" {
		t.Fatalf("runtime coordinator result = %+v", applied.Result)
	}
}

func TestServiceApplyRuntimeToolCallReplacesStaleMissionLink(t *testing.T) {
	repo := newMemoryRepository(t)
	seedRunningDiscussionRun(t, repo, "conv-stale", "run-stale", testTime)
	if err := repo.SaveMissionLink(context.Background(), LinkedMissionRecord{
		ConversationID: "conv-stale", MissionID: "mission-deleted", Title: "Deleted mission", Status: "active", CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	commands := &capturingMissionCommands{
		mission:   missioncontrol.MissionDTO{MissionID: "mission-recreated", Title: "Permission upgrade", Status: "active", CreatedAt: testTime},
		task:      missioncontrol.TaskCardDTO{TaskID: "root-mission-recreated", UpdatedAt: testTime},
		updateErr: missioncontrol.ErrProjectionNotFound,
	}
	service := newTestService(ServiceOptions{
		Repository: repo, MissionTooling: NewMissionToolApplier(commands), Clock: fixedClock{now: testTime},
	})

	prepared, links, err := service.ApplyRuntimeToolCall(
		context.Background(), "conv-stale", "run-stale", "lead-agent", "user-conv-stale", "",
		RuntimeToolCallDTO("mission_prepare", "tool-recreate", map[string]any{
			"title": "Permission upgrade", "final_goal": "Upgrade permissions", "root_goal": "Coordinate three agents",
		}),
		[]DiscussionAgentBoundTool{{ToolID: "mission_prepare"}}, testTime,
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Result["mission_id"] != "mission-recreated" || len(links) != 1 || links[0].MissionID != "mission-recreated" {
		t.Fatalf("prepared=%+v links=%+v", prepared, links)
	}
	view := mustConversationView(t, repo, "conv-stale")
	if len(view.LinkedMissions) != 1 || view.LinkedMissions[0].MissionID != "mission-recreated" {
		t.Fatalf("persisted mission links = %+v", view.LinkedMissions)
	}
}

type lifecycleReentrantDiscussionAgentRunner struct {
	service *Service
	request missioncontrol.CanonicalConversationMembershipSetRequest
}

func (r *lifecycleReentrantDiscussionAgentRunner) DiscussionAgentID() string {
	return "lead-agent"
}

func (r *lifecycleReentrantDiscussionAgentRunner) RunDiscussion(ctx context.Context, _ DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	if err := r.service.ReconcileMissionAgentMembers(ctx, r.request); err != nil {
		return DiscussionAgentResponse{}, err
	}
	return DiscussionAgentResponse{AssistantMessage: "Worker assigned", Status: "completed", RuntimeKind: "managed", SessionID: "provider-session-1"}, nil
}

func TestDiscussionTurnMissionMembershipReconciliationDoesNotRelockLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	conversationID := "conv-runtime-membership"
	missionID := "mission-1"
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: conversationID,
		Title:          "Runtime membership",
		Status:         "active",
		CreatedBy:      "user-1",
		TenantID:       "global",
		CreatedAt:      testTime,
		UpdatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, conversationID, "user-1")
	if err := repo.SaveMember(ctx, ConversationMemberRecord{
		ConversationID: conversationID,
		MemberKind:     "agent",
		MemberID:       "lead-agent",
		DisplayName:    "Lead",
		Role:           "Lead",
		CreatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{
		ConversationID: conversationID,
		MissionID:      missionID,
		Title:          "Mission",
		Status:         "active",
		CreatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}
	runner := &lifecycleReentrantDiscussionAgentRunner{}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "worker-agent", AgentInstanceID: "worker-agent", DisplayName: "Worker", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock: fixedClock{now: testTime},
	})
	runner.service = service
	runner.request = missioncontrol.CanonicalConversationMembershipSetRequest{
		ActorKind: "system", ActorID: "mission-lifecycle", TenantID: "global", ConversationID: conversationID,
		MissionID: missionID, AgentIDs: []string{"worker-agent"}, ManagedSource: "mission-task-assignee:global/mission-1",
	}

	done := make(chan error, 1)
	go func() {
		_, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, TenantID: "global"}, SendMessageCommand{
			ConversationID: conversationID, Content: "Start", IdempotencyKey: "runtime-membership-1",
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		release := service.beginRetryLifecycle(conversationID)
		release()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		t.Fatal("Mission membership reconciliation relocked the active discussion lifecycle")
	}
	view := mustConversationView(t, repo, conversationID)
	if member, ok := findConversationMember(view.Members, "agent", "worker-agent"); !ok || !conversationMemberHasManagedSource(member, runner.request.ManagedSource) {
		t.Fatalf("managed worker membership was not reconciled: %+v", view.Members)
	}
}

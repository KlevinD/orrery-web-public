package discussionsession

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
)

const missionToolTestAgentID = "coordinator-agent"

func missionToolTestViewer() missioncontrol.ViewerRef {
	return missioncontrol.ViewerRef{ViewerID: missionToolTestAgentID, ViewerKind: missioncontrol.ViewerKindAgent}
}

func TestMissionToolApplierCreatesMissionLinksFromDiscussionAgentToolResults(t *testing.T) {
	missions := &capturingMissionCommands{mission: missioncontrol.MissionDTO{MissionID: "mission-1", Title: "Launch plan", Status: "active", CreatedAt: testTime}}
	applier := NewMissionToolApplier(missions)

	calls := []ToolCallResult{{
		Name: "mission_brief_create",
		Arguments: map[string]string{
			"title":              "Launch plan",
			"final_goal":         "Coordinate launch",
			"business_direction": "Growth",
		},
	}}
	links, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", calls)
	if err != nil {
		t.Fatal(err)
	}

	if !missions.createCalled {
		t.Fatalf("mission create was not called")
	}
	if missions.createCmd.Title != "Launch plan" || missions.createCmd.FinalGoal != "Coordinate launch" || missions.createCmd.IdempotencyKey == "" {
		t.Fatalf("create command = %+v", missions.createCmd)
	}
	if len(links) != 1 || links[0].MissionID != "mission-1" || links[0].Title != "Launch plan" {
		t.Fatalf("links = %+v", links)
	}
	if calls[0].Result["mission_id"] != "mission-1" || calls[0].Result["operation"] != "created" {
		t.Fatalf("tool result = %+v, want created mission marker", calls[0].Result)
	}
}

func TestMissionToolApplierPreparesMissionAndRootTaskAtomically(t *testing.T) {
	missions := &capturingMissionCommands{
		mission: missioncontrol.MissionDTO{MissionID: "mission-created", Title: "Snake", Status: "active", CreatedAt: testTime},
		task:    missioncontrol.TaskCardDTO{TaskID: "root-mission-created", UpdatedAt: testTime},
	}
	applier := NewMissionToolApplier(missions)

	calls := toolCallResults([]ToolCallDTO{{
		ToolCallID: "prepare-snake-1",
		Name:       "mission_prepare",
		Arguments: map[string]string{
			"title":                 "Snake",
			"final_goal":            "Build a wraparound HTML snake game",
			"root_goal":             "Team goal: deliver one playable wraparound HTML snake game",
			"expected_deliverables": "snake.html",
			"idempotency_key":       "prepare-snake",
		},
	}})
	links, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", calls)
	if err != nil {
		t.Fatal(err)
	}

	if !missions.createCalled || !missions.createTaskCalled {
		t.Fatalf("mission_prepare should create mission and root task, create=%v task=%v", missions.createCalled, missions.createTaskCalled)
	}
	if missions.createCmd.Title != "Snake" || missions.createCmd.FinalGoal != "Build a wraparound HTML snake game" || missions.createCmd.IdempotencyKey != "prepare-snake" {
		t.Fatalf("create command = %+v", missions.createCmd)
	}
	if missions.createTaskCmd.MissionID != missioncore.MissionID("mission-created") ||
		missions.createTaskCmd.TaskID != missioncore.TaskID("root-mission-created") ||
		missions.createTaskCmd.Goal != "Team goal: deliver one playable wraparound HTML snake game" {
		t.Fatalf("root task command = %+v", missions.createTaskCmd)
	}
	if missions.createTaskCmd.AssigneeID != "" || missions.createTaskCmd.WorkerPersonalGoal != "" {
		t.Fatalf("mission_prepare root task should stay unassigned team goal: %+v", missions.createTaskCmd)
	}
	if !strings.HasPrefix(missions.createTaskCmd.IdempotencyKey, "root:mission-created:") {
		t.Fatalf("root task idempotency = %q", missions.createTaskCmd.IdempotencyKey)
	}
	if len(links) != 1 || links[0].MissionID != "mission-created" {
		t.Fatalf("links = %+v", links)
	}
	if calls[0].Result["mission_id"] != "mission-created" || calls[0].Result["task_id"] != "root-mission-created" || calls[0].Result["operation"] != "created" {
		t.Fatalf("tool result = %+v", calls[0].Result)
	}
}

func TestPrepareRootTaskCallDerivesIdempotencyFromEffectiveTaskRequest(t *testing.T) {
	first := prepareRootTaskCall(ToolCallResult{Name: "mission_prepare", Arguments: map[string]string{
		"root_goal":             "Coordinate the upgrade",
		"scope":                 "Permission model",
		"expected_deliverables": "Design\nMigration plan",
		"tool_call_id":          "provider-call-1",
		"root_idempotency_key":  "agent-root-key-1",
	}}, "mission-1")
	retry := prepareRootTaskCall(ToolCallResult{Name: "mission_prepare", Arguments: map[string]string{
		"root_goal":             "Coordinate the upgrade",
		"scope":                 "Permission model",
		"expected_deliverables": "Design\nMigration plan",
		"tool_call_id":          "provider-call-2",
		"root_idempotency_key":  "agent-root-key-2",
	}}, "mission-1")

	firstKey := first.Arguments["idempotency_key"]
	if firstKey == "" || firstKey != retry.Arguments["idempotency_key"] {
		t.Fatalf("equivalent root task keys first=%q retry=%q", firstKey, retry.Arguments["idempotency_key"])
	}

	changedGoal := prepareRootTaskCall(ToolCallResult{Name: "mission_prepare", Arguments: map[string]string{
		"root_goal":             "Coordinate the upgraded permission rollout",
		"scope":                 "Permission model",
		"expected_deliverables": "Design\nMigration plan",
		"tool_call_id":          "provider-call-1",
		"root_idempotency_key":  "agent-root-key-1",
	}}, "mission-1")
	if changedGoal.Arguments["idempotency_key"] == firstKey {
		t.Fatalf("changed root goal reused idempotency key %q", firstKey)
	}

	changedDeliverables := prepareRootTaskCall(ToolCallResult{Name: "mission_prepare", Arguments: map[string]string{
		"root_goal":             "Coordinate the upgrade",
		"scope":                 "Permission model",
		"expected_deliverables": "Design\nMigration plan\nRollback plan",
		"tool_call_id":          "provider-call-1",
		"root_idempotency_key":  "agent-root-key-1",
	}}, "mission-1")
	if changedDeliverables.Arguments["idempotency_key"] == firstKey {
		t.Fatalf("changed root deliverables reused idempotency key %q", firstKey)
	}
}

func TestMissionToolApplierUsesConversationOriginPortForMissionPrepare(t *testing.T) {
	missions := &originAwareCapturingMissionCommands{
		capturingMissionCommands: capturingMissionCommands{
			mission: missioncontrol.MissionDTO{MissionID: "mission-created", Title: "PA launch", Status: "active", CreatedAt: testTime},
			task:    missioncontrol.TaskCardDTO{TaskID: "root-mission-created", UpdatedAt: testTime},
		},
	}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-pa-origin", []ToolCallResult{{
		Name: "mission_prepare",
		Arguments: map[string]string{
			"title": "PA launch", "final_goal": "Keep the assistant identity", "idempotency_key": "prepare-pa-origin",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !missions.originCreateCalled || missions.originConversationID != "conv-pa-origin" {
		t.Fatalf("origin-aware create = called %v conversation %q", missions.originCreateCalled, missions.originConversationID)
	}
	if missions.createCalled || missions.originCmd.SourcePersonalAssistantConversationID != "" {
		t.Fatalf("tool arguments must not supply source provenance: regular=%v command=%+v", missions.createCalled, missions.originCmd)
	}
}

func TestMissionToolApplierUpdatesExistingMissionAndRootTaskWithStableID(t *testing.T) {
	missions := &capturingMissionCommands{
		updatedMission: missioncontrol.MissionDTO{MissionID: "mission-existing", Title: "Snake v2", FinalGoal: "Ship the clarified snake game", Status: "active", CreatedAt: testTime},
		task:           missioncontrol.TaskCardDTO{TaskID: "root-mission-existing", UpdatedAt: testTime},
	}
	applier := NewMissionToolApplier(missions)

	calls := toolCallResults([]ToolCallDTO{{
		ToolCallID: "prepare-existing-v2",
		Name:       "mission_prepare",
		Arguments: map[string]string{
			"title":      "Snake v2",
			"final_goal": "Ship the clarified snake game",
			"root_goal":  "Team goal: update the snake game scope and keep staffing separate",
		},
	}})
	links, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", calls, LinkedMissionDTO{MissionID: "mission-existing", Title: "Snake", Status: "active", CreatedAt: testTime})
	if err != nil {
		t.Fatal(err)
	}

	if missions.createCalled {
		t.Fatalf("mission_prepare should update existing mission instead of creating another: %+v", missions.createCmd)
	}
	if !missions.updateCalled || !missions.createTaskCalled {
		t.Fatalf("mission_prepare should update mission and upsert stable root task, update=%v task=%v", missions.updateCalled, missions.createTaskCalled)
	}
	if missions.updateCmd.MissionID != missioncore.MissionID("mission-existing") || missions.updateCmd.FinalGoal != "Ship the clarified snake game" ||
		missions.updateCmd.IdempotencyKey != "prepare-existing-v2" {
		t.Fatalf("update command = %+v", missions.updateCmd)
	}
	if missions.createTaskCmd.MissionID != missioncore.MissionID("mission-existing") ||
		missions.createTaskCmd.TaskID != missioncore.TaskID("root-mission-existing") ||
		missions.createTaskCmd.Goal != "Team goal: update the snake game scope and keep staffing separate" {
		t.Fatalf("root task command = %+v", missions.createTaskCmd)
	}
	if !strings.HasPrefix(missions.createTaskCmd.IdempotencyKey, "root:mission-existing:") {
		t.Fatalf("root task idempotency = %q", missions.createTaskCmd.IdempotencyKey)
	}
	if len(links) != 1 || links[0].MissionID != "mission-existing" {
		t.Fatalf("links = %+v", links)
	}
	if calls[0].Result["mission_id"] != "mission-existing" || calls[0].Result["task_id"] != "root-mission-existing" || calls[0].Result["operation"] != "updated" {
		t.Fatalf("tool result = %+v", calls[0].Result)
	}
}

func TestMissionToolApplierRecreatesMissionWhenLinkedProjectionIsMissing(t *testing.T) {
	missions := &capturingMissionCommands{
		mission:   missioncontrol.MissionDTO{MissionID: "mission-recreated", Title: "Permission upgrade", Status: "active", CreatedAt: testTime},
		task:      missioncontrol.TaskCardDTO{TaskID: "root-mission-recreated", UpdatedAt: testTime},
		updateErr: missioncontrol.ErrProjectionNotFound,
	}
	applier := NewMissionToolApplier(missions)
	calls := []ToolCallResult{{
		Name: "mission_prepare",
		Arguments: map[string]string{
			"title":      "Permission upgrade",
			"final_goal": "Upgrade Orrery user data permissions",
			"root_goal":  "Coordinate three agents on the permission upgrade",
		},
	}}

	links, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", calls,
		LinkedMissionDTO{MissionID: "mission-deleted", Title: "Deleted mission", Status: "active", CreatedAt: testTime})
	if err != nil {
		t.Fatal(err)
	}
	if !missions.updateCalled || !missions.createCalled || !missions.createTaskCalled {
		t.Fatalf("recovery calls update=%v create=%v task=%v", missions.updateCalled, missions.createCalled, missions.createTaskCalled)
	}
	if len(links) != 1 || links[0].MissionID != "mission-recreated" || links[0].ReplacesMissionID != "mission-deleted" {
		t.Fatalf("replacement links = %+v", links)
	}
	if calls[0].Result["operation"] != "created" || calls[0].Result["mission_id"] != "mission-recreated" {
		t.Fatalf("tool result = %+v", calls[0].Result)
	}
}

func TestMissionToolApplierPassesInitialHumanAgentToCreatedMission(t *testing.T) {
	missions := &capturingMissionCommands{mission: missioncontrol.MissionDTO{MissionID: "mission-1", Title: "Launch plan", Status: "active", CreatedAt: testTime}}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{{
		Name: "mission_brief_create",
		Arguments: map[string]string{
			"title":                      "Launch plan",
			"final_goal":                 "Coordinate launch",
			"initial_human_agent_id":     "user-1",
			"initial_human_display_name": "本地用户",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if missions.createViewer.ViewerID != missionToolTestAgentID || missions.createViewer.ViewerKind != missioncontrol.ViewerKindAgent {
		t.Fatalf("mission should still be created by discussion agent viewer: %+v", missions.createViewer)
	}
	if missions.createCmd.InitialHumanAgentID != "user-1" {
		t.Fatalf("initial human agent id was not propagated: %+v", missions.createCmd)
	}
	if missions.createCmd.InitialHumanDisplayName != "本地用户" {
		t.Fatalf("initial human display name was not propagated: %+v", missions.createCmd)
	}
}

func TestMissionToolApplierSkipsEmptyMissionBriefCreateTool(t *testing.T) {
	missions := &capturingMissionCommands{mission: missioncontrol.MissionDTO{MissionID: "mission-1", Title: "Launch plan", Status: "active", CreatedAt: testTime}}
	applier := NewMissionToolApplier(missions)

	links, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{{
		Name:      "mission_brief_create",
		Arguments: map[string]string{},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if missions.createCalled {
		t.Fatalf("empty mission brief tool should not create a mission: %+v", missions.createCmd)
	}
	if len(links) != 0 {
		t.Fatalf("links = %+v", links)
	}
}

func TestMissionToolApplierUpdatesExistingMissionBriefOnRepeatedCreate(t *testing.T) {
	missions := &capturingMissionCommands{updatedMission: missioncontrol.MissionDTO{MissionID: "mission-existing", Title: "Adjusted launch", FinalGoal: "Ship the clarified launch plan", Status: "active", CreatedAt: testTime}}
	applier := NewMissionToolApplier(missions)
	calls := []ToolCallResult{{
		Name: "mission_brief_create",
		Arguments: map[string]string{
			"title":      "Adjusted launch",
			"final_goal": "Ship the clarified launch plan",
		},
	}}

	links, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", calls, LinkedMissionDTO{MissionID: "mission-existing", Title: "Launch plan", Status: "active", CreatedAt: testTime})
	if err != nil {
		t.Fatal(err)
	}

	if missions.createCalled {
		t.Fatalf("repeated brief should update existing mission, not create a second mission: %+v", missions.createCmd)
	}
	if !missions.updateCalled || missions.updateCmd.MissionID.String() != "mission-existing" || missions.updateCmd.Title != "Adjusted launch" || missions.updateCmd.FinalGoal != "Ship the clarified launch plan" {
		t.Fatalf("update command = %+v", missions.updateCmd)
	}
	if len(links) != 1 || links[0].MissionID != "mission-existing" || links[0].Title != "Adjusted launch" {
		t.Fatalf("links = %+v", links)
	}
	if calls[0].Result["mission_id"] != "mission-existing" || calls[0].Result["operation"] != "updated" {
		t.Fatalf("tool result = %+v, want existing mission update marker", calls[0].Result)
	}
}

func TestMissionToolApplierUsesFinalGoalForMissingMissionTitleAndIdempotency(t *testing.T) {
	missions := &capturingMissionCommands{mission: missioncontrol.MissionDTO{MissionID: "mission-1", Title: "Ship snake game", Status: "active", CreatedAt: testTime}}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{{
		Name: "mission_brief_create",
		Arguments: map[string]string{
			"final_goal": "Ship a simple HTML snake game quickly",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if missions.createCmd.Title != "Ship a simple HTML snake game quickly" || missions.createCmd.FinalGoal != "Ship a simple HTML snake game quickly" {
		t.Fatalf("create command should use final_goal as title fallback: %+v", missions.createCmd)
	}
	if missions.createCmd.IdempotencyKey == "" {
		t.Fatalf("create command should have idempotency fallback: %+v", missions.createCmd)
	}
}

func TestMissionToolApplierCreatesRootTaskFromDiscussionAgentToolResults(t *testing.T) {
	missions := &capturingMissionCommands{task: missioncontrol.TaskCardDTO{TaskID: "task-root", UpdatedAt: testTime}}
	applier := NewMissionToolApplier(missions)

	calls := []ToolCallResult{{
		Name: "mission_task_create_root",
		Arguments: map[string]string{
			"mission_id":            "mission-1",
			"goal":                  "Coordinate launch work",
			"worker_personal_goal":  "Own launch coordination from kickoff to QA",
			"scope":                 "Root delivery task",
			"priority":              "high",
			"expected_deliverables": "Launch checklist\nRisk register",
			"required_evidence":     `["Published launch checklist","QA run record"]`,
			"tool_call_id":          "tool-root",
		},
	}}
	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", calls)
	if err != nil {
		t.Fatal(err)
	}

	if !missions.createTaskCalled {
		t.Fatal("task create was not called")
	}
	if missions.createTaskCmd.MissionID != missioncore.MissionID("mission-1") || missions.createTaskCmd.Goal != "Coordinate launch work" {
		t.Fatalf("create task command = %+v", missions.createTaskCmd)
	}
	if missions.createTaskCmd.IdempotencyKey != "tool-root" || len(missions.createTaskCmd.ExpectedDeliverables) != 2 || len(missions.createTaskCmd.AcceptanceCriteria) != 1 || len(missions.createTaskCmd.EvidenceRequirements) != 2 {
		t.Fatalf("create task idempotency/deliverables = %+v", missions.createTaskCmd)
	}
	if missions.createTaskCmd.EvidenceRequirements[0].Description != "Published launch checklist" || !missions.createTaskCmd.EvidenceRequirements[0].Required {
		t.Fatalf("create task evidence requirements = %+v", missions.createTaskCmd.EvidenceRequirements)
	}
	if calls[0].Result["task_id"] != "task-root" || calls[0].Result["mission_id"] != "mission-1" {
		t.Fatalf("task tool result was not recorded: %+v", calls[0].Result)
	}
}

func TestMissionToolApplierRejectsRootTaskWorkerInternalQAAssignee(t *testing.T) {
	missions := &capturingMissionCommands{task: missioncontrol.TaskCardDTO{TaskID: "task-root", UpdatedAt: testTime}}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{{
		Name: "mission_task_create_root",
		Arguments: map[string]string{
			"mission_id":   "mission-1",
			"goal":         "Prepare QA plan",
			"assignee_id":  "qa-check-agent",
			"tool_call_id": "tool-root",
		},
	}})
	if err == nil {
		t.Fatal("expected worker-internal QA assignee to fail")
	}
	if !strings.Contains(err.Error(), "worker-internal QA") {
		t.Fatalf("error = %v, want worker-internal QA rejection", err)
	}
	if missions.createTaskCalled {
		t.Fatalf("created task despite worker-internal QA assignee: %+v", missions.createTaskCmd)
	}
}

func TestMissionToolApplierUsesNewMissionAsDefaultForFollowingTaskCalls(t *testing.T) {
	missions := &capturingMissionCommands{
		mission: missioncontrol.MissionDTO{MissionID: "mission-created", Title: "Launch plan", Status: "active", CreatedAt: testTime},
		task:    missioncontrol.TaskCardDTO{TaskID: "task-root", UpdatedAt: testTime},
	}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{
		{
			Name: "mission_brief_create",
			Arguments: map[string]string{
				"title":           "Launch plan",
				"final_goal":      "Coordinate launch",
				"idempotency_key": "mission-from-agent",
			},
		},
		{
			Name: "mission_task_create_root",
			Arguments: map[string]string{
				"goal":                 "Coordinate launch work",
				"worker_personal_goal": "Own launch coordination",
				"assignee_id":          "profile-worker",
				"idempotency_key":      "root-task-from-agent",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !missions.createTaskCalled {
		t.Fatal("task create was not called")
	}
	if missions.createTaskCmd.MissionID != missioncore.MissionID("mission-created") {
		t.Fatalf("task mission id = %q, want mission-created", missions.createTaskCmd.MissionID)
	}
}

func TestMissionToolApplierUsesNewRootTaskAsDefaultForFollowingDelegationCalls(t *testing.T) {
	missions := &capturingMissionCommands{
		mission: missioncontrol.MissionDTO{MissionID: "mission-created", Title: "Launch plan", Status: "active", CreatedAt: testTime},
		task:    missioncontrol.TaskCardDTO{TaskID: "task-root", UpdatedAt: testTime},
	}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{
		{
			Name: "mission_brief_create",
			Arguments: map[string]string{
				"title":           "Launch plan",
				"final_goal":      "Coordinate launch",
				"idempotency_key": "mission-from-agent",
			},
		},
		{
			Name: "mission_task_create_root",
			Arguments: map[string]string{
				"goal":                 "Coordinate launch work",
				"worker_personal_goal": "Own launch coordination",
				"assignee_id":          "agent-worker",
				"idempotency_key":      "root-task-from-agent",
			},
		},
		{
			Name: "mission_task_delegate",
			Arguments: map[string]string{
				"assignee_id":     "agent-worker-2",
				"reason":          "Delegate after root task creation",
				"idempotency_key": "delegate-from-agent",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !missions.assignCalled {
		t.Fatal("task delegation was not called")
	}
	if missions.assignCmd.MissionID != missioncore.MissionID("mission-created") || missions.assignCmd.TaskID != missioncore.TaskID("task-root") {
		t.Fatalf("assign command = %+v", missions.assignCmd)
	}
}

func TestMissionToolApplierDelegatesTaskFromDiscussionAgentToolResults(t *testing.T) {
	missions := &capturingMissionCommands{}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{{
		Name: "mission_task_delegate",
		Arguments: map[string]string{
			"mission_id":      "mission-1",
			"task_id":         "task-root",
			"assignee_id":     "agent-worker",
			"reason":          "Best fit for launch coordination",
			"idempotency_key": "delegate-1",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if !missions.assignCalled {
		t.Fatal("task assign was not called")
	}
	if missions.assignCmd.MissionID != missioncore.MissionID("mission-1") || missions.assignCmd.TaskID != missioncore.TaskID("task-root") || missions.assignCmd.AssigneeID != "agent-worker" {
		t.Fatalf("assign command = %+v", missions.assignCmd)
	}
	if missions.assignCmd.IdempotencyKey != "delegate-1" || missions.assignCmd.Reason != "Best fit for launch coordination" {
		t.Fatalf("assign command idempotency/reason = %+v", missions.assignCmd)
	}
}

func TestMissionToolApplierRejectsDelegateWorkerInternalQAAssignee(t *testing.T) {
	missions := &capturingMissionCommands{}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{{
		Name: "mission_task_delegate",
		Arguments: map[string]string{
			"mission_id":      "mission-1",
			"task_id":         "task-root",
			"assignee_id":     "qa-plan-agent",
			"reason":          "Direct QA planning should stay internal to the worker",
			"idempotency_key": "delegate-qa",
		},
	}})
	if err == nil {
		t.Fatal("expected worker-internal QA assignee to fail")
	}
	if !strings.Contains(err.Error(), "worker-internal QA") {
		t.Fatalf("error = %v, want worker-internal QA rejection", err)
	}
	if missions.assignCalled {
		t.Fatalf("assigned task despite worker-internal QA assignee: %+v", missions.assignCmd)
	}
}

func TestMissionToolApplierSubmitsConversationCloseoutAsMissionResult(t *testing.T) {
	missions := &capturingMissionCommands{}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{{
		Name: "discussion_close",
		Arguments: map[string]string{
			"mission_id":      "mission-1",
			"mission_task_id": "task-1",
			"note":            "Coordinator answered directly with a verified summary.",
			"idempotency_key": "closeout-1",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if !missions.submitCalled {
		t.Fatal("closeout result was not submitted")
	}
	if missions.submitCmd.MissionID != missioncore.MissionID("mission-1") || missions.submitCmd.TaskID != missioncore.TaskID("task-1") {
		t.Fatalf("submit command target = %+v", missions.submitCmd)
	}
	if missions.submitCmd.IdempotencyKey != "closeout-1" || missions.submitCmd.Result.Summary.Text != "Coordinator answered directly with a verified summary." {
		t.Fatalf("submit command result = %+v", missions.submitCmd)
	}
	if len(missions.submitCmd.Result.Deliverables) != 1 || missions.submitCmd.Result.Deliverables[0].Title != "Coordinator answered directly with a verified summary." {
		t.Fatalf("closeout deliverables = %+v", missions.submitCmd.Result.Deliverables)
	}
}

func TestMissionToolApplierUsesGenericConversationCloseoutFallback(t *testing.T) {
	missions := &capturingMissionCommands{}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, "conv-1", []ToolCallResult{{
		Name: "discussion_close",
		Arguments: map[string]string{
			"mission_id":      "mission-1",
			"mission_task_id": "task-1",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if !missions.submitCalled {
		t.Fatal("closeout result was not submitted")
	}
	if missions.submitCmd.Result.Summary.Text != "Discussion answer closed." {
		t.Fatalf("closeout fallback summary = %q", missions.submitCmd.Result.Summary.Text)
	}
	if len(missions.submitCmd.Result.Deliverables) != 1 || missions.submitCmd.Result.Deliverables[0].Title != "Discussion answer closed." {
		t.Fatalf("closeout fallback deliverables = %+v", missions.submitCmd.Result.Deliverables)
	}
}

func TestMissionToolApplierSkipsConversationCloseoutWithoutMissionTask(t *testing.T) {
	missions := &capturingMissionCommands{}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{{
		Name: "discussion_close",
		Arguments: map[string]string{
			"note": "Simple chat answer only.",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if missions.submitCalled {
		t.Fatalf("closeout without mission task must not submit fake evidence: %+v", missions.submitCmd)
	}
}

func TestMissionToolApplierDoesNotExecuteUnregisteredStaffingTools(t *testing.T) {
	missions := &capturingMissionCommands{}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{
		{
			Name: "staffing_request",
			Arguments: map[string]string{
				"mission_id":      "mission-1",
				"role":            "Worker",
				"capability_gap":  "Launch coordination",
				"reason":          "Need a launch worker",
				"idempotency_key": "staffing-1",
			},
		},
		{
			Name: "staffing_appoint",
			Arguments: map[string]string{
				"mission_id":      "mission-1",
				"profile_id":      "profile-worker",
				"role":            "Worker",
				"responsibility":  "Own launch coordination",
				"idempotency_key": "appoint-1",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if missions.createCalled || missions.createTaskCalled || missions.assignCalled {
		t.Fatalf("unregistered staffing tools must not execute mission commands: create=%v task=%v assign=%v", missions.createCalled, missions.createTaskCalled, missions.assignCalled)
	}
}

func TestMissionToolApplierDoesNotResolveUnregisteredAppointmentTools(t *testing.T) {
	missions := &capturingMissionCommands{}
	applier := NewMissionToolApplier(missions)

	_, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{{
		Name: "staffing_appoint",
		Arguments: map[string]string{
			"mission_id":      "mission-1",
			"agent_id":        "agent-worker",
			"role":            "Worker",
			"responsibility":  "Own launch coordination",
			"idempotency_key": "appoint-1",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if missions.createCalled || missions.createTaskCalled || missions.assignCalled {
		t.Fatalf("unregistered appointment tool must not execute mission commands: create=%v task=%v assign=%v", missions.createCalled, missions.createTaskCalled, missions.assignCalled)
	}
}

func TestMissionToolApplierAcceptsStandardToolNames(t *testing.T) {
	missions := &capturingMissionCommands{mission: missioncontrol.MissionDTO{MissionID: "mission-1", Title: "Launch plan", Status: "active", CreatedAt: testTime}}
	applier := NewMissionToolApplier(missions)

	links, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", []ToolCallResult{
		{
			Name: "mission.brief.create",
			Arguments: map[string]string{
				"title":      "Launch plan",
				"final_goal": "Coordinate launch",
			},
		},
		{
			Name: "mission_task_create_root",
			Arguments: map[string]string{
				"mission_id":   "mission-1",
				"goal":         "Coordinate launch work",
				"tool_call_id": "tool-root",
			},
		},
		{
			Name: "mission_task_delegate",
			Arguments: map[string]string{
				"mission_id":      "mission-1",
				"task_id":         "task-root",
				"assignee_id":     "agent-worker",
				"idempotency_key": "delegate-1",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(links) != 1 || links[0].MissionID != "mission-1" {
		t.Fatalf("links = %+v", links)
	}
	if !missions.createCalled || !missions.createTaskCalled || !missions.assignCalled {
		t.Fatalf("mission commands create=%v task=%v assign=%v", missions.createCalled, missions.createTaskCalled, missions.assignCalled)
	}
}

type capturingMissionCommands struct {
	createCalled     bool
	createViewer     missioncontrol.ViewerRef
	createCmd        missioncontrol.CreateMissionCommand
	mission          missioncontrol.MissionDTO
	updateCalled     bool
	updateCmd        missioncontrol.UpdateMissionBriefCommand
	updatedMission   missioncontrol.MissionDTO
	updateErr        error
	createTaskCalled bool
	createTaskCmd    missioncontrol.CreateTaskCommand
	createTaskCalls  []missioncontrol.CreateTaskCommand
	task             missioncontrol.TaskCardDTO
	assignCalled     bool
	assignCmd        missioncontrol.AssignTaskCommand
	assignCalls      []missioncontrol.AssignTaskCommand
	submitCalled     bool
	submitCmd        missioncontrol.SubmitResultCommand
}

type originAwareCapturingMissionCommands struct {
	capturingMissionCommands
	originCreateCalled   bool
	originConversationID string
	originCmd            missioncontrol.CreateMissionCommand
}

func (f *originAwareCapturingMissionCommands) CreateMissionFromDiscussion(_ context.Context, viewer missioncontrol.ViewerRef, conversationID string, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	f.originCreateCalled = true
	f.originConversationID = conversationID
	f.originCmd = cmd
	f.createViewer = viewer
	return f.mission, nil
}

func (f *capturingMissionCommands) CreateMission(_ context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	f.createCalled = true
	f.createViewer = viewer
	f.createCmd = cmd
	return f.mission, nil
}

func (f *capturingMissionCommands) UpdateMissionBrief(_ context.Context, _ missioncontrol.ViewerRef, cmd missioncontrol.UpdateMissionBriefCommand) (missioncontrol.MissionDTO, error) {
	f.updateCalled = true
	f.updateCmd = cmd
	return f.updatedMission, f.updateErr
}

func (f *capturingMissionCommands) CreateTask(_ context.Context, _ missioncontrol.ViewerRef, cmd missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	f.createTaskCalled = true
	f.createTaskCmd = cmd
	f.createTaskCalls = append(f.createTaskCalls, cmd)
	if f.task.TaskID != "" {
		return f.task, nil
	}
	return missioncontrol.TaskCardDTO{TaskID: "task-" + strconv.Itoa(len(f.createTaskCalls)), UpdatedAt: time.Date(2026, 6, 2, 1, 3, 0, 0, time.UTC)}, nil
}

func (f *capturingMissionCommands) AssignTask(_ context.Context, _ missioncontrol.ViewerRef, cmd missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.assignCalled = true
	f.assignCmd = cmd
	f.assignCalls = append(f.assignCalls, cmd)
	return missioncontrol.CommandReceiptDTO{CommandID: "assign-1", Status: "accepted", MissionID: missioncore.MissionID("mission-1").String(), TaskID: missioncore.TaskID("task-1").String()}, nil
}

func (f *capturingMissionCommands) SubmitAgentResult(_ context.Context, _ missioncontrol.ViewerRef, cmd missioncontrol.SubmitResultCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.submitCalled = true
	f.submitCmd = cmd
	return missioncontrol.CommandReceiptDTO{CommandID: "submit-1", Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(), AcceptedAt: testTime}, nil
}

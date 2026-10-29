package discussionsession

import (
	"fmt"
	"strings"
	"testing"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestMergeLinkedMissionsReplacesStaleMission(t *testing.T) {
	merged := mergeLinkedMissions(
		[]LinkedMissionDTO{{MissionID: "mission-deleted", Title: "Deleted mission"}},
		[]LinkedMissionDTO{{MissionID: "mission-new", Title: "New mission", ReplacesMissionID: "mission-deleted"}},
	)

	if len(merged) != 1 || merged[0].MissionID != "mission-new" {
		t.Fatalf("merged missions = %+v", merged)
	}
}

func TestMissionToolingFailureReasonUsesGenericAgentDirectoryGuidance(t *testing.T) {
	reason := missionToolingFailureReason([]ToolCallDTO{{
		Name:      "mission_task_create_root",
		Arguments: map[string]string{"assignee_id": "agent-missing"},
	}}, fmt.Errorf("%w: assignee_id must reference an enabled agent profile", missioncontrol.ErrInvalidRequest))

	for _, want := range []string{
		"当前 Agent 已经尝试创建或分配 Mission 任务",
		"agent-missing",
		"人员目录",
		"请选择真实可用成员",
	} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason missing %q: %s", want, reason)
		}
	}
	for _, unexpected := range []string{"PM", "HR"} {
		if strings.Contains(reason, unexpected) {
			t.Fatalf("reason should not mention %s: %s", unexpected, reason)
		}
	}
}

func TestOrchestrationStopWarningPreservesDirectAnswerAfterMissionPrepare(t *testing.T) {
	applied := []LinkedMissionDTO{{MissionID: "mission-1"}}
	calls := []ToolCallDTO{{
		Name:   "mission_prepare",
		Status: "completed",
		Result: map[string]string{"mission_id": "mission-1", "task_id": "root-mission-1"},
	}}

	message := "[@本地用户](mention://user/user-1) 这是我直接给出的完整方案。"
	if err := orchestrationStopError(applied, calls, message); err != nil {
		t.Fatalf("orchestrationStopError = %v, want direct answer preserved", err)
	}
	warning := orchestrationStopWarning(applied, calls, message)
	if warning != missionPrepareDirectAnswerWarning || !strings.Contains(warning, "可能并非专业回答") {
		t.Fatalf("orchestrationStopWarning = %q", warning)
	}
}

func TestOrchestrationStopErrorStillRejectsMissionPrepareWithoutAnswerOrHandoff(t *testing.T) {
	applied := []LinkedMissionDTO{{MissionID: "mission-1"}}
	calls := []ToolCallDTO{{
		Name:   "mission_prepare",
		Status: "completed",
		Result: map[string]string{"mission_id": "mission-1", "task_id": "root-mission-1"},
	}}

	err := orchestrationStopError(applied, calls, "")
	if err == nil || !strings.Contains(err.Error(), "mission_prepare") || !strings.Contains(err.Error(), "discussion_member_add") {
		t.Fatalf("orchestrationStopError = %v, want missing handoff diagnostic", err)
	}
}

func TestOrchestrationStopErrorAllowsMissionPrepareWithAgentHandoff(t *testing.T) {
	applied := []LinkedMissionDTO{{MissionID: "mission-1"}}
	calls := []ToolCallDTO{{
		Name:   "mission_prepare",
		Status: "completed",
		Result: map[string]string{"mission_id": "mission-1", "task_id": "root-mission-1"},
	}}

	if err := orchestrationStopError(applied, calls, "[@秦若兰](mention://agent/staffing-agent) 请为这个 Mission 安排执行人。"); err != nil {
		t.Fatalf("orchestrationStopError = %v, want nil", err)
	}
	if warning := orchestrationStopWarning(applied, calls, "[@秦若兰](mention://agent/staffing-agent) 请为这个 Mission 安排执行人。"); warning != "" {
		t.Fatalf("orchestrationStopWarning = %q, want empty", warning)
	}
}

func TestOrchestrationStopErrorRejectsPlainTextMissionCompletion(t *testing.T) {
	applied := []LinkedMissionDTO{{MissionID: "mission-1"}}
	calls := []ToolCallDTO{{
		Name:   "mission_task_create_root",
		Status: "completed",
		Result: map[string]string{"mission_id": "mission-1", "task_id": "root-mission-1"},
	}}

	err := orchestrationStopError(applied, calls, "[@本地用户](mention://user/user-1) 任务已完成，目标已经交付。")
	if err == nil || !strings.Contains(err.Error(), "discussion_close") {
		t.Fatalf("orchestrationStopError = %v, want discussion_close diagnostic", err)
	}
}

func TestOrchestrationStopErrorAllowsKnowledgeSummaryMentioningCompletedTasks(t *testing.T) {
	applied := []LinkedMissionDTO{{MissionID: "mission-1"}}
	calls := []ToolCallDTO{{
		Name:   string(toolstd.KnowledgeSearch),
		Status: "completed",
	}}

	message := "现有资料列出了已完成任务、负责人和下一步，但不足以判断整体进度。"
	if missionCompletionClaimedWithoutClose(applied, calls, message) {
		t.Fatal("knowledge summary must not be treated as a Mission completion claim")
	}
}

func TestOrchestrationStopErrorAllowsDiscussionCloseCompletion(t *testing.T) {
	applied := []LinkedMissionDTO{{MissionID: "mission-1"}}
	calls := []ToolCallDTO{
		{
			Name:   "mission_task_create_root",
			Status: "completed",
			Result: map[string]string{"mission_id": "mission-1", "task_id": "root-mission-1"},
		},
		{
			Name:   "discussion_close",
			Status: "completed",
			Result: map[string]string{"mission_id": "mission-1", "task_id": "root-mission-1"},
		},
	}

	if err := orchestrationStopError(applied, calls, "[@本地用户](mention://user/user-1) 任务已完成，目标已经交付。"); err != nil {
		t.Fatalf("orchestrationStopError = %v, want nil", err)
	}
}

func TestOrchestrationStopErrorDoesNotTreatPendingDiscussionCloseAsCompletion(t *testing.T) {
	applied := []LinkedMissionDTO{{MissionID: "mission-1"}}
	calls := []ToolCallDTO{{
		Name:   "discussion_close",
		Status: "completed",
		Result: map[string]string{"status": MissionClosePending, "mission_id": "mission-1", "task_id": "task-worker"},
	}}

	err := orchestrationStopError(applied, calls, "Mission completed.")
	if err == nil || !strings.Contains(err.Error(), "discussion_close") {
		t.Fatalf("pending closeout must not authorize a completion claim: %v", err)
	}
	if err := orchestrationStopError(applied, calls, "The worker run has started."); err != nil {
		t.Fatalf("pending closeout should allow a progress update: %v", err)
	}
}

func TestOrchestrationStopErrorIncludesToolFailureReason(t *testing.T) {
	err := orchestrationStopError(nil, []ToolCallDTO{{
		Name:   "mission_coordinator_plan_parallel",
		Status: "failed",
		Result: map[string]string{"error": "worker_lanes[0].assignee_id is required"},
	}}, "")

	if err == nil || !strings.Contains(err.Error(), "mission_coordinator_plan_parallel") || !strings.Contains(err.Error(), "assignee_id is required") {
		t.Fatalf("orchestrationStopError = %v, want method and original failure reason", err)
	}
}

func TestOrchestrationStopErrorIgnoresFailedAttemptAfterCorrectedRetry(t *testing.T) {
	calls := []ToolCallDTO{
		{Name: "mission_coordinator_plan_parallel", Status: "failed", Result: map[string]string{"error": "assignee_id is required"}},
		{Name: "mission_coordinator_plan_parallel", Status: "completed", Result: map[string]string{"task_ids": "task-1"}},
	}

	if err := orchestrationStopError(nil, calls, "已修正参数并完成任务编排。"); err != nil {
		t.Fatalf("corrected retry should resolve the earlier failed call: %v", err)
	}
}

func TestOrchestrationStopErrorAllowsExistingMissionWithoutNewMissionTool(t *testing.T) {
	applied := []LinkedMissionDTO{{MissionID: "mission-existing"}}
	calls := []ToolCallDTO{{
		Name: "enterprise_graph_search_objects", Status: "completed",
		Result: map[string]string{"items": "[]"},
	}}

	if err := orchestrationStopError(applied, calls, "The existing Mission is active."); err != nil {
		t.Fatalf("existing Mission must not be mistaken for a Mission created in this turn: %v", err)
	}
}

func TestOrchestrationStopErrorAllowsSuccessfulCloseAfterAlternativeToolFailures(t *testing.T) {
	calls := []ToolCallDTO{
		{Name: "mission_coordinator_plan_parallel", Status: "failed", Result: map[string]string{"error": "worker lanes are required"}},
		{Name: "discussion_close", Status: "completed", Result: map[string]string{"status": "completed"}},
	}

	if err := orchestrationStopError(nil, calls, "Verification completed and evidence was recorded."); err != nil {
		t.Fatalf("successful discussion_close should resolve earlier alternative attempts: %v", err)
	}
}

func TestOrchestrationStopErrorAllowsOptionalRetrievalFailureWithAnswer(t *testing.T) {
	for _, toolName := range []string{
		string(toolstd.KnowledgeSearch),
		string(toolstd.MemorySearch),
		string(toolstd.EnterpriseGraphBuildContextBundle),
	} {
		t.Run(toolName, func(t *testing.T) {
			err := orchestrationStopError(nil, []ToolCallDTO{{
				Name:   toolName,
				Status: "failed",
				Result: map[string]string{"error": "optional source unavailable"},
			}}, "检索暂时不可用，我先根据当前上下文回答。")
			if err != nil {
				t.Fatalf("optional retrieval failure should not discard a usable answer: %v", err)
			}
		})
	}
}

func TestOrchestrationStopErrorStillRejectsOptionalRetrievalFailureWithoutAnswer(t *testing.T) {
	err := orchestrationStopError(nil, []ToolCallDTO{{
		Name:   string(toolstd.KnowledgeSearch),
		Status: "failed",
		Result: map[string]string{"error": "optional source unavailable"},
	}}, "")

	if err == nil || !strings.Contains(err.Error(), string(toolstd.KnowledgeSearch)) {
		t.Fatalf("empty response after retrieval failure must remain incomplete: %v", err)
	}
}

func TestOrchestrationStopErrorPreservesAgentExplanationOfWebFailure(t *testing.T) {
	for _, name := range []string{string(toolstd.WebFetch), string(toolstd.WebSearch)} {
		t.Run(name, func(t *testing.T) {
			calls := []ToolCallDTO{{
				Name: name, Status: "failed",
				Result: map[string]string{"error": "context deadline exceeded"},
			}}
			if err := orchestrationStopError(nil, calls, "网页访问超时，我无法核实页面内容。可以换一个来源继续查证。"); err != nil {
				t.Fatalf("Agent explanation must remain deliverable: %v", err)
			}
			if calls[0].Status != "failed" {
				t.Fatal("delivering an explanation must not mark the failed tool successful")
			}
			if err := orchestrationStopError(nil, calls, ""); err == nil {
				t.Fatal("a failed web request without an Agent answer must remain incomplete")
			}
			calls[0].Status = "running"
			if err := orchestrationStopError(nil, calls, "正在访问网页。"); err == nil {
				t.Fatal("a running web request must not be treated as a handled failure")
			}
		})
	}
}

func TestOrchestrationStopErrorPreservesAnswerAfterRuntimeApprovalBlock(t *testing.T) {
	err := orchestrationStopError(nil, []ToolCallDTO{
		{
			Name:   "bash",
			Status: "failed",
			Result: map[string]string{"output": "Blocked by Orrery runtime approval"},
		},
		{
			Name:   "read",
			Status: "failed",
			Result: map[string]string{"output": "ENOENT: no such file or directory"},
		},
	}, "The command was blocked by runtime approval, so it was not executed.")
	if err != nil {
		t.Fatalf("a truthful answer about non-executed tools must be preserved: %v", err)
	}
}

func TestOrchestrationStopErrorStillRejectsRuntimeApprovalBlockWithoutAnswer(t *testing.T) {
	err := orchestrationStopError(nil, []ToolCallDTO{{
		Name:   "bash",
		Status: "failed",
		Result: map[string]string{"output": "Blocked by Orrery runtime approval"},
	}}, "")

	if err == nil || !strings.Contains(err.Error(), "bash") {
		t.Fatalf("an empty response after an approval block must remain incomplete: %v", err)
	}
}

func TestOrchestrationStopErrorStillRejectsArbitraryShellFailureWithAnswer(t *testing.T) {
	err := orchestrationStopError(nil, []ToolCallDTO{{
		Name:   "bash",
		Status: "failed",
		Result: map[string]string{"output": "exit status 1"},
	}}, "The operation completed.")

	if err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("an ordinary shell failure must not be hidden by an answer: %v", err)
	}
}

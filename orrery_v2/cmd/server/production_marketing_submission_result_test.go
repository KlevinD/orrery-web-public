package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/testkit"
)

func TestMarketingSubmissionCompletesScheduledResultFromReceipt(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	input := responsibilityMaterializerTestInput()
	input.RuntimeOptions.ProviderSpecific = map[string]string{"run_source": schedulerAgentTaskRunSource, "generation_id": "generation-1", "scheduled_run_id": "generation-1", "scheduled_task_id": "task-1"}
	input.RuntimeToolBindings = []agentcore.RuntimeToolBinding{{ToolID: marketingWechatSubmissionTool, Alias: "submit_article", GrantedPermissions: []string{"marketing.wechat.article.submit"}}}
	args := map[string]any{"generation_id": "generation-1", "scheduled_run_id": "generation-1", "scheduled_task_id": "task-1"}
	hash := "sha256:" + strings.Repeat("a", 64)
	receipt := map[string]any{"generation_id": "generation-1", "submission_id": "wechat-generation:generation-1:submit_approval", "status": "approval_pending", "content_hash": hash, "asset_hashes": []string{hash, hash, hash}}
	encoded, _ := json.Marshal(receipt)
	response := agentcore.RuntimeResponse{RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted, Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: "Submitted. ```orrery-agent-result\n{broken json}\n```"}}, ToolCalls: []agentcore.RuntimeToolCall{{ID: "call-submit", Name: "submit_article", Input: args, Output: string(encoded)}}}
	normalizer := missionTaskAgentResultNormalizer{store: env.FileStore, scheduledArtifacts: newProductionSchedulerArtifactStore("", env.Config.FileStore, env.FileStore, nil)}
	result, err := normalizer.NormalizeContext(context.Background(), input, response, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Blocker != nil || len(result.Evidence) != 1 || result.Evidence[0].ToolLogRef.ID != "call-submit" || len(result.Deliverables) != 1 {
		t.Fatalf("result=%+v", result)
	}
	if !strings.Contains(result.Summary.Text, "正式审批") {
		t.Fatalf("summary=%s", result.Summary.Text)
	}
	result.InternalTraceRef = "artifact://trace-submission"
	for name, validator := range map[string]agentcore.ResultValidator{
		"evidence": productionEvidenceResultValidator{},
		"qa":       productionQAResultValidator{},
		"policy":   productionPolicyResultValidator{},
	} {
		if err := validator.Validate(input, result); err != nil {
			t.Fatalf("%s validation: %v", name, err)
		}
	}
	for _, change := range []string{"other_generation", "failed", "bad_hash", "unbound_tool", "wrong_run", "other_task"} {
		t.Run(change, func(t *testing.T) {
			copyInput := input
			copyResponse := response
			copyReceipt := map[string]any{}
			for key, value := range receipt {
				copyReceipt[key] = value
			}
			copyResponse.ToolCalls = append([]agentcore.RuntimeToolCall(nil), response.ToolCalls...)
			switch change {
			case "other_generation":
				copyReceipt["generation_id"] = "old"
			case "failed":
				copyReceipt["status"] = "failed"
			case "bad_hash":
				copyReceipt["content_hash"] = "sha256:placeholder"
			case "unbound_tool":
				copyInput.RuntimeToolBindings = nil
			case "wrong_run":
				copyResponse.RunID = "another-run"
			case "other_task":
				copyResponse.ToolCalls[0].Input = map[string]any{"generation_id": "generation-1", "scheduled_run_id": "generation-1", "scheduled_task_id": "other"}
			}
			body, _ := json.Marshal(copyReceipt)
			copyResponse.ToolCalls[0].Output = string(body)
			if _, err := normalizer.NormalizeContext(context.Background(), copyInput, copyResponse, time.Now()); err == nil {
				t.Fatal("invalid submission was accepted")
			}
		})
	}
}

func TestMarketingSubmissionProgressKeepsQueueAndFormalApprovalDistinct(t *testing.T) {
	queued := marketingSubmissionProgressSummary("approval_submitting", "")
	if !strings.Contains(queued, "当前仍在提交队列中") || strings.Contains(queued, "已进入正式审批") {
		t.Fatalf("queued: %s", queued)
	}
	pending := marketingSubmissionProgressSummary("approval_pending", "decision-1")
	if !strings.Contains(pending, "已进入正式审批") || !strings.Contains(pending, "decision-1") {
		t.Fatalf("pending: %s", pending)
	}
	if strings.Contains(marketingSubmissionProgressSummary("approval_pending", ""), "已进入正式审批") {
		t.Fatal("unbound status claimed formal approval")
	}
}

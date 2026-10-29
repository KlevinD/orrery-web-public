package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const marketingWechatSubmissionTool = "mcp.marketing.marketing_wechat_article_submit_for_approval"

// Submission is the scheduled worker's terminal business action. Its durable
// tool receipt, rather than a model-authored evidence alias, proves completion.
func (n missionTaskAgentResultNormalizer) materializeMarketingWechatSubmission(ctx context.Context, input agentcore.AgentInput, response agentcore.RuntimeResponse, result agentcore.AgentResult) (agentcore.AgentResult, bool, error) {
	metadata := input.RuntimeOptions.ProviderSpecific
	generationID := strings.TrimSpace(metadata["generation_id"])
	if metadata["run_source"] != schedulerAgentTaskRunSource || generationID == "" || generationID != metadata["scheduled_run_id"] ||
		response.RunID != input.RunID || response.Status != agentcore.RuntimeStatusCompleted ||
		len(input.Task.ExpectedDeliverables) != 1 || len(input.Task.AcceptanceCriteria) != 1 || len(input.Task.EvidenceRequirements) != 1 {
		return agentcore.AgentResult{}, false, nil
	}
	requirement := input.Task.EvidenceRequirements[0]
	if requirement.Kind != string(agentcore.EvidenceKindToolLog) || !requirement.Required {
		return agentcore.AgentResult{}, false, nil
	}
	for index := len(response.ToolCalls) - 1; index >= 0; index-- {
		call := response.ToolCalls[index]
		binding, ok := runtimeToolBindingForCall(call.Name, input.RuntimeToolBindings)
		if !ok || string(binding.ToolID) != marketingWechatSubmissionTool || call.ID == "" ||
			call.Input["generation_id"] != generationID || call.Input["scheduled_run_id"] != generationID ||
			call.Input["scheduled_task_id"] != metadata["scheduled_task_id"] {
			continue
		}
		var receipt struct {
			DecisionID   string   `json:"decision_id"`
			GenerationID string   `json:"generation_id"`
			SubmissionID string   `json:"submission_id"`
			Status       string   `json:"status"`
			ContentHash  string   `json:"content_hash"`
			AssetHashes  []string `json:"asset_hashes"`
		}
		if json.Unmarshal([]byte(call.Output), &receipt) != nil || receipt.GenerationID != generationID ||
			receipt.SubmissionID != "wechat-generation:"+generationID+":submit_approval" ||
			(receipt.Status != "approval_submitting" && receipt.Status != "approval_pending" && receipt.Status != "approved_executing" && receipt.Status != "draft_created") ||
			!marketingReceiptHash(receipt.ContentHash) || len(receipt.AssetHashes) < 3 || len(receipt.AssetHashes) > 5 {
			continue
		}
		valid := true
		for _, hash := range receipt.AssetHashes {
			valid = valid && marketingReceiptHash(hash)
		}
		if !valid {
			continue
		}
		summary := marketingSubmissionProgressSummary(receipt.Status, receipt.DecisionID)
		artifact, err := n.persistRuntimeOutput(ctx, input, summary)
		if err != nil {
			return agentcore.AgentResult{}, true, err
		}
		deliverable := input.Task.ExpectedDeliverables[0]
		result.Summary = agentcore.Summary{Text: summary}
		result.Blocker = nil
		result.Deliverables = []agentcore.Deliverable{{ID: agentcore.DeliverableID("deliverable-" + deliverable.ID), ExpectedDeliverableID: deliverable.ID, Title: deliverable.Title, ArtifactRef: artifact}}
		result.Evidence = []agentcore.Evidence{toolLogEvidence(input.RunID, requirement.ID, input.Task.AcceptanceCriteria[0].ID, agentcore.EvidenceKindToolLog, call.ID)}
		result, err = n.finalizeScheduledResultArtifacts(ctx, input, result)
		return result, true, err
	}
	return agentcore.AgentResult{}, false, nil
}

func marketingReceiptHash(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func marketingSubmissionProgressSummary(status, decisionID string) string {
	switch status {
	case "approval_pending":
		if strings.TrimSpace(decisionID) != "" {
			return "文章和全部配图已生成，已进入正式审批。\n\n请在 Orrery 审批中心查看完整材料并决定是否创建公众号草稿。审批单：" + decisionID
		}
		return "文章和全部配图已生成。Marketing 返回待审批状态，但本次回执未包含正式审批单编号，请在内容项目中核对审批进度。"
	case "approved_executing":
		return "文章已获批，Marketing 正在创建公众号草稿。请等待草稿创建回执。"
	case "draft_created":
		return "公众号草稿已创建。请在 Marketing 查看创建回执并检查微信草稿箱；内容尚未公开发布。"
	default:
		return "文章和全部配图已生成并保存到 Marketing，正在提交正式审批。\n\n当前仍在提交队列中，取得审批单后才能进入待审批状态。可在 Marketing 内容项目中查看进度；通信失败时后台会重试。"
	}
}

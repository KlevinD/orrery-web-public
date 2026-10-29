package missioncontrol

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
)

func durableHILWechatContinuationPrompt(metadata map[string]string, continuation runtimecore.ApprovalContinuation) string {
	intentID := strings.TrimSpace(metadata["approval_material_intent_id"])
	base := runtimecore.ApprovalContinuationPrompt(metadata, continuation)
	return strings.Join([]string{
		base,
		"This is a detached, already-approved WeChat draft continuation. Do not repeat the scheduled workflow, create or modify content, or request another approval.",
		"Read only the approved checkpoint at wechat/publish-intents/" + intentID + ".json in the current Agent workspace.",
		"Immediately call marketing_wechat_draft_create exactly once with the checkpoint's exact account_id, intent_id, intent_hash, article_md_path, cover_path, share_thumbnail_path, ordered body_image_paths, title, author, and digest. The share_thumbnail_path is required even when it equals cover_path.",
		"This operation is draft-only. Never call freepublish or any public publishing tool. After the draft tool returns, report its real media_id and receipt, then stop.",
	}, "\n\n")
}

func minimizeDurableHILContinuationRun(run *runtimedaemon.AgentRun, prompt string) {
	if run == nil {
		return
	}
	run.Task.Goal = "Execute the approved WeChat draft creation exactly once."
	run.Task.WorkerPersonalGoal = prompt
	run.Task.AcceptanceCriteria = nil
	run.Task.EvidenceRequirements = nil
	run.Task.ExpectedDeliverables = nil
	contextPack := run.Request.ContextPack
	contextPack.MissionFinalGoal = run.Task.Goal
	contextPack.DiscussionGroupGoal = run.Task.Goal
	contextPack.WorkerPersonalGoal = prompt
	contextPack.TaskContractSummary = run.Task.Goal
	contextPack.DiscussionDigestRef = agentcore.DiscussionDigestRef{}
	contextPack.DiscussionDigestSummary = ""
	contextPack.SourceRefs = nil
	run.Request.ContextPack = contextPack
	systemPrompt := strings.TrimSpace(run.Request.RuntimeOptions.SystemPrompt)
	run.Request.InputMessages = nil
	if systemPrompt != "" {
		run.Request.InputMessages = append(run.Request.InputMessages, agentcore.RuntimeChatMessage{Role: "system", Content: systemPrompt})
	}
	run.Request.InputMessages = append(run.Request.InputMessages, agentcore.RuntimeChatMessage{Role: "user", Content: prompt})
	run.Request.RuntimeOptions.PromptBlocks = nil
	run.Request.RuntimeOptions.MaxTurns = 6
}

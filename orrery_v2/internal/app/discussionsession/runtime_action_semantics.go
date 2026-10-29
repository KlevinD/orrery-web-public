package discussionsession

import (
	"context"
	"encoding/json"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
)

const discussionActionSemanticsPromptBlockID = "current_action_semantics"

func (r *BaseAgentDiscussionAgentRunner) resolveActionSemantics(ctx context.Context, request DiscussionAgentRequest) actioncontract.ActionSemanticFrame {
	if request.ActionSemantics.Recognized {
		return request.ActionSemantics
	}
	utterance := strings.TrimSpace(discussionCurrentTriggerMessage(request).Content)
	if utterance == "" {
		return actioncontract.ActionSemanticFrame{}
	}
	tenantID := firstNonBlank(request.ConversationTenantID, request.Viewer.TenantID, "global")
	if r != nil && r.actionInterpreter != nil {
		if frame, err := r.actionInterpreter.InterpretAction(ctx, tenantID, utterance); err == nil && frame.Recognized {
			return frame
		}
	}
	return actioncontract.InterpretDefaultAction(tenantID, utterance)
}

func discussionActionSemanticsPrompt(frame actioncontract.ActionSemanticFrame) string {
	if !frame.Recognized {
		return ""
	}
	payload, err := json.Marshal(frame)
	if err != nil {
		return ""
	}
	return strings.Join([]string{
		"<current_action_semantics>",
		"The following frame is a deterministic interpretation of the current user utterance.",
		"It is context, not authorization and not proof that an action occurred.",
		"Execute only when execution_requested is true; otherwise respond to the speech act without performing the action.",
		"retrieval_mode is optional routing guidance. Use knowledge, memory, or ontology only when it helps the response.",
		string(payload),
		"</current_action_semantics>",
	}, "\n")
}

func appendDiscussionActionSemanticsBlock(blocks []agentcore.RuntimePromptBlock, frame actioncontract.ActionSemanticFrame) []agentcore.RuntimePromptBlock {
	content := discussionActionSemanticsPrompt(frame)
	if content == "" {
		return blocks
	}
	return append(blocks, agentcore.RuntimePromptBlock{
		BlockID: discussionActionSemanticsPromptBlockID,
		Code:    discussionActionSemanticsPromptBlockID,
		Role:    agentcore.RuntimePromptRoleSystem,
		Content: content,
		Enabled: true,
	})
}

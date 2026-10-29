package discussionsession

import (
	"context"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestApprovalBoundarySurvivesNativeAndManagedPromptConstruction(t *testing.T) {
	for _, kind := range []agentcore.RuntimeKind{agentcore.RuntimeKindManaged, agentcore.RuntimeKindCodex, agentcore.RuntimeKindClaude, agentcore.RuntimeKindPi} {
		t.Run(string(kind), func(t *testing.T) {
			runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{RuntimeSpec: agentcore.RuntimeSpec{Kind: kind}})
			input, err := runner.agentInput(context.Background(), DiscussionAgentRequest{
				ConversationID: "conv", ConversationTenantID: "tenant-a", MessageID: "msg", RunID: "run", Content: "完成已授权维护",
			})
			if err != nil {
				t.Fatal(err)
			}
			messages := agentcore.BuildAgentPromptMessages(agentcore.AgentPromptMessagesRequest{
				InputMessages: input.InputMessages, RuntimeVariables: input.RuntimeVariables, ContextPack: input.ContextPack,
				Task: input.Task, RuntimeContextParts: input.RuntimeContextParts, Blocks: input.RuntimeOptions.PromptBlocks,
			})
			prompt := agentcore.RuntimePromptMessagesSystemPrompt(messages) + agentcore.RuntimePromptMessagesFallbackPrompt(messages)
			if !strings.Contains(prompt, agentcore.DefaultApprovalBoundaryPrompt()) {
				t.Fatalf("%s runtime lost authorization boundaries", kind)
			}
			if input.ExecutionPrincipal.ID != "" || input.ExecutionPrincipal.Kind != "" || len(input.ExecutionPrincipal.PermissionScope) != 0 {
				t.Fatal("prompt policy created an execution principal")
			}
		})
	}
}

func TestApprovalBoundaryRemainsRequiredOnResumedNativeTurn(t *testing.T) {
	request := DiscussionAgentRequest{ConversationID: "conv", ConversationTenantID: "tenant-a", Content: "继续"}
	parts := discussionExternalCLIContextParts(request, discussionExternalCLISession{
		SessionID: "existing", ContextFingerprint: discussionExternalCLIContextFingerprint(request),
	})
	count := 0
	for _, part := range parts {
		if strings.Contains(part.Content, agentcore.DefaultApprovalBoundaryPrompt()) {
			count++
			if !part.Required {
				t.Fatal("approval boundary can be dropped during native context trimming")
			}
		}
	}
	if count != 1 {
		t.Fatalf("resumed native turn has %d approval boundaries", count)
	}
}

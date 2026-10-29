package discussionsession

import (
	"context"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
)

type unrecognizedActionInterpreter struct{}

func (unrecognizedActionInterpreter) InterpretAction(context.Context, string, string) (actioncontract.ActionSemanticFrame, error) {
	return actioncontract.ActionSemanticFrame{}, nil
}

func TestAgentInputAutomaticallyInjectsCurrentActionSemantics(t *testing.T) {
	runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{
		RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged},
	})
	input, err := runner.agentInput(context.Background(), DiscussionAgentRequest{
		ConversationID: "conv-1", ConversationTenantID: "tenant-1", MessageID: "msg-1", RunID: "run-1",
		Content: "我已经提交代码",
	})
	if err != nil {
		t.Fatalf("agentInput: %v", err)
	}
	var content string
	for _, block := range input.RuntimeOptions.PromptBlocks {
		if block.BlockID == discussionActionSemanticsPromptBlockID {
			content = block.Content
			break
		}
	}
	for _, want := range []string{`"action_type_id":"development.submit_code"`, `"speech_act":"assertion"`, `"execution_requested":false`} {
		if !strings.Contains(content, want) {
			t.Fatalf("action semantics block missing %q: %s", want, content)
		}
	}
	messages := agentcore.BuildAgentPromptMessages(agentcore.AgentPromptMessagesRequest{
		InputMessages: input.InputMessages, RuntimeVariables: input.RuntimeVariables,
		ContextPack: input.ContextPack, Task: input.Task, Blocks: input.RuntimeOptions.PromptBlocks,
	})
	if prompt := agentcore.RuntimePromptMessagesFallbackPrompt(messages); !strings.Contains(prompt, "我已经提交代码") {
		t.Fatalf("action semantics replaced the current input: %q", prompt)
	}
	if !strings.Contains(agentcore.RuntimePromptMessagesSystemPrompt(messages), "系统原则") {
		t.Fatal("action semantics replaced the default instructions")
	}
}

func TestExternalCLIPromptCarriesActionSemanticsBeforeCurrentInput(t *testing.T) {
	runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{
		RuntimeSpec:       agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi},
		ActionInterpreter: unrecognizedActionInterpreter{},
	})
	input, err := runner.agentInput(context.Background(), DiscussionAgentRequest{
		ConversationID: "conv-1", ConversationTenantID: "tenant-1", MessageID: "msg-1", RunID: "run-1",
		TriggerMessage: MessageDTO{MessageID: "msg-1", Content: "怎么提交代码？"},
	})
	if err != nil {
		t.Fatalf("agentInput: %v", err)
	}
	prompt := input.RuntimeVariables[agentcore.PromptBlockRuntimeContext]
	semanticIndex := strings.Index(prompt, "<current_action_semantics>")
	inputIndex := strings.Index(prompt, "<current_user_input")
	if semanticIndex < 0 || inputIndex < 0 || semanticIndex > inputIndex {
		t.Fatalf("external CLI prompt order is invalid: %s", prompt)
	}
	if !strings.Contains(prompt, `"retrieval_mode":"explain"`) || !strings.Contains(prompt, `"execution_requested":false`) {
		t.Fatalf("external CLI action frame is invalid: %s", prompt)
	}
}

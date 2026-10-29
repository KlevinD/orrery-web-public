package discussionsession

import (
	"context"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestDiscussionPromptKeepsCurrentInputAndAuditGoals(t *testing.T) {
	const utterance = "查询订单 ORD-42 的状态。\n只读查询，不得取消、退款或通知客户。"
	for _, tc := range []struct {
		name    string
		goal    string
		mission string
		trigger string
		custom  bool
		compact bool
	}{
		{name: "synthetic goal", compact: true},
		{name: "explicit goal", goal: "核查所有待发货订单"},
		{name: "real mission", mission: "mission-42"},
		{name: "different trigger", trigger: "只核查订单 ORD-43"},
		{name: "custom goal-only template", custom: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{
				RuntimeSpec:       agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged},
				ActionInterpreter: unrecognizedActionInterpreter{},
			})
			input, err := runner.agentInput(context.Background(), DiscussionAgentRequest{
				ConversationID: "conversation-42", MessageID: "message-42", RunID: "run-42",
				Content: utterance, DiscussionGoal: tc.goal, MissionID: tc.mission,
				TriggerMessage: MessageDTO{Content: tc.trigger},
				History:        []MessageDTO{{Role: "user", Content: "确认"}, {Role: "assistant", Content: "请再次确认范围"}, {Role: "user", Content: "确认"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			blocks := input.RuntimeOptions.PromptBlocks
			if tc.custom {
				blocks = []agentcore.RuntimePromptBlock{{BlockID: agentcore.PromptBlockRuntimeInputTemplate, Role: "user", Content: "<<runtime_context>>", Enabled: true}}
			}
			messages := agentcore.BuildAgentPromptMessages(agentcore.AgentPromptMessagesRequest{
				InputMessages: input.InputMessages, RuntimeVariables: input.RuntimeVariables,
				ContextPack: input.ContextPack, Task: input.Task, Blocks: blocks,
			})
			prompt := agentcore.RuntimePromptMessagesFallbackPrompt(messages)
			if tc.compact {
				if strings.Count(prompt, utterance) != 1 || strings.Contains(prompt, "任务目标：") {
					t.Fatalf("synthetic goal repeated or original input lost: %s", prompt)
				}
				if strings.Count(prompt, "内容：\n确认") != 2 {
					t.Fatal("separate historical confirmations must remain")
				}
			} else if !strings.Contains(prompt, "任务目标") {
				t.Fatalf("explicit/custom task context was removed: %s", prompt)
			}
			wantGoal := utterance
			if tc.goal != "" {
				wantGoal = tc.goal
			}
			if input.Task.Goal != wantGoal || input.ContextPack.MissionFinalGoal != wantGoal || input.ContextPack.WorkerPersonalGoal != wantGoal {
				t.Fatal("prompt compaction changed the audited source contract")
			}
			if tc.custom && !strings.Contains(prompt, utterance) {
				t.Fatal("goal-only template lost the user's request")
			}
			if tc.goal != "" && !strings.Contains(prompt, tc.goal) {
				t.Fatal("independent discussion goal lost")
			}
			if tc.goal != "" && !tc.custom && strings.Count(prompt, tc.goal) != 1 {
				t.Fatal("independent discussion goal was rendered more than once")
			}
			if !tc.custom {
				found := false
				for _, message := range messages {
					if !strings.Contains(message.Content, "## 当前用户输入") {
						continue
					}
					for _, part := range message.ContextParts {
						found = found || part.Required
					}
				}
				if !found {
					t.Fatal("current input must survive context budgeting")
				}
			}
		})
	}
}

func TestDiscussionWorkspacePromptSharesRootWithoutDroppingBoundaries(t *testing.T) {
	prompt := discussionWorkspaceInfoPrompt(DiscussionAgentRequest{Workspace: DiscussionWorkspaceContext{
		RootDir: "/workspace/conversation-42", AgentWorkDir: "/workspace/conversation-42/agents/agent-42",
	}})
	if strings.Count(prompt, "：/workspace/conversation-42\n") != 1 || !strings.Contains(prompt, "会话工作区根目录") {
		t.Fatalf("root was repeated or lost: %s", prompt)
	}
	if !strings.Contains(prompt, "当前 Agent 工作目录") || !strings.Contains(prompt, "Delivery rule:") {
		t.Fatal("distinct working directory or delivery boundary lost")
	}
}

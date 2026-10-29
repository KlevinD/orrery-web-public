package core

import (
	"strings"
	"testing"
)

func TestDiscussionGoalSharesFullTextOnlyInBuiltInCombinedTemplate(t *testing.T) {
	goal := "核查 ORD-42；不得退款。\n保留完整原始回执。"
	for _, mode := range []string{"default", "custom", "standalone", "different", "explicit", "structured", "overridden"} {
		t.Run(mode, func(t *testing.T) {
			req := AgentPromptMessagesRequest{ContextPack: ContextPack{DiscussionGroupGoal: goal, MissionFinalGoal: goal, WorkerPersonalGoal: goal}, Task: TaskInput{Goal: goal, WorkerPersonalGoal: "只读且保留凭证"}, RuntimeVariables: map[string]string{PromptBlockDiscussionGoal: "## 讨论组目标\n" + goal}}
			switch mode {
			case "custom":
				req.Blocks = []RuntimePromptBlock{{BlockID: PromptBlockRuntimeInputTemplate, Enabled: true, Role: RuntimePromptRoleUser, Content: "<<runtime_context>>"}}
			case "standalone":
				req.Blocks = []RuntimePromptBlock{{BlockID: PromptBlockRuntimeContext, Enabled: true}}
			case "different":
				req.Task.Goal = "核查 ORD-43"
			case "explicit":
				req.RuntimeVariables[PromptBlockRuntimeContext] = "明确约束：" + goal
			case "structured":
				req.RuntimeContextParts = []RuntimeContextPart{{Content: goal, Required: true}}
			case "overridden":
				req.RuntimeVariables[PromptBlockDiscussionGoal] = "其他讨论目标"
			}
			prompt := RuntimePromptMessagesFallbackPrompt(BuildAgentPromptMessages(req))
			if !strings.Contains(prompt, goal) || !strings.Contains(prompt, "只读且保留凭证") && mode != "explicit" && mode != "structured" {
				t.Fatalf("contract lost: %s", prompt)
			}
			if mode == "default" || mode == "different" {
				if strings.Count(prompt, goal) != 1 || !strings.Contains(prompt, "Mission 目标 / Worker 个人目标") || !strings.Contains(prompt, "见“讨论组目标”") {
					t.Fatalf("duplicate or missing scopes: %s", prompt)
				}
			}
			if mode == "different" && !strings.Contains(prompt, "任务目标：核查 ORD-43") {
				t.Fatal("distinct goal lost")
			}
			if req.ContextPack.MissionFinalGoal != goal {
				t.Fatal("source contract changed")
			}
		})
	}
}

func TestRuntimeContextSharesIdenticalGoalsWithAllScopeLabels(t *testing.T) {
	goal := "检查订单 ORD-42 的状态。\n只读查询；不得取消、退款或通知客户。"
	req := AgentPromptMessagesRequest{
		ContextPack: ContextPack{MissionFinalGoal: "\n" + goal + "\n", WorkerPersonalGoal: goal},
		Task: TaskInput{ID: "task-42", Goal: goal, WorkerPersonalGoal: goal,
			AcceptanceCriteria:   []TaskAcceptanceInput{{ID: "accept-42", Description: "保留订单状态原始回执"}},
			EvidenceRequirements: []TaskEvidenceInput{{ID: "evidence-42", Description: "只读订单查询日志", Kind: "tool_log", Required: true}},
			ExpectedDeliverables: []TaskDeliverableInput{{ID: "deliver-42", Title: "订单状态报告"}},
		},
		Blocks: []RuntimePromptBlock{{BlockID: PromptBlockRuntimeContext, Enabled: true}},
	}
	messages := BuildAgentPromptMessages(req)
	if len(messages) != 1 {
		t.Fatalf("messages = %d", len(messages))
	}
	prompt := messages[0].Content
	if strings.Count(prompt, goal) != 1 || !strings.Contains(prompt, "Mission 目标 / Worker 个人目标 / 任务目标 / 任务个人目标："+goal) {
		t.Fatalf("identical goals lost scope or were repeated: %s", prompt)
	}
	for _, text := range []string{"[accept-42]", "[evidence-42]", "[deliver-42]", "required=true", "```orrery-agent-result", "不得编造引用"} {
		if !strings.Contains(prompt, text) {
			t.Fatalf("lost completion contract %q", text)
		}
	}
	if len(messages[0].ContextParts) != 1 || !messages[0].ContextParts[0].Required {
		t.Fatal("shared task contract must remain required context")
	}
	if req.ContextPack.WorkerPersonalGoal != goal || req.Task.Goal != goal {
		t.Fatal("rendering changed the source task")
	}
}

func TestRuntimeContextPreservesDifferentGoalsAndResponsibility(t *testing.T) {
	mission := "查询订单并向用户汇报"
	worker := "查询订单"
	task := "查询订单；不得取消"
	personal := "查询订单；必须先得到负责人确认"
	prompt := buildRuntimeContextPrompt(AgentPromptMessagesRequest{
		ContextPack: ContextPack{
			MissionFinalGoal: mission, WorkerPersonalGoal: worker,
			ResponsibilityContext: &ResponsibilityContext{ContextID: "responsibility-42", Boundaries: []string{"不得跨租户读取"}},
		},
		Task: TaskInput{Goal: task, WorkerPersonalGoal: personal},
	})
	for _, line := range []string{
		"- Mission 目标：" + mission,
		"- Worker 个人目标：" + worker,
		"- 任务目标：" + task,
		"- 任务个人目标：" + personal,
		"responsibility-42", "不得跨租户读取",
	} {
		if !strings.Contains(prompt, line) {
			t.Fatalf("lost distinct goal or responsibility %q: %s", line, prompt)
		}
	}
}

func TestRuntimeContextGoalSharingPreservesEmptyAndPartialGroups(t *testing.T) {
	for _, tc := range []struct {
		name string
		pack ContextPack
		task TaskInput
		want string
	}{
		{name: "no goals", want: "## 当前任务上下文"},
		{name: "task only", task: TaskInput{Goal: "A"}, want: "## 当前任务上下文\n- 任务目标：A"},
		{name: "same worker and task", pack: ContextPack{WorkerPersonalGoal: "A"}, task: TaskInput{Goal: "A", WorkerPersonalGoal: "B"},
			want: "## 当前任务上下文\n- Worker 个人目标 / 任务目标：A\n- 任务个人目标：B"},
		{name: "different internal whitespace", pack: ContextPack{MissionFinalGoal: "A\nB"}, task: TaskInput{Goal: "A B"},
			want: "## 当前任务上下文\n- Mission 目标：A\nB\n- 任务目标：A B"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildRuntimeContextPrompt(AgentPromptMessagesRequest{ContextPack: tc.pack, Task: tc.task}); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRuntimeContextGoalSharingDoesNotRewriteExplicitContextOrHistory(t *testing.T) {
	const explicit = "自定义上下文\n- 任务目标：重复\n- 任务个人目标：重复"
	history := []RuntimeChatMessage{{Role: "user", Content: "同意"}, {Role: "assistant", Content: "请确认查询范围"}, {Role: "user", Content: "同意"}}
	messages := BuildAgentPromptMessages(AgentPromptMessagesRequest{
		Task:             TaskInput{ID: "task-42", Goal: "重复", WorkerPersonalGoal: "重复"},
		RuntimeVariables: map[string]string{PromptBlockRuntimeContext: explicit},
		InputMessages:    history,
		Blocks: []RuntimePromptBlock{
			{BlockID: PromptBlockRuntimeContext, Enabled: true},
			{BlockID: PromptBlockConversationHistory, Enabled: true},
			{BlockID: PromptBlockCurrentUserInput, Enabled: true},
		},
	})
	if len(messages) != 4 || messages[0].Content != explicit {
		t.Fatalf("explicit context or history changed: %+v", messages)
	}
	for i, original := range history {
		if messages[i+1].Role != original.Role || messages[i+1].Content != original.Content {
			t.Fatal("repeated user confirmations must keep their position and role")
		}
	}
}

func TestCurrentInputGoalCompactionRequiresCompleteDefaultInput(t *testing.T) {
	const goal = "查询 ORD-42；不得退款或取消"
	for _, tc := range []struct {
		name      string
		current   string
		variables map[string]string
		blocks    []RuntimePromptBlock
		compact   bool
	}{
		{name: "host hint and intact input", current: goal, variables: map[string]string{RuntimeCurrentInputGoalVariable: goal}, compact: true},
		{name: "no host hint", current: goal},
		{name: "missing current input", variables: map[string]string{RuntimeCurrentInputGoalVariable: goal}},
		{name: "different current input", current: "只查询 ORD-43", variables: map[string]string{RuntimeCurrentInputGoalVariable: goal}},
		{name: "overridden current variable", current: goal, variables: map[string]string{RuntimeCurrentInputGoalVariable: goal, PromptBlockCurrentUserInput: "其他输入"}},
		{name: "explicit runtime context", current: goal, variables: map[string]string{RuntimeCurrentInputGoalVariable: goal, PromptBlockRuntimeContext: "自定义约束：" + goal}},
		{name: "standalone context", current: goal, variables: map[string]string{RuntimeCurrentInputGoalVariable: goal}, blocks: []RuntimePromptBlock{{BlockID: PromptBlockRuntimeContext, Enabled: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := AgentPromptMessagesRequest{
				ContextPack:      ContextPack{MissionFinalGoal: goal},
				Task:             TaskInput{ID: "task-42", Goal: goal, EvidenceRequirements: []TaskEvidenceInput{{ID: "evidence-42", Description: "只读原始回执", Required: true}}},
				RuntimeVariables: tc.variables, Blocks: tc.blocks,
			}
			if tc.current != "" {
				req.InputMessages = []RuntimeChatMessage{{Role: "user", Content: tc.current}}
			}
			prompt := RuntimePromptMessagesFallbackPrompt(BuildAgentPromptMessages(req))
			if tc.compact {
				if strings.Count(prompt, goal) != 1 || !strings.Contains(prompt, "[evidence-42]") || !strings.Contains(prompt, "```orrery-agent-result") {
					t.Fatalf("compaction lost current input or completion contract: %s", prompt)
				}
			} else if !strings.Contains(prompt, goal) {
				t.Fatalf("goal lost without a guaranteed current input: %s", prompt)
			}
			if req.Task.Goal != goal || req.ContextPack.MissionFinalGoal != goal {
				t.Fatal("source contract mutated")
			}
		})
	}
}

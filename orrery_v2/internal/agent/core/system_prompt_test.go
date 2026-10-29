package core

import (
	"strings"
	"testing"
)

func TestBuildAgentSystemPromptIncludesProfileWorkingInstructions(t *testing.T) {
	prompt := BuildAgentSystemPrompt(AgentSystemPromptRequest{
		Profile: AgentProfile{
			ID:          "lead-agent",
			RealName:    "顾明澈",
			DisplayName: "顾明澈",
			RoleName:    "Mission Control 主管 · 协作编排者",
			Persona: AgentPersona{
				Backstory:              "顾明澈是 Orrery 任务控制中心的主管，擅长在混乱中理清需求。",
				ResponsibilityBoundary: "负责澄清需求、生成 Mission Brief、编排团队、推进审计化协作。",
				BehaviorBoundary:       "通过 Orrery Mission 原生记录协调和报告。不绕过已配置的 Agent 权限、资源绑定或审计边界。",
			},
			BaseAgent: BaseAgentSpec{
				SystemPrompt: "你是顾明澈，Mission Control 主管 · 协作编排者。作为 Lead 只编排不实现。",
				SkillScope:   "profile_bound",
				ToolScope:    "profile_bound",
				MCPScope:     "profile_bound",
			},
		},
	})

	for _, want := range []string{
		"系统原则",
		"回复正文必须直接从内容开始，不要补全、模拟或自写任何说话人前缀、署名头或身份头。",
		"禁止在回复开头写类似「姓名：」「姓名 (id):」「姓名（角色）：」「|姓名：|」「AgentName:」的前缀",
		"请结合已发送的角色设定、能力资源、当前讨论组信息、历史消息和当前用户消息工作。",
		"凡是希望对方看到、回应、确认、冒泡、接手、处理、执行或被明确点名参与时，都必须使用系统提供的 mention:// 标准链接。",
		"顾明澈",
		"Mission Control 主管 · 协作编排者",
		"顾明澈是 Orrery 任务控制中心的主管，擅长在混乱中理清需求。",
		"负责澄清需求、生成 Mission Brief、编排团队、推进审计化协作。",
		"通过 Orrery Mission 原生记录协调和报告。不绕过已配置的 Agent 权限、资源绑定或审计边界。",
		"作为 Lead 只编排不实现",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, unexpected := range []string{
		"全局 BaseAgent 模板",
		"生命周期信号",
		"Current user request",
		"Previous request",
		"<character_card>",
		"<capabilities>",
		"<tools>",
		"mission-orchestration",
		"mission_prepare",
		"discussion_close",
		"agent_core_prompt",
		"核心指令",
		"TODO(memory-team)",
		"TODO(knowledge-team)",
		"## 角色设定",
		"## Agent ID",
		"## Agent 显示名",
		"## Agent 岗位",
		"## Agent 背景",
	} {
		if strings.Contains(prompt, unexpected) {
			t.Fatalf("prompt should not contain %q:\n%s", unexpected, prompt)
		}
	}
}

func TestDefaultRuntimePromptBlocksUseVariablesWithoutAgentConfigBlocks(t *testing.T) {
	blocks := DefaultRuntimePromptBlocks()
	ids := map[string]bool{}
	for _, block := range blocks {
		ids[block.BlockID] = true
	}
	for _, want := range []string{
		PromptBlockSystemPrinciples,
		PromptBlockAgentProfileTemplate,
		PromptBlockWorkspaceInfo,
		PromptBlockConversationHistory,
		PromptBlockRuntimeInputTemplate,
	} {
		if !ids[want] {
			t.Fatalf("default prompt blocks missing %q: %+v", want, blocks)
		}
	}
	for _, unexpected := range []string{
		PromptBlockAgentID,
		PromptBlockAgentRealName,
		PromptBlockAgentDisplayName,
		PromptBlockAgentRoleName,
		PromptBlockAgentKind,
		PromptBlockAgentBackstory,
		PromptBlockAgentResponsibilityBoundary,
		PromptBlockAgentBehaviorBoundary,
		PromptBlockDiscussionGoal,
		PromptBlockDiscussionMembers,
		PromptBlockRuntimeContext,
		PromptBlockKnowledgeContext,
		PromptBlockCurrentUserInput,
		PromptBlockAgentProfile,
	} {
		if ids[unexpected] {
			t.Fatalf("default prompt blocks should expose %q as a variable, not as a block: %+v", unexpected, blocks)
		}
	}
}

func TestKnownRuntimePromptBlocksDoNotExposeCoreOrRuntimeSystemPrompt(t *testing.T) {
	for _, block := range KnownRuntimePromptBlocks() {
		if block.BlockID == legacyPromptBlockAgentCorePrompt || block.BlockID == "runtime_system_prompt" {
			t.Fatalf("known prompt blocks should not expose fixed core/runtime system variables: %+v", block)
		}
	}
}

func TestKnownRuntimePromptBlocksKeepOnlyHistoryLockedDynamic(t *testing.T) {
	for _, block := range KnownRuntimePromptBlocks() {
		switch block.BlockID {
		case PromptBlockConversationHistory:
			if !block.Dynamic || block.Editable {
				t.Fatalf("conversation history should stay locked dynamic: %+v", block)
			}
		default:
			if block.Dynamic || !block.Editable {
				t.Fatalf("known prompt block should be editable template except history: %+v", block)
			}
			if strings.TrimSpace(block.Content) == "" {
				t.Fatalf("editable known prompt block should carry template content: %+v", block)
			}
		}
	}
}

func TestBuildAgentPromptMessagesResolvesAgentVariablesWithoutDefaultAgentBlocks(t *testing.T) {
	messages := BuildAgentPromptMessages(AgentPromptMessagesRequest{
		Profile: AgentProfile{
			ID:          "lead-agent",
			DisplayName: "顾明澈",
			RoleName:    "Lead",
			BaseAgent:   BaseAgentSpec{SystemPrompt: "只做编排。"},
		},
		Blocks: []RuntimePromptBlock{
			{BlockID: PromptBlockSystemPrinciples, Role: RuntimePromptRoleSystem, Content: "你是 <<agent_display_name>>，岗位是 <<agent_role_name>>。", Enabled: true},
			{BlockID: PromptBlockConversationHistory, Role: RuntimePromptRoleUser, Enabled: true},
		},
	})

	if len(messages) != 1 {
		t.Fatalf("messages len = %d, messages=%+v", len(messages), messages)
	}
	if !strings.Contains(messages[0].Content, "你是 顾明澈，岗位是 Lead。") ||
		strings.Contains(messages[0].Content, "只做编排") ||
		strings.Contains(messages[0].Content, "agent_core_prompt") {
		t.Fatalf("agent variables were not resolved:\n%s", messages[0].Content)
	}
}

func TestBuildAgentSystemPromptUsesProfileVariablesWithoutSynthesizingCorePrompt(t *testing.T) {
	prompt := BuildAgentSystemPrompt(AgentSystemPromptRequest{
		Profile: AgentProfile{
			ID:       "agent-1",
			RealName: "秦若兰",
			RoleName: "Staffing Agent",
			Persona: AgentPersona{
				ResponsibilityBoundary: "负责人员协调。",
			},
			BaseAgent: BaseAgentSpec{
				SkillScope: "profile_bound",
				ToolScope:  "profile_bound",
				MCPScope:   "profile_bound",
			},
		},
	})

	for _, want := range []string{
		"秦若兰",
		"Staffing Agent",
		"负责人员协调。",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, unexpected := range []string{"你是秦若兰，Staffing Agent。负责人员协调。", "当前 Agent 未绑定技能", "当前 Agent 未绑定工具", "TODO(memory-team)", "TODO(knowledge-team)", "<tools>", "## Agent 真实姓名", "## Agent 岗位", "## Agent 职责边界", "## Agent 核心指令", "agent_core_prompt"} {
		if strings.Contains(prompt, unexpected) {
			t.Fatalf("prompt should not include resource placeholders %q:\n%s", unexpected, prompt)
		}
	}
}

func TestBuildAgentPromptMessagesUsesConfiguredBlockOrder(t *testing.T) {
	messages := BuildAgentPromptMessages(AgentPromptMessagesRequest{
		Profile: AgentProfile{
			ID:          "lead-agent",
			DisplayName: "顾明澈",
			RoleName:    "Lead",
			BaseAgent: BaseAgentSpec{
				SystemPrompt: "只做编排，不直接实现。",
			},
		},
		RuntimeVariables: map[string]string{
			PromptBlockRuntimeContext: "## 运行上下文\n- 讨论组标题：嘿 都冒个泡",
		},
		InputMessages: []RuntimeChatMessage{
			{Role: "user", Content: "本地用户：前文"},
			{Role: "assistant", Content: "秦若兰：收到"},
			{Role: "user", Content: "本地用户：继续"},
		},
		Blocks: []RuntimePromptBlock{
			{BlockID: PromptBlockCurrentUserInput, Role: RuntimePromptRoleUser, Enabled: true},
			{BlockID: PromptBlockSystemPrinciples, Role: RuntimePromptRoleSystem, Content: "平台原则：不要模拟说话人前缀。", Enabled: true},
			{BlockID: "custom-profile", Role: RuntimePromptRoleSystem, Content: "只做编排，不直接实现。", Enabled: true},
			{BlockID: PromptBlockConversationHistory, Role: RuntimePromptRoleUser, Enabled: true},
			{BlockID: PromptBlockRuntimeContext, Role: RuntimePromptRoleUser, Enabled: true},
		},
	})

	if len(messages) != 6 {
		t.Fatalf("messages len = %d, messages=%+v", len(messages), messages)
	}
	if messages[0].Role != "user" || !strings.Contains(messages[0].Content, "本地用户：继续") {
		t.Fatalf("current input should follow configured order first: %+v", messages[0])
	}
	if messages[1].Role != "system" || !strings.Contains(messages[1].Content, "平台原则") {
		t.Fatalf("system principles should be second: %+v", messages[1])
	}
	if messages[2].Role != "system" || !strings.Contains(messages[2].Content, "只做编排") {
		t.Fatalf("agent core prompt should be third: %+v", messages[2])
	}
	if messages[3].Role != "assistant" || !strings.Contains(messages[3].Content, "秦若兰") {
		if messages[4].Role != "assistant" || !strings.Contains(messages[4].Content, "秦若兰") {
			t.Fatalf("history should preserve message roles: %+v", messages)
		}
	}
	if messages[5].Role != "user" || !strings.Contains(messages[5].Content, "## 运行上下文") {
		t.Fatalf("runtime context should be last: %+v", messages[5])
	}
}

func TestBuildAgentPromptMessagesMigratesLegacyAgentProfileBlock(t *testing.T) {
	messages := BuildAgentPromptMessages(AgentPromptMessagesRequest{
		Profile: AgentProfile{
			ID:          "lead-agent",
			RealName:    "顾明澈",
			DisplayName: "顾明澈",
			RoleName:    "Lead",
			Persona: AgentPersona{
				Backstory:              "负责把模糊需求拆清楚。",
				ResponsibilityBoundary: "只做编排。",
				BehaviorBoundary:       "不直接写代码。",
			},
			BaseAgent: BaseAgentSpec{SystemPrompt: "调用 Mission 工具完成状态变更。"},
		},
		Blocks: []RuntimePromptBlock{
			{BlockID: PromptBlockSystemPrinciples, Role: RuntimePromptRoleSystem, Content: "平台原则", Enabled: true},
			{BlockID: PromptBlockAgentProfile, Role: RuntimePromptRoleSystem, Enabled: true},
		},
	})

	joined := ""
	for _, message := range messages {
		joined += message.Content + "\n"
	}
	for _, want := range []string{"lead-agent", "顾明澈", "负责把模糊需求拆清楚。", "只做编排。", "不直接写代码。", "调用 Mission 工具完成状态变更。"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("legacy agent profile migration missing %q:\n%s", want, joined)
		}
	}
	for _, unexpected := range []string{"agent_core_prompt", "核心指令", "## 角色设定", "## Agent ID", "## Agent 显示名", "## Agent 背景", "## Agent 职责边界", "## Agent 核心指令", "## Agent 行为边界"} {
		if strings.Contains(joined, unexpected) {
			t.Fatalf("legacy agent_profile should render as one variable-based profile block, found %q:\n%s", unexpected, joined)
		}
	}
}

func TestBuildAgentPromptMessagesResolvesDiscussionRuntimeVariables(t *testing.T) {
	messages := BuildAgentPromptMessages(AgentPromptMessagesRequest{
		RuntimeVariables: map[string]string{
			PromptBlockDiscussionGoal:    "",
			PromptBlockDiscussionMembers: "当前讨论组成员：\n- @秦若兰：agent staffing-agent，身份 Staffing，通知链接 [@秦若兰](mention://agent/staffing-agent)",
			PromptBlockWorkspaceInfo:     "## 工作区信息\n- 会话工作区：D:/data/discussions/conv-1\n- 当前 Agent 工作目录：D:/data/discussions/conv-1/agents/staffing-agent",
			PromptBlockKnowledgeContext:  "## 相关知识\n- handbook.md：发布流程必须先完成灰度验收。",
			PromptBlockRuntimeContext:    "",
		},
		InputMessages: []RuntimeChatMessage{
			{Role: "assistant", Content: "顾明澈：收到"},
			{Role: "user", Content: "本地用户：继续"},
		},
		Blocks: []RuntimePromptBlock{
			{BlockID: PromptBlockWorkspaceInfo, Role: RuntimePromptRoleUser, Content: "<<workspace_info>>", Enabled: true},
			{BlockID: PromptBlockRuntimeInputTemplate, Role: RuntimePromptRoleUser, Content: "<<discussion_goal>>\n<<discussion_members>>\n<<knowledge_context>>\n<<runtime_context>>\n<<current_user_input>>", Enabled: true},
			{BlockID: PromptBlockConversationHistory, Role: RuntimePromptRoleUser, Enabled: true},
		},
	})

	if len(messages) != 3 {
		t.Fatalf("messages len = %d, messages=%+v", len(messages), messages)
	}
	content := messages[1].Content
	if strings.Contains(content, "讨论组目标") || strings.Contains(content, "当前任务上下文") {
		t.Fatalf("empty discussion goal/runtime context should stay empty:\n%s", content)
	}
	for _, want := range []string{"当前讨论组成员", "mention://agent/staffing-agent", "相关知识", "发布流程必须先完成灰度验收", "本地用户：继续"} {
		if !strings.Contains(content, want) {
			t.Fatalf("runtime variable prompt missing %q:\n%s", want, content)
		}
	}
	if !strings.Contains(messages[0].Content, "工作区信息") || !strings.Contains(messages[0].Content, "当前 Agent 工作目录") {
		t.Fatalf("default prompt blocks should render workspace_info before runtime input:\n%+v", messages)
	}
}

func TestNormalizeRuntimePromptBlocksDropsLegacyAgentCorePrompt(t *testing.T) {
	blocks := NormalizeRuntimePromptBlocks([]RuntimePromptBlock{
		{BlockID: legacyPromptBlockAgentCorePrompt, Role: RuntimePromptRoleSystem, Content: "<<agent_core_prompt>>", Enabled: true},
		{BlockID: PromptBlockSystemPrinciples, Role: RuntimePromptRoleSystem, Content: "平台原则", Enabled: true},
	})

	for _, block := range blocks {
		if block.BlockID == legacyPromptBlockAgentCorePrompt || strings.Contains(block.Content, "agent_core_prompt") {
			t.Fatalf("legacy agent_core_prompt block should be dropped: %+v", blocks)
		}
	}
}

func TestBuildAgentPromptMessagesResolvesPromptBlockVariables(t *testing.T) {
	messages := BuildAgentPromptMessages(AgentPromptMessagesRequest{
		Profile: AgentProfile{
			ID:          "lead-agent",
			DisplayName: "顾明澈",
			RoleName:    "Lead",
		},
		Blocks: []RuntimePromptBlock{
			{BlockID: PromptBlockAgentDisplayName, Code: "lead_name", Role: RuntimePromptRoleSystem, Dynamic: true, VariableOnly: true, Enabled: true},
			{BlockID: "custom-tone", Code: "tone", Title: "输出语气", Role: RuntimePromptRoleSystem, Content: "保持简洁。", Editable: true, VariableOnly: true, Enabled: true},
			{BlockID: "custom-rule", Code: "rule", Title: "引用规则", Role: RuntimePromptRoleSystem, Content: "请对 <<lead_name>> 使用 <<tone>> 兼容 {{tone}}", Editable: true, Enabled: true},
		},
	})

	if len(messages) != 1 {
		t.Fatalf("messages len = %d, messages=%+v", len(messages), messages)
	}
	content := messages[0].Content
	for _, want := range []string{"顾明澈", "保持简洁。", "兼容 保持简洁。"} {
		if !strings.Contains(content, want) {
			t.Fatalf("resolved prompt missing %q:\n%s", want, content)
		}
	}
	for _, unexpected := range []string{"<<lead_name>>", "{{tone}}", "输出语气"} {
		if strings.Contains(content, unexpected) {
			t.Fatalf("variable-only blocks should not be auto-rendered or leak references %q:\n%s", unexpected, content)
		}
	}
}

func TestBuildAgentPromptMessagesAddsTaskContextWhenNoInputMessages(t *testing.T) {
	messages := BuildAgentPromptMessages(AgentPromptMessagesRequest{
		Profile: AgentProfile{
			ID:       "agent-1",
			RoleName: "Worker",
			BaseAgent: BaseAgentSpec{
				SystemPrompt: "实现任务。",
			},
		},
		Task: TaskInput{
			Goal:                 "实现 prompt block 设置页",
			WorkerPersonalGoal:   "按阶段交付",
			AcceptanceCriteria:   []TaskAcceptanceInput{{ID: "ac-1", Description: "支持拖拽排序"}},
			EvidenceRequirements: []TaskEvidenceInput{{ID: "ev-1", Description: "测试通过", Kind: "test_log"}},
			ExpectedDeliverables: []TaskDeliverableInput{{ID: "de-1", Title: "代码变更", Description: "前后端实现"}},
		},
		ContextPack: ContextPack{MissionFinalGoal: "统一 Agent prompt pipeline"},
		Blocks: []RuntimePromptBlock{
			{BlockID: PromptBlockRuntimeContext, Role: RuntimePromptRoleUser, Enabled: true},
		},
	})

	if len(messages) != 1 {
		t.Fatalf("messages len = %d, messages=%+v", len(messages), messages)
	}
	context := messages[0].Content
	for _, want := range []string{"当前任务上下文", "统一 Agent prompt pipeline", "实现 prompt block 设置页", "支持拖拽排序", "测试通过", "代码变更"} {
		if !strings.Contains(context, want) {
			t.Fatalf("runtime context missing %q:\n%s", want, context)
		}
	}
}

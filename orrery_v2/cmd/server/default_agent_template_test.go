package main

import (
	"encoding/json"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform/config"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestDefaultMissionSystemRolesComeFromTemplates(t *testing.T) {
	var templates []defaultAgentTemplate
	for _, raw := range []string{
		`{
			"slug": "primary-agent",
			"name": "主协调者",
			"instructions": "Lead missions.",
			"worker_config": {"role": "Primary coordinator"},
			"mission_role": {
				"enabled": true,
				"role": "Lead",
				"responsibility": "Lead mission coordination",
				"authority_scope": "mission.lead",
				"idempotency_suffix": "lead"
			}
		}`,
		`{
			"slug": "staffing-agent",
			"name": "人员协调者",
			"instructions": "Staff missions.",
			"worker_config": {"role": "Staffing coordinator"},
			"mission_role": {
				"enabled": true,
				"role": "Staffing",
				"responsibility": "Select mission contributors",
				"authority_scope": "mission.staffing",
				"idempotency_suffix": "staffing"
			}
		}`,
	} {
		var template defaultAgentTemplate
		if err := json.Unmarshal([]byte(raw), &template); err != nil {
			t.Fatal(err)
		}
		templates = append(templates, template)
	}

	roles := defaultSystemRoleBootstrapsFromTemplates("custom-primary", templates)

	if len(roles) != 2 ||
		roles[0].ProfileID != agentcore.AgentProfileID("custom-primary") ||
		roles[0].Role != missionteam.MissionRoleLead ||
		roles[0].Responsibility != "Lead mission coordination" ||
		roles[0].AuthorityScope != "mission.lead" ||
		roles[0].IdempotencySuffix != "lead" ||
		roles[1].ProfileID != agentcore.AgentProfileID("staffing-agent") ||
		roles[1].Role != missionteam.MissionRoleStaffing ||
		roles[1].Responsibility != "Select mission contributors" ||
		roles[1].AuthorityScope != "mission.staffing" ||
		roles[1].IdempotencySuffix != "staffing" {
		t.Fatalf("system roles = %+v", roles)
	}

	cfg, err := config.LoadYAML(testConfigSource(t, map[string]string{"discussion.agent_id": "custom-primary"}))
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := defaultSystemRoleBootstrapsFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaults) != 1 ||
		defaults[0].ProfileID != agentcore.AgentProfileID("custom-primary") ||
		defaults[0].Role != missionteam.MissionRoleLead {
		t.Fatalf("default system roles = %+v", defaults)
	}
}

func TestDefaultAgentRuntimeSpecIgnoresDiscussionRuntimeConfigForProfileSeeds(t *testing.T) {
	cfg, err := config.LoadYAML(writeTestConfigYAML(t, `
discussion:
  agent:
    runtime_kind: codex
    runtime_config:
      executable: codex-fixture
agents:
  custom-primary:
    runtime_kind: openclaw
    runtime_config:
      executable: openclaw-agent
`))
	if err != nil {
		t.Fatal(err)
	}

	defaultPrimaryRuntime := defaultAgentRuntimeSpec(cfg, defaultLeadAgentID)
	configuredPrimaryRuntime := defaultAgentRuntimeSpec(cfg, "custom-primary")

	if defaultPrimaryRuntime.Kind != agentcore.RuntimeKindPi || len(defaultPrimaryRuntime.Config) != 0 {
		t.Fatalf("default primary runtime should ignore discussion runtime config and fall back to pi: %+v config=%s", defaultPrimaryRuntime, defaultPrimaryRuntime.Config)
	}
	if configuredPrimaryRuntime.Kind != agentcore.RuntimeKindOpenClaw || !strings.Contains(string(configuredPrimaryRuntime.Config), "openclaw-agent") {
		t.Fatalf("configured primary runtime = %+v config=%s", configuredPrimaryRuntime, configuredPrimaryRuntime.Config)
	}
}

func TestDefaultCoordinatorAgentProfileUsesTemplateAuthorityScope(t *testing.T) {
	var template defaultAgentTemplate
	if err := json.Unmarshal([]byte(`{
		"slug": "staffing-agent",
		"name": "人员协调者",
		"instructions": "Staff missions.",
		"worker_config": {
			"role": "Staffing coordinator",
			"authority_scope": "mission_staffing"
		}
	}`), &template); err != nil {
		t.Fatal(err)
	}

	profile := defaultCoordinatorAgentProfile("staffing-agent", "staffing", agentcore.RuntimeSpec{}, template)

	if profile.OrgIdentity.AuthorityScope != "mission_staffing" {
		t.Fatalf("authority scope = %q", profile.OrgIdentity.AuthorityScope)
	}
}

func TestDefaultCoordinatorAgentProfileUsesAgentNeutralBehaviorBoundary(t *testing.T) {
	var template defaultAgentTemplate
	if err := json.Unmarshal([]byte(`{
		"slug": "workflow-agent",
		"name": "协作成员",
		"instructions": "Coordinate work.",
		"worker_config": {"role": "Workflow member"}
	}`), &template); err != nil {
		t.Fatal(err)
	}

	profile := defaultCoordinatorAgentProfile("workflow-agent", "workflow", agentcore.RuntimeSpec{}, template)

	for _, unexpected := range []string{"PM", "HR"} {
		if strings.Contains(profile.Persona.BehaviorBoundary, unexpected) {
			t.Fatalf("behavior boundary should be agent-neutral, got %q", profile.Persona.BehaviorBoundary)
		}
	}
	if !strings.Contains(profile.Persona.BehaviorBoundary, "configured Agent") || !strings.Contains(profile.Persona.BehaviorBoundary, "audit") {
		t.Fatalf("behavior boundary = %q", profile.Persona.BehaviorBoundary)
	}
}

func TestDefaultCoordinatorAgentTemplatesUseNeutralNarrative(t *testing.T) {
	type narrativeTemplate struct {
		Description  string `json:"description"`
		Instructions string `json:"instructions"`
		WorkerConfig struct {
			Role             string   `json:"role"`
			Responsibilities []string `json:"responsibilities"`
			Deliverables     []string `json:"deliverables"`
		} `json:"worker_config"`
	}

	for _, seed := range defaultCoordinatorAgentSeeds() {
		fileName := seed.templateFile
		raw, err := defaultAgentTemplateFS.ReadFile("agent_templates/" + fileName)
		if err != nil {
			t.Fatalf("read template %s: %v", fileName, err)
		}
		var template narrativeTemplate
		if err := json.Unmarshal(raw, &template); err != nil {
			t.Fatalf("unmarshal template %s: %v", fileName, err)
		}
		fields := map[string]string{
			"description":        template.Description,
			"instructions":       template.Instructions,
			"worker_config.role": template.WorkerConfig.Role,
		}
		for index, value := range template.WorkerConfig.Responsibilities {
			fields["worker_config.responsibilities"] += "\n" + value
			fields["worker_config.responsibilities."+string(rune('0'+index))] = value
		}
		for index, value := range template.WorkerConfig.Deliverables {
			fields["worker_config.deliverables"] += "\n" + value
			fields["worker_config.deliverables."+string(rune('0'+index))] = value
		}
		for field, text := range fields {
			for _, unexpected := range []string{"PM", "HR", "PMAgent", "pm.", "legacy Project", "legacy issue", "Project/Issue"} {
				if strings.Contains(text, unexpected) {
					t.Fatalf("%s %s should use neutral Agent narrative, found %q in %q", fileName, field, unexpected, text)
				}
			}
		}
	}
}

func TestGenericWorkerAgentTemplateUsesNeutralNarrative(t *testing.T) {
	raw, err := defaultAgentTemplateFS.ReadFile("agent_templates/subagent-worker.json")
	if err != nil {
		t.Fatalf("read subagent-worker template: %v", err)
	}
	for _, unexpected := range []string{"PM", "HR", "PMAgent"} {
		if strings.Contains(string(raw), unexpected) {
			t.Fatalf("subagent-worker template should use neutral Agent narrative, found %q", unexpected)
		}
	}
}

func TestDefaultCoordinatorAgentSeedsUseNeutralAgentIDs(t *testing.T) {
	seeds := defaultCoordinatorAgentSeeds()
	if len(seeds) < 1 {
		t.Fatalf("default coordinator seeds = %+v", seeds)
	}
	if seeds[0].id != defaultLeadAgentID || seeds[0].templateFile != "lead-agent.json" || seeds[0].role != "lead" {
		t.Fatalf("primary seed should use neutral lead agent identity, got %+v", seeds[0])
	}
	for _, seed := range seeds {
		if seed.id == "staffing-agent" || seed.templateFile == "staffing-agent.json" || seed.role == "staffing" {
			t.Fatalf("default workflow should not seed a dedicated staffing agent: %+v", seed)
		}
	}
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatal(err)
	}
	if configuredDiscussionAgentID(cfg) != defaultLeadAgentID {
		t.Fatalf("default discussion agent id = %q, want %s", configuredDiscussionAgentID(cfg), defaultLeadAgentID)
	}
	for _, seed := range seeds {
		for _, legacy := range []string{"pm-agent", "hr-agent", "pm-agent.json", "hr-agent.json"} {
			if strings.Contains(seed.id, legacy) || strings.Contains(seed.templateFile, legacy) {
				t.Fatalf("seed should not use legacy PM/HR identity %q: %+v", legacy, seed)
			}
		}
	}
}

func TestDefaultAgentTemplatesDoNotExposeDedicatedStaffingAgent(t *testing.T) {
	for _, fileName := range defaultAgentTemplateFiles() {
		if fileName == "staffing-agent.json" {
			t.Fatalf("default agent templates should not expose retired dedicated staffing agent")
		}
	}
}

func TestDefaultLeadAgentOwnsStaffingTools(t *testing.T) {
	template, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{}, template)
	tools := agentToolRefs(profile)
	for _, want := range []string{"discussion_talent_list", "discussion_member_add"} {
		if !tools[want] {
			t.Fatalf("lead profile should own staffing tool %s after HR removal, refs = %+v", want, profile.ToolRefs)
		}
	}
	if strings.Contains(template.Instructions, "Staffing Agent") || strings.Contains(template.Instructions, "由 Staffing 处理") {
		t.Fatalf("lead instructions should not hand staffing work to a dedicated Staffing Agent: %s", template.Instructions)
	}
	for _, want := range []string{"一旦调用 mission_prepare", "不得由 Lead 直接回答", "真实 Agent"} {
		if !strings.Contains(template.Instructions, want) {
			t.Fatalf("lead instructions should require a real Agent handoff after mission_prepare; missing %q in %s", want, template.Instructions)
		}
	}
}

func TestDefaultLeadAgentInstructionsDefineMissionGoalAndStaffingDecisions(t *testing.T) {
	template, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}

	for _, want := range []string{
		"一次性专业请求",
		"本系统没有单独的 goal 创建工具",
		"Mission 最终目标通过 mission_prepare 的 final_goal 保存",
		"以下情况不创建 Mission",
		"discussion_talent_list 获取真实候选人",
		"agent_ids 只能来自刚返回的候选列表",
		"完成目标所需的最小团队",
		"不得由 Lead 越权补位",
		"优先通过 worker_lanes 传入结构化数组",
		"不要因为一次参数错误停止交互",
		"具体工具名、参数问题、原始错误",
		"只有全部承诺的交付物和验收条件已满足，才调用 discussion_close",
	} {
		if !strings.Contains(template.Instructions, want) {
			t.Fatalf("lead decision instructions missing %q", want)
		}
	}
}

func TestLegacyLeadPromptDetectionRefreshesPriorDefaultButPreservesCustomPrompts(t *testing.T) {
	priorDefault := "你是顾明澈。分析用户需求，澄清模糊目标，判断对话类型。简单任务可直接闭环并附加证据。"
	if !isLegacyDefaultBaseAgentPrompt(priorDefault) {
		t.Fatal("prior default Lead prompt should be refreshed from the new template")
	}
	priorStructuredDefault := "你是顾明澈，Mission Control 主管 · 协作编排者，也是 Orrery Mission Control 的 Lead Agent。默认顺序是：识别意图 -> 澄清关键缺口。"
	if !isLegacyDefaultBaseAgentPrompt(priorStructuredDefault) {
		t.Fatal("prior structured Lead prompt should be refreshed with tool recovery rules")
	}
	currentDefault := priorStructuredDefault + "所有工具调用都按可恢复操作处理。"
	if isLegacyDefaultBaseAgentPrompt(currentDefault) {
		t.Fatal("current Lead prompt should not be treated as legacy")
	}
	if isLegacyDefaultBaseAgentPrompt("你是我的自定义项目负责人，请按照内部流程协调交付。") {
		t.Fatal("custom Lead prompt should be preserved")
	}
}

func TestDefaultWorkflowAgentsOwnKnowledgeTools(t *testing.T) {
	for _, seed := range defaultCoordinatorAgentSeeds() {
		template, err := readDefaultAgentTemplate(seed.templateFile)
		if err != nil {
			t.Fatalf("read %s: %v", seed.templateFile, err)
		}
		profile := defaultCoordinatorAgentProfile(seed.id, seed.role, agentcore.RuntimeSpec{}, template)
		found := map[agentcore.ToolID]bool{}
		for _, ref := range profile.ToolRefs {
			if ref.ToolID == toolstd.KnowledgeAdd {
				found[toolstd.KnowledgeAdd] = true
				if ref.PermissionScope != toolstd.PermissionKnowledgeWrite {
					t.Fatalf("%s knowledge_add permission = %q, want %q", seed.id, ref.PermissionScope, toolstd.PermissionKnowledgeWrite)
				}
			}
			if ref.ToolID == toolstd.KnowledgeSearch {
				found[toolstd.KnowledgeSearch] = true
				if ref.PermissionScope != toolstd.PermissionKnowledgeRead {
					t.Fatalf("%s knowledge_search permission = %q, want %q", seed.id, ref.PermissionScope, toolstd.PermissionKnowledgeRead)
				}
			}
		}
		if !found[toolstd.KnowledgeAdd] {
			t.Fatalf("%s should own knowledge_add tool, refs = %+v", seed.id, profile.ToolRefs)
		}
		if !found[toolstd.KnowledgeSearch] {
			t.Fatalf("%s should own knowledge_search tool, refs = %+v", seed.id, profile.ToolRefs)
		}
	}
}

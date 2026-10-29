package core

import "strings"

const (
	DefaultSystemLLMAPIKeyRef = "secret://system/llm-api-key"
)

func DefaultBaseAgentSpec(name, roleName string) BaseAgentSpec {
	return BaseAgentSpec{
		InheritsBaseAgent: true,
		AuditRequired:     true,
		SystemPrompt:      DefaultBaseAgentSystemPrompt(name, roleName),
		LLMAPIKeyRef:      DefaultSystemLLMAPIKeyRef,
		SkillScope:        "profile_bound",
		ToolScope:         "profile_bound",
		MCPScope:          "profile_bound",
	}
}

func DefaultBaseAgentSystemPrompt(name, roleName string) string {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = "Orrery Agent"
	}
	prompt := "你是 Orrery 系统托管的 Agent「" + trimmedName + "」。"
	if strings.TrimSpace(roleName) != "" {
		prompt += " 岗位职责：" + strings.TrimSpace(roleName) + "。"
	}
	return prompt
}

func ApplyDefaultBaseAgentSpec(existing BaseAgentSpec, name, roleName string) BaseAgentSpec {
	seeded := DefaultBaseAgentSpec(name, roleName)
	return MergeDefaultBaseAgentSpec(existing, seeded)
}

func MergeDefaultBaseAgentSpec(existing, seeded BaseAgentSpec) BaseAgentSpec {
	merged := existing
	if !merged.InheritsBaseAgent {
		merged.InheritsBaseAgent = seeded.InheritsBaseAgent
	}
	if !merged.AuditRequired {
		merged.AuditRequired = seeded.AuditRequired
	}
	if strings.TrimSpace(merged.SystemPrompt) == "" {
		merged.SystemPrompt = seeded.SystemPrompt
	}
	if strings.TrimSpace(merged.LLMAPIKeyRef) == "" {
		merged.LLMAPIKeyRef = seeded.LLMAPIKeyRef
	}
	if strings.TrimSpace(merged.SkillScope) == "" {
		merged.SkillScope = seeded.SkillScope
	}
	if strings.TrimSpace(merged.ToolScope) == "" {
		merged.ToolScope = seeded.ToolScope
	}
	if strings.TrimSpace(merged.MCPScope) == "" {
		merged.MCPScope = seeded.MCPScope
	}
	if len(merged.Extensions) == 0 && len(seeded.Extensions) > 0 {
		merged.Extensions = cloneRawMessageMap(seeded.Extensions)
	}
	return merged
}

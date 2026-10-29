package core

import "strings"

type AgentSystemPromptRequest struct {
	Profile             AgentProfile
	FallbackCorePrompt  string
	RuntimeSystemPrompt string
}

func BuildAgentSystemPrompt(req AgentSystemPromptRequest) string {
	messages := BuildAgentPromptMessages(AgentPromptMessagesRequest{
		Profile:             req.Profile,
		FallbackCorePrompt:  req.FallbackCorePrompt,
		RuntimeSystemPrompt: req.RuntimeSystemPrompt,
		Blocks: []RuntimePromptBlock{
			{BlockID: PromptBlockSystemPrinciples, Role: RuntimePromptRoleSystem, Content: defaultSystemPrinciplesPrompt(), Enabled: true},
			{BlockID: PromptBlockAgentProfileTemplate, Role: RuntimePromptRoleSystem, Content: defaultAgentProfileTemplatePrompt(), Enabled: true},
		},
	})
	return RuntimePromptMessagesSystemPrompt(messages)
}

func agentPromptName(profile AgentProfile) string {
	return firstTrimmed(profile.DisplayName, profile.RealName, string(profile.ID), "Orrery Agent")
}

func agentPromptRole(profile AgentProfile) string {
	return firstTrimmed(profile.RoleName, "Orrery Agent")
}

func agentPromptResponsibility(profile AgentProfile, role string) string {
	return firstTrimmed(profile.Persona.ResponsibilityBoundary, role)
}

func firstTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

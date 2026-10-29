package core

import "strings"

// A normal owner session can invoke its configured resources with tool.call.
// Preserve the resource grants alongside (not in) the human's scopes, so the
// kernel and Gateway see the same authority as the binding resolver. Task,
// responsibility and incoming sealed envelopes retain their existing fences.
func personalAssistantBoundToolPermissionScopes(profile AgentProfile, input AgentInput, principal ExecutionPrincipal, tools []RuntimeToolBinding) map[string][]string {
	if !profile.IsPersonalAssistant() || principal.Kind != ExecutionPrincipalUser ||
		principal.Source != "system_assistant" || strings.TrimSpace(principal.ID) == "" ||
		principal.TenantID != strings.TrimSpace(profile.PersonalAssistantTenantID) ||
		(strings.TrimSpace(input.ExecutionPrincipal.TenantID) != "" && input.ExecutionPrincipal.TenantID != principal.TenantID) ||
		input.ExecutionEnvelope != nil || input.ContextPack.ResponsibilityContext != nil ||
		!personalAssistantOwnerConversation(input) ||
		!executionEnvelopeSelectorAllows(principal.PermissionScope, "tool.call") {
		return nil
	}
	var grants map[string][]string
	for _, binding := range tools {
		toolID := string(binding.ToolID)
		isMCP := binding.Source == "mcp_server" && strings.TrimSpace(binding.ServerToolID) != "" &&
			strings.TrimSpace(binding.DownstreamToolName) != "" &&
			toolID == binding.ServerToolID+"."+binding.DownstreamToolName
		isLearning := toolID == "rsi_learning_plan" && binding.ServerToolID == "" && binding.DownstreamToolName == ""
		browserScope := ""
		if binding.ServerToolID == "" && binding.DownstreamToolName == "" {
			switch toolID {
			case "personal_browser_read":
				browserScope = "browser.personal.read"
			case "personal_browser_control":
				browserScope = "browser.personal.control"
			}
		}
		if !isMCP && !isLearning && browserScope == "" {
			continue
		}
		for _, ref := range profile.ToolRefs {
			if string(ref.ToolID) != toolID && (!isMCP || string(ref.ToolID) != binding.ServerToolID) {
				continue
			}
			// Only the permission actually resolved from this profile's binding;
			// no wildcard, unrelated native permission or model-declared grant.
			scope := strings.TrimSpace(ref.PermissionScope)
			if scope == "" || strings.Contains(scope, "*") || (isLearning && scope != "rsi.learning.read") || (browserScope != "" && scope != browserScope) {
				continue
			}
			for _, permission := range binding.GrantedPermissions {
				if permission == scope {
					if grants == nil {
						grants = make(map[string][]string)
					}
					grants[toolID] = append(grants[toolID], scope)
				}
			}
		}
	}
	return grants
}

func personalAssistantOwnerConversation(input AgentInput) bool {
	if input.Task.ID == "" && input.Task.MissionID == "" && input.ContextPack.TaskID.IsZero() && input.ContextPack.MissionID.IsZero() {
		return true
	}
	// Discussion runs have synthetic task/mission identifiers. A real Mission
	// task must never acquire its owner's resource grants through this path.
	conversationID := strings.TrimSpace(input.RuntimeOptions.ProviderSpecific["discussion_id"])
	return conversationID != "" && input.Task.ID == conversationID &&
		input.ContextPack.TaskID.String() == conversationID && string(input.ContextPack.DiscussionGroupID) == conversationID &&
		input.Task.MissionID == "discussion-"+conversationID && input.ContextPack.MissionID.String() == input.Task.MissionID
}

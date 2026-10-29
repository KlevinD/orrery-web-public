package main

import (
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	discussionsession "orrery_v2/internal/app/discussionsession"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform/graphprotocol"
)

func isPersonalAssistantProfileID(value string) bool {
	value = strings.TrimSpace(value)
	for _, prefix := range []string{"agent:", "person:", "memory-"} {
		value = strings.TrimPrefix(value, prefix)
	}
	return agentcore.IsPersonalAssistantProfileID(value) || discussionsession.IsPersonalAssistantAgentID(value)
}

func isPersonalAssistantMemoryProfile(profile memcore.MemoryProfile) bool {
	return isPersonalAssistantProfileID(string(profile.OwnerRef.AgentID)) ||
		(profile.DefaultScope.Kind == memcore.ScopeAgent && isPersonalAssistantProfileID(profile.DefaultScope.ID)) ||
		isPersonalAssistantProfileID(string(profile.ID))
}

func isPersonalAssistantMemoryRecord(record memcore.MemoryRecord) bool {
	return isPersonalAssistantProfileID(string(record.ProfileID)) ||
		(record.Scope.Kind == memcore.ScopeAgent && isPersonalAssistantProfileID(record.Scope.ID))
}

func isPersonalAssistantMemoryEvent(event graphprotocol.DomainEvent) bool {
	if isPersonalAssistantProfileID(event.TenantID) {
		return true
	}
	for _, key := range []string{"agent_id", "owner_agent_id", "scope_id", "profile_id"} {
		if isPersonalAssistantProfileID(ontologyEventPayloadString(event.Payload, key)) {
			return true
		}
	}
	return false
}

func ontologyEventPayloadString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

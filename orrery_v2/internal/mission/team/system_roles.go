package team

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	agentidentity "orrery_v2/internal/agent/identity"
)

func IsWorkerInternalQAAgent(profileID agentcore.AgentProfileID, roleRefs []agentcore.RoleRef) bool {
	if IsWorkerInternalQARoleIdentifier(profileID.String()) {
		return true
	}
	for _, role := range roleRefs {
		if IsWorkerInternalQARoleIdentifier(string(role.ID)) {
			return true
		}
	}
	return false
}

func IsWorkerInternalQARoleIdentifier(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case strings.ToLower(agentidentity.QAPlanProfileID), strings.ToLower(agentidentity.QACheckProfileID),
		"qa-plan-agent", "qa-check-agent",
		"qa_plan_agent", "qa_check_agent",
		"qaplan", "qacheck",
		strings.ToLower(string(MissionRoleQAPlan)),
		strings.ToLower(string(MissionRoleQACheck)):
		return true
	default:
		return false
	}
}

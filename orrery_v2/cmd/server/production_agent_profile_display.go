package main

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

type productionAgentProfileReader interface {
	GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

func (s *productionAgentExecutionOntologySink) agentDisplayName(ctx context.Context, agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if s == nil || s.profiles == nil || agentID == "" {
		return agentID
	}
	profile, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID))
	if err != nil {
		return agentID
	}
	return firstNonBlank(profile.DisplayName, profile.RealName, agentID)
}

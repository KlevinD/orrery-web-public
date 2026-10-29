package main

import (
	"context"
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform/config"
)

type configuredRuntimeProfileStore struct {
	inner missioncontrolMentionProfileStore
	cfg   config.Config
}

type missioncontrolMentionProfileStore interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

func (s configuredRuntimeProfileStore) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if s.inner == nil {
		return agentcore.AgentProfile{}, fmt.Errorf("%w: agent profile directory unavailable", discussionsession.ErrRuntimeUnavailable)
	}
	profile, err := s.inner.GetProfile(ctx, id)
	if err != nil {
		return agentcore.AgentProfile{}, err
	}
	if spec := configuredConversationAgentRuntimeOptions(s.cfg, profile.ID.String()).RuntimeSpec; spec.HasKind() {
		profile.RuntimeSpec = spec
		return profile, nil
	}
	if !profile.RuntimeSpec.HasKind() && systemManagedDiscussionProfile(profile) {
		profile.RuntimeSpec = defaultAgentRuntimeSpec(s.cfg, profile.ID.String())
	}
	return profile, nil
}

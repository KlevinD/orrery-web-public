package core

import (
	"context"
	"fmt"
)

type StaticInstanceProvisioner struct {
	warm map[AgentProfileID]AgentInstanceRef
}

func NewStaticInstanceProvisioner(warm map[AgentProfileID]AgentInstanceRef) *StaticInstanceProvisioner {
	copied := make(map[AgentProfileID]AgentInstanceRef, len(warm))
	for id, ref := range warm {
		copied[id] = ref
	}
	return &StaticInstanceProvisioner{warm: copied}
}

func (p *StaticInstanceProvisioner) GetOrCreateInstance(ctx context.Context, profile AgentProfile, scope AgentInstanceScope) (AgentInstanceRef, error) {
	if err := ctx.Err(); err != nil {
		return AgentInstanceRef{}, err
	}
	if profile.ID.IsZero() {
		return AgentInstanceRef{}, fmt.Errorf("agent profile id is required")
	}
	stable := AgentInstanceRef{ID: AgentInstanceID(profile.ID.String())}
	if scope.ReuseWarm {
		if ref, ok := p.warm[profile.ID]; ok && ref.ID == stable.ID {
			return ref, nil
		}
	}
	return stable, nil
}

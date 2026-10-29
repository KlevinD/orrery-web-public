package core

import "context"

type AgentInstanceScope struct {
	MissionID       string
	IsolationPolicy string
	Permissions     []string
	ReuseWarm       bool
}

type AgentInstanceProvisioner interface {
	GetOrCreateInstance(ctx context.Context, profile AgentProfile, scope AgentInstanceScope) (AgentInstanceRef, error)
}

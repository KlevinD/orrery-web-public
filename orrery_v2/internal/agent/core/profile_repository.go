package core

import "context"

type AgentProfileRepository interface {
	GetProfile(ctx context.Context, id AgentProfileID) (AgentProfile, error)
	QueryDirectory(ctx context.Context, q AgentDirectoryQuery) (AgentDirectoryPage, error)
	GetAvailability(ctx context.Context, id AgentProfileID) (AgentAvailability, error)
}

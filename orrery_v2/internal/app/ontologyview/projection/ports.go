package projection

import (
	"context"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
)

type AuditReader interface {
	ListVisibleProjectionAudits(context.Context, contextgraph.ProjectionAuditCursorQuery) (contextgraph.ProjectionAuditCursorPage, error)
	GetVisibleProjectionAudit(context.Context, string, string, contextgraph.VisibilityConstraint) (contextgraph.ProjectionAudit, error)
	ListVisibleProjectionReplayHistory(context.Context, contextgraph.ProjectionReplayHistoryCursorQuery) (contextgraph.ProjectionReplayHistoryCursorPage, error)
}

type AuditCommandStore interface {
	GetProjectionAudit(context.Context, string, string) (contextgraph.ProjectionAudit, error)
	ClaimProjectionAuditCommand(context.Context, contextgraph.ProjectionAuditCommandClaimRequest) (contextgraph.ProjectionAuditCommandClaim, error)
	CompleteProjectionAuditCommand(context.Context, contextgraph.ProjectionAuditCommandCompletion) error
	FailProjectionAuditCommand(context.Context, contextgraph.ProjectionAuditCommandFailure) error
}

type CommandActor struct {
	Kind string
	ID   string
}

type ReplayPreparation struct {
	Result contextgraph.ProjectionAudit
	Effect func(context.Context, contextgraph.WriterStore) error
}

type CommandExecutor interface {
	PrepareProjectionAuditReplay(context.Context, contextgraph.ProjectionAudit, CommandActor) (ReplayPreparation, error)
	AcknowledgeProjectionAudit(context.Context, contextgraph.ProjectionAudit, CommandActor, string) (contextgraph.ProjectionAudit, error)
}

type VisibilityPolicy interface {
	Evaluate(context.Context, datavisibility.EvaluateRequest) (datavisibility.Decision, error)
}

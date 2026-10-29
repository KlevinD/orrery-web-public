package hil

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidRequest       = errors.New("invalid HIL request")
	ErrNotFound             = errors.New("HIL record not found")
	ErrConflict             = errors.New("HIL record conflict")
	ErrIdempotencyConflict  = errors.New("HIL idempotency conflict")
	ErrVoteImmutable        = errors.New("council vote is immutable")
	ErrContributionRequired = errors.New("council contribution is required before voting")
	ErrContextUnavailable   = errors.New("ontology context unavailable")
	ErrParticipantRequired  = errors.New("council participant is required")
	ErrDecisionDenied       = errors.New("HIL decision denied")
)

type Repository interface {
	CreateDecision(context.Context, DecisionRequest) error
	CreateDecisionWithCouncil(context.Context, DecisionRequest, *CouncilSession) error
	GetDecision(context.Context, string, string) (DecisionRequest, error)
	GetDecisionByIdempotencyKey(context.Context, string, string) (DecisionRequest, error)
	ListDecisions(context.Context, DecisionListQuery) (DecisionPage, error)
	ResolveDecision(context.Context, DecisionRequest, DecisionReceipt) error
	CreateCouncil(context.Context, CouncilSession) error
	GetCouncil(context.Context, string, string) (CouncilSession, error)
	GetCouncilByRequestID(context.Context, string, string) (CouncilSession, error)
	SaveContribution(context.Context, string, string, CouncilContribution, string, string) error
	SaveVote(context.Context, string, string, CouncilVote, string, string) error
	ResolveCouncil(context.Context, CouncilSession, DecisionReceipt) error
	ListDecisionReceipts(context.Context, string, string) ([]DecisionReceipt, error)
}

type WorkflowRepository interface {
	SaveTemplateDraft(context.Context, WorkflowTemplate, string, string) error
	PublishTemplate(context.Context, WorkflowTemplate, WorkflowTemplateVersion, []WorkflowTemplateBinding, string, string) (WorkflowTemplateVersion, error)
	DisableTemplate(context.Context, string, string, ActorRef, string) error
	PutTemplateBinding(context.Context, WorkflowTemplateBinding, ActorRef, string) error
	ListTemplates(context.Context, WorkflowTemplateListQuery) ([]WorkflowTemplateSummary, error)
	GetWorkflowTemplate(context.Context, string, string) (WorkflowTemplate, error)
	ResolveTemplate(context.Context, WorkflowTemplateSelector) (WorkflowTemplateVersion, error)
	GetDecisionByIdempotencyKey(context.Context, string, string) (DecisionRequest, error)
	CreateDecisionWorkflow(context.Context, DecisionRequest, WorkflowCase, WorkflowInstance, []WorkflowStageInstance, []WorkflowTask, []WorkflowAuditEvent, []WorkflowNotification) error
	GetWorkflowInstance(context.Context, string, string) (WorkflowInstanceDetail, error)
	GetWorkflowInstanceByCouncilID(context.Context, string, string) (WorkflowInstanceDetail, error)
	GetWorkflowInstanceByDecisionID(context.Context, string, string) (WorkflowInstanceDetail, error)
	GetWorkflowInstanceByTaskID(context.Context, string, string) (WorkflowInstanceDetail, error)
	GetCouncil(context.Context, string, string) (CouncilSession, error)
	SaveContribution(context.Context, string, string, CouncilContribution, string, string) error
	ListWorkflowInstances(context.Context, WorkflowListQuery) (WorkflowInstancePage, error)
	RecordWorkflowViewed(context.Context, string, string, ActorRef, string, string) error
	ApplyWorkflowCommand(context.Context, WorkflowCommand) (WorkflowInstanceDetail, error)
	ClaimDueWorkflowWork(context.Context, string, time.Time, int) ([]WorkflowDueItem, error)
	EnqueueWorkflowNotifications(context.Context, []WorkflowNotification) error
	ClaimWorkflowNotifications(context.Context, string, time.Time, int) ([]WorkflowNotification, error)
	CompleteWorkflowNotification(context.Context, WorkflowNotificationResult) error
}

// DecisionReceiptConsumer is an optional repository capability used by
// execution gateways. It atomically consumes a successful, single-use receipt
// after the caller has verified the request binding.
type DecisionReceiptConsumer interface {
	ConsumeDecisionReceipt(context.Context, string, string, string, time.Time) error
}

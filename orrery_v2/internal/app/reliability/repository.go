package reliability

import (
	"context"
	"time"
)

type Repository interface {
	ObserveFailure(context.Context, FailureObservation, FailureIncident) (FailureIncident, bool, error)
	GetIncident(context.Context, string, string) (FailureIncident, error)
	GetLatestRepairTarget(context.Context, string, string) (RepairTarget, error)
	UpdateIncidentStatus(context.Context, string, string, int64, IncidentStatus, time.Time) (FailureIncident, error)
	CreateDiagnosisCase(context.Context, string, DiagnosisCase) (DiagnosisCase, bool, error)
	GetDiagnosisCase(context.Context, string, string) (DiagnosisCase, error)
	ListDiagnosisCasesByIncident(context.Context, string, string, int) ([]DiagnosisCase, error)
	DecideDiagnosisCase(context.Context, DiagnosisDecisionCommand, time.Time) (DiagnosisCase, bool, error)
	UpsertRepairRule(context.Context, string, RepairRule) (RepairRule, bool, error)
	GetRepairRule(context.Context, string, string) (RepairRule, error)
	ListRepairRules(context.Context, string, string) ([]RepairRule, error)
	RecordRepairOutcome(context.Context, RepairOutcome, time.Time) (RepairRule, bool, error)
	RecordRepairPreflight(context.Context, RepairPreflight) (RepairPreflight, bool, error)
	GetRepairPreflight(context.Context, string, string) (RepairPreflight, error)
	BeginRepairExecution(context.Context, RepairExecution) (RepairExecution, bool, error)
	GetRepairExecution(context.Context, string, string) (RepairExecution, error)
	UpdateRepairExecution(context.Context, RepairExecutionUpdate) (RepairExecution, error)
	ListActiveRepairExecutions(context.Context, int) ([]RepairExecution, error)
}

// WorkspaceRepository exposes bounded incident history for the operator
// workbench without widening the mutation-oriented Repository contract.
type WorkspaceRepository interface {
	Repository
	ListRepairPreflightsByIncident(context.Context, string, string, int) ([]RepairPreflight, error)
	ListRepairExecutionsByIncident(context.Context, string, string, int) ([]RepairExecution, error)
}

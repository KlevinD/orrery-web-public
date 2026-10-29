package responsibility

import (
	"context"
	"strings"
	"time"

	"orrery_v2/internal/platform/uow"
)

type HistoricalMissionTaskAssignmentInput struct {
	TenantID       string
	MissionID      string
	TaskID         string
	SourceKind     string
	SourceID       string
	SourceStepID   string
	AgentProfileID string
	State          AssignmentState
	Priority       int
	AcceptBy       time.Time
	DueAt          time.Time
	PolicyRef      string
	ActorID        string
	Reason         string
}

func (r *Repository) ImportHistoricalMissionTaskTx(ctx context.Context, tx uow.Tx, input HistoricalMissionTaskAssignmentInput, occurredAt time.Time) (AssignmentRecord, error) {
	if r == nil || r.unit == nil || tx == nil || occurredAt.IsZero() {
		return AssignmentRecord{}, ErrInvalidContract
	}
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.MissionID = strings.TrimSpace(input.MissionID)
	input.TaskID = strings.TrimSpace(input.TaskID)
	input.SourceKind = strings.TrimSpace(input.SourceKind)
	input.SourceID = strings.TrimSpace(input.SourceID)
	input.SourceStepID = strings.TrimSpace(input.SourceStepID)
	input.AgentProfileID = strings.TrimSpace(input.AgentProfileID)
	input.PolicyRef = strings.TrimSpace(input.PolicyRef)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Priority < 0 {
		input.Priority = 0
	}
	if input.AcceptBy.IsZero() {
		input.AcceptBy = occurredAt
		if !input.DueAt.IsZero() && input.DueAt.Before(input.AcceptBy) {
			input.AcceptBy = input.DueAt
		}
	}
	if input.TenantID == "" || input.MissionID == "" || input.TaskID == "" || input.AgentProfileID == "" || !historicalAssignmentState(input.State) {
		return AssignmentRecord{}, ErrInvalidContract
	}
	assignmentID := AssignmentID(input.TenantID, WorkKindMissionTask, input.TaskID)
	if err := lockResponsibilityAggregateTx(ctx, tx, assignmentID); err != nil {
		return AssignmentRecord{}, err
	}
	existing, found, err := getAssignmentTx(ctx, tx, assignmentID)
	if err != nil {
		return AssignmentRecord{}, err
	}
	if found {
		if existing.TenantID == input.TenantID && existing.WorkKind == WorkKindMissionTask && existing.WorkID == input.TaskID &&
			existing.MissionID == input.MissionID && existing.AccountableAgent == input.AgentProfileID && existing.State == input.State {
			changed, err := mergeAssignmentSource(&existing, input.SourceKind, input.SourceID, input.SourceStepID)
			if err != nil {
				return AssignmentRecord{}, err
			}
			if changed {
				if err := putAssignmentTx(ctx, tx, existing); err != nil {
					return AssignmentRecord{}, err
				}
				if err := putAuditTx(ctx, tx, AuditRecord{
					TenantID: existing.TenantID, ResponsibilityID: existing.ID, EntityType: "assignment", EntityID: existing.ID,
					Action: AuditAssignmentSourceLinked, ActorID: input.ActorID, Details: assignmentSourceDetails(existing), OccurredAt: occurredAt,
				}); err != nil {
					return AssignmentRecord{}, err
				}
			}
			return existing, nil
		}
		return AssignmentRecord{}, ErrConflict
	}
	assignment := AssignmentRecord{
		ResponsibilityAssignment: ResponsibilityAssignment{
			ID: assignmentID, TenantID: input.TenantID, WorkKind: WorkKindMissionTask, WorkID: input.TaskID,
			AccountableAgent: input.AgentProfileID, State: input.State, Priority: input.Priority,
			AcceptBy: input.AcceptBy, DueAt: input.DueAt, PolicyRef: input.PolicyRef, Version: 1,
		},
		MissionID: input.MissionID, SourceKind: input.SourceKind, SourceID: input.SourceID, SourceStepID: input.SourceStepID,
		CreatedAt: occurredAt, UpdatedAt: occurredAt,
	}
	if input.State == AssignmentStateTerminated {
		assignment.TerminatedAt = occurredAt
		assignment.TerminatedBy = input.ActorID
		assignment.TerminationNote = input.Reason
	}
	if err := assignment.Validate(); err != nil {
		return AssignmentRecord{}, err
	}
	if err := putAssignmentTx(ctx, tx, assignment); err != nil {
		return AssignmentRecord{}, err
	}
	auditDetails := input.Reason
	if sourceDetails := assignmentSourceDetails(assignment); sourceDetails != "" {
		if auditDetails != "" {
			auditDetails += "; "
		}
		auditDetails += sourceDetails
	}
	if err := putAuditTx(ctx, tx, AuditRecord{
		TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "assignment", EntityID: assignment.ID,
		Action: AuditHistoricalAssignmentImported, ActorID: input.ActorID, ToState: string(assignment.State),
		Details: auditDetails, OccurredAt: occurredAt,
	}); err != nil {
		return AssignmentRecord{}, err
	}
	return assignment, nil
}

func historicalAssignmentState(state AssignmentState) bool {
	switch state {
	case AssignmentStateSubmitted, AssignmentStateFulfilled, AssignmentStateTerminated:
		return true
	default:
		return false
	}
}

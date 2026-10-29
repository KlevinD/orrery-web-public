package responsibility

import (
	"context"
	"strings"
	"time"

	"orrery_v2/internal/platform/uow"
)

// ExpireIdleScheduledAssignment atomically rejects expiration if dispatch has
// started a live attempt since the caller checked the occurrence window.
func (r *Repository) ExpireIdleScheduledAssignment(ctx context.Context, command AssignmentControl, now time.Time) (AssignmentRecord, error) {
	if strings.TrimSpace(command.Reason) == "" || command.ExpectedVersion < 1 {
		return AssignmentRecord{}, ErrInvalidContract
	}
	return r.updateAssignmentControl(ctx, command, "expire_idle_scheduled_occurrence", now, func(ctx context.Context, tx uow.Tx, assignment *AssignmentRecord) error {
		if assignment.SourceKind != "scheduler_agent_task" || assignment.Paused ||
			(assignment.State != AssignmentStateOffered && assignment.State != AssignmentStateActive) {
			return ErrConflict
		}
		records, err := uow.ListRecords(ctx, tx, attemptTable)
		if err != nil {
			return err
		}
		var latest AttemptRecord
		for _, record := range records {
			var attempt AttemptRecord
			if err := decodeRecord(record, &attempt); err != nil {
				return err
			}
			if attempt.ResponsibilityID == assignment.ID && attempt.Outcome == "" {
				return ErrConflict
			}
			if attempt.ResponsibilityID == assignment.ID && attempt.Attempt > latest.Attempt {
				latest = attempt
			}
		}
		if latest.Outcome == AttemptOutcomeCompleted {
			return ErrConflict
		}
		from := assignment.State
		if err := ValidateAssignmentTransition(from, AssignmentStateTerminated); err != nil {
			return err
		}
		assignment.State = AssignmentStateTerminated
		assignment.Paused = false
		assignment.TerminatedAt = now
		assignment.TerminatedBy = strings.TrimSpace(command.ActorID)
		assignment.TerminationNote = strings.TrimSpace(command.Reason)
		assignment.Version++
		assignment.UpdatedAt = now
		if err := supersedeOlderActivationsTx(ctx, tx, assignment.ID, assignment.Version, now); err != nil {
			return err
		}
		if err := resolveOpenBlockersForTerminalAssignmentTx(ctx, tx, *assignment, command.ActorID, command.Reason, now); err != nil {
			return err
		}
		return putAuditTx(ctx, tx, AuditRecord{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
			EntityType: "assignment", EntityID: assignment.ID, Action: AuditResponsibilityTerminated,
			ActorID: command.ActorID, FromState: string(from), ToState: string(assignment.State), Details: command.Reason, OccurredAt: now})
	})
}

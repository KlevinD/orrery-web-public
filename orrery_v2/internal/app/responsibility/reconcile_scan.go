package responsibility

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

const reconcileCursorTable = "responsibility_reconcile_cursors"

type reconcileCursor struct {
	AfterID string `json:"after_id"`
}

// ListAssignmentsForReconcile advances a durable cursor independently of the
// priority ordering used to dispatch work. Waiting reviews cannot indefinitely
// hide newer assignments, even across coordinator restarts.
func (r *Repository) ListAssignmentsForReconcile(ctx context.Context, limit int) ([]AssignmentRecord, error) {
	return r.listReconcileAssignments(ctx, "open-assignments", limit, false)
}

func (r *Repository) listRuntimeSupervisionAssignments(ctx context.Context, limit int) ([]AssignmentRecord, error) {
	return r.listReconcileAssignments(ctx, "runtime-attempts", limit, true)
}

func (r *Repository) listReconcileAssignments(ctx context.Context, key string, limit int, liveOnly bool) ([]AssignmentRecord, error) {
	if r == nil || r.unit == nil {
		return nil, ErrInvalidContract
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []AssignmentRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, reconcileCursorTable, key); err != nil {
			return err
		}
		var cursor reconcileCursor
		if record, found := tx.Get(reconcileCursorTable, key); found {
			if err := decodeRecord(record, &cursor); err != nil {
				return err
			}
		}
		live := map[string]bool{}
		if liveOnly {
			records, err := uow.ListRecords(ctx, tx, attemptTable)
			if err != nil {
				return err
			}
			for _, record := range records {
				var attempt AttemptRecord
				if err := decodeRecord(record, &attempt); err != nil {
					return err
				}
				if attempt.Outcome == "" {
					live[attempt.ResponsibilityID] = true
				}
			}
		}
		records, err := uow.ListRecords(ctx, tx, assignmentTable)
		if err != nil {
			return err
		}
		var candidates []AssignmentRecord
		for _, record := range records {
			var assignment AssignmentRecord
			if err := decodeRecord(record, &assignment); err != nil {
				return err
			}
			if liveOnly {
				if !live[assignment.ID] {
					continue
				}
			} else if assignmentTerminal(assignment.State) || assignment.State == AssignmentStateEscalated || assignment.Paused {
				continue
			}
			candidates = append(candidates, assignment)
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
		start := sort.Search(len(candidates), func(i int) bool { return candidates[i].ID > cursor.AfterID })
		if start == len(candidates) {
			start = 0
		}
		count := min(limit, len(candidates))
		for i := 0; i < count; i++ {
			out = append(out, candidates[(start+i)%len(candidates)])
		}
		if len(out) != 0 {
			return putRecordTx(ctx, tx, reconcileCursorTable, key, reconcileCursor{AfterID: out[len(out)-1].ID})
		}
		return ctx.Err()
	})
	return out, err
}

// Runtime recovery has its own scan window. A full business review backlog must
// not prevent failed or cancelled queue jobs from releasing Agent capacity.
func (c *Coordinator) reconcileRuntimeAttempts(ctx context.Context, limit int) (ReconcileStats, error) {
	stats := ReconcileStats{}
	assignments, err := c.repository.listRuntimeSupervisionAssignments(ctx, limit)
	if err != nil {
		return stats, err
	}
	var errList []error
	for _, assignment := range assignments {
		if err := ctx.Err(); err != nil {
			return stats, errors.Join(append(errList, err)...)
		}
		policy, err := c.repository.ResolvePolicy(ctx, assignment)
		if err != nil {
			errList = append(errList, fmt.Errorf("resolve assignment %s runtime policy: %w", assignment.ID, err))
			continue
		}
		if !assignment.Paused && !assignmentTerminal(assignment.State) && assignment.State != AssignmentStateEscalated {
			health, err := c.reconcileRuntimeHealth(ctx, assignment, policy)
			stats.Heartbeats += health.Heartbeats
			stats.TimedOut += health.TimedOut
			stats.BudgetBlocked += health.BudgetBlocked
			if err != nil {
				errList = append(errList, fmt.Errorf("supervise assignment %s runtime: %w", assignment.ID, err))
				continue
			}
		}
		recovered, err := c.recoverTerminalRuntimeOutcomes(ctx, assignment)
		stats.RecoveredOutcomes += recovered
		if err != nil {
			errList = append(errList, fmt.Errorf("recover assignment %s runtime outcome: %w", assignment.ID, err))
		}
	}
	stats.Failed = len(errList)
	return stats, errors.Join(errList...)
}

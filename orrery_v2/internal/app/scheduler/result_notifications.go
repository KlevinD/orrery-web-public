package scheduler

import (
	"context"
	"time"
)

// ListResultNotificationRuns includes every execution mode. CompletedAt fences
// intermediate failures which the dispatcher may still retry.
func (r *Repository) ListResultNotificationRuns(ctx context.Context, since time.Time, replay map[string]bool) ([]ScheduledRun, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	records, err := r.loadRunSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	var out []ScheduledRun
	for _, record := range records {
		var run ScheduledRun
		if err := decodePayload(record, &run); err != nil {
			return nil, err
		}
		if !ResultNotificationTerminal(run) {
			continue
		}
		at := run.CompletedAt
		if at.IsZero() {
			at = run.CreatedAt
		}
		if at.Before(since) && !replay[run.ScheduledRunID] {
			continue
		}
		out = append(out, run)
	}
	sortRuns(out)
	return out, nil
}

func ResultNotificationTerminal(run ScheduledRun) bool {
	switch run.Status {
	case RunStatusCompleted:
		return !run.CompletedAt.IsZero()
	case RunStatusFailed:
		return !run.CompletedAt.IsZero()
	case RunStatusSkipped:
		return true
	default:
		return false
	}
}

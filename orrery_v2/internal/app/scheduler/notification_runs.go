package scheduler

import (
	"context"
	"time"
)

// ListNotificationRuns shares the repository snapshot cache across publications
// and decodes only matching runs. Notification reconciliation never reads traces.
func (r *Repository) ListNotificationRuns(ctx context.Context, tenantID, taskID string, since time.Time) ([]ScheduledRun, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	records, err := r.loadRunSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	out := []ScheduledRun{}
	for _, record := range records {
		if recordString(record, "scheduled_task_id") != taskID {
			continue
		}
		var run ScheduledRun
		if err := decodePayload(record, &run); err != nil {
			return nil, err
		}
		if run.TenantID != tenantID || run.CreatedAt.Before(since) {
			continue
		}
		if run.Status != RunStatusCompleted && run.Status != RunStatusFailed {
			continue
		}
		out = append(out, run)
	}
	sortRuns(out)
	return out, nil
}

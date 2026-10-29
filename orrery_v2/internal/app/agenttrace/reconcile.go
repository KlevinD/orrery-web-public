package agenttrace

import (
	"context"
	"fmt"
	"time"
)

const interruptedTraceStaleAge = 10 * time.Minute

type InterruptedTraceRepository interface {
	ListInterruptedCandidates(context.Context, time.Time, int) ([]TraceRecord, error)
	Save(context.Context, TraceRecord) error
}

type InterruptedTraceObjects interface {
	AbortTrace(context.Context, TraceRecord, time.Time) error
}

type RunActivitySource interface {
	RunActive(context.Context, string, string) (bool, error)
}

type ReconcileReport struct {
	Scanned int
	Aborted int
}

type Reconciler struct {
	repository InterruptedTraceRepository
	objects    InterruptedTraceObjects
	activity   RunActivitySource
	now        func() time.Time
}

func NewReconciler(repository InterruptedTraceRepository, objects InterruptedTraceObjects, activity RunActivitySource, now func() time.Time) *Reconciler {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Reconciler{repository: repository, objects: objects, activity: activity, now: now}
}

func (r *Reconciler) Run(ctx context.Context, limit int) (ReconcileReport, error) {
	if r == nil || r.repository == nil || r.objects == nil || r.activity == nil {
		return ReconcileReport{}, fmt.Errorf("trace reconciliation dependencies are required")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	now := r.now().UTC()
	records, err := r.repository.ListInterruptedCandidates(ctx, now.Add(-interruptedTraceStaleAge), limit)
	if err != nil {
		return ReconcileReport{}, err
	}
	report := ReconcileReport{Scanned: len(records)}
	for _, record := range records {
		active, err := r.activity.RunActive(ctx, record.TenantID, record.RunID)
		if err != nil {
			return report, err
		}
		if active {
			continue
		}
		if err := r.objects.AbortTrace(ctx, record, now); err != nil {
			return report, err
		}
		record.Status = TraceHot
		record.TerminalStatus = "aborted"
		record.CompletedAt = now
		record.LatestEventAt = now
		record.RetainUntil = now.Add(abnormalTraceHotRetention)
		if err := r.repository.Save(ctx, record); err != nil {
			return report, err
		}
		report.Aborted++
	}
	return report, nil
}

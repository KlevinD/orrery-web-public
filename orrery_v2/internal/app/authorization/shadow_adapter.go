package authorization

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"orrery_v2/internal/app/identity"
)

type ShadowDecisionRecord struct {
	Request    CheckRequest
	Primary    Decision
	Shadow     Decision
	PrimaryErr error
	ShadowErr  error
	Mismatch   bool
	RecordedAt time.Time
}

type ShadowDecisionRecorder interface {
	RecordAuthorizationShadowDecision(ctx context.Context, record ShadowDecisionRecord)
}

type ShadowAdapter struct {
	primary  Adapter
	shadow   Adapter
	recorder ShadowDecisionRecorder
	now      func() time.Time
}

func NewShadowAdapter(primary Adapter, shadow Adapter, recorder ShadowDecisionRecorder) *ShadowAdapter {
	return &ShadowAdapter{primary: primary, shadow: shadow, recorder: recorder, now: time.Now}
}

func (a *ShadowAdapter) Check(ctx context.Context, req CheckRequest) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if a == nil || a.primary == nil {
		return Decision{}, ErrServiceDependency
	}
	primaryDecision, primaryErr := a.primary.Check(ctx, req)
	if primaryErr != nil {
		a.record(ctx, req, primaryDecision, Decision{}, primaryErr, nil)
		return primaryDecision, primaryErr
	}
	if a.shadow != nil {
		shadowDecision, shadowErr := a.shadow.Check(ctx, req)
		a.record(ctx, req, primaryDecision, shadowDecision, nil, shadowErr)
	}
	return primaryDecision, nil
}

func (a *ShadowAdapter) BatchCheck(ctx context.Context, reqs []CheckRequest) ([]Decision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil || a.primary == nil {
		return nil, ErrServiceDependency
	}
	primaryDecisions, primaryErr := a.primary.BatchCheck(ctx, reqs)
	if primaryErr != nil {
		for _, req := range reqs {
			a.record(ctx, req, Decision{}, Decision{}, primaryErr, nil)
		}
		return nil, primaryErr
	}
	if len(primaryDecisions) != len(reqs) {
		for _, req := range reqs {
			a.record(ctx, req, Decision{}, Decision{}, ErrServiceDependency, nil)
		}
		return nil, ErrServiceDependency
	}
	if a.shadow == nil {
		return primaryDecisions, nil
	}
	shadowDecisions, shadowErr := a.shadow.BatchCheck(ctx, reqs)
	if shadowErr == nil && len(shadowDecisions) != len(reqs) {
		shadowErr = ErrServiceDependency
	}
	for i, req := range reqs {
		var shadowDecision Decision
		if shadowErr == nil {
			shadowDecision = shadowDecisions[i]
		}
		a.record(ctx, req, primaryDecisions[i], shadowDecision, nil, shadowErr)
	}
	return primaryDecisions, nil
}

func (a *ShadowAdapter) ListObjects(ctx context.Context, principal identity.Principal, relation string, objectType string, tenantID string) ([]identity.ObjectRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil || a.primary == nil {
		return nil, ErrServiceDependency
	}
	objects, err := a.primary.ListObjects(ctx, principal, relation, objectType, tenantID)
	if errors.Is(err, ErrUnsupported) && a.shadow != nil {
		return a.shadow.ListObjects(ctx, principal, relation, objectType, tenantID)
	}
	return objects, err
}

func (a *ShadowAdapter) CheckRelation(ctx context.Context, tuple RelationTuple) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if a == nil || a.primary == nil {
		return false, ErrServiceDependency
	}
	if checker, ok := a.shadow.(RelationChecker); ok {
		return checker.CheckRelation(ctx, tuple)
	}
	if checker, ok := a.primary.(RelationChecker); ok {
		return checker.CheckRelation(ctx, tuple)
	}
	return false, ErrUnsupported
}

func (a *ShadowAdapter) ListRelations(ctx context.Context) ([]RelationTuple, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil {
		return nil, ErrServiceDependency
	}
	if lister, ok := a.shadow.(RelationLister); ok {
		return lister.ListRelations(ctx)
	}
	if lister, ok := a.primary.(RelationLister); ok {
		return lister.ListRelations(ctx)
	}
	return nil, ErrUnsupported
}

func (a *ShadowAdapter) WriteRelations(ctx context.Context, tuples []RelationTuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil || a.primary == nil {
		return ErrServiceDependency
	}
	primaryErr := a.primary.WriteRelations(ctx, tuples)
	if errors.Is(primaryErr, ErrUnsupported) {
		primaryErr = nil
	}
	if a.shadow != nil {
		if err := a.shadow.WriteRelations(ctx, tuples); err != nil {
			return err
		}
	}
	return primaryErr
}

func (a *ShadowAdapter) DeleteRelations(ctx context.Context, tuples []RelationTuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil || a.primary == nil {
		return ErrServiceDependency
	}
	primaryErr := a.primary.DeleteRelations(ctx, tuples)
	if errors.Is(primaryErr, ErrUnsupported) {
		primaryErr = nil
	}
	if a.shadow != nil {
		if err := a.shadow.DeleteRelations(ctx, tuples); err != nil {
			return err
		}
	}
	return primaryErr
}

func (a *ShadowAdapter) record(ctx context.Context, req CheckRequest, primary Decision, shadow Decision, primaryErr error, shadowErr error) {
	if a == nil || a.recorder == nil {
		return
	}
	now := time.Now
	if a.now != nil {
		now = a.now
	}
	a.recorder.RecordAuthorizationShadowDecision(ctx, ShadowDecisionRecord{
		Request:    normalizeCheckRequest(req),
		Primary:    primary,
		Shadow:     shadow,
		PrimaryErr: primaryErr,
		ShadowErr:  shadowErr,
		Mismatch:   primary.Allowed != shadow.Allowed || primaryErr != nil || shadowErr != nil,
		RecordedAt: now().UTC(),
	})
}

type LoggingShadowRecorder struct {
	Logger interface {
		Printf(format string, v ...any)
	}
	mu    sync.Mutex
	stats ShadowDecisionStats
}

type ShadowDecisionStats struct {
	ShadowDecisionCounter
	ByObjectType map[string]ShadowDecisionCounter
	ByAction     map[string]ShadowDecisionCounter
}

type ShadowDecisionCounter struct {
	Total                  int
	Mismatches             int
	PrimaryErrors          int
	ShadowErrors           int
	PrimaryAllowShadowDeny int
	PrimaryDenyShadowAllow int
}

func NewLoggingShadowRecorder(logger interface {
	Printf(format string, v ...any)
}) *LoggingShadowRecorder {
	if logger == nil {
		logger = log.Default()
	}
	return &LoggingShadowRecorder{Logger: logger}
}

func (r *LoggingShadowRecorder) RecordAuthorizationShadowDecision(ctx context.Context, record ShadowDecisionRecord) {
	_ = ctx
	if r == nil {
		return
	}
	record.Request = normalizeCheckRequest(record.Request)
	record.Mismatch = record.Mismatch || record.Primary.Allowed != record.Shadow.Allowed || record.PrimaryErr != nil || record.ShadowErr != nil
	stats := r.updateStats(record)
	if r.Logger == nil || !record.Mismatch {
		return
	}
	r.Logger.Printf(
		"authorization shadow mismatch action=%s object=%s:%s principal=%s:%s primary_allowed=%t shadow_allowed=%t primary_err=%v shadow_err=%v total=%d mismatches=%d primary_allow_shadow_deny=%d primary_deny_shadow_allow=%d primary_errors=%d shadow_errors=%d",
		record.Request.Action,
		record.Request.Object.Type,
		record.Request.Object.ID,
		record.Request.Principal.ViewerKind,
		record.Request.Principal.UserID,
		record.Primary.Allowed,
		record.Shadow.Allowed,
		record.PrimaryErr,
		record.ShadowErr,
		stats.Total,
		stats.Mismatches,
		stats.PrimaryAllowShadowDeny,
		stats.PrimaryDenyShadowAllow,
		stats.PrimaryErrors,
		stats.ShadowErrors,
	)
}

func (r *LoggingShadowRecorder) updateStats(record ShadowDecisionRecord) ShadowDecisionStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	applyShadowDecisionCounter(&r.stats.ShadowDecisionCounter, record)
	r.stats.ByObjectType = applyShadowDecisionBucket(r.stats.ByObjectType, shadowDecisionObjectType(record), record)
	r.stats.ByAction = applyShadowDecisionBucket(r.stats.ByAction, shadowDecisionAction(record), record)
	return cloneShadowDecisionStats(r.stats)
}

func (r *LoggingShadowRecorder) Snapshot() ShadowDecisionStats {
	if r == nil {
		return ShadowDecisionStats{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneShadowDecisionStats(r.stats)
}

func applyShadowDecisionBucket(buckets map[string]ShadowDecisionCounter, key string, record ShadowDecisionRecord) map[string]ShadowDecisionCounter {
	if buckets == nil {
		buckets = map[string]ShadowDecisionCounter{}
	}
	counter := buckets[key]
	applyShadowDecisionCounter(&counter, record)
	buckets[key] = counter
	return buckets
}

func applyShadowDecisionCounter(counter *ShadowDecisionCounter, record ShadowDecisionRecord) {
	if counter == nil {
		return
	}
	counter.Total++
	if record.Mismatch {
		counter.Mismatches++
	}
	if record.PrimaryErr != nil {
		counter.PrimaryErrors++
	}
	if record.ShadowErr != nil {
		counter.ShadowErrors++
	}
	if record.PrimaryErr == nil && record.ShadowErr == nil {
		switch {
		case record.Primary.Allowed && !record.Shadow.Allowed:
			counter.PrimaryAllowShadowDeny++
		case !record.Primary.Allowed && record.Shadow.Allowed:
			counter.PrimaryDenyShadowAllow++
		}
	}
}

func shadowDecisionObjectType(record ShadowDecisionRecord) string {
	objectType := normalizeKind(record.Request.Object.Type)
	if objectType == "" {
		return "unknown"
	}
	return objectType
}

func shadowDecisionAction(record ShadowDecisionRecord) string {
	action := normalizeAction(record.Request.Action)
	if action == "" {
		return "unknown"
	}
	return action
}

func cloneShadowDecisionStats(stats ShadowDecisionStats) ShadowDecisionStats {
	stats.ByObjectType = cloneShadowDecisionCounterMap(stats.ByObjectType)
	stats.ByAction = cloneShadowDecisionCounterMap(stats.ByAction)
	return stats
}

func cloneShadowDecisionCounterMap(in map[string]ShadowDecisionCounter) map[string]ShadowDecisionCounter {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]ShadowDecisionCounter, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

var _ Adapter = (*ShadowAdapter)(nil)

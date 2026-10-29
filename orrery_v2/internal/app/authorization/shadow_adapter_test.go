package authorization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"orrery_v2/internal/app/identity"
)

func TestShadowAdapterReturnsPrimaryDecisionAndRecordsMismatch(t *testing.T) {
	recorder := &recordingShadowRecorder{}
	adapter := NewShadowAdapter(
		&fakeAuthorizationAdapter{decision: Decision{Allowed: true, Source: DecisionSourceScope}},
		&fakeAuthorizationAdapter{decision: Decision{Allowed: false, Source: DecisionSourceOpenFGA}},
		recorder,
	)

	decision, err := adapter.Check(context.Background(), CheckRequest{
		Principal: testPrincipalWithScopes("mission.read"),
		Action:    "mission.view",
		Object:    identity.ObjectRef{Type: "mission", ID: "mission-1"},
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !decision.Allowed || decision.Source != DecisionSourceScope {
		t.Fatalf("decision = %+v", decision)
	}
	if len(recorder.records) != 1 || !recorder.records[0].Mismatch {
		t.Fatalf("records = %+v", recorder.records)
	}
}

func TestShadowAdapterIgnoresShadowCheckError(t *testing.T) {
	shadowErr := errors.New("openfga unavailable")
	recorder := &recordingShadowRecorder{}
	adapter := NewShadowAdapter(
		&fakeAuthorizationAdapter{decision: Decision{Allowed: true, Source: DecisionSourceScope}},
		&fakeAuthorizationAdapter{err: shadowErr},
		recorder,
	)

	decision, err := adapter.Check(context.Background(), CheckRequest{
		Principal: testPrincipalWithScopes("mission.read"),
		Action:    "mission.view",
		Object:    identity.ObjectRef{Type: "mission", ID: "mission-1"},
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("decision = %+v", decision)
	}
	if len(recorder.records) != 1 || !errors.Is(recorder.records[0].ShadowErr, shadowErr) {
		t.Fatalf("records = %+v", recorder.records)
	}
}

func TestShadowAdapterBatchCheckUsesAdapterBatchMethods(t *testing.T) {
	primary := &fakeAuthorizationAdapter{decision: Decision{Allowed: true, Source: DecisionSourceScope}}
	shadow := &fakeAuthorizationAdapter{decision: Decision{Allowed: false, Source: DecisionSourceOpenFGA}}
	recorder := &recordingShadowRecorder{}
	adapter := NewShadowAdapter(primary, shadow, recorder)
	requests := []CheckRequest{
		{Principal: testPrincipalWithScopes("mission.read"), Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: "mission-1"}},
		{Principal: testPrincipalWithScopes("mission.read"), Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: "mission-2"}},
	}

	decisions, err := adapter.BatchCheck(context.Background(), requests)
	if err != nil {
		t.Fatalf("BatchCheck: %v", err)
	}
	if len(decisions) != 2 || !decisions[0].Allowed || !decisions[1].Allowed {
		t.Fatalf("decisions = %+v", decisions)
	}
	if len(primary.batchChecked) != 1 || len(shadow.batchChecked) != 1 || len(primary.checked) != 0 || len(shadow.checked) != 0 {
		t.Fatalf("primary batch=%d single=%d, shadow batch=%d single=%d", len(primary.batchChecked), len(primary.checked), len(shadow.batchChecked), len(shadow.checked))
	}
	if len(recorder.records) != 2 || !recorder.records[0].Mismatch || !recorder.records[1].Mismatch {
		t.Fatalf("records = %+v", recorder.records)
	}
}

func TestShadowAdapterWritesRelationsToShadowWhenPrimaryUnsupported(t *testing.T) {
	shadow := &fakeAuthorizationAdapter{}
	adapter := NewShadowAdapter(&fakeAuthorizationAdapter{writeErr: ErrUnsupported}, shadow, nil)
	tuples := []RelationTuple{{
		Subject:  SubjectRef{Kind: "user", ID: "operator"},
		Relation: "editor",
		Object:   identity.ObjectRef{Type: "mission", ID: "mission-1"},
	}}

	if err := adapter.WriteRelations(context.Background(), tuples); err != nil {
		t.Fatalf("WriteRelations: %v", err)
	}
	if len(shadow.written) != 1 || shadow.written[0].Object.ID != "mission-1" {
		t.Fatalf("shadow writes = %+v", shadow.written)
	}
}

func TestShadowAdapterDeletesRelationsFromShadowWhenPrimaryUnsupported(t *testing.T) {
	shadow := &fakeAuthorizationAdapter{}
	adapter := NewShadowAdapter(&fakeAuthorizationAdapter{deleteErr: ErrUnsupported}, shadow, nil)
	tuples := []RelationTuple{{
		Subject:  SubjectRef{Kind: "agent", ID: "agent-1"},
		Relation: "operator",
		Object:   identity.ObjectRef{Type: "resource", ID: "tool.deploy"},
	}}

	if err := adapter.DeleteRelations(context.Background(), tuples); err != nil {
		t.Fatalf("DeleteRelations: %v", err)
	}
	if len(shadow.deleted) != 1 || shadow.deleted[0].Object.ID != "tool.deploy" {
		t.Fatalf("shadow deletes = %+v", shadow.deleted)
	}
}

func TestShadowAdapterChecksRelationsAgainstShadowFirst(t *testing.T) {
	primary := &fakeAuthorizationAdapter{relationAllowed: false}
	shadow := &fakeAuthorizationAdapter{relationAllowed: true}
	adapter := NewShadowAdapter(primary, shadow, nil)
	tuple := RelationTuple{
		Subject:  SubjectRef{Kind: "agent", ID: "agent-1"},
		Relation: "operator",
		Object:   identity.ObjectRef{Type: "resource", ID: "tool.deploy"},
	}

	allowed, err := adapter.CheckRelation(context.Background(), tuple)
	if err != nil {
		t.Fatalf("CheckRelation: %v", err)
	}
	if !allowed {
		t.Fatalf("CheckRelation allowed = false")
	}
	if len(shadow.checkedRelations) != 1 || shadow.checkedRelations[0].Object.ID != "tool.deploy" {
		t.Fatalf("shadow checked relations = %+v", shadow.checkedRelations)
	}
	if len(primary.checkedRelations) != 0 {
		t.Fatalf("primary should not be checked when shadow supports relation check: %+v", primary.checkedRelations)
	}
}

func TestLoggingShadowRecorderAggregatesMismatchStats(t *testing.T) {
	logger := &recordingPrintfLogger{}
	recorder := NewLoggingShadowRecorder(logger)

	recorder.RecordAuthorizationShadowDecision(context.Background(), ShadowDecisionRecord{
		Request:  CheckRequest{Principal: testPrincipalWithScopes("mission.read"), Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: "mission-1"}},
		Primary:  Decision{Allowed: true},
		Shadow:   Decision{Allowed: false},
		Mismatch: true,
	})
	recorder.RecordAuthorizationShadowDecision(context.Background(), ShadowDecisionRecord{
		Request:   CheckRequest{Principal: testPrincipalWithScopes("mission.read"), Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: "mission-2"}},
		Primary:   Decision{Allowed: false},
		ShadowErr: errors.New("openfga down"),
		Mismatch:  true,
	})
	recorder.RecordAuthorizationShadowDecision(context.Background(), ShadowDecisionRecord{
		Request: CheckRequest{Principal: testPrincipalWithScopes("mission.read"), Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: "mission-3"}},
		Primary: Decision{Allowed: false},
		Shadow:  Decision{Allowed: true},
	})

	stats := recorder.Snapshot()
	if stats.Total != 3 || stats.Mismatches != 3 || stats.PrimaryAllowShadowDeny != 1 || stats.PrimaryDenyShadowAllow != 1 || stats.ShadowErrors != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.ByObjectType["mission"].Total != 3 || stats.ByObjectType["mission"].Mismatches != 3 {
		t.Fatalf("object type stats = %+v", stats.ByObjectType)
	}
	if stats.ByAction["mission.view"].Total != 3 || stats.ByAction["mission.view"].ShadowErrors != 1 {
		t.Fatalf("action stats = %+v", stats.ByAction)
	}
	stats.ByObjectType["mission"] = ShadowDecisionCounter{}
	if next := recorder.Snapshot(); next.ByObjectType["mission"].Total != 3 {
		t.Fatalf("snapshot should be isolated from caller mutation: %+v", next.ByObjectType)
	}
	if len(logger.lines) != 3 || !strings.Contains(logger.lines[2], "mismatches=3") || !strings.Contains(logger.lines[2], "primary_deny_shadow_allow=1") {
		t.Fatalf("logs = %+v", logger.lines)
	}
}

type recordingShadowRecorder struct {
	records []ShadowDecisionRecord
}

func (r *recordingShadowRecorder) RecordAuthorizationShadowDecision(ctx context.Context, record ShadowDecisionRecord) {
	_ = ctx
	r.records = append(r.records, record)
}

type fakeAuthorizationAdapter struct {
	decision         Decision
	err              error
	writeErr         error
	deleteErr        error
	relationAllowed  bool
	relationErr      error
	checked          []CheckRequest
	batchChecked     [][]CheckRequest
	written          []RelationTuple
	deleted          []RelationTuple
	checkedRelations []RelationTuple
}

func (f *fakeAuthorizationAdapter) Check(ctx context.Context, req CheckRequest) (Decision, error) {
	_ = ctx
	f.checked = append(f.checked, req)
	return f.decision, f.err
}

func (f *fakeAuthorizationAdapter) BatchCheck(ctx context.Context, reqs []CheckRequest) ([]Decision, error) {
	f.batchChecked = append(f.batchChecked, append([]CheckRequest(nil), reqs...))
	out := make([]Decision, 0, len(reqs))
	for range reqs {
		out = append(out, f.decision)
	}
	return out, f.err
}

func (f *fakeAuthorizationAdapter) ListObjects(ctx context.Context, principal identity.Principal, relation string, objectType string, tenantID string) ([]identity.ObjectRef, error) {
	_ = ctx
	_ = principal
	_ = relation
	_ = objectType
	_ = tenantID
	return nil, ErrUnsupported
}

func (f *fakeAuthorizationAdapter) WriteRelations(ctx context.Context, tuples []RelationTuple) error {
	_ = ctx
	if f.writeErr != nil {
		return f.writeErr
	}
	f.written = append(f.written, tuples...)
	return nil
}

func (f *fakeAuthorizationAdapter) DeleteRelations(ctx context.Context, tuples []RelationTuple) error {
	_ = ctx
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, tuples...)
	return nil
}

func (f *fakeAuthorizationAdapter) CheckRelation(ctx context.Context, tuple RelationTuple) (bool, error) {
	_ = ctx
	f.checkedRelations = append(f.checkedRelations, tuple)
	if f.relationErr != nil {
		return false, f.relationErr
	}
	return f.relationAllowed, nil
}

var _ Adapter = (*fakeAuthorizationAdapter)(nil)
var _ RelationChecker = (*fakeAuthorizationAdapter)(nil)

type recordingPrintfLogger struct {
	lines []string
}

func (l *recordingPrintfLogger) Printf(format string, v ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, v...))
}

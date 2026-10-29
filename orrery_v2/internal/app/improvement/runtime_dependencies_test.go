package improvement

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func dependencyTraceFixture(t *testing.T) (*runtimeEvidenceTraceSession, RuntimeRequestEvidence) {
	t.Helper()
	store := testStore(t)
	req, request := runtimeEvidenceFixture(t, "dependency-attempt")
	execution := executionFixture(t, request)
	execution.Snapshot.DependencyProtocol = agentcore.RuntimeDependencyProtocol
	identity := execution.Implementation
	recorder := RuntimeEvidenceRecorder{Recorder: &runtimeEvidenceTestTrace{}, Evidence: store.RuntimeEvidence(), Implementation: &identity, Now: func() time.Time { return request.CapturedAt }, Scope: func(agentcore.RuntimeRequest) (RuntimeEvidenceScope, error) { return request.Scope, nil }}
	trace, err := recorder.BeginTrace(context.Background(), agentcore.AgentInput{})
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.RecordRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	s := trace.(*runtimeEvidenceTraceSession)
	if err := s.RecordExecution(context.Background(), execution.Snapshot); err != nil {
		t.Fatal(err)
	}
	return s, *s.captured
}

func dependencyModelObservation(request RuntimeRequestEvidence) agentcore.RuntimeDependencyObservation {
	return agentcore.RuntimeDependencyObservation{TenantID: request.ExecutionTenantID, AgentID: request.AgentID, RunID: request.RunID, Kind: "model_dispatch", InputSHA256: Digest("private model input"), ToolsSHA256: Digest("resolved schemas")}
}

func TestRuntimeDependenciesFreezeOriginalAttemptAndKeepBaselineGaps(t *testing.T) {
	ctx := context.Background()
	s, request := dependencyTraceFixture(t)
	s.RecordDependency(dependencyModelObservation(request))
	o := agentcore.RuntimeDependencyObservation{TenantID: request.ExecutionTenantID, AgentID: request.AgentID, RunID: request.RunID, Kind: "tool_dispatch", ToolID: "tool-a", Version: "v2", InputSHA256: Digest("private arguments"), BindingSHA256: request.Bindings[0].Fingerprint, CallSHA256: Digest("call-a")}
	s.RecordDependency(o)
	s.RecordDependency(agentcore.RuntimeDependencyObservation{TenantID: request.ExecutionTenantID, AgentID: request.AgentID, RunID: request.RunID, Kind: "runner_returned"})
	if err := s.Finish(ctx, runtimeEvidenceTerminal()); err != nil {
		t.Fatal(err)
	}
	end, err := s.evidence.GetTerminal(ctx, request.Scope.TenantID, request.AttemptID)
	if err != nil || end.Dependencies == nil || len(end.Dependencies.Entries) != 2 || !end.Dependencies.RunnerReturned || end.Dependencies.ExecutionID != s.execution.ID {
		t.Fatalf("receipt %+v %v", end, err)
	}
	raw := encode(end)["payload"].(string)
	for _, secret := range []string{"private model input", "private arguments", "private credential"} {
		if strings.Contains(raw, secret) {
			t.Fatal("raw content stored")
		}
	}
	s.RecordDependency(o)
	if err := s.Finish(ctx, runtimeEvidenceTerminal()); err != nil {
		t.Fatal("repeat finish changed frozen receipt", err)
	}
	again, _ := s.evidence.GetTerminal(ctx, request.Scope.TenantID, request.AttemptID)
	if again.ID != end.ID {
		t.Fatal("late callback changed terminal")
	}
	r, persisted, err := s.evidence.GetReview(ctx, request.Scope.TenantID, request.AttemptID)
	if err != nil || !persisted || r.BaselineReady || r.BusinessQuality != "unassessed" || r.CausalAttribution != "unresolved" || len(r.Domains) != 9 {
		t.Fatalf("invented baseline: %+v %v", r, err)
	}
	for _, d := range r.Domains {
		if len(d.Gaps) == 0 {
			t.Fatal("dispatch erased domain gaps")
		}
	}
	if _, err := s.evidence.GetTerminal(ctx, "other-tenant", request.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-tenant receipt", err)
	}
	c, err := s.evidence.store.Get(ctx, request.Scope.TenantID, caseID(mustObservation(t, request, end)))
	if err != nil {
		t.Fatal(err)
	}
	event, err := s.evidence.store.RuntimeDependenciesForCase(ctx, c)
	if err != nil || event == nil || event.Payload["model_dispatches"] != 1 || event.Payload["tool_dispatches"] != 1 || event.Payload["baseline_ready"] != false {
		t.Fatalf("projection %+v %v", event, err)
	}
	end.Dependencies.Entries[0].InputSHA256 = Digest("changed")
	if validateRuntimeTerminal(request, end) == nil {
		t.Fatal("changed receipt accepted")
	}
}

func mustObservation(t *testing.T, request RuntimeRequestEvidence, end RuntimeTerminalEvidence) Observation {
	t.Helper()
	o, err := capturedRuntimeObservation(request, end)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestRuntimeDependenciesBoundAndCrossRunObservationsStayPartial(t *testing.T) {
	s, request := dependencyTraceFixture(t)
	o := dependencyModelObservation(request)
	wrong := o
	wrong.RunID = "other-run"
	s.RecordDependency(wrong)
	for i := 0; i < maxRuntimeDependencyEntries+3; i++ {
		s.RecordDependency(o)
	}
	if err := s.Finish(context.Background(), runtimeEvidenceTerminal()); err != nil {
		t.Fatal(err)
	}
	end, err := s.evidence.GetTerminal(context.Background(), request.Scope.TenantID, request.AttemptID)
	if err != nil || end.Dependencies.Dropped != 3 || end.Dependencies.Invalid != 1 || end.Dependencies.RunnerReturned || len(end.Dependencies.Entries) != maxRuntimeDependencyEntries {
		t.Fatalf("false completeness %+v %v", end.Dependencies, err)
	}
	feedback := RuntimeFeedback{TenantID: request.Scope.TenantID, Terminal: end}
	journal := feedbackJournal(t, t.TempDir())
	if err := journal.Save(context.Background(), feedback); err != nil {
		t.Fatal("bounded receipt cannot be journaled", err)
	}
	entries, err := journal.Scan(context.Background(), 10)
	if err != nil || len(entries) != 1 || Digest(entries[0]) != Digest(feedback) {
		t.Fatal("journal changed receipt", err)
	}
	if _, err := s.evidence.RecoverFeedback(context.Background(), journal, 10); err != nil {
		t.Fatal("idempotent recovery", err)
	}
}

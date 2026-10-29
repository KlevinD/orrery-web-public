package improvement

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

func TestRuntimeReviewSeparatesFactsQualityCausalityAndCoverage(t *testing.T) {
	_, request := runtimeEvidenceFixture(t, "review-a")
	prepared, err := BuildRuntimeReview(request, nil)
	if err != nil || prepared.PipelineStatus != "unknown" || prepared.TerminalID != "" || prepared.DurationMS != nil || prepared.TraceStatus != "unknown" {
		t.Fatalf("prepared attempt acquired an outcome: %+v %v", prepared, err)
	}
	for _, status := range []agentcore.RuntimeStatus{agentcore.RuntimeStatusCompleted, agentcore.RuntimeStatusFailed, agentcore.RuntimeStatusTimeout, agentcore.RuntimeStatusCancelled, agentcore.RuntimeStatusBlocked} {
		end := runtimeEvidenceTerminal()
		end.Status, end.Response.DurationMs = status, 0
		terminal, err := buildRuntimeTerminal(request, end, "succeeded")
		if err != nil {
			t.Fatal(err)
		}
		review, err := BuildRuntimeReview(request, &terminal)
		if err != nil || review.PipelineStatus != string(status) || review.BusinessQuality != "unassessed" || review.CausalAttribution != "unresolved" || review.BaselineReady || len(review.Domains) != 9 || review.DurationMS == nil || *review.DurationMS != 0 {
			t.Fatalf("status promoted to business judgment: %+v %v", review, err)
		}
		byKind := map[TargetKind]RuntimeDomainCoverage{}
		for _, domain := range review.Domains {
			if len(domain.Gaps) == 0 {
				t.Fatal("partial capture was treated as a complete baseline")
			}
			byKind[domain.Kind] = domain
		}
		if byKind[TargetAgentLoop].Coverage != "controls_only" || byKind[TargetAgentLoop].ControlsSHA256 != request.ControlsSHA256 || byKind[TargetKnowledge].Coverage != "uncaptured" || byKind[TargetSkill].Coverage != "declared_bindings_only" {
			t.Fatalf("false dependency coverage: %+v", byKind)
		}
		raw := encode(review)["payload"].(string)
		for _, private := range []string{"private task content", "private prompt", "private credential", "private tool credential"} {
			if strings.Contains(raw, private) {
				t.Fatal("review leaked raw task content")
			}
		}
		review.ReportedUsage["model-a"] = agentcore.TokenUsage{InputTokens: 999}
		if terminal.Usage["model-a"].InputTokens != 15 {
			t.Fatal("review aliases terminal usage")
		}
	}
}

func TestRuntimeReviewRetainsMissingAndConflictingBindings(t *testing.T) {
	_, e := runtimeEvidenceFixture(t, "review-conflicts")
	e.Bindings = append(e.Bindings, RuntimeBindingEvidence{Kind: TargetTool, ID: "tool-a", Alias: "second-alias", Fingerprint: Digest("another contract")})
	e.ID = ""
	e.ID = "runtime-request-evidence:" + Digest(e)
	r, err := BuildRuntimeReview(e, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range r.Domains {
		if d.Kind != TargetTool {
			continue
		}
		gaps := strings.Join(d.Gaps, ",")
		if len(d.Bindings) != 2 || !strings.Contains(gaps, "resource_version_missing") || !strings.Contains(gaps, "resource_version_ambiguous") || !strings.Contains(gaps, "tool_implementation_unverified") {
			t.Fatalf("conflicting/missing version was collapsed: %+v", d)
		}
	}
	bad := e
	bad.ControlsSHA256 = Digest("tampered")
	if _, err := BuildRuntimeReview(bad, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("review accepted a request with invalid identity")
	}
}

func TestRuntimeReviewRejectsSourceReplayBeforeExternalOperations(t *testing.T) {
	w, model, runner, spec := replayTestWorker(t)
	for _, trigger := range []string{"captured_runtime_completed", "runtime_completed", "runtime_failed", "runtime_timed_out", "runtime_cancelled", "runtime_blocked"} {
		spec.Observation.Trigger = trigger
		if _, err := w.Run(context.Background(), spec); !errors.Is(err, ErrUnavailable) || model.calls != 0 || runner.calls != 0 {
			t.Fatalf("production baseline reached unqualified source runner: %v model=%d runner=%d", err, model.calls, runner.calls)
		}
	}
	page, err := w.Store.List(context.Background(), spec.Observation.TenantID, "", 100)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("rejected replay wrote a case or claimed work")
	}
}

func TestRuntimeReviewAtomicPersistenceLegacyReplayAndIsolation(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	evidence := NewRuntimeEvidenceStore(store.core)
	_, request := runtimeEvidenceFixture(t, "review-persisted")
	if err := evidence.Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, persisted, err := evidence.GetReview(ctx, request.Scope.TenantID, request.AttemptID); err != nil || persisted {
		t.Fatalf("prepared read wrote a review: %v %v", persisted, err)
	}
	terminal := runtimeEvidenceTerminal()
	c, err := evidence.Complete(ctx, request, terminal)
	if err != nil {
		t.Fatal(err)
	}
	r, persisted, err := evidence.GetReview(ctx, c.TenantID, request.AttemptID)
	if err != nil || !persisted || r.RequestID != request.ID {
		t.Fatalf("review missing: %+v %v %v", r, persisted, err)
	}
	if _, _, err := evidence.GetReview(ctx, "tenant-b", request.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("review crossed tenant: %v", err)
	}
	key := runtimeReviewKey(c.TenantID, request.AttemptID)
	record, _, _ := store.core.GetRecord(ctx, runtimeReviewTable, key)
	if deleted, err := store.core.CompareAndDeleteRecord(ctx, runtimeReviewTable, key, record); err != nil || !deleted {
		t.Fatal(err)
	}
	derived, persisted, err := evidence.GetReview(ctx, c.TenantID, request.AttemptID)
	if err != nil || persisted || !reflect.DeepEqual(derived, r) {
		t.Fatalf("legacy read changed evidence: %+v %v %v", derived, persisted, err)
	}
	if _, exists, _ := store.core.GetRecord(ctx, runtimeReviewTable, key); exists {
		t.Fatal("read-only inspection backfilled a row")
	}
	again, err := evidence.Complete(ctx, request, terminal)
	if err != nil || again.Revision != c.Revision || !reflect.DeepEqual(again.Observation, c.Observation) {
		t.Fatalf("legacy journal replay rewrote a case: %+v %v", again, err)
	}
	if got, err := store.RuntimeReviewForCase(ctx, c); err != nil || got.ID != r.ID {
		t.Fatalf("case cannot resolve original review: %+v %v", got, err)
	}
	wrong := privateCopy(c)
	wrong.Observation.Task.ID = "other-task"
	if _, err := store.RuntimeReviewForCase(ctx, wrong); !errors.Is(err, ErrInvalid) {
		t.Fatalf("review attached to another case: %v", err)
	}
	for _, table := range []string{caseTable, historyTable, outboxTable, runtimeReviewTable} {
		page, err := store.core.ListRecords(ctx, table, "", 100)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("duplicate %s: %d %v", table, len(page.Items), err)
		}
	}
	forged := r
	forged.BaselineReady = true
	forged.ID = ""
	forged.ID = "runtime-review:" + Digest(forged)
	if err := store.core.PutRecord(ctx, runtimeReviewTable, key, encode(forged)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := evidence.GetReview(ctx, c.TenantID, request.AttemptID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self-consistent forged ready flag accepted: %v", err)
	}
}

func TestRuntimeReviewConflictRollsBackTerminalCaseAndOutbox(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	evidence := NewRuntimeEvidenceStore(store.core)
	_, request := runtimeEvidenceFixture(t, "review-rollback")
	if err := evidence.Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	key := runtimeReviewKey(request.Scope.TenantID, request.AttemptID)
	if err := store.core.WithTx(ctx, func(tx db.Tx) error {
		_, err := db.InsertRecordTx(ctx, tx, runtimeReviewTable, key, encode("conflict"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := evidence.Complete(ctx, request, runtimeEvidenceTerminal()); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected review conflict: %v", err)
	}
	if _, err := evidence.GetTerminal(ctx, request.Scope.TenantID, request.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan terminal committed: %v", err)
	}
	for _, table := range []string{caseTable, historyTable, outboxTable} {
		page, _ := store.core.ListRecords(ctx, table, "", 100)
		if len(page.Items) != 0 {
			t.Fatalf("orphan %s committed", table)
		}
	}
}

func TestRuntimeReviewEmptyUsageRemainsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	evidence := NewRuntimeEvidenceStore(store.core)
	_, request := runtimeEvidenceFixture(t, "review-empty-usage")
	if err := evidence.Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	end := runtimeEvidenceTerminal()
	end.Response.Usage = map[string]agentcore.TokenUsage{}
	for range 2 {
		if _, err := evidence.Complete(ctx, request, end); err != nil {
			t.Fatalf("empty usage changed review identity on replay: %v", err)
		}
	}
}

type reviewTestVerifier struct {
	*testVerifier
	baselineCalls int
	err           error
}

func (v *reviewTestVerifier) VerifyRuntimeBaseline(_ context.Context, r RuntimeReview, c Case, t Transition) error {
	v.baselineCalls++
	if r.BaselineReady || len(r.Domains) != 9 || r.RequestID != c.Observation.EvidenceIDs[0] || t.ChangeSet != nil {
		return ErrInvalid
	}
	// Test integration only: exercise the port and isolation, not real proof.
	r.Domains[0].Gaps[0] = "mutated"
	c.ChangeSet.Baseline[0].SHA256 = "mutated"
	return v.err
}

func TestRuntimeReviewGatesExperimentsAndAdoptionWithoutBlockingCompensation(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	evidence := NewRuntimeEvidenceStore(store.core)
	_, request := runtimeEvidenceFixture(t, "review-admission")
	if err := evidence.Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	c, err := evidence.Complete(ctx, request, runtimeEvidenceTerminal())
	if err != nil {
		t.Fatal(err)
	}
	c, err = store.Claim(ctx, c.TenantID, c.ID, "learner-a", c.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cmd := testCommand(c, Diagnosed)
	cmd.Diagnosis = &Diagnosis{Summary: "test hypothesis awaiting proof", EvidenceIDs: c.Observation.EvidenceIDs, AffectedKinds: []TargetKind{TargetTool, TargetAgentLoop, TargetKnowledge}}
	c = advanceTest(t, store, cmd, nil)
	cs := testChangeSet()
	for _, dep := range c.Observation.Dependencies {
		if dep.SHA256 == "" {
			dep.SHA256 = Digest("test-resolved-bytes")
		}
		cs.Baseline = append(cs.Baseline, dep)
	}
	cmd = testCommand(c, Proposed)
	cmd.ChangeSet = &cs
	c = advanceTest(t, store, cmd, nil)
	cmd = testCommand(c, Tested)
	cmd.Experiment = &Experiment{ID: "test-exp", ChangeSetHash: Digest(c.ChangeSet), BaselineRun: ObjectRef{Kind: "agent_run", ID: "baseline"}, CandidateRun: ObjectRef{Kind: "agent_run", ID: "candidate"}, Manifest: testArtifact("manifest"), Result: testArtifact("result")}
	plain := &testVerifier{}
	if _, err := store.Advance(ctx, cmd, plain); !errors.Is(err, ErrUnavailable) || plain.called != 0 {
		t.Fatalf("plain verifier bypassed baseline admission: %v", err)
	}
	trusted := &reviewTestVerifier{testVerifier: &testVerifier{}, err: ErrInvalid}
	if _, err := store.Advance(ctx, cmd, trusted); !errors.Is(err, ErrInvalid) || trusted.called != 0 {
		t.Fatalf("failed baseline still ran execution verification: %v", err)
	}
	trusted.err = nil
	c = advanceTest(t, store, cmd, trusted)
	c = evaluatedTestCase(t, store, c, trusted)
	cmd = testCommand(c, Monitoring)
	r := &Release{ID: "test-release", EvaluationID: c.Evaluation.ID, ChangeSetHash: Digest(c.ChangeSet)}
	for _, change := range c.ChangeSet.Changes {
		r.Targets = append(r.Targets, TargetReceipt{Version: change.Candidate, ActionRunID: "test-apply", Receipt: testArtifact("receipt")})
	}
	cmd.Release = r
	if _, err := store.Advance(ctx, cmd, plain); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("adoption bypassed baseline admission: %v", err)
	}
	c = advanceTest(t, store, cmd, trusted)
	if trusted.baselineCalls != 3 || c.ChangeSet.Baseline[0].SHA256 == "mutated" {
		t.Fatalf("baseline port/copy isolation: %d", trusted.baselineCalls)
	}
	cmd = testCommand(c, RollbackRequired)
	cmd.Outcome = &Outcome{ID: "regressed", ReleaseID: c.Release.ID, Runs: []ObjectRef{{Kind: "agent_run", ID: "post-release"}}, Report: testArtifact("outcome"), Gain: -1, Cost: 1, DurationMS: 1, RegressionCount: 1}
	c = advanceTest(t, store, cmd, plain)
	cmd = testCommand(c, RolledBack)
	rb := &Rollback{ReleaseID: c.Release.ID, Reason: "restore actual baseline"}
	for _, change := range c.ChangeSet.Changes {
		rb.Targets = append(rb.Targets, TargetReceipt{Version: change.Baseline, ActionRunID: "test-rollback", Receipt: testArtifact("rollback")})
	}
	cmd.Rollback = rb
	c = advanceTest(t, store, cmd, plain)
	if c.State != RolledBack {
		t.Fatal("new baseline admission blocked compensation")
	}
}

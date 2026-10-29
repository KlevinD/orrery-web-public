package improvement

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

func testObservation(id string) Observation {
	return Observation{ID: id, TenantID: "tenant-a", Task: ObjectRef{Kind: "task", ID: "report-task"}, Run: ObjectRef{Kind: "agent_run", ID: "run-" + id},
		Trigger: "quality_feedback", Summary: "报告交付需要改进", EvidenceIDs: []string{"evidence-" + id}, OccurredAt: time.Unix(100, 0).UTC(),
		SourceAccessRef: graphprotocol.AccessRef{Kind: "task", ID: "report-task", TenantID: "tenant-a"}}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	core, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	return NewStore(core)
}

func testArtifact(id string) ArtifactRef { return ArtifactRef{ID: id, SHA256: Digest(id)} }

func testChangeSet() ChangeSet {
	changes := []TargetChange{}
	baseline := []ResourceVersion{}
	for _, kind := range []TargetKind{TargetTool, TargetAgentLoop, TargetKnowledge} {
		v0 := ResourceVersion{Kind: kind, ID: "report-" + string(kind), Version: "v0", SHA256: Digest(string(kind) + ":v0")}
		v1 := v0
		v1.Version = "v1"
		v1.SHA256 = Digest(string(kind) + ":v1")
		baseline = append(baseline, v0)
		changes = append(changes, TargetChange{Baseline: v0, Candidate: v1, Patch: testArtifact(string(kind) + ":patch"), Rollback: testArtifact(string(kind) + ":rollback")})
	}
	changes[1].DependsOn = []string{ResourceKey(changes[0].Baseline)}
	return ChangeSet{ID: "report-changes", Goal: Goal{Metric: "delivery_quality", Direction: "higher", MinimumGain: .05, MaxCost: 10, MaxDurationMS: 10000},
		Baseline: baseline, Changes: changes, Dataset: testArtifact("held-out-report-tasks"), Policy: testArtifact("report-business-policy"), Generator: testArtifact("generator-v0"), Evaluator: testArtifact("independent-evaluator-v0"), Budget: 20}
}

type testVerifier struct {
	called int
	err    error
}

func (v *testVerifier) Verify(_ context.Context, _ Case, _ Transition) error {
	v.called++
	return v.err
}

func testCommand(c Case, to State) Transition {
	return Transition{ID: fmt.Sprintf("transition-%s-%d", to, c.Revision), TenantID: c.TenantID, CaseID: c.ID, ExpectedRevision: c.Revision, LeaseToken: c.Lease.Token, Actor: ObjectRef{Kind: "learner", ID: c.Lease.Owner}, To: to}
}

func advanceTest(t *testing.T, store *Store, command Transition, verifier Verifier) Case {
	t.Helper()
	c, err := store.Advance(context.Background(), command, verifier)
	if err != nil {
		t.Fatalf("advance %s: %v", command.To, err)
	}
	return c
}

func proposedTestCase(t *testing.T, store *Store, o Observation) Case {
	t.Helper()
	ctx := context.Background()
	c, err := store.Observe(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	c, err = store.Claim(ctx, c.TenantID, c.ID, "learner-a", c.Revision, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	command := testCommand(c, Diagnosed)
	command.Diagnosis = &Diagnosis{Summary: "工具映射、知识和检查步骤共同导致返工", EvidenceIDs: o.EvidenceIDs, AffectedKinds: []TargetKind{TargetTool, TargetAgentLoop, TargetKnowledge}}
	c = advanceTest(t, store, command, nil)
	command = testCommand(c, Proposed)
	cs := testChangeSet()
	command.ChangeSet = &cs
	return advanceTest(t, store, command, nil)
}

func testedTestCase(t *testing.T, store *Store, c Case, verifier Verifier) Case {
	t.Helper()
	command := testCommand(c, Tested)
	command.Experiment = &Experiment{ID: "experiment-1", ChangeSetHash: Digest(c.ChangeSet), BaselineRun: ObjectRef{Kind: "agent_run", ID: "baseline-run"}, CandidateRun: ObjectRef{Kind: "agent_run", ID: "candidate-run"}, Manifest: testArtifact("execution-manifest"), Result: testArtifact("execution-result")}
	return advanceTest(t, store, command, verifier)
}

func evaluatedTestCase(t *testing.T, store *Store, c Case, verifier Verifier) Case {
	t.Helper()
	command := testCommand(c, Evaluated)
	command.Evaluation = &Evaluation{ID: "evaluation-1", ExperimentID: c.Experiment.ID, ChangeSetHash: Digest(c.ChangeSet), Report: testArtifact("evaluation-report"), BaselineScore: .6, CandidateScore: .8, Cost: 3, DurationMS: 500, HeldOutPassed: true, BusinessConstraintsPassed: true, SafetyPassed: true}
	return advanceTest(t, store, command, verifier)
}

func monitoringTestCase(t *testing.T, store *Store, c Case, verifier Verifier) Case {
	t.Helper()
	command := testCommand(c, Monitoring)
	r := Release{ID: "release-1", EvaluationID: c.Evaluation.ID, ChangeSetHash: Digest(c.ChangeSet)}
	for _, change := range c.ChangeSet.Changes {
		r.Targets = append(r.Targets, TargetReceipt{Version: change.Candidate, ActionRunID: "apply-" + ResourceKey(change.Candidate), Receipt: testArtifact("apply-receipt-" + ResourceKey(change.Candidate))})
	}
	command.Release = &r
	return advanceTest(t, store, command, verifier)
}

func TestCrossDomainEvidenceLifecycleReopenAndNextRound(t *testing.T) {
	// This is a persistence/state-machine integration test with a test verifier;
	// it is not a claim of real model generation or production resource adoption.
	ctx := context.Background()
	cfg := db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "improvements.db")}
	core, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(core)
	verifier := &testVerifier{}
	c := proposedTestCase(t, store, testObservation("first"))
	token := c.Lease.Token
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	core, err = db.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	store = NewStore(core)
	c, err = store.Get(ctx, c.TenantID, c.ID)
	if err != nil || c.Lease.Token != token || len(c.ChangeSet.Changes) != 3 {
		t.Fatalf("reopen: %+v %v", c, err)
	}
	c = testedTestCase(t, store, c, verifier)
	c = evaluatedTestCase(t, store, c, verifier)
	c = monitoringTestCase(t, store, c, verifier)
	command := testCommand(c, Verified)
	command.Outcome = &Outcome{ID: "outcome-1", ReleaseID: c.Release.ID, Runs: []ObjectRef{{Kind: "agent_run", ID: "post-release-run"}}, Report: testArtifact("online-outcome"), Gain: .1, Cost: 3, DurationMS: 600, BusinessConstraintsPassed: true}
	c = advanceTest(t, store, command, verifier)
	if c.State != Verified || c.Lease != nil || verifier.called != 4 {
		t.Fatalf("completed: %+v verifier=%d", c, verifier.called)
	}
	replayed, err := store.Advance(ctx, command, nil)
	if err != nil || replayed.Revision != c.Revision {
		t.Fatalf("retry after terminal state: %+v %v", replayed, err)
	}
	command.Outcome.Gain = .2
	if _, err := store.Advance(ctx, command, verifier); !errors.Is(err, ErrConflict) {
		t.Fatalf("rewritten command accepted: %v", err)
	}
	next := testObservation("second")
	next.ParentCaseID = c.ID
	nextCase, err := store.Observe(ctx, next)
	if err != nil || nextCase.Observation.ParentCaseID != c.ID {
		t.Fatalf("next round: %+v %v", nextCase, err)
	}
	nextCase = proposedTestCase(t, store, next)
	if nextCase.ID == c.ID || c.Observation.Run == nextCase.Observation.Run {
		t.Fatal("rounds were merged")
	}
	page, err := store.List(ctx, "tenant-a", "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	page2, err := store.List(ctx, "tenant-a", page.NextCursor, 1)
	if err != nil || len(page2.Items) != 1 || page2.Items[0].ID == page.Items[0].ID || page2.NextCursor != "" {
		t.Fatalf("second page: %+v %v", page2, err)
	}
	if _, err := store.Get(ctx, "tenant-b", c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant read: %v", err)
	}
	if _, err := store.List(ctx, "tenant-b", tenantPrefix("tenant-a")+Digest(c.ID), 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign cursor: %v", err)
	}
}

func TestLeaseFencingRejectsStaleWorkersAndConcurrentClaims(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	now := time.Unix(200, 0).UTC()
	store.now = func() time.Time { return now }
	c, err := store.Observe(ctx, testObservation("lease"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, owner := range []string{"worker-a", "worker-b"} {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			_, err := store.Claim(ctx, c.TenantID, c.ID, owner, c.Revision, time.Second)
			results <- err
		}(owner)
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrLease) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("claim winners: %d", successes)
	}
	c, err = store.Get(ctx, c.TenantID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := testCommand(c, Rejected)
	stale.Reason = "skip"
	now = now.Add(2 * time.Second)
	if _, err := store.Advance(ctx, stale, nil); !errors.Is(err, ErrLease) {
		t.Fatalf("expired worker: %v", err)
	}
	fresh, err := store.Claim(ctx, c.TenantID, c.ID, "worker-c", c.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Lease.Token == stale.LeaseToken || fresh.Lease.Epoch <= c.Lease.Epoch {
		t.Fatal("fencing token did not rotate")
	}
	stale.ExpectedRevision = fresh.Revision
	if _, err := store.Advance(ctx, stale, nil); !errors.Is(err, ErrLease) {
		t.Fatalf("old token accepted: %v", err)
	}
}

func TestFrozenEvidenceAndVerifierFailuresCannotPublish(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	c := proposedTestCase(t, store, testObservation("guards"))
	command := testCommand(c, Monitoring)
	command.Release = &Release{ID: "skip-evaluation"}
	if _, err := store.Advance(ctx, command, &testVerifier{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("skipped stages: %v", err)
	}
	command = testCommand(c, Tested)
	command.Experiment = &Experiment{ID: "experiment", ChangeSetHash: Digest(c.ChangeSet), BaselineRun: ObjectRef{Kind: "run", ID: "b"}, CandidateRun: ObjectRef{Kind: "run", ID: "c"}, Manifest: testArtifact("manifest"), Result: testArtifact("result")}
	if _, err := store.Advance(ctx, command, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing verifier: %v", err)
	}
	denied := errors.New("artifact provenance rejected")
	if _, err := store.Advance(ctx, command, &testVerifier{err: denied}); !errors.Is(err, denied) {
		t.Fatalf("failed verifier: %v", err)
	}
	command.Experiment.ChangeSetHash = Digest("different-candidate")
	if _, err := store.Advance(ctx, command, &testVerifier{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("candidate mismatch: %v", err)
	}
	c = testedTestCase(t, store, c, &testVerifier{})
	command = testCommand(c, Evaluated)
	command.Evaluation = &Evaluation{ID: "bad", BaselineScore: math.NaN()}
	if _, err := store.Advance(ctx, command, &testVerifier{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NaN erased during copy: %v", err)
	}
	command.Evaluation = &Evaluation{ID: "no-gain", ExperimentID: c.Experiment.ID, ChangeSetHash: Digest(c.ChangeSet), Report: testArtifact("no-gain"), BaselineScore: .6, CandidateScore: .6, Cost: 1, DurationMS: 100, HeldOutPassed: true, BusinessConstraintsPassed: true, SafetyPassed: true}
	c = advanceTest(t, store, command, &testVerifier{})
	command = testCommand(c, Monitoring)
	command.Release = &Release{ID: "cannot-publish", EvaluationID: c.Evaluation.ID, ChangeSetHash: Digest(c.ChangeSet)}
	if _, err := store.Advance(ctx, command, &testVerifier{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("equal score published: %v", err)
	}
	command = testCommand(c, Rejected)
	command.Reason = "no measurable gain"
	c = advanceTest(t, store, command, nil)
	if c.Evaluation == nil || c.State != Rejected {
		t.Fatal("rejection lost evaluation evidence")
	}
}

func TestRegressionRequiresAllRollbackReceipts(t *testing.T) {
	store := testStore(t)
	v := &testVerifier{}
	ctx := context.Background()
	c := monitoringTestCase(t, store, evaluatedTestCase(t, store, testedTestCase(t, store, proposedTestCase(t, store, testObservation("regression")), v), v), v)
	command := testCommand(c, RollbackRequired)
	command.Outcome = &Outcome{ID: "regressed", ReleaseID: c.Release.ID, Runs: []ObjectRef{{Kind: "agent_run", ID: "post-release"}}, Report: testArtifact("regression-report"), Gain: -.1, Cost: 1, DurationMS: 100, RegressionCount: 1, BusinessConstraintsPassed: true}
	c = advanceTest(t, store, command, v)
	command = testCommand(c, RolledBack)
	command.Rollback = &Rollback{ReleaseID: c.Release.ID, Reason: "quality regression"}
	for _, change := range c.ChangeSet.Changes {
		command.Rollback.Targets = append(command.Rollback.Targets, TargetReceipt{Version: change.Baseline, ActionRunID: "rollback-" + ResourceKey(change.Baseline), Receipt: testArtifact("rollback-" + ResourceKey(change.Baseline))})
	}
	partial := privateCopy(command)
	partial.Rollback.Targets = partial.Rollback.Targets[:2]
	if _, err := store.Advance(ctx, partial, v); !errors.Is(err, ErrInvalid) {
		t.Fatalf("partial rollback: %v", err)
	}
	command.Rollback.Targets[0].Version = c.ChangeSet.Changes[0].Candidate
	if _, err := store.Advance(ctx, command, v); !errors.Is(err, ErrInvalid) {
		t.Fatalf("candidate recorded as rollback: %v", err)
	}
	command.Rollback.Targets[0].Version = c.ChangeSet.Changes[0].Baseline
	c = advanceTest(t, store, command, v)
	if c.State != RolledBack || c.Outcome == nil || c.Rollback == nil {
		t.Fatal("rollback lost release/outcome evidence")
	}
}

func TestChangeSetRejectsCyclesAndObservationMutation(t *testing.T) {
	cs := testChangeSet()
	cs.Changes[0].DependsOn = []string{ResourceKey(cs.Changes[1].Baseline)}
	if err := validateChangeSet(cs); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cyclic graph: %v", err)
	}
	cs = testChangeSet()
	cs.Evaluator = cs.Generator
	if err := validateChangeSet(cs); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self evaluation accepted: %v", err)
	}
	store := testStore(t)
	o := testObservation("immutability")
	c, err := store.Observe(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	o.EvidenceIDs[0] = "rewritten"
	got, err := store.Get(context.Background(), c.TenantID, c.ID)
	if err != nil || got.Observation.EvidenceIDs[0] == "rewritten" {
		t.Fatal("caller mutated durable observation")
	}
	if _, err := store.Observe(context.Background(), o); !errors.Is(err, ErrConflict) {
		t.Fatalf("observation id reused: %v", err)
	}
}

type testProjectionSink struct {
	fail  bool
	cases []Case
}

func (s *testProjectionSink) ProjectImprovement(_ context.Context, c Case) error {
	if s.fail {
		return errors.New("projection offline")
	}
	s.cases = append(s.cases, c)
	return nil
}

func TestProjectionOutboxRetriesWithoutLosingCommittedObservation(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	c, err := store.Observe(ctx, testObservation("outbox"))
	if err != nil {
		t.Fatal(err)
	}
	sink := &testProjectionSink{fail: true}
	if n, err := store.ProjectPending(ctx, sink, 10); err == nil || n != 0 {
		t.Fatal("failed projection was acknowledged")
	}
	if _, err := store.Get(ctx, c.TenantID, c.ID); err != nil {
		t.Fatal("observation lost")
	}
	sink.fail = false
	if n, err := store.ProjectPending(ctx, sink, 10); err != nil || n != 1 {
		t.Fatalf("retry: %d %v", n, err)
	}
	if n, err := store.ProjectPending(ctx, sink, 10); err != nil || n != 0 {
		t.Fatalf("already acknowledged: %d %v", n, err)
	}
}

func TestAdapterCoverageDoesNotClaimUnimplementedDomains(t *testing.T) {
	r, err := NewAdapterRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Capabilities()) != 9 {
		t.Fatal("system domains omitted")
	}
	for _, capability := range r.Capabilities() {
		if capability.Available {
			t.Fatalf("unimplemented adapter marked available: %s", capability.Kind)
		}
		if _, err := r.Resolve(capability.Kind); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
}

func TestObservationAndOutboxCommitAtomically(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	observation := testObservation("atomic")
	_, err := store.core.Exec(ctx, `CREATE TRIGGER fail_improvement_outbox BEFORE INSERT ON platform_records
WHEN NEW.table_name = 'ontology_improvement_projection_outbox'
BEGIN SELECT RAISE(ABORT, 'simulated outbox failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observe(ctx, observation); err == nil {
		t.Fatal("outbox failure ignored")
	}
	if _, err := store.Get(ctx, observation.TenantID, caseID(observation)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("aggregate committed without outbox: %v", err)
	}
	page, err := store.core.ListRecords(ctx, historyTable, "", 100)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("orphan revision committed: %+v %v", page, err)
	}
	if _, err := store.core.Exec(ctx, "DROP TRIGGER fail_improvement_outbox"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observe(ctx, observation); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
}

package ontologyreasoning

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
)

func TestInferenceSupportTracksProofAndNegativeConstraints(t *testing.T) {
	ctx := context.Background()
	pack, rules := inferenceFixture()
	pack.CandidateObjects = append(pack.CandidateObjects, contextgraph.Object{ID: "unrelated", TenantID: pack.TenantID, ObjectType: "project", Label: "before"})
	service := NewService(ServiceOptions{Store: NewMemoryStore()})
	first, err := service.Evaluate(ctx, pack, evaluationRecord(rules), nil)
	if err != nil {
		t.Fatal(err)
	}
	id := first.Candidates[0].ID
	hash := first.Request.Evaluation.SupportHashes[id]
	pack.CandidateObjects[2].Label = "after"
	next, err := service.Evaluate(ctx, pack, evaluationRecord(rules), nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Request.Evaluation.SupportHashes[id] != hash {
		t.Fatal("unrelated object invalidated support")
	}
	pack.Facts = append(pack.Facts, contextgraph.Fact{ID: "new-conflict", TenantID: pack.TenantID, Subject: contextgraph.Ref{ID: "mission-a"}, Predicate: "risk", Value: false})
	next, err = service.Evaluate(ctx, pack, evaluationRecord(rules), nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Request.Evaluation.SupportHashes[id] == hash {
		t.Fatal("new constraint assertion did not invalidate support")
	}
	pack.Facts = pack.Facts[:len(pack.Facts)-1]
	pack.AllowedPropertyTypes[1].AuthoritySource = "new-authority"
	next, err = service.Evaluate(ctx, pack, evaluationRecord(rules), nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Request.Evaluation.SupportHashes[id] == hash {
		t.Fatal("authority constraint did not invalidate support")
	}
}

func testDerivedLinkRevisionRetraction(t *testing.T, postgres bool) {
	ctx := context.Background()
	core := workflowDatabase(t, postgres)
	s, run, req, trusted := workflowFixture(t, core)
	req, _ = approveAndPlan(t, s, run, req, trusted)
	state, err := s.Workflow(ctx, "apply", req, run, trusted)
	if err != nil {
		t.Fatal(err)
	}
	firstReceipt := *state.Receipt
	firstReq := req
	refreshGuard := func() {
		stamp, err := s.InputStamp(ctx, run.TenantID)
		if err != nil {
			t.Fatal(err)
		}
		trusted.Guard = func(ctx context.Context, tx db.Tx) error { return s.FenceInput(ctx, tx, run.TenantID, stamp) }
	}
	refreshGuard()
	// A rule revision retains candidate identity but changes its exact proof.
	rules := append([]Rule(nil), run.Request.Evaluation.Rules...)
	rules[0].Version = "2"
	newRun, err := NewService(ServiceOptions{Store: NewMemoryStore()}).Evaluate(ctx, run.ContextPack, evaluationRecord(rules), nil)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(ctx, SessionFor(run))
	states, err := s.Track(ctx, newRun, current.Revision, trusted.Guard)
	if err != nil {
		t.Fatal(err)
	}
	run = newRun
	trusted.Fresh = run
	state = states[0]
	if state.Status != "revision_required" || state.Receipt.ID != firstReceipt.ID {
		t.Fatalf("revision lost assertion: %+v", state)
	}
	req = WorkflowRequest{TenantID: run.TenantID, RunID: run.ID, CandidateID: state.CandidateID, ExpectedRevision: state.Revision, SupportHash: state.SupportHash, Decision: "approve", Reason: "new rule is supported"}
	state, err = s.Workflow(ctx, "review", req, run, trusted)
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedRevision = state.Revision
	state, err = s.Workflow(ctx, "plan", req, run, trusted)
	if err != nil {
		t.Fatal(err)
	}
	if state.Plan.Effect != "revise" {
		t.Fatalf("effect: %+v", state.Plan)
	}
	req.ExpectedRevision = state.Revision
	req.PlanID = state.Plan.ID
	req.Confirmed = true
	req.IdempotencyKey = "revision-once"
	state, err = s.Workflow(ctx, "apply", req, run, trusted)
	if err != nil {
		t.Fatal(err)
	}
	if state.Receipt.LinkID != firstReceipt.LinkID || state.Receipt.Effect != "revise" {
		t.Fatalf("revision: %+v", state)
	}
	refreshGuard()
	// All independent proofs disappear; tracking alone keeps the assertion.
	newRun = run
	newRun.ID = "withdrawn-run"
	newRun.Request.Evaluation = &EvaluationRecord{ActorHash: run.Request.Evaluation.ActorHash, QueryHash: run.Request.Evaluation.QueryHash, SupportHashes: map[string]string{}}
	current, _ = s.Get(ctx, SessionFor(run))
	states, err = s.Track(ctx, newRun, current.Revision, trusted.Guard)
	if err != nil {
		t.Fatal(err)
	}
	run = newRun
	trusted.Fresh = run
	state = states[0]
	req = WorkflowRequest{TenantID: run.TenantID, RunID: run.ID, CandidateID: state.CandidateID, ExpectedRevision: state.Revision, SupportHash: state.SupportHash, Decision: "retract", Reason: "last independent support was removed"}
	state, err = s.Workflow(ctx, "review", req, run, trusted)
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedRevision = state.Revision
	state, err = s.Workflow(ctx, "plan", req, run, trusted)
	if err != nil {
		t.Fatal(err)
	}
	if state.Plan.Effect != "retract" {
		t.Fatal("retraction not planned")
	}
	req.ExpectedRevision = state.Revision
	req.PlanID = state.Plan.ID
	req.IdempotencyKey = "retract-once"
	req.Confirmed = true
	state, err = s.Workflow(ctx, "apply", req, run, trusted)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "retracted" || state.Receipt.Effect != "retract" {
		t.Fatalf("retraction: %+v", state)
	}
	var count int
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_links WHERE tenant_id=?`, run.TenantID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("source relationship changed: %d %v", count, err)
	}
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_inference_receipts`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("receipt history lost: %d %v", count, err)
	}
	refreshGuard()
	// The first request still replays its original receipt after two later effects.
	replayed, err := s.Workflow(ctx, "apply", firstReq, runWithID(run, firstReq.RunID), trusted)
	if err != nil || replayed.Receipt.ID != firstReceipt.ID {
		t.Fatalf("historical retry: %+v %v", replayed, err)
	}
}

func runWithID(run ReasoningRun, id string) ReasoningRun { run.ID = id; return run }
func TestInferenceDerivedLinkRevisionAndRetraction(t *testing.T) {
	testDerivedLinkRevisionRetraction(t, false)
}
func TestPostgresInferenceDerivedLinkRevisionAndRetraction(t *testing.T) {
	// Each workflow fixture expects a fresh session. Keep this additional
	// PostgreSQL lifecycle scenario separate from the assertion-only scenario.
	admin := workflowDatabase(t, true)
	u, err := url.Parse(os.Getenv("ORRERY_TEST_POSTGRES_URL"))
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	schema := "orrery_inference_test_derived_" + randomRunSuffix()
	if _, err := admin.Exec(context.Background(), `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`); err != nil {
			t.Error(err)
		}
	})
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	t.Setenv("ORRERY_TEST_POSTGRES_URL", u.String())
	testDerivedLinkRevisionRetraction(t, true)
}

func TestInferenceOwnedRetractionRollsBackAndRejectsForeignProjection(t *testing.T) {
	for _, scenario := range []string{"disabled_action", "foreign_projection"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			core := workflowDatabase(t, false)
			s, run, req, trusted := workflowFixture(t, core)
			req, _ = approveAndPlan(t, s, run, req, trusted)
			state, err := s.Workflow(ctx, "apply", req, run, trusted)
			if err != nil {
				t.Fatal(err)
			}
			refresh := func() {
				stamp, err := s.InputStamp(ctx, run.TenantID)
				if err != nil {
					t.Fatal(err)
				}
				trusted.Guard = func(ctx context.Context, tx db.Tx) error { return s.FenceInput(ctx, tx, run.TenantID, stamp) }
			}
			refresh()
			req.ExpectedRevision = state.Revision
			req.Decision = "retract"
			state, err = s.Workflow(ctx, "review", req, run, trusted)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "foreign_projection" {
				if _, err := core.Exec(ctx, `UPDATE context_graph_links SET source_stream_id='foreign-owner' WHERE tenant_id=? AND id=?`, run.TenantID, state.Receipt.LinkID); err != nil {
					t.Fatal(err)
				}
				refresh()
			}
			req.ExpectedRevision = state.Revision
			state, err = s.Workflow(ctx, "plan", req, run, trusted)
			if scenario == "foreign_projection" {
				if !errors.Is(err, ErrStateConflict) {
					t.Fatalf("foreign projection accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := core.Exec(ctx, `UPDATE action_types SET enabled=FALSE WHERE id=?`, DerivedLinkActionID); err != nil {
				t.Fatal(err)
			}
			req.ExpectedRevision = state.Revision
			req.PlanID = state.Plan.ID
			req.IdempotencyKey = "retraction-fails"
			req.Confirmed = true
			if _, err = s.Workflow(ctx, "apply", req, run, trusted); err == nil {
				t.Fatal("disabled command executed")
			}
			var count int
			for table, want := range map[string]int{"context_graph_links": 2, "context_graph_evidence": 1, "ontology_inference_receipts": 1, "action_runs": 1} {
				if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != want {
					t.Fatalf("partial retraction %s: %d %v", table, count, err)
				}
			}
		})
	}
}

func TestInferenceRulePublicationAndObservationAreDurable(t *testing.T) {
	ctx := context.Background()
	core := workflowDatabase(t, false)
	store := &RuleSetStore{Store: core}
	_, rules := inferenceFixture()
	now := time.Now().UTC()
	set := RuleSet{TenantID: "tenant-a", ID: "watch", AgentID: "model", RootID: "root", Rules: rules, Active: true, PublishedAt: now}
	guard := func(context.Context, db.Tx) error { return nil }
	saved, err := store.Publish(ctx, set, 0, guard)
	if err != nil || saved.Version != 1 {
		t.Fatalf("publish: %+v %v", saved, err)
	}
	saved, err = store.Publish(ctx, set, 0, guard)
	if err != nil || saved.Version != 1 {
		t.Fatalf("retry: %+v %v", saved, err)
	}
	var count int
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM platform_outbox_jobs`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("publication duplicate: %d %v", count, err)
	}
	stamp := func(context.Context, RuleSet) (string, error) { return "source-v1", nil }
	for i := 1; i <= 2; i++ {
		if err := store.Observe(ctx, now.Add(time.Duration(i)*time.Minute), 10, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM platform_outbox_jobs`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("unchanged observation duplicated: %d %v", count, err)
	}
	set.Active = false
	if _, err = store.Publish(ctx, set, 0, guard); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("stale pause: %v", err)
	}
	if _, err = store.Publish(ctx, set, 1, guard); err != nil {
		t.Fatal(err)
	}
	err = core.WithTx(ctx, func(tx db.Tx) error { return store.CheckActiveTx(ctx, tx, set.TenantID, set.ID, set.AgentID, 1) })
	if !errors.Is(err, ErrStateConflict) {
		t.Fatalf("paused task can commit: %v", err)
	}
	box := outbox.New(core)
	job, ok, err := box.Claim(ctx, "old-worker", now.Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("claim: %+v %v", job, err)
	}
	if n, err := box.RecoverStale(ctx, now.Add(2*time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("recovery: %d %v", n, err)
	}
	if _, ok, err := box.Claim(ctx, "new-worker", now.Add(2*time.Hour)); err != nil || !ok {
		t.Fatalf("restart did not recover durable task: %v", err)
	}
}

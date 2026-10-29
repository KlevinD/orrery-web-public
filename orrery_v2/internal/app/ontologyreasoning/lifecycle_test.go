package ontologyreasoning

import (
	"context"
	"errors"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestInferenceInvalidDependencyDoesNotProduceProposals(t *testing.T) {
	pack, rules := inferenceFixture()
	rules[0].Then = Atom{Kind: "fact", Subject: "?p", Predicate: "unregistered", Value: true}
	rules = append(rules, Rule{ID: "downstream", Version: "1", When: []Atom{{Kind: "fact", Subject: "?p", Predicate: "unregistered", Value: true}, {Kind: "link", Type: "supports", Subject: "?p", Object: "?m"}}, Then: Atom{Kind: "fact", Subject: "?m", Predicate: "risk", Value: true}})
	s := NewService(ServiceOptions{Store: NewMemoryStore()})
	run, err := s.Evaluate(context.Background(), pack, evaluationRecord(rules), nil)
	if err != nil || run.RejectedCandidateCount != 2 || len(run.Proposals) != 0 {
		t.Fatalf("invalid dependency promoted: %+v %v", run, err)
	}
	rules = append(rules, Rule{ID: "independent", Version: "1", When: []Atom{{Kind: "link", Type: "supports", Subject: "?p", Object: "?m"}}, Then: Atom{Kind: "fact", Subject: "?m", Predicate: "risk", Value: true}})
	run, err = s.Evaluate(context.Background(), pack, evaluationRecord(rules), nil)
	if err != nil || run.RejectedCandidateCount != 1 || len(run.Proposals) != 1 {
		t.Fatalf("valid alternative was removed: %+v %v", run, err)
	}
	for _, c := range run.Request.Evaluation.Closure.Conclusions {
		if c.Atom.Predicate == "risk" && (len(c.Derivations) != 1 || c.Derivations[0].RuleID != "independent") {
			t.Fatalf("invalid proof retained: %+v", c)
		}
	}
}

func TestInferenceDocumentWithdrawalAndRuleRevisionAreExplicit(t *testing.T) {
	ctx := context.Background()
	pack, rules := inferenceFixture()
	s := NewService(ServiceOptions{Store: NewMemoryStore()})
	doc := DocumentInput{Text: "Reported risk.", ExtractorVersion: "v1", Claims: []DocumentClaim{{Quote: "Reported risk", Candidate: Candidate{Kind: CandidateFact, Subject: &ObjectRef{Type: "mission", ID: "mission-a"}, Predicate: "risk", Value: true}}}}
	first, err := s.Evaluate(ctx, pack, evaluationRecord(nil), []DocumentInput{doc})
	if err != nil {
		t.Fatal(err)
	}
	record := evaluationRecord(nil)
	record.PreviousRunID = first.ID
	next, err := s.Evaluate(ctx, pack, record, nil)
	if err != nil || !slices.Contains(next.Request.Evaluation.WithdrawnIDs, first.Candidates[0].ID) {
		t.Fatalf("document candidate not withdrawn: %+v %v", next, err)
	}
	first, err = s.Evaluate(ctx, pack, evaluationRecord(rules), nil)
	if err != nil {
		t.Fatal(err)
	}
	rules[0].Version = "2"
	record = evaluationRecord(rules)
	record.PreviousRunID = first.ID
	next, err = s.Evaluate(ctx, pack, record, nil)
	if err != nil || len(next.Request.Evaluation.RevisedIDs) != 1 || next.Candidates[0].ID != first.Candidates[0].ID {
		t.Fatalf("revision hidden: %+v %v", next, err)
	}
	old, _ := s.GetRun(ctx, pack.TenantID, first.ID)
	if old.Request.Evaluation.Rules[0].Version != "1" {
		t.Fatal("historical rule changed")
	}
}

func TestInferenceReportsFactAuthorityConflict(t *testing.T) {
	pack, rules := inferenceFixture()
	pack.AllowedPropertyTypes[1].AuthoritySource = "mission"
	pack.AllowedPropertyTypes[1].ConflictPolicy = "manual_review"
	pack.Facts = append(pack.Facts, contextgraph.Fact{TenantID: pack.TenantID, ID: "manual", Subject: contextgraph.Ref{ID: "mission-a"}, Predicate: "risk", Value: false})
	run, err := NewService(ServiceOptions{Store: NewMemoryStore()}).Evaluate(context.Background(), pack, evaluationRecord(rules), nil)
	if err != nil {
		t.Fatal(err)
	}
	codes := []string{}
	for _, issue := range run.Validation[0].Issues {
		codes = append(codes, issue.Code)
	}
	for _, code := range []string{"fact_value_conflict", "fact_authority_review", "fact_conflict_policy"} {
		if !slices.Contains(codes, code) {
			t.Fatalf("missing %s: %+v", code, codes)
		}
	}
}

func workflowDatabase(t *testing.T, postgres bool) *db.Store {
	t.Helper()
	cfg := db.Config{Driver: db.DriverMemory, Name: "workflow-" + t.Name()}
	if postgres {
		raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
		if raw == "" {
			t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
		}
		u, err := url.Parse(raw)
		if err != nil || !strings.HasPrefix(u.Query().Get("search_path"), "orrery_inference_test_") {
			t.Fatal("an isolated inference test schema is required")
		}
		cfg = db.Config{Driver: db.DriverPostgres, URL: raw}
	}
	registry, err := db.OpenRegistry(context.Background(), db.RegistryConfig{Databases: map[string]db.Config{db.RoleCore: cfg}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { registry.Close() })
	coordinator := migration.NewCoordinator(registry)
	for _, register := range []func(*migration.Coordinator) error{ontology.RegisterMigrations, contextgraph.RegisterMigrations, actioncontract.RegisterMigrations, RegisterMigrations} {
		if err = register(coordinator); err != nil {
			t.Fatal(err)
		}
	}
	if err = coordinator.UpRole(context.Background(), db.RoleCore); err != nil {
		t.Fatal(err)
	}
	core, err := registry.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	return core
}

func workflowFixture(t *testing.T, core *db.Store) (*LifecycleStore, ReasoningRun, WorkflowRequest, WorkflowContext) {
	t.Helper()
	ctx := context.Background()
	pack, _ := inferenceFixture()
	// Separate source and inferred relation types make source ownership explicit.
	pack.AllowedLinkTypes = append(pack.AllowedLinkTypes, ontology.LinkType{ID: "inferred_supports", FromObjectType: "project", ToObjectType: "mission", Cardinality: ontology.CardinalityManyToMany, SuggestedAllowed: true, AllowedActions: []string{DerivedLinkActionID}})
	rules := []Rule{{ID: "derive-link", Version: "1", When: []Atom{{Kind: "link", Type: "supports", Subject: "?p", Object: "?m"}}, Then: Atom{Kind: "link", Type: "inferred_supports", Subject: "?p", Object: "?m"}}}
	for _, a := range actioncontract.DefaultActionTypes() {
		if err := actioncontract.NewService(actioncontract.NewPlatformRepository(core)).RegisterActionType(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	graph := contextgraph.NewPlatformStore(core)
	access := contextgraph.AccessRef{Kind: "organization", ID: "org-1", TenantID: "org-1"}
	for _, o := range pack.CandidateObjects {
		o.OwnerAccessRef = access
		if err := graph.SaveObject(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range pack.NeighborLinks {
		l.SourceAccessRef = access
		if err := graph.SaveLink(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	s := &LifecycleStore{Store: core}
	stamp, err := s.InputStamp(ctx, pack.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	record := evaluationRecord(rules)
	record.InputStamp = stamp
	run, err := NewService(ServiceOptions{Store: NewMemoryStore()}).Evaluate(ctx, pack, record, nil)
	if err != nil {
		t.Fatal(err)
	}
	guard := func(ctx context.Context, tx db.Tx) error { return s.FenceInput(ctx, tx, pack.TenantID, stamp) }
	states, err := s.Track(ctx, run, 0, guard)
	if err != nil {
		t.Fatal(err)
	}
	return s, run, WorkflowRequest{TenantID: run.TenantID, RunID: run.ID, CandidateID: states[0].CandidateID, ExpectedRevision: states[0].Revision, SupportHash: states[0].SupportHash}, WorkflowContext{Actor: "human-1", AuthorizationHash: "auth-1", Fresh: run, SourceAccessRef: access, Guard: guard, Now: time.Now().UTC()}
}

func approveAndPlan(t *testing.T, s *LifecycleStore, run ReasoningRun, req WorkflowRequest, trusted WorkflowContext) (WorkflowRequest, CandidateState) {
	t.Helper()
	req.Decision = "approve"
	req.Reason = "Review note containing earlier source details"
	state, err := s.Workflow(context.Background(), "review", req, run, trusted)
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedRevision = state.Revision
	state, err = s.Workflow(context.Background(), "plan", req, run, trusted)
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedRevision = state.Revision
	req.PlanID = state.Plan.ID
	req.IdempotencyKey = "apply-once"
	req.Confirmed = true
	return req, state
}

func checkWorkflowAtomicity(t *testing.T, postgres bool) {
	core := workflowDatabase(t, postgres)
	s, run, req, trusted := workflowFixture(t, core)
	if postgres {
		trusted.ActorKind = actioncontract.SubjectAgent
		trusted.ActorID = "model-agent-1"
	}
	req, _ = approveAndPlan(t, s, run, req, trusted)
	state, err := s.Workflow(context.Background(), "apply", req, run, trusted)
	if err != nil || state.Status != "applied" || state.Receipt == nil {
		t.Fatalf("apply: %+v %v", state, err)
	}
	ctx := context.Background()
	action, err := actioncontract.NewPlatformRepository(core).GetActionRun(ctx, actioncontract.ActionRunID(state.Receipt.ActionRunID))
	if err != nil || action.Status != actioncontract.RunStatusCompleted {
		t.Fatalf("action: %+v %v", action, err)
	}
	if postgres && (action.Actor.Kind != actioncontract.SubjectAgent || action.Actor.ID != "model-agent-1" || action.Runtime.Kind != actioncontract.RuntimeAgent || state.Receipt.ActorKind != "agent") {
		t.Fatalf("PostgreSQL model actor was not preserved: %+v %+v", action, state.Receipt)
	}
	receipts, err := actioncontract.NewPlatformRepository(core).ListWritebackReceipts(ctx, action.ID)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipt: %+v %v", receipts, err)
	}
	_, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, run.TenantID)
	if err != nil || len(links) != 2 || len(evidence) != 1 {
		t.Fatalf("business projection: %d links %d evidence %v", len(links), len(evidence), err)
	}
	stamp, err := s.InputStamp(ctx, run.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	trusted.Guard = func(ctx context.Context, tx db.Tx) error { return s.FenceInput(ctx, tx, run.TenantID, stamp) }
	s = &LifecycleStore{Store: core}
	replayed, err := s.Workflow(ctx, "apply", req, run, trusted)
	if err != nil || replayed.Receipt.ID != state.Receipt.ID {
		t.Fatalf("restart replay: %+v %v", replayed, err)
	}
	if replayed.DecisionReason != "" || replayed.DecisionBy != "" || replayed.Plan != nil {
		t.Fatal("receipt replay disclosed historical review material")
	}
	req.IdempotencyKey = "different"
	if _, err = s.Workflow(ctx, "apply", req, run, trusted); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("different key: %v", err)
	}
}

func TestInferenceWorkflowCommitsLinkAndReceiptAtomically(t *testing.T) {
	checkWorkflowAtomicity(t, false)
}
func TestPostgresInferenceWorkflowCommitsLinkAndReceiptAtomically(t *testing.T) {
	checkWorkflowAtomicity(t, true)
}

func TestInferenceWorkflowRollback(t *testing.T) {
	core := workflowDatabase(t, false)
	s, run, req, trusted := workflowFixture(t, core)
	req, _ = approveAndPlan(t, s, run, req, trusted)
	if _, err := core.Exec(context.Background(), `UPDATE action_types SET enabled=FALSE WHERE id=?`, DerivedLinkActionID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Workflow(context.Background(), "apply", req, run, trusted); err == nil {
		t.Fatal("disabled command executed")
	}
	var count int
	for _, table := range []string{"ontology_inference_links", "context_graph_evidence", "action_runs", "action_writeback_receipts"} {
		if err := core.QueryRow(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial commit %s: %d %v", table, count, err)
		}
	}
	current, err := s.Get(context.Background(), SessionFor(run))
	if err != nil || current.Candidates[req.CandidateID].Status != "approved" {
		t.Fatalf("candidate commit survived rollback: %+v %v", current, err)
	}
}

func TestInferenceWorkflowRejectsStaleExpiredAndUnauthorizedPlans(t *testing.T) {
	for _, kind := range []string{"expired", "actor", "authorization", "support", "revision", "confirmation", "source", "tenant", "opt_in"} {
		t.Run(kind, func(t *testing.T) {
			core := workflowDatabase(t, false)
			s, run, req, trusted := workflowFixture(t, core)
			req, _ = approveAndPlan(t, s, run, req, trusted)
			switch kind {
			case "expired":
				trusted.Now = trusted.Now.Add(11 * time.Minute)
			case "actor":
				trusted.Actor = "human-2"
			case "authorization":
				trusted.AuthorizationHash = "new-policy"
			case "support":
				trusted.Fresh.Request.Evaluation.SupportHashes = map[string]string{}
			case "revision":
				req.ExpectedRevision--
			case "confirmation":
				req.Confirmed = false
			case "source":
				if _, err := core.Exec(context.Background(), `UPDATE context_graph_objects SET label='changed' WHERE tenant_id=?`, run.TenantID); err != nil {
					t.Fatal(err)
				}
			case "tenant":
				req.TenantID = "foreign"
			case "opt_in":
				for i := range trusted.Fresh.ContextPack.AllowedLinkTypes {
					if trusted.Fresh.ContextPack.AllowedLinkTypes[i].ID == "inferred_supports" {
						trusted.Fresh.ContextPack.AllowedLinkTypes[i].AllowedActions = nil
					}
				}
			}
			if _, err := s.Workflow(context.Background(), "apply", req, run, trusted); err == nil {
				t.Fatalf("accepted %s", kind)
			}
			var count int
			if err := core.QueryRow(context.Background(), `SELECT COUNT(*) FROM ontology_inference_links`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("side effect: %d %v", count, err)
			}
			if kind == "support" {
				current, _ := s.Get(context.Background(), SessionFor(run))
				if current.Candidates[req.CandidateID].Status != "withdrawn" {
					t.Fatal("stale candidate was not invalidated")
				}
			}
		})
	}
}

func TestInferenceLifecycleInvalidatesChangedApprovalAndRejectsStaleWriters(t *testing.T) {
	core := workflowDatabase(t, false)
	s, run, req, trusted := workflowFixture(t, core)
	_, old := approveAndPlan(t, s, run, req, trusted)
	current, _ := s.Get(context.Background(), SessionFor(run))
	run.Request.Evaluation.SupportHashes[req.CandidateID] = "new-proof"
	run.ID = "next-run"
	states, err := s.Track(context.Background(), run, current.Revision, trusted.Guard)
	if err != nil || states[0].Status != "proposed" || states[0].Plan != nil || states[0].Revision <= old.Revision {
		t.Fatalf("old approval retained: %+v %v", states, err)
	}
	if _, err = s.Track(context.Background(), run, current.Revision, trusted.Guard); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("stale writer: %v", err)
	}
	other, err := s.Get(context.Background(), SessionKey{Tenant: "other", Actor: "actor-1", Query: "query-1"})
	if err != nil || len(other.Candidates) != 0 {
		t.Fatalf("tenant leak: %+v %v", other, err)
	}
}

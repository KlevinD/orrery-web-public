package ontologyreasoning

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func inferenceFixture() (ContextPack, []Rule) {
	pack := ContextPack{TenantID: "tenant-A", AllowedObjectTypes: []ontology.ObjectType{{ID: "project"}, {ID: "mission"}}, AllowedLinkTypes: []ontology.LinkType{{ID: "supports", FromObjectType: "project", ToObjectType: "mission", Cardinality: ontology.CardinalityManyToMany}}, AllowedPropertyTypes: []ontology.PropertyType{{ID: "delay", ObjectTypeID: "project", PropertyID: "status", ValueType: ontology.ValueTypeString}, {ID: "risk", ObjectTypeID: "mission", PropertyID: "risk", ValueType: ontology.ValueTypeBoolean}}, CandidateObjects: []contextgraph.Object{{TenantID: "tenant-A", ID: "Project-A", ObjectType: "project"}, {TenantID: "tenant-A", ID: "mission-a", ObjectType: "mission"}}, NeighborLinks: []contextgraph.Link{{TenantID: "tenant-A", ID: "support-1", LinkType: "supports", FromObjectID: "Project-A", ToObjectID: "mission-a"}}, Facts: []contextgraph.Fact{{TenantID: "tenant-A", ID: "delay-1", Subject: contextgraph.Ref{ID: "Project-A"}, Predicate: "status", Value: "delayed"}, {TenantID: "tenant-A", ID: "delay-2", Subject: contextgraph.Ref{ID: "Project-A"}, Predicate: "status", Value: "delayed"}}}
	rules := []Rule{{ID: "delay-propagation", Version: "1", When: []Atom{{Kind: "fact", Subject: "?p", Predicate: "status", Value: "delayed"}, {Kind: "link", Type: "supports", Subject: "?p", Object: "?m"}}, Then: Atom{Kind: "fact", Subject: "?m", Predicate: "risk", Value: true}}}
	return pack, rules
}

func evaluationRecord(rules []Rule) EvaluationRecord {
	now := time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)
	return EvaluationRecord{ActorHash: "actor-1", QueryHash: "query-1", BusinessAsOf: now, KnownAt: now, Rules: rules}
}

func TestInferenceMaintainsMultipleSupportsAndWithdrawsLast(t *testing.T) {
	ctx := context.Background()
	pack, rules := inferenceFixture()
	store := NewMemoryStore()
	s := NewService(ServiceOptions{Store: store})
	run1, err := s.Evaluate(ctx, pack, evaluationRecord(rules), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(run1.Candidates) != 1 || len(run1.Request.Evaluation.Closure.Conclusions[0].Derivations) != 2 || len(run1.Proposals) != 1 || run1.Validation[0].Status != ValidationReviewRequired {
		t.Fatalf("unexpected inference: %+v", run1)
	}
	pack.Facts = pack.Facts[1:]
	record := evaluationRecord(rules)
	record.PreviousRunID = run1.ID
	run2, err := s.Evaluate(ctx, pack, record, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(run2.Candidates) != 1 || len(run2.Request.Evaluation.RetainedIDs) != 1 || len(run2.Request.Evaluation.WithdrawnIDs) != 0 || len(run2.Request.Evaluation.Closure.Conclusions[0].Derivations) != 1 {
		t.Fatal("withdrawing one support removed a supported conclusion")
	}
	pack.Facts = nil
	record.PreviousRunID = run2.ID
	run3, err := s.Evaluate(ctx, pack, record, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(run3.Candidates) != 0 || len(run3.Request.Evaluation.WithdrawnIDs) != 1 {
		t.Fatalf("last support not withdrawn: %+v", run3)
	}
	old, _ := s.GetRun(ctx, pack.TenantID, run1.ID)
	if len(old.Request.Evaluation.Closure.Conclusions[0].Derivations) != 2 {
		t.Fatal("historical derivations were mutated")
	}
	record.ActorHash = "other-actor"
	if _, err := s.Evaluate(ctx, pack, record, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("previous run actor isolation: %v", err)
	}
}

func TestInferenceRecursiveClosureAndIdentity(t *testing.T) {
	rules := []Rule{{ID: "transitive", Version: "1", When: []Atom{{Kind: "link", Type: "reaches", Subject: "?x", Object: "?y"}, {Kind: "link", Type: "reaches", Subject: "?y", Object: "?z"}}, Then: Atom{Kind: "link", Type: "reaches", Subject: "?x", Object: "?z"}}}
	premises := []Premise{{ID: "a", Atom: Atom{Kind: "link", Type: "reaches", Subject: "Mission-A", Object: "mission-a"}}, {ID: "b", Atom: Atom{Kind: "link", Type: "reaches", Subject: "mission-a", Object: "C"}}, {ID: "c", Atom: Atom{Kind: "link", Type: "reaches", Subject: "C", Object: "D"}}}
	got, err := EvaluateRules(context.Background(), "tenant-A", rules, premises)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Conclusions) != 3 {
		t.Fatalf("recursive closure: %+v", got)
	}
	other, err := EvaluateRules(context.Background(), "tenant-B", rules, premises)
	if err != nil || got.Conclusions[0].ID == other.Conclusions[0].ID {
		t.Fatal("conclusion IDs are not tenant-scoped")
	}
	premises[0], premises[2] = premises[2], premises[0]
	ordered, err := EvaluateRules(context.Background(), "tenant-A", rules, premises)
	if err != nil || !reflect.DeepEqual(got, ordered) {
		t.Fatal("closure depends on input order")
	}
	rules[0].Then.Subject = "?unbound"
	if _, err := EvaluateRules(context.Background(), "tenant-A", rules, premises); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("unsafe rule accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rules[0].Then.Subject = "?x"
	if _, err := EvaluateRules(ctx, "tenant-A", rules, premises); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestInferenceRejectsForeignContextAndInvalidClaims(t *testing.T) {
	pack, rules := inferenceFixture()
	s := NewService(ServiceOptions{Store: NewMemoryStore()})
	pack.Facts[0].TenantID = "foreign"
	if _, err := s.Evaluate(context.Background(), pack, evaluationRecord(rules), nil); err == nil {
		t.Fatal("foreign fact accepted")
	}
	pack, rules = inferenceFixture()
	doc := DocumentInput{Text: "Mission has a reported risk.", ExtractorVersion: "extractor-v1", Claims: []DocumentClaim{{Quote: "reported risk", Candidate: Candidate{Kind: CandidateFact, Subject: &ObjectRef{Type: "mission", ID: "mission-a"}, Predicate: "risk", Value: "wrong-type"}}}}
	run, err := s.Evaluate(context.Background(), pack, evaluationRecord(rules), []DocumentInput{doc})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Candidates) != 2 || run.RejectedCandidateCount != 1 || len(run.Proposals) != 1 || len(run.Request.Evaluation.Documents) != 1 {
		t.Fatalf("claim quality gate: %+v", run)
	}
	doc.Claims[0].Quote = "invented quote"
	if _, err := s.Evaluate(context.Background(), pack, evaluationRecord(rules), []DocumentInput{doc}); !errors.Is(err, ErrValidationFailed) {
		t.Fatal("fabricated quote accepted")
	}
	pack.Facts = nil
	run, err = s.Evaluate(context.Background(), pack, evaluationRecord(rules), nil)
	if err != nil || len(run.Candidates) != 0 {
		t.Fatal("missing/masked facts still affect inference")
	}
}

func TestInferenceLedgerSurvivesStoreRecreation(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "inference-ledger"})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	checkInferenceLedgerPersistence(t, core)
}

func TestPostgresInferenceLedgerSurvivesStoreRecreation(t *testing.T) {
	raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(u.Query().Get("search_path"), "orrery_inference_test_") {
		t.Fatal("an isolated inference test schema is required")
	}
	core, err := db.Open(context.Background(), db.Config{Driver: db.DriverPostgres, URL: raw, Name: "inference-postgres-ledger"})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	checkInferenceLedgerPersistence(t, core)
}

func checkInferenceLedgerPersistence(t *testing.T, core *db.Store) {
	t.Helper()
	ctx := context.Background()
	if err := migration.NewRunner([]migration.Migration{ontologyReasoningRunsMigration()}).Up(ctx, core); err != nil {
		t.Fatal(err)
	}
	pack, rules := inferenceFixture()
	run, err := NewService(ServiceOptions{Store: NewPlatformStore(core)}).Evaluate(ctx, pack, evaluationRecord(rules), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(ServiceOptions{Store: NewPlatformStore(core)})
	got, err := s.GetRun(ctx, pack.TenantID, run.ID)
	if err != nil || got.Request.Evaluation == nil || len(got.Request.Evaluation.Closure.Conclusions[0].Derivations) != 2 {
		t.Fatalf("ledger persistence: %+v, %v", got, err)
	}
	if _, err := s.GetRun(ctx, "other-tenant", run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant isolation: %v", err)
	}
	pack.Facts = nil
	record := evaluationRecord(rules)
	record.PreviousRunID = run.ID
	got, err = s.Evaluate(ctx, pack, record, nil)
	if err != nil || len(got.Request.Evaluation.WithdrawnIDs) != 1 {
		t.Fatalf("persistent truth maintenance: %v", err)
	}
}

func TestInferenceRecursiveCycleCannotSupportItselfAfterWithdrawal(t *testing.T) {
	rules := []Rule{{ID: "a-to-b", Version: "1", When: []Atom{{Kind: "fact", Subject: "?x", Predicate: "a", Value: true}}, Then: Atom{Kind: "fact", Subject: "?x", Predicate: "b", Value: true}}, {ID: "b-to-a", Version: "1", When: []Atom{{Kind: "fact", Subject: "?x", Predicate: "b", Value: true}}, Then: Atom{Kind: "fact", Subject: "?x", Predicate: "a", Value: true}}}
	got, err := EvaluateRules(context.Background(), "tenant", rules, []Premise{{ID: "base", Atom: Atom{Kind: "fact", Subject: "obj", Predicate: "a", Value: true}}})
	if err != nil || len(got.Conclusions) != 2 {
		t.Fatalf("cycle closure: %v %+v", err, got)
	}
	got, err = EvaluateRules(context.Background(), "tenant", rules, nil)
	if err != nil || len(got.Conclusions) != 0 {
		t.Fatal("cycle retained a conclusion without an external premise")
	}
}

func TestInferenceRetainsSupportWhenConclusionHasAnExternalAssertion(t *testing.T) {
	pack, rules := inferenceFixture()
	s := NewService(ServiceOptions{Store: NewMemoryStore()})
	first, err := s.Evaluate(context.Background(), pack, evaluationRecord(rules), nil)
	if err != nil {
		t.Fatal(err)
	}
	pack.Facts = append(pack.Facts, contextgraph.Fact{TenantID: pack.TenantID, ID: "external-risk", Subject: contextgraph.Ref{ID: "mission-a"}, Predicate: "risk", Value: true})
	record := evaluationRecord(rules)
	record.PreviousRunID = first.ID
	next, err := s.Evaluate(context.Background(), pack, record, nil)
	if err != nil || len(next.Request.Evaluation.WithdrawnIDs) != 0 || len(next.Request.Evaluation.RetainedIDs) != 1 {
		t.Fatalf("external assertion lost rule support: %v", err)
	}
}

func TestInferenceBudgetNeverReturnsPartialClosure(t *testing.T) {
	rules := []Rule{{ID: "dense", Version: "1", When: []Atom{{Kind: "object", Type: "node", Subject: "?x"}, {Kind: "object", Type: "node", Subject: "?y"}}, Then: Atom{Kind: "link", Type: "dense", Subject: "?x", Object: "?y"}}}
	var premises []Premise
	for i := 0; i < 100; i++ {
		id := stableID("p:", i)
		premises = append(premises, Premise{ID: id, Atom: Atom{Kind: "object", Type: "node", Subject: id}})
	}
	got, err := EvaluateRules(context.Background(), "tenant", rules, premises)
	if !errors.Is(err, ErrValidationFailed) || len(got.Conclusions) != 0 {
		t.Fatalf("partial or unbounded closure: %v", err)
	}
}

func TestInferenceQualityAndSchemaExport(t *testing.T) {
	pack, _ := inferenceFixture()
	pack.AllowedPropertyTypes = append(pack.AllowedPropertyTypes, ontology.PropertyType{ID: "project.budget", ObjectTypeID: "project", PropertyID: "budget", ValueType: ontology.ValueTypeNumber})
	pack.AllowedObjectTypes[1].Properties = []ontology.PropertyDefinition{{PropertyID: "risk", ValueType: ontology.ValueTypeBoolean, Required: true}}
	results := QualityCheck(pack, []Candidate{{ID: "bad-entity", Kind: CandidateEntity, Operation: OperationSuggest, ObjectType: "mission"}})
	if results[0].Status != ValidationRejected {
		t.Fatal("required property was not enforced")
	}
	exported, err := ExportSchema(pack)
	if err != nil || exported.OWL == "" || exported.SHACL == "" {
		t.Fatalf("schema export: %v", err)
	}
	if path := os.Getenv("ORRERY_INFERENCE_SCHEMA_ARTIFACT"); path != "" {
		data, err := json.MarshalIndent(exported, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	pack.AllowedLinkTypes[0].ToObjectType = "missing"
	if _, err := ExportSchema(pack); !errors.Is(err, ErrValidationFailed) {
		t.Fatal("dangling schema reference exported")
	}
}

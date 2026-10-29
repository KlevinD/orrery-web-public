package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/platform/db"
)

type conflictDocumentSources struct {
	sources     map[string]knowledgeinstances.Source
	denied      map[string]bool
	beforeFence func(knowledgeinstances.Source)
}

func (s *conflictDocumentSources) Load(_ context.Context, _ identity.Principal, watch knowledgeinstances.Watch) (knowledgeinstances.Source, error) {
	source, ok := s.sources[watch.RootID]
	if !ok || s.denied[watch.RootID] {
		return source, ontologyscope.ErrAccessDenied
	}
	return source, nil
}

func (s *conflictDocumentSources) Fence(_ context.Context, _ db.Tx, source knowledgeinstances.Source) error {
	if s.beforeFence != nil {
		s.beforeFence(source)
	}
	if source.Stamp() != s.sources[source.RootID].Stamp() {
		return knowledgeinstances.ErrConflict
	}
	return nil
}

type conflictDocumentModel struct {
	extracts, reviews int
	selectRoot        string
	beforeReview      func()
	plans             []knowledgeinstances.Plan
	inputs            []documentExtractionInput
}

func (m *conflictDocumentModel) ModelID() string { return "test-conflict-model" }
func (m *conflictDocumentModel) Extract(_ context.Context, input documentExtractionInput) (knowledgeinstances.Extraction, error) {
	m.extracts++
	m.inputs = append(m.inputs, input)
	if !strings.Contains(input.Source.Content, "Acme") {
		return knowledgeinstances.Extraction{Reason: "The revised source withdraws Acme."}, nil
	}
	id := ""
	for _, c := range input.Candidates {
		if c.Name == "Acme" {
			id = c.ID
		}
	}
	country := "CN"
	if strings.Contains(input.Source.Content, "US") {
		country = "US"
	}
	quote := "Acme operates in " + country
	return knowledgeinstances.Extraction{Reason: "Literal source establishes the company and country.", Entities: []knowledgeinstances.EntityInput{{Ref: "acme", ExistingID: id, Kind: "company", Name: "Acme", Quote: quote, Properties: []knowledgeinstances.Property{{Predicate: "country", Value: country, Quote: quote}}}}}, nil
}
func (m *conflictDocumentModel) Review(_ context.Context, p knowledgeinstances.Plan) (knowledgeinstances.Decision, error) {
	m.reviews++
	m.plans = append(m.plans, p)
	if m.beforeReview != nil {
		m.beforeReview()
	}
	d := knowledgeinstances.Decision{Decision: "approve", Reason: "Independent model checked the exact replacement and its source evidence."}
	for _, c := range p.Conflicts {
		choice := knowledgeinstances.ResolutionChoice{EntityID: c.EntityID, Predicate: c.Predicate, Decision: "defer", Reason: "The offered sources do not justify one current jurisdiction."}
		for _, a := range c.Assertions {
			if a.Claim.Source.RootID == m.selectRoot {
				choice.Decision, choice.ClaimID, choice.Reason = "accept", a.Claim.ID, "The selected offered source explicitly states the applicable current country."
			}
		}
		d.Resolutions = append(d.Resolutions, choice)
	}
	return d, nil
}

func conflictDocumentFixture(t *testing.T) (*productionOntologyDocumentInference, *continuousInferenceProfiles, *conflictDocumentSources, *conflictDocumentModel, knowledgeinstances.Watch, knowledgeinstances.Watch) {
	t.Helper()
	w, profiles, initial, _, a := documentInferenceFixture(t)
	sources := &conflictDocumentSources{sources: map[string]knowledgeinstances.Source{initial.source.RootID: initial.source}, denied: map[string]bool{}}
	other := initial.source
	other.RootID, other.DocumentID, other.ContentHash, other.Content = "doc-other", "other-provider-doc", "sha256:other-v1", "Acme operates in US."
	sources.sources[other.RootID] = other
	ctx := context.Background()
	if err := contextgraph.NewPlatformStore(w.store.Core).SaveObject(ctx, contextgraph.Object{ID: other.RootID, TenantID: other.TenantID, ObjectType: "knowledge_document", CanonicalKey: "knowledge_document:" + other.RootID, Label: "Other source", Properties: map[string]any{"knowledge_base_id": "kb", "provider_document_id": other.DocumentID}, OwnerAccessRef: other.Access}); err != nil {
		t.Fatal(err)
	}
	model := &conflictDocumentModel{}
	w.sources, w.model = sources, model
	p, err := w.api.documentInferencePrincipal(ctx, a.TenantID, a.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := w.api.DocumentInference(ctx, p, knowledgeinstances.WatchRequest{Operation: "publish", TenantID: a.TenantID, ID: "other-watch", RootID: other.RootID})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Process(ctx, documentJob(a, "initial-source")); err != nil {
		t.Fatal(err)
	}
	return w, profiles, sources, model, a, result["watch"].(knowledgeinstances.Watch)
}

func TestProductionOntologyDocumentConflictModelDecisionPersistsAndReplays(t *testing.T) {
	w, _, sources, model, a, b := conflictDocumentFixture(t)
	ctx := context.Background()
	model.selectRoot = a.RootID
	job := documentJob(b, "accept-source-a")
	if err := w.Process(ctx, job); err != nil {
		t.Fatal(err)
	}
	batch, err := w.store.GetBatch(ctx, b.TenantID, job.ID)
	if err != nil || batch.Status != "applied" || batch.Receipt.ResolutionCount != 1 || len(batch.Plan.Conflicts) != 1 || len(batch.Plan.Conflicts[0].Assertions) != 2 {
		t.Fatalf("adjudicated batch: %+v %v", batch, err)
	}
	if batch.Decision.ModelID != model.ModelID() || batch.Decision.Resolutions[0].Decision != "accept" {
		t.Fatal("model adjudication was not persisted")
	}
	entities, _ := w.store.Candidates(ctx, a.TenantID, sources.sources[a.RootID].Access)
	if entities[0].Properties["country"] != "CN" || len(entities[0].Resolutions) != 1 {
		t.Fatalf("accepted entity: %+v", entities)
	}
	if err := w.Process(ctx, job); err != nil {
		t.Fatal(err)
	}
	if model.reviews != 2 || model.extracts != 4 {
		t.Fatal("duplicate delivery reran adjudication")
	}
	// A restart also reuses the persisted model decision and receipt.
	w.store = &knowledgeinstances.Store{Core: w.store.Core}
	if err := w.Process(ctx, job); err != nil || model.reviews != 2 {
		t.Fatalf("restart replay: %v", err)
	}
	// Candidate prompts include only policy-filtered properties; internal
	// adjudication reasons and claim identifiers must not bypass the read API.
	current, _ := w.current(ctx, a)
	previous, _ := w.store.Claims(ctx, a.TenantID, a.ID)
	candidates, err := w.candidates(ctx, current, previous)
	if err != nil || len(candidates) != 1 || len(candidates[0].Resolutions) != 0 {
		t.Fatalf("candidate audit leakage: %+v %v", candidates, err)
	}
	// Status includes authorized audit decisions but hides their reasons after
	// a competing document becomes unreadable, even if the primary stays visible.
	p, _ := w.api.documentInferencePrincipal(ctx, a.TenantID, a.AgentID)
	sources.denied[a.RootID] = true
	// Historical visibility uses the object policy; move that source outside
	// the principal's boundary while retaining the separate assertion evidence.
	if _, err := w.store.Core.Exec(ctx, `UPDATE context_graph_objects SET owner_access_id='hidden-boundary' WHERE tenant_id=? AND id=?`, a.TenantID, a.RootID); err != nil {
		t.Fatal(err)
	}
	status, err := w.api.DocumentInference(ctx, p, knowledgeinstances.WatchRequest{Operation: "status", TenantID: b.TenantID, ID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	entry := status["batches"].([]map[string]any)[0]
	redacted, ok := entry["decision"].(map[string]any)
	if !ok || redacted["reason"] != nil || redacted["resolutions"] != nil {
		t.Fatalf("hidden source adjudication disclosed: %+v", entry)
	}
}

func TestProductionOntologyDocumentConflictRefreshFencesOtherSources(t *testing.T) {
	for _, mode := range []string{"source-changed-in-model", "source-changed-in-transaction", "source-denied", "primary-grant-revoked"} {
		t.Run(mode, func(t *testing.T) {
			w, profiles, sources, model, a, b := conflictDocumentFixture(t)
			ctx := context.Background()
			model.selectRoot = a.RootID
			advance := func() {
				source := sources.sources[a.RootID]
				source.Revision++
				source.ContentHash = "sha256:changed"
				sources.sources[a.RootID] = source
			}
			switch mode {
			case "source-changed-in-model":
				model.beforeReview = advance
			case "source-changed-in-transaction":
				sources.beforeFence = func(source knowledgeinstances.Source) {
					if source.RootID == a.RootID {
						advance()
					}
				}
			case "source-denied":
				model.beforeReview = func() { sources.denied[a.RootID] = true }
			case "primary-grant-revoked":
				model.beforeReview = func() { profiles.profile.ToolRefs = nil }
			}
			job := documentJob(b, "fenced-adjudication")
			err := w.Process(ctx, job)
			if mode == "primary-grant-revoked" {
				if !errors.Is(err, ontologyscope.ErrAccessDenied) {
					t.Fatalf("grant fence: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			claims, _ := w.store.Claims(ctx, b.TenantID, b.ID)
			batch, _ := w.store.GetBatch(ctx, b.TenantID, job.ID)
			if len(claims) != 0 || batch.Receipt != nil || (mode != "primary-grant-revoked" && batch.Status != "superseded") {
				t.Fatalf("stale adjudication committed: %+v", batch)
			}
		})
	}
}

func TestProductionOntologyDocumentUnreadableConflictStaysDisputed(t *testing.T) {
	for _, mode := range []string{"source-unreadable", "fact-value-masked"} {
		t.Run(mode, func(t *testing.T) {
			w, _, sources, model, a, b := conflictDocumentFixture(t)
			ctx := context.Background()
			if mode == "source-unreadable" {
				sources.denied[a.RootID] = true
			} else if err := datapolicy.NewPlatformStore(w.store.Core).SaveFieldPolicy(ctx, datapolicy.FieldPolicy{ID: "mask-competing-fact", TenantID: a.TenantID, Consumer: "ontology_workbench", Purpose: "ontology.context.read", ObjectType: "fact", FieldPath: "value", Effect: datapolicy.EffectMask, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			model.selectRoot = a.RootID
			if err := w.Process(ctx, documentJob(b, "hidden-source")); err != nil {
				t.Fatal(err)
			}
			last := model.plans[len(model.plans)-1]
			if len(last.Conflicts) != 0 || last.ClaimSetHash == "" {
				t.Fatalf("hidden source reached model: %+v", last.Conflicts)
			}
			entities, _ := w.store.Candidates(ctx, a.TenantID, sources.sources[a.RootID].Access)
			if entities[0].Properties["country"] != nil || len(entities[0].Conflicts) != 1 || len(entities[0].Resolutions) != 0 {
				t.Fatalf("hidden evidence auto-selected: %+v", entities)
			}
		})
	}
}

func TestProductionOntologyDocumentAdjudicationDrivesInferenceRetractionAndRecovery(t *testing.T) {
	w, _, sources, model, a, b := conflictDocumentFixture(t)
	ctx := context.Background()
	entities, _ := w.store.Candidates(ctx, a.TenantID, sources.sources[a.RootID].Access)
	id := entities[0].ID
	definition, _ := w.api.registry.GetLinkType(ctx, "workflow_derived")
	definition.FromObjectType, definition.ToObjectType = "knowledge_document", "knowledge_entity"
	if err := ontology.NewPlatformRegistry(w.store.Core).SaveLinkType(ctx, definition); err != nil {
		t.Fatal(err)
	}
	w.api.ruleSets = &ontologyreasoning.RuleSetStore{Store: w.store.Core}
	p, _ := w.api.continuousInferencePrincipal(ctx, a.TenantID, a.AgentID)
	rule := ontologyreasoning.Rule{ID: "country-cn", Version: "1", When: []ontologyreasoning.Atom{
		{Kind: "link", Type: knowledgeinstances.DocumentLink, Subject: "?doc", Object: "?entity"},
		{Kind: "fact", Subject: "?entity", Predicate: "country", Value: "CN"},
		{Kind: "fact", Subject: "?doc", Predicate: "provider_document_id", Value: "provider-doc"},
	}, Then: ontologyreasoning.Atom{Kind: "link", Type: "workflow_derived", Subject: "?doc", Object: "?entity"}}
	sets, err := w.api.InferenceRules(ctx, p, ontologyreasoning.RuleSetRequest{Operation: "publish", TenantID: a.TenantID, ID: "accepted-country", RootID: id, Rules: []ontologyreasoning.Rule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	judge := &continuousInferenceJudge{}
	continuous := &productionContinuousInference{api: w.api, judge: judge}
	evaluation := continuousInferenceEvaluationJob(sets[0])
	decide := func(calls int, support bool, effect string) {
		t.Helper()
		if err := continuous.Evaluate(ctx, evaluation); err != nil {
			t.Fatal(err)
		}
		jobs := continuousInferenceJobs(t, continuous, ontologyreasoning.InferenceDecisionTopic)
		if len(jobs) != calls {
			t.Fatalf("decision tasks %d want %d", len(jobs), calls)
		}
		if err := continuous.Decide(ctx, jobs[len(jobs)-1]); err != nil {
			t.Fatal(err)
		}
		input := judge.inputs[len(judge.inputs)-1]
		if judge.calls != calls || input.CurrentSupport != support || input.Preview.Effect != effect {
			t.Fatalf("lifecycle calls=%d support=%v effect=%s", judge.calls, input.CurrentSupport, input.Preview.Effect)
		}
	}
	process := func(watch knowledgeinstances.Watch, job string) {
		t.Helper()
		source := sources.sources[watch.RootID]
		source.Revision++
		source.ContentHash = fmt.Sprintf("sha256:%s:%d", watch.RootID, source.Revision)
		sources.sources[watch.RootID] = source
		if err := w.Process(ctx, documentJob(watch, job)); err != nil {
			t.Fatal(err)
		}
	}
	decide(1, true, "assert")
	process(b, "b-disputed")
	decide(2, false, "retract")
	model.selectRoot = a.RootID
	process(b, "b-accept-cn")
	decide(3, true, "reactivate")
	model.selectRoot = b.RootID
	process(a, "a-accept-us")
	decide(4, false, "retract")
	source := sources.sources[b.RootID]
	source.Content = "This source now withdraws the company."
	sources.sources[b.RootID] = source
	process(b, "b-withdraw")
	decide(5, true, "reactivate")
}

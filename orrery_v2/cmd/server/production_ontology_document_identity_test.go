package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/platform/db"
)

type identityDocumentModel struct {
	draft   knowledgeinstances.Extraction
	inputs  []documentExtractionInput
	resolve func(documentExtractionInput, *knowledgeinstances.Extraction)
	reviews int
}

func (m *identityDocumentModel) ModelID() string { return "identity-test-model" }
func (m *identityDocumentModel) Extract(_ context.Context, input documentExtractionInput) (knowledgeinstances.Extraction, error) {
	m.inputs = append(m.inputs, input)
	raw, _ := json.Marshal(m.draft)
	var result knowledgeinstances.Extraction
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	if input.Phase == "resolve" && m.resolve != nil {
		m.resolve(input, &result)
	}
	return result, nil
}
func (m *identityDocumentModel) Review(_ context.Context, p knowledgeinstances.Plan) (knowledgeinstances.Decision, error) {
	m.reviews++
	return knowledgeinstances.Decision{Decision: "approve", Reason: "Source-qualified identity assignments independently checked.", ModelID: m.ModelID(), PlanHash: knowledgeinstances.Digest(p)}, nil
}

func setIdentityDocument(source *documentSourceFixture, model *identityDocumentModel, name, value string) {
	quote := fmt.Sprintf("%s is registered in CN by Registry A with number %s.", name, value)
	source.source.Content, source.source.ContentHash = quote, knowledgeinstances.Digest(quote)
	model.draft = knowledgeinstances.Extraction{Reason: "Literal registration identifies the company.", Entities: []knowledgeinstances.EntityInput{{Ref: "company", Name: name, Kind: "company", Quote: quote, Identifiers: []knowledgeinstances.IdentifierInput{{Identifier: knowledgeinstances.Identifier{Scheme: "company_registration", Jurisdiction: "CN", Authority: "Registry A", Value: value}, Quote: quote}}}}}
}

func TestProductionOntologyDocumentQualifiedHomonymsAndIdentityReplay(t *testing.T) {
	ctx := context.Background()
	w, _, source, _, watch := documentInferenceFixture(t)
	m := &identityDocumentModel{}
	w.model = m
	m.resolve = func(input documentExtractionInput, x *knowledgeinstances.Extraction) {
		for _, e := range input.Candidates {
			for _, key := range e.IdentityKeys {
				if key.Value == x.Entities[0].Identifiers[0].Value {
					x.Entities[0].ExistingID = e.ID
				}
			}
		}
	}
	setIdentityDocument(source, m, "Acme", "00123")
	firstJob := documentJob(watch, "identity-source-1")
	if err := w.Process(ctx, firstJob); err != nil {
		t.Fatal(err)
	}
	first, _ := w.store.GetBatch(ctx, watch.TenantID, firstJob.ID)
	if first.Receipt == nil || first.Plan.CandidateRecall == nil || len(first.Plan.IdentityAssignments) != 1 || len(m.inputs) != 2 || m.inputs[0].Phase != "mentions" || m.inputs[1].Phase != "resolve" || len(m.inputs[0].Candidates) != 0 {
		t.Fatalf("identity phases/audit missing: %+v", first)
	}
	if err := w.Process(ctx, firstJob); err != nil || len(m.inputs) != 2 || m.reviews != 1 {
		t.Fatalf("identity replay reran model: %v", err)
	}
	source.source.Revision++
	setIdentityDocument(source, m, "Acme", "00456")
	if err := w.Process(ctx, documentJob(watch, "identity-source-2")); err != nil {
		t.Fatal(err)
	}
	second, _ := w.store.GetBatch(ctx, watch.TenantID, "identity-source-2")
	if second.Receipt == nil || second.Plan.Entities[0].ID == first.Plan.Entities[0].ID {
		t.Fatal("same-name registrations were merged")
	}
	source.source.Revision++
	setIdentityDocument(source, m, "Renamed Acme", "00456")
	if err := w.Process(ctx, documentJob(watch, "identity-source-3")); err != nil {
		t.Fatal(err)
	}
	third, _ := w.store.GetBatch(ctx, watch.TenantID, "identity-source-3")
	if third.Plan.Entities[0].ID != second.Plan.Entities[0].ID || third.Plan.IdentityAssignments[0].New {
		t.Fatal("strong registration did not preserve existing identity")
	}
	entities, _ := w.store.Candidates(ctx, watch.TenantID, source.source.Access)
	if len(entities) != 2 || m.reviews != 3 {
		t.Fatalf("homonym history/model approvals: %+v %d", entities, m.reviews)
	}
}

func TestProductionOntologyDocumentIdentityRecallRespectsMaskedMetadata(t *testing.T) {
	ctx := context.Background()
	w, _, source, _, watch := documentInferenceFixture(t)
	m := &identityDocumentModel{}
	w.model = m
	setIdentityDocument(source, m, "Visible", "00123")
	if err := w.Process(ctx, documentJob(watch, "identity-visible")); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"properties.identity_keys", "properties.aliases", "properties.registration_id"} {
		if err := datapolicy.NewPlatformStore(w.store.Core).SaveFieldPolicy(ctx, datapolicy.FieldPolicy{ID: field, TenantID: watch.TenantID, Consumer: "ontology_workbench", Purpose: "ontology.context.read", ObjectType: "knowledge_entity", FieldPath: field, Effect: datapolicy.EffectMask, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	setIdentityDocument(source, m, "Unrelated", "00123")
	current, err := w.current(ctx, watch)
	if err != nil {
		t.Fatal(err)
	}
	x, candidates, recall, err := w.extractWithIdentityRecall(ctx, current, nil)
	if err != nil || len(candidates) != 0 || len(recall.CandidateIDs) != 0 || !recall.Truncated || !x.Defer {
		t.Fatalf("raw registration bypassed field policy: %+v %+v %v", candidates, recall, err)
	}
	last := m.inputs[len(m.inputs)-1]
	if len(last.Candidates) != 0 {
		t.Fatal("masked registration leaked to identity resolver")
	}
}

func TestProductionOntologyDocumentIdentityChecksPreviousScopeBeforeModel(t *testing.T) {
	w, _, source, _, watch := documentInferenceFixture(t)
	m := &identityDocumentModel{}
	w.model = m
	setIdentityDocument(source, m, "Acme", "00123")
	ctx := context.Background()
	if err := w.Process(ctx, documentJob(watch, "identity-before-scope-change")); err != nil {
		t.Fatal(err)
	}
	source.source.Access.ID = "different-access-boundary"
	source.source.Revision++
	if err := w.Process(ctx, documentJob(watch, "identity-after-scope-change")); !errors.Is(err, knowledgeinstances.ErrConflict) {
		t.Fatalf("scope migration exposed previous assertions: %v", err)
	}
	if len(m.inputs) != 2 || m.reviews != 1 {
		t.Fatal("old assertions reached a model before their scope was checked")
	}
}

func TestProductionOntologyDocumentIdentityTruncationDefersNewEntities(t *testing.T) {
	ctx := context.Background()
	w, _, source, _, watch := documentInferenceFixture(t)
	m := &identityDocumentModel{draft: knowledgeinstances.Extraction{Reason: "Source explicitly names Acme.", Entities: []knowledgeinstances.EntityInput{{Ref: "a", Name: "Acme", Kind: "company", Quote: source.source.Content}}}}
	w.model = m
	if err := w.store.Core.WithTx(ctx, func(tx db.Tx) error {
		graph := contextgraph.NewPlatformStoreTx(tx)
		for i := 0; i < 201; i++ {
			id := fmt.Sprintf("crowded-%03d", i)
			e := knowledgeinstances.Entity{TenantID: watch.TenantID, ID: id, Kind: "company", Name: "Acme", Version: 1, Access: source.source.Access, Properties: map[string]any{"entity_kind": "company"}, Status: "active"}
			raw, _ := json.Marshal(e)
			if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_instances(tenant_id,id,access_hash,version,body_json) VALUES(?,?,?,?,?)`, e.TenantID, e.ID, knowledgeinstances.Digest(e.Access), e.Version, string(raw)); err != nil {
				return err
			}
			if err := graph.SaveObject(ctx, contextgraph.Object{TenantID: e.TenantID, ID: e.ID, ObjectType: "knowledge_entity", CanonicalKey: "knowledge_entity:" + id, Label: e.Name, Properties: e.Properties, OwnerAccessRef: e.Access, Status: "active"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	job := documentJob(watch, "truncated-recall")
	if err := w.Process(ctx, job); err != nil {
		t.Fatal(err)
	}
	b, err := w.store.GetBatch(ctx, watch.TenantID, job.ID)
	if err != nil || b.Status != "defer" || b.Receipt != nil || !b.Plan.CandidateRecall.Truncated || m.reviews != 0 || len(m.inputs) != 2 {
		t.Fatalf("incomplete recall created identity: %+v %v", b, err)
	}
	if err := w.Process(ctx, job); err != nil || len(m.inputs) != 2 {
		t.Fatalf("deferred duplicate reran model: %v", err)
	}
	var count int
	if err := w.store.Core.QueryRow(ctx, `SELECT COUNT(*) FROM action_runs WHERE action_type_id=?`, knowledgeinstances.ActionID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deferred new identity committed: %d %v", count, err)
	}
}

func TestProductionOntologyDocumentIdentityRejectsUnsearchedChangesAndIDs(t *testing.T) {
	for _, mode := range []string{"changed_mention", "unoffered_id"} {
		t.Run(mode, func(t *testing.T) {
			w, _, source, _, watch := documentInferenceFixture(t)
			m := &identityDocumentModel{}
			w.model = m
			setIdentityDocument(source, m, "Acme", "00123")
			m.resolve = func(_ documentExtractionInput, x *knowledgeinstances.Extraction) {
				if mode == "changed_mention" {
					x.Entities[0].Name = "Different"
				} else {
					x.Entities[0].ExistingID = "unoffered"
				}
			}
			if err := w.Process(context.Background(), documentJob(watch, mode)); !errors.Is(err, knowledgeinstances.ErrInvalid) || m.reviews != 0 {
				t.Fatalf("unsearched resolution accepted: %v", err)
			}
		})
	}
}

func TestProductionOntologyIdentityHistoryCannotBecomeInferencePremise(t *testing.T) {
	w, _, source, _, watch := documentInferenceFixture(t)
	m := &identityDocumentModel{}
	w.model = m
	setIdentityDocument(source, m, "Acme", "00123")
	ctx := context.Background()
	if err := w.Process(ctx, documentJob(watch, "historical-identity")); err != nil {
		t.Fatal(err)
	}
	b, _ := w.store.GetBatch(ctx, watch.TenantID, "historical-identity")
	id := b.Plan.Entities[0].ID
	// Even an explicit graph fact must not turn host identity metadata into a
	// business premise; the ordinary object/fact read paths both see this field.
	keys := []any{map[string]any{"scheme": "company_registration", "jurisdiction": "cn", "authority": "registry a", "value": "00123"}}
	if err := contextgraph.NewPlatformStore(w.store.Core).SaveFact(ctx, contextgraph.Fact{TenantID: watch.TenantID, ID: "identity-metadata-fact", Subject: contextgraph.Ref{Kind: "knowledge_entity", ID: id}, Predicate: "identity_keys", Value: keys, Status: "active", SourceAccessRef: source.source.Access, SourceStreamID: "identity-metadata-fixture", SourceVersion: 1}); err != nil {
		t.Fatal(err)
	}
	p, err := w.api.documentInferencePrincipal(ctx, watch.TenantID, watch.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := w.api.Evaluate(ctx, p, ontologyreasoning.EvaluateRequest{TenantID: watch.TenantID, RootID: id, Rules: []ontologyreasoning.Rule{{ID: "identity-is-not-a-premise", Version: "1", When: []ontologyreasoning.Atom{{Kind: "object", Type: "knowledge_entity", Subject: "?entity"}, {Kind: "fact", Subject: "?entity", Predicate: "identity_keys", Value: keys}}, Then: ontologyreasoning.Atom{Kind: "fact", Subject: "?entity", Predicate: "country", Value: "CN"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Candidates) != 0 || slices.ContainsFunc(run.Request.ContextPack.Facts, func(f contextgraph.Fact) bool { return f.Predicate == "identity_keys" }) {
		t.Fatalf("historical registration became a business premise: %+v", run.Candidates)
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type documentSourceFixture struct {
	source      knowledgeinstances.Source
	beforeFence func()
	denied      bool
}

func (s *documentSourceFixture) Load(_ context.Context, _ identity.Principal, w knowledgeinstances.Watch) (knowledgeinstances.Source, error) {
	if s.denied || s.source.TenantID != w.TenantID || s.source.RootID != w.RootID {
		return s.source, ontologyscope.ErrAccessDenied
	}
	return s.source, nil
}
func (s *documentSourceFixture) Fence(_ context.Context, _ db.Tx, source knowledgeinstances.Source) error {
	if s.beforeFence != nil {
		s.beforeFence()
	}
	if source.Stamp() != s.source.Stamp() {
		return knowledgeinstances.ErrConflict
	}
	return nil
}

type documentModelFixture struct {
	extracts, reviews           int
	beforeExtract, beforeReview func()
	failReview                  bool
	decision                    string
	deferExtraction             bool
}

func (m *documentModelFixture) ModelID() string { return "test-document-model" }
func (m *documentModelFixture) Extract(_ context.Context, input documentExtractionInput) (knowledgeinstances.Extraction, error) {
	m.extracts++
	if m.beforeExtract != nil {
		m.beforeExtract()
	}
	if m.deferExtraction {
		return knowledgeinstances.Extraction{Defer: true, Reason: "Two offered identities are ambiguous."}, nil
	}
	if !strings.Contains(input.Source.Content, "Acme") {
		return knowledgeinstances.Extraction{Reason: "The new revision no longer describes Acme."}, nil
	}
	id := ""
	for _, e := range input.Candidates {
		if e.Name == "Acme" {
			id = e.ID
		}
	}
	return knowledgeinstances.Extraction{Reason: "Acme is explicitly named in the source.", Entities: []knowledgeinstances.EntityInput{{Ref: "acme", ExistingID: id, Kind: "company", Name: "Acme", Quote: "Acme operates in CN", Properties: []knowledgeinstances.Property{{Predicate: "country", Value: "CN", Quote: "Acme operates in CN"}}}}}, nil
}
func (m *documentModelFixture) Review(_ context.Context, p knowledgeinstances.Plan) (knowledgeinstances.Decision, error) {
	m.reviews++
	if m.beforeReview != nil {
		m.beforeReview()
	}
	if m.failReview {
		return knowledgeinstances.Decision{}, errors.New("model temporarily unavailable")
	}
	decision := m.decision
	if decision == "" {
		decision = "approve"
	}
	return knowledgeinstances.Decision{Decision: decision, Reason: "The exact quoted replacement was checked.", ModelID: m.ModelID(), PlanHash: knowledgeinstances.Digest(p)}, nil
}

func documentInferenceFixture(t *testing.T) (*productionOntologyDocumentInference, *continuousInferenceProfiles, *documentSourceFixture, *documentModelFixture, knowledgeinstances.Watch) {
	t.Helper()
	api, core, _, input := productionInferenceWorkflowFixture(t)
	ctx := context.Background()
	profiles := &continuousInferenceProfiles{profile: agentcore.AgentProfile{ID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true, ToolRefs: []agentcore.ToolRef{
		{ToolID: toolstd.OntologyInfer, PermissionScope: "ontology.manage"}, {ToolID: toolstd.OntologyInferenceWorkflow, PermissionScope: "ontology.manage"}, {ToolID: toolstd.OntologyInferenceRules, PermissionScope: "ontology.manage"}, {ToolID: toolstd.OntologyDocumentInference, PermissionScope: "ontology.manage"}, {ToolID: toolstd.KnowledgeSearch, PermissionScope: "knowledge.read"},
	}}}
	api.profiles = profiles
	source := &documentSourceFixture{source: knowledgeinstances.Source{TenantID: input.TenantID, RootID: "doc-root", KnowledgeBaseID: "kb", DocumentID: "provider-doc", Revision: 1, ContentHash: "sha256:v1", Content: "Acme operates in CN.", Access: contextgraph.AccessRef{Kind: "organization", ID: input.TenantID, TenantID: input.TenantID}}}
	if err := contextgraph.NewPlatformStore(core).SaveObject(ctx, contextgraph.Object{ID: "doc-root", TenantID: input.TenantID, ObjectType: "knowledge_document", Label: "Source document", Properties: map[string]any{"knowledge_base_id": "kb", "provider_document_id": "provider-doc"}, OwnerAccessRef: source.source.Access}); err != nil {
		t.Fatal(err)
	}
	model := &documentModelFixture{}
	w := &productionOntologyDocumentInference{api: api, store: &knowledgeinstances.Store{Core: core}, watches: &knowledgeinstances.WatchStore{Store: core}, sources: source, model: model}
	api.documents = w
	p, err := api.documentInferencePrincipal(ctx, input.TenantID, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	value, err := api.DocumentInference(ctx, p, knowledgeinstances.WatchRequest{Operation: "publish", TenantID: input.TenantID, ID: "document-watch", RootID: "doc-root"})
	if err != nil {
		t.Fatal(err)
	}
	return w, profiles, source, model, value["watch"].(knowledgeinstances.Watch)
}

func documentJob(watch knowledgeinstances.Watch, id string) outbox.Job {
	payload, _ := json.Marshal(knowledgeinstances.DocumentJob{TenantID: watch.TenantID, WatchID: watch.ID, Version: watch.Version})
	return outbox.Job{ID: id, Topic: knowledgeinstances.DocumentTopic, Payload: payload}
}

func TestProductionOntologyDocumentExtractReviewApplyAndRevision(t *testing.T) {
	w, _, source, model, watch := documentInferenceFixture(t)
	ctx := context.Background()
	job := documentJob(watch, "document-job-1")
	if err := w.Process(ctx, job); err != nil {
		t.Fatal(err)
	}
	b, err := w.store.GetBatch(ctx, watch.TenantID, job.ID)
	if err != nil || b.Status != "applied" || b.Receipt == nil || model.extracts != 2 || model.reviews != 1 {
		t.Fatalf("workflow: %+v %v", b, err)
	}
	if b.Plan.ExtractorModelID != "test-document-model" || b.Decision.ModelID != "test-document-model" {
		t.Fatal("model provenance missing")
	}
	if err := w.Process(ctx, job); err != nil {
		t.Fatal(err)
	}
	if model.extracts != 2 || model.reviews != 1 {
		t.Fatal("duplicate job reran a model")
	}
	source.source.Revision = 2
	source.source.ContentHash = "sha256:v2"
	source.source.Content = "There is no current company information."
	if err := w.Process(ctx, documentJob(watch, "document-job-2")); err != nil {
		t.Fatal(err)
	}
	claims, _ := w.store.Claims(ctx, watch.TenantID, watch.ID)
	if len(claims) != 0 {
		t.Fatal("old source assertions survived a replacement")
	}
	entities, _ := w.store.Candidates(ctx, watch.TenantID, source.source.Access)
	if len(entities) != 1 || entities[0].Status != "withdrawn" {
		t.Fatalf("identity history lost: %+v", entities)
	}
	if model.extracts != 3 || model.reviews != 2 {
		t.Fatal("source withdrawal bypassed model judgment")
	}
	var count int
	if err := w.store.Core.QueryRow(ctx, `SELECT COUNT(*) FROM action_runs WHERE actor_kind='agent' AND actor_id='agent-1' AND action_type_id=?`, knowledgeinstances.ActionID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("domain actions: %d %v", count, err)
	}
}

func TestProductionOntologyDocumentFailsClosedAcrossModelLatency(t *testing.T) {
	for _, scenario := range []string{"source_revision", "source_fence", "revoke", "pause", "reject", "defer", "extract_defer", "source_access"} {
		t.Run(scenario, func(t *testing.T) {
			w, profiles, source, model, watch := documentInferenceFixture(t)
			ctx := context.Background()
			model.beforeReview = func() {
				switch scenario {
				case "source_revision":
					source.source.Revision++
					source.source.ContentHash = "changed"
				case "revoke":
					profiles.profile.ToolRefs = nil
				case "pause":
					watch.Active = false
					if _, err := w.watches.Publish(ctx, watch, watch.Version, func(context.Context, db.Tx) error { return nil }); err != nil {
						t.Fatal(err)
					}
				case "source_access":
					source.denied = true
				}
			}
			if scenario == "source_fence" {
				source.beforeFence = func() { source.source.Revision++ }
			}
			if scenario == "reject" || scenario == "defer" {
				model.decision = scenario
			}
			if scenario == "extract_defer" {
				model.deferExtraction = true
			}
			job := documentJob(watch, "fenced-job")
			err := w.Process(ctx, job)
			if scenario == "revoke" || scenario == "source_access" {
				if !errors.Is(err, ontologyscope.ErrAccessDenied) {
					t.Fatalf("access revocation accepted: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var count int
			for _, table := range []string{"knowledge_instances", "knowledge_instance_claims", "ontology_document_receipts"} {
				if err := w.store.Core.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("%s partial effects: %d %v", table, count, err)
				}
			}
			if scenario == "source_revision" || scenario == "source_fence" {
				batch, _ := w.store.GetBatch(ctx, watch.TenantID, job.ID)
				if batch.Status != "superseded" {
					t.Fatalf("stale plan will retry forever: %+v", batch)
				}
			}
			if scenario == "extract_defer" && model.reviews != 0 {
				t.Fatal("ambiguous extraction proceeded to approval")
			}
		})
	}
}

func TestProductionOntologyDocumentRestartReusesPreparedModelWork(t *testing.T) {
	w, _, _, model, watch := documentInferenceFixture(t)
	ctx := context.Background()
	model.failReview = true
	job := documentJob(watch, "restart-job")
	if err := w.Process(ctx, job); err == nil {
		t.Fatal("expected transient model failure")
	}
	model.failReview = false
	w.store = &knowledgeinstances.Store{Core: w.store.Core}
	if err := w.Process(ctx, job); err != nil {
		t.Fatal(err)
	}
	if model.extracts != 2 || model.reviews != 2 {
		t.Fatalf("prepared extraction was lost: %+v", model)
	}
	if err := w.Process(ctx, job); err != nil {
		t.Fatal(err)
	}
	if model.reviews != 2 {
		t.Fatal("approved job was not idempotent")
	}
}

func TestProductionOntologyDocumentMCPUsesRuntimeTenantAndAgent(t *testing.T) {
	w, _, _, _, watch := documentInferenceFixture(t)
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Now().UTC()) {
		if definition.ID == toolstd.OntologyDocumentInference {
			if err := registry.Register(ctx, definition); err != nil {
				t.Fatal(err)
			}
		}
	}
	router := mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: registry, OntologyExecutor: productionOntologyStewardToolExecutor{inference: w.api}})
	result, err := router.CallTool(ctx, mcptransport.CallToolRequest{TenantID: watch.TenantID, ToolID: string(toolstd.OntologyDocumentInference), AgentID: "agent-1", PrincipalID: "agent-1", Permissions: []string{"ontology.manage"}, Arguments: map[string]any{"operation": "status", "tenant_id": "forged-tenant", "watch_id": watch.ID}})
	if err != nil || result.Output["document_inference"] == nil {
		t.Fatalf("MCP integration: %+v %v", result, err)
	}
	_, err = router.CallTool(ctx, mcptransport.CallToolRequest{TenantID: watch.TenantID, ToolID: string(toolstd.OntologyDocumentInference), AgentID: "agent-1", PrincipalID: "agent-1", Permissions: []string{"ontology.read"}, Arguments: map[string]any{"operation": "list"}})
	if err == nil {
		t.Fatal("read-only credential managed document extraction")
	}
}

func TestProductionOntologyDocumentObservationAvoidsOwnWriteLoopAndAllowsModelRetry(t *testing.T) {
	w, _, source, model, watch := documentInferenceFixture(t)
	ctx := context.Background()
	count := func() int {
		t.Helper()
		var n int
		if err := w.store.Core.QueryRow(ctx, `SELECT COUNT(*) FROM platform_outbox_jobs WHERE topic=?`, knowledgeinstances.DocumentTopic).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	observe := func() {
		t.Helper()
		if _, err := w.store.Core.Exec(ctx, `UPDATE ontology_document_watches SET next_check_at=0`); err != nil {
			t.Fatal(err)
		}
		if err := w.Observe(ctx); err != nil {
			t.Fatal(err)
		}
	}
	before := count()
	if err := w.Process(ctx, documentJob(watch, "initial-model-job")); err != nil {
		t.Fatal(err)
	}
	observe()
	if count() != before {
		t.Fatal("own entity/fact writes retriggered extraction")
	}
	source.source.Revision++
	source.source.ContentHash = "sha256:source-v2"
	observe()
	observe()
	if count() != before+1 {
		t.Fatal("source observation was lost or duplicated")
	}
	p, err := w.api.documentInferencePrincipal(ctx, watch.TenantID, watch.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.api.DocumentInference(ctx, p, knowledgeinstances.WatchRequest{Operation: "retry", TenantID: watch.TenantID, ID: watch.ID, ExpectedVersion: watch.Version}); err != nil {
		t.Fatal(err)
	}
	observe()
	if count() != before+2 || model.extracts != 2 {
		t.Fatal("model retry did not queue exactly one fresh task")
	}
}

func TestProductionOntologyDocumentRevocationDuringExtractionPreventsReview(t *testing.T) {
	w, profiles, _, model, watch := documentInferenceFixture(t)
	model.beforeExtract = func() { profiles.profile.ToolRefs = nil }
	if err := w.Process(context.Background(), documentJob(watch, "revoked-in-extraction")); !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatal(err)
	}
	if model.reviews != 0 {
		t.Fatal("review model received evidence after grant revocation")
	}
}

type documentSchemaRaceRegistry struct {
	ontology.Registry
	afterRead func([]ontology.PropertyType) error
}

func (r *documentSchemaRaceRegistry) ListPropertyTypes(ctx context.Context, object string) ([]ontology.PropertyType, error) {
	properties, err := r.Registry.ListPropertyTypes(ctx, object)
	if err == nil && r.afterRead != nil {
		err = r.afterRead(properties)
	}
	return properties, err
}

func TestProductionOntologyDocumentSchemaChangeDuringFinalReadSupersedesPlan(t *testing.T) {
	w, _, _, model, watch := documentInferenceFixture(t)
	ctx := context.Background()
	registry := &documentSchemaRaceRegistry{Registry: w.api.registry}
	w.api.registry = registry
	changed := false
	model.beforeReview = func() {
		registry.afterRead = func(properties []ontology.PropertyType) error {
			registry.afterRead = nil
			for _, property := range properties {
				if property.ObjectTypeID == knowledgeinstances.EntityType && property.PropertyID == "country" {
					property.AllowedActions = nil
					changed = true
					return registry.SavePropertyType(ctx, property)
				}
			}
			return errors.New("owned country property missing")
		}
	}
	job := documentJob(watch, "schema-race-job")
	if err := w.Process(ctx, job); err != nil {
		t.Fatal(err)
	}
	batch, err := w.store.GetBatch(ctx, watch.TenantID, job.ID)
	if err != nil || !changed || batch.Status != "superseded" || batch.Receipt != nil {
		t.Fatalf("schema opt-out raced past commit: changed=%v status=%s receipt=%v err=%v", changed, batch.Status, batch.Receipt, err)
	}
	var count int
	for _, table := range []string{"knowledge_instances", "knowledge_instance_claims", "ontology_document_receipts"} {
		if err := w.store.Core.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s partial effects: %d %v", table, count, err)
		}
	}
}

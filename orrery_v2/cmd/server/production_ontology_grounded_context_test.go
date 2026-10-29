package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/retrievalevidence"
)

type groundingSnapshotHook struct {
	inferenceContextReader
	reader groundedSnapshotReader
	calls  int
	hook   func(int) error
}

func (s *groundingSnapshotHook) InferenceSnapshot(ctx context.Context, p identity.Principal, q contextview.InferenceSnapshotQuery) (contextview.InferenceSnapshot, error) {
	s.calls++
	if s.hook != nil {
		if err := s.hook(s.calls); err != nil {
			return contextview.InferenceSnapshot{}, err
		}
	}
	return s.reader.InferenceSnapshot(ctx, p, q)
}

func TestProductionOntologyGroundedContextUsesReadScopeAndCreatesNoWorkflow(t *testing.T) {
	a, core, p, input := productionInferenceWorkflowFixture(t)
	p.CoarseScopes = []string{"ontology.read"}
	p.CredentialScopeRestricted = true
	p.CredentialScopes = []string{"ontology.read"}
	request := ontologyreasoning.GroundedContextRequest{TenantID: input.TenantID, RootID: input.RootID, Rules: input.Rules}
	first, err := a.GroundedContext(context.Background(), p, request)
	if err != nil || len(first.Claims) != 4 || first.AccessHash == "" || first.CheckedAt.IsZero() {
		t.Fatalf("read context: %+v %v", first, err)
	}
	second, err := a.GroundedContext(context.Background(), p, request)
	if err != nil || first.SnapshotHash != second.SnapshotHash {
		t.Fatalf("stable snapshot: %s %s %v", first.SnapshotHash, second.SnapshotHash, err)
	}
	for _, table := range []string{"ontology_reasoning_runs", "ontology_inference_sessions", "ontology_document_batches", "ontology_document_receipts", "ontology_inference_receipts"} {
		var n int
		if err := core.QueryRow(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("read mutated %s: %d %v", table, n, err)
		}
	}
	p.CredentialScopes = []string{"knowledge.read"}
	if _, err := a.GroundedContext(context.Background(), p, request); !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatalf("credential scope widened: %v", err)
	}
}

func TestProductionOntologyGroundedContextRejectsMidReadChanges(t *testing.T) {
	for _, scenario := range []string{"source", "acl"} {
		t.Run(scenario, func(t *testing.T) {
			a, core, p, input := productionInferenceWorkflowFixture(t)
			hook := &groundingSnapshotHook{inferenceContextReader: a.context, reader: a.context.(groundedSnapshotReader)}
			hook.hook = func(call int) error {
				if call != 2 {
					return nil
				}
				if scenario == "acl" {
					return ontologyscope.ErrAccessDenied
				}
				_, err := core.Exec(context.Background(), `UPDATE context_graph_links SET status='withdrawn' WHERE tenant_id=? AND id='source'`, input.TenantID)
				return err
			}
			a.context = hook
			_, err := a.GroundedContext(context.Background(), p, ontologyreasoning.GroundedContextRequest{TenantID: input.TenantID, RootID: input.RootID, Rules: input.Rules})
			if scenario == "source" && !errors.Is(err, retrievalevidence.ErrInputChanged) {
				t.Fatalf("mixed source snapshot returned: %v", err)
			}
			if scenario == "acl" && !errors.Is(err, ontologyscope.ErrAccessDenied) {
				t.Fatalf("revoked ACL reused: %v", err)
			}
		})
	}
}

func TestProductionOntologyGroundedSourceHeadRejectsWithdrawalAndRepublish(t *testing.T) {
	w, _, fake, _, watch := documentInferenceFixture(t)
	ctx := context.Background()
	source := fake.source
	source.FolderID, source.Generation = "folder", 1
	binding := knowledgeontology.Binding{TenantID: watch.TenantID, KnowledgeBaseID: source.KnowledgeBaseID, FolderID: source.FolderID, SourceAccessRef: source.Access, Enabled: true}
	bridge := knowledgeontology.NewPlatformStore(w.store.Core)
	if err := bridge.SaveBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	projection := knowledgeontology.Projection{TenantID: watch.TenantID, KnowledgeBaseID: source.KnowledgeBaseID, DocumentID: source.DocumentID, KnowledgeDocumentObjectID: watch.RootID, Revision: source.Revision, ContentHash: source.ContentHash}
	if err := bridge.SaveProjectionAndCursor(ctx, binding, projection, "1"); err != nil {
		t.Fatal(err)
	}
	head := knowledgeontology.SourceHead{TenantID: source.TenantID, KnowledgeBaseID: source.KnowledgeBaseID, FolderID: source.FolderID, DocumentID: source.DocumentID, RootID: source.RootID, Revision: source.Revision, ContentHash: source.ContentHash, Access: source.Access, Generation: 1, Status: "published"}
	if _, err := w.store.Core.Exec(ctx, `INSERT INTO knowledge_ontology_source_heads(tenant_id,knowledge_base_id,document_id,body_json) VALUES(?,?,?,?) ON CONFLICT(tenant_id,knowledge_base_id,document_id) DO UPDATE SET body_json=excluded.body_json`, source.TenantID, source.KnowledgeBaseID, source.DocumentID, jsonString(head)); err != nil {
		t.Fatal(err)
	}
	repo := documentSourceRepository{base: knowledgecore.KnowledgeBase{ID: "kb", Status: knowledgecore.KnowledgeActive, ProviderResourceID: "folder"}, doc: knowledgecore.KnowledgeDocument{ID: knowledgecore.KnowledgeDocumentID(watch.RootID), KnowledgeBaseID: "kb", ProviderDocumentID: source.DocumentID, Status: knowledgecore.DocumentIndexed}}
	loader := &productionOntologyDocumentSource{api: w.api, core: w.store.Core, repo: repo}
	p, err := w.api.documentInferencePrincipal(ctx, watch.TenantID, watch.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := loader.checkGroundingSource(ctx, p, source)
	if err != nil || stamp == "" {
		t.Fatalf("current source rejected: %v", err)
	}
	fake.source = source
	if err := w.Process(ctx, documentJob(watch, "grounding-document")); err != nil {
		t.Fatal(err)
	}
	w.sources = loader
	request := ontologyreasoning.GroundedContextRequest{TenantID: watch.TenantID, RootID: watch.RootID}
	first, err := w.api.GroundedContext(ctx, p, request)
	if err != nil {
		t.Fatalf("attested document context: %v", err)
	}
	var countryClaim string
	for _, claim := range first.Claims {
		if claim.Atom.Predicate == "country" {
			countryClaim = claim.ID
		}
	}
	if countryClaim == "" {
		t.Fatalf("document-to-claim/source wiring absent: %+v", first)
	}
	request.Candidates = []ontologyreasoning.GroundingCandidate{{ID: "country", RequiredClaims: []string{countryClaim}}}
	for _, scenario := range []string{"withdrawn", "republished", "new-revision"} {
		t.Run(scenario, func(t *testing.T) {
			changed := head
			switch scenario {
			case "withdrawn":
				changed.Status = "withdrawn"
			case "republished":
				changed.Generation++
			case "new-revision":
				changed.Revision++
			}
			if _, err := w.store.Core.Exec(ctx, `UPDATE knowledge_ontology_source_heads SET body_json=? WHERE tenant_id=? AND knowledge_base_id=? AND document_id=?`, jsonString(changed), source.TenantID, source.KnowledgeBaseID, source.DocumentID); err != nil {
				t.Fatal(err)
			}
			actual, err := loader.checkGroundingSource(ctx, p, source)
			if err != nil || actual != "" {
				t.Fatalf("obsolete source accepted: %s %v", actual, err)
			}
			context, err := w.api.GroundedContext(ctx, p, request)
			if err != nil || len(context.Decisions) != 1 || context.Decisions[0].Eligible {
				t.Fatalf("old source retained a current claim: %+v %v", context, err)
			}
		})
	}
	// An independently owned assertion can keep the claim true in this
	// snapshot, but cannot stand in for the document's exact support version.
	var prior ontologyreasoning.GroundedSupport
	for _, support := range first.Supports {
		if support.Atom.Predicate == "country" {
			prior = support
			break
		}
	}
	if prior.AssertionRef == "" {
		t.Fatal("missing original document support")
	}
	if err := contextgraph.NewPlatformStore(w.store.Core).SaveFact(ctx, contextgraph.Fact{TenantID: watch.TenantID, ID: "independent-country", Subject: contextgraph.Ref{ID: prior.Atom.Subject}, Predicate: prior.Atom.Predicate, Value: prior.Atom.Value, Status: "active", SourceAccessRef: source.Access, SourceStreamID: "independent-country-source", SourceVersion: 1}); err != nil {
		t.Fatal(err)
	}
	request.Candidates = append(request.Candidates, ontologyreasoning.GroundingCandidate{ID: "exact-document", RequiredSupports: []ontologyreasoning.ExactSupport{prior.ExactSupport}})
	current, err := w.api.GroundedContext(ctx, p, request)
	if err != nil || len(current.Decisions) != 2 || !current.Decisions[0].Eligible || current.Decisions[1].Eligible {
		t.Fatalf("independent support replacement: %+v %v", current, err)
	}
	// Merely reading source metadata must not fetch provider bytes or create a
	// new extraction/review. The provider is deliberately absent here.
}

func jsonString(value any) string { data, _ := json.Marshal(value); return string(data) }

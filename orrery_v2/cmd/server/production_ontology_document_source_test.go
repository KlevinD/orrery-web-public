package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	knowledgestore "orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/platform/db"
)

type documentSourceRepository struct {
	knowledgestore.Repository
	base knowledgecore.KnowledgeBase
	doc  knowledgecore.KnowledgeDocument
}

func (r documentSourceRepository) GetKnowledgeBase(context.Context, knowledgecore.KnowledgeBaseID) (knowledgecore.KnowledgeBase, error) {
	return r.base, nil
}
func (r documentSourceRepository) GetDocument(context.Context, knowledgecore.KnowledgeDocumentID) (knowledgecore.KnowledgeDocument, error) {
	return r.doc, nil
}

type documentRevisionFixture struct {
	provider.Provider
	provider.RevisionProvider
	revision provider.DocumentRevision
	source   provider.SemanticSource
	legacy   bool
	reads    int
}

func (p *documentRevisionFixture) GetDocumentRevision(context.Context, provider.DocumentRevisionRequest) (provider.DocumentRevision, error) {
	p.reads++
	if p.legacy {
		return provider.DocumentRevision{}, errors.New("no editable revision history")
	}
	return p.revision, nil
}
func (p *documentRevisionFixture) GetSemanticSource(context.Context, provider.SemanticSourceRequest) (provider.SemanticSource, error) {
	p.reads++
	return p.source, nil
}
func (p *documentRevisionFixture) GetIndexedDocumentSource(context.Context, provider.IndexedDocumentSourceRequest) (provider.IndexedDocumentSource, error) {
	p.reads++
	return provider.IndexedDocumentSource{Revision: p.revision, Source: p.source}, nil
}

type documentProviderFixture struct {
	provider.Registry
	revision *documentRevisionFixture
}

func (p documentProviderFixture) ForBase(knowledgecore.KnowledgeBase) provider.Provider {
	return p.revision
}

type documentHiddenReader struct{ inferenceContextReader }

func (r documentHiddenReader) GetObject(context.Context, identity.Principal, string, string) (contextview.ObjectDetail, error) {
	return contextview.ObjectDetail{}, ontologyscope.ErrAccessDenied
}

func TestProductionOntologyDocumentLoadsOnlyAttestedPublishedBytes(t *testing.T) {
	for _, scenario := range []string{"published", "indexed_legacy", "wrong_hash", "draft", "wrong_folder", "wrong_revision", "invisible"} {
		t.Run(scenario, func(t *testing.T) {
			w, _, fake, _, watch := documentInferenceFixture(t)
			ctx := context.Background()
			core := w.store.Core
			digest := sha256.Sum256([]byte(fake.source.Content))
			hash := "sha256:" + hex.EncodeToString(digest[:])
			binding := knowledgeontology.Binding{TenantID: watch.TenantID, KnowledgeBaseID: "kb", FolderID: "folder", SourceAccessRef: fake.source.Access, Enabled: true}
			bridge := knowledgeontology.NewPlatformStore(core)
			if err := bridge.SaveBinding(ctx, binding); err != nil {
				t.Fatal(err)
			}
			projection := knowledgeontology.Projection{TenantID: watch.TenantID, KnowledgeBaseID: "kb", DocumentID: "provider-doc", KnowledgeDocumentObjectID: watch.RootID, Revision: 1, ContentHash: hash}
			if err := bridge.SaveProjectionAndCursor(ctx, binding, projection, "1"); err != nil {
				t.Fatal(err)
			}
			provided := &documentRevisionFixture{revision: provider.DocumentRevision{DocumentID: "provider-doc", FolderID: "folder", Revision: 1, ContentHash: hash, PublicationStatus: "published"}, source: provider.SemanticSource{DocumentID: "provider-doc", Revision: 1, ContentHash: hash, ContentType: "text/markdown", Content: fake.source.Content}}
			switch scenario {
			case "indexed_legacy":
				provided.legacy = true
			case "wrong_hash":
				provided.source.Content = "Altered bytes"
			case "draft":
				provided.revision.PublicationStatus = "draft"
			case "wrong_folder":
				provided.revision.FolderID = "other-folder"
			case "wrong_revision":
				provided.source.Revision = 2
			case "invisible":
				w.api.context = documentHiddenReader{inferenceContextReader: w.api.context}
			}
			repo := documentSourceRepository{base: knowledgecore.KnowledgeBase{ID: "kb", Status: knowledgecore.KnowledgeActive, ProviderResourceID: "folder"}, doc: knowledgecore.KnowledgeDocument{ID: knowledgecore.KnowledgeDocumentID(watch.RootID), KnowledgeBaseID: "kb", ProviderDocumentID: "provider-doc", Status: knowledgecore.DocumentIndexed}}
			loader := &productionOntologyDocumentSource{api: w.api, core: core, repo: repo, providers: documentProviderFixture{revision: provided}}
			p, err := w.api.documentInferencePrincipal(ctx, watch.TenantID, watch.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			source, err := loader.Load(ctx, p, watch)
			if scenario != "published" && scenario != "indexed_legacy" {
				if err == nil {
					t.Fatal("untrusted source accepted")
				}
				if scenario == "invisible" && provided.reads != 0 {
					t.Fatal("provider was read before authorization")
				}
				return
			}
			if err != nil || source.ContentHash != hash || source.Content != fake.source.Content {
				t.Fatalf("load: %+v %v", source, err)
			}
			if err := core.WithTx(ctx, func(tx db.Tx) error { return loader.Fence(ctx, tx, source) }); err != nil {
				t.Fatal(err)
			}
			if _, err := core.Exec(ctx, `UPDATE knowledge_ontology_projections SET revision=2 WHERE tenant_id=? AND document_id=?`, watch.TenantID, "provider-doc"); err != nil {
				t.Fatal(err)
			}
			if err := core.WithTx(ctx, func(tx db.Tx) error { return loader.Fence(ctx, tx, source) }); !errors.Is(err, knowledgeinstances.ErrConflict) {
				t.Fatalf("changed head accepted: %v", err)
			}
		})
	}
}

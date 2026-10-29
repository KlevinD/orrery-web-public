package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeadmin"
	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgestore "orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionContextAccessGraphReadOnlyAllowsActivePublicKnowledge(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	coreStore, err := runtime.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	repo := knowledgestore.NewPlatformStore(coreStore)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	bases := []knowledgecore.KnowledgeBase{
		contextAccessTestKnowledgeBase("kb-public", knowledgecore.VisibilityPublic, knowledgecore.KnowledgeActive, now),
		contextAccessTestKnowledgeBase("kb-scoped", knowledgecore.VisibilityScoped, knowledgecore.KnowledgeActive, now),
		contextAccessTestKnowledgeBase("kb-private", knowledgecore.VisibilityPrivate, knowledgecore.KnowledgeActive, now),
		contextAccessTestKnowledgeBase("kb-disabled-public", knowledgecore.VisibilityPublic, knowledgecore.KnowledgeDisabled, now),
	}
	refs := make([]datavisibility.AccessRef, 0, len(bases)+2)
	for _, base := range bases {
		if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
			t.Fatalf("save knowledge base %s: %v", base.ID, err)
		}
		document := knowledgecore.KnowledgeDocument{
			ID:                 knowledgecore.KnowledgeDocumentID("doc-" + string(base.ID)),
			KnowledgeBaseID:    base.ID,
			ProviderDocumentID: "provider-" + string(base.ID),
			Filename:           string(base.ID) + ".md",
			ContentType:        "text/markdown",
			Status:             knowledgecore.DocumentIndexed,
			CreatedAt:          now,
			UpdatedAt:          now,
			IndexedAt:          now,
		}
		if err := repo.SaveDocument(ctx, document); err != nil {
			t.Fatalf("save knowledge document %s: %v", document.ID, err)
		}
		refs = append(refs, datavisibility.AccessRef{Kind: "knowledge_document", ID: string(document.ID), TenantID: "minvia"})
	}
	foreignTenantRef := datavisibility.AccessRef{Kind: "knowledge_document", ID: "doc-kb-public", TenantID: "other-org"}
	missingRef := datavisibility.AccessRef{Kind: "knowledge_document", ID: "doc-missing", TenantID: "minvia"}
	refs = append(refs, foreignTenantRef, missingRef)

	policy := productionContextAccessPolicy{
		knowledge:    repo,
		knowledgeACL: knowledgeadmin.NewAccessPolicy(repo),
	}
	graphPrincipal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "orrery",
		Subject:         "agent-1",
		ViewerKind:      identity.ViewerKindAgent,
		AgentProfileID:  "agent-1",
		TenantID:        "minvia",
		CoarseScopes:    []string{"enterprise_graph.read"},
	})
	decisions, err := policy.authorizeKnowledgeRefs(ctx, graphPrincipal, refs)
	if err != nil {
		t.Fatalf("authorize graph-only knowledge refs: %v", err)
	}
	for _, ref := range refs {
		want := ref.ID == "doc-kb-public" && ref.TenantID == "minvia"
		if decisions[ref] != want {
			t.Fatalf("graph-only decision for %+v = %v, want %v", ref, decisions[ref], want)
		}
	}

	noScopePrincipal := graphPrincipal
	noScopePrincipal.CoarseScopes = nil
	decisions, err = policy.authorizeKnowledgeRefs(ctx, noScopePrincipal, refs)
	if err != nil {
		t.Fatalf("authorize unscoped knowledge refs: %v", err)
	}
	for _, ref := range refs {
		if decisions[ref] {
			t.Fatalf("unscoped decision for %+v unexpectedly allowed", ref)
		}
	}
}

func contextAccessTestKnowledgeBase(id knowledgecore.KnowledgeBaseID, visibility knowledgecore.Visibility, status knowledgecore.KnowledgeStatus, now time.Time) knowledgecore.KnowledgeBase {
	return knowledgecore.KnowledgeBase{
		ID:                 id,
		Name:               string(id),
		OwnerType:          knowledgecore.OwnerOrganization,
		OwnerID:            "minvia",
		Visibility:         visibility,
		Provider:           knowledgecore.ProviderPageIndex,
		ProviderResourceID: "folder-" + string(id),
		ChunkPolicy:        core.ChunkPolicy{MaxRunes: 2400, OverlapRunes: 240, MinRunes: 400},
		Status:             status,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

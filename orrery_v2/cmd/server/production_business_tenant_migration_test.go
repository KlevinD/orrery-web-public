package main

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memcore "orrery_v2/internal/memory/core"
	mempageindex "orrery_v2/internal/memory/pageindex"
	memstore "orrery_v2/internal/memory/store"
	doccore "orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/testkit"
)

type recordingBusinessTenantState struct {
	calls []string
}

func (s *recordingBusinessTenantState) ReassignTenant(_ context.Context, fromID, toID string) (int, error) {
	s.calls = append(s.calls, fromID+"->"+toID)
	return 1, nil
}

func TestReconcileLegacyBusinessTenantStateMigratesEveryRetiredOrganization(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()

	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	memory := memstore.NewPlatformStore(env.DB)
	pageIndex := mempageindex.NewPlatformState(env.DB)
	knowledge := knowledgestore.NewPlatformStore(env.DB)
	canonicalMemoryProfileID := memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: defaultBusinessOrganizationID})
	if err := pageIndex.SaveProfileBinding(ctx, mempageindex.ProfileBinding{ProfileID: canonicalMemoryProfileID, FolderID: "canonical-minvia-folder"}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 22, 1, 0, 0, 0, time.UTC)
	canonicalBase := businessTenantTestKnowledgeBase("kb-minvia", defaultBusinessOrganizationID, now)
	if err := knowledge.SaveKnowledgeBase(ctx, canonicalBase); err != nil {
		t.Fatal(err)
	}
	for index, legacyID := range legacyBusinessOrganizationIDs() {
		profile := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "legacy-agent-" + legacyID}, memcore.OwnerRef{
			AgentID:        agentcore.AgentID("legacy-agent-" + legacyID),
			OrganizationID: legacyID,
		})
		if err := memory.SaveProfile(ctx, profile); err != nil {
			t.Fatalf("save legacy memory profile %s: %v", legacyID, err)
		}
		legacyMemoryProfileID := memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: legacyID})
		if err := pageIndex.SaveProfileBinding(ctx, mempageindex.ProfileBinding{ProfileID: legacyMemoryProfileID, FolderID: "legacy-folder-" + legacyID}); err != nil {
			t.Fatal(err)
		}
		if err := pageIndex.SaveDocumentBinding(ctx, mempageindex.DocumentBinding{
			MemoryID:  "legacy-memory-" + legacyID,
			ProfileID: legacyMemoryProfileID,
			FolderID:  "legacy-folder-" + legacyID,
		}); err != nil {
			t.Fatal(err)
		}
		base := businessTenantTestKnowledgeBase(knowledgecore.KnowledgeBaseID("kb-legacy-"+legacyID), legacyID, now)
		if err := knowledge.SaveKnowledgeBase(ctx, base); err != nil {
			t.Fatal(err)
		}
		if err := knowledge.SaveBinding(ctx, knowledgecore.KnowledgeBinding{
			ID:              knowledgecore.KnowledgeBindingID("binding-legacy-" + legacyID),
			KnowledgeBaseID: canonicalBase.ID,
			BoundToType:     knowledgecore.BindingSubjectOrganization,
			BoundToID:       legacyID,
			Access:          knowledgecore.AccessRead,
			CreatedAt:       now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	memoryOntology := &recordingBusinessTenantState{}
	diary := &recordingBusinessTenantState{}
	graph := &recordingBusinessTenantState{}
	discussions := &recordingBusinessTenantState{}
	if err := reconcileLegacyBusinessTenantState(
		ctx, nil, profiles, memory, pageIndex, memoryOntology, diary, graph, discussions, knowledge, func() time.Time { return now },
	); err != nil {
		t.Fatalf("reconcileLegacyBusinessTenantState: %v", err)
	}

	memoryProfiles, err := memory.ListProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range memoryProfiles {
		if isLegacyBusinessOrganizationID(profile.OwnerRef.OrganizationID) {
			t.Fatalf("legacy memory owner survived: %+v", profile)
		}
	}
	canonicalBinding, err := pageIndex.GetProfileBinding(ctx, canonicalMemoryProfileID)
	if err != nil || canonicalBinding.FolderID != "canonical-minvia-folder" {
		t.Fatalf("canonical PageIndex folder = %+v, %v", canonicalBinding, err)
	}
	for _, legacyID := range legacyBusinessOrganizationIDs() {
		legacyMemoryProfileID := memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: legacyID})
		if _, err := pageIndex.GetProfileBinding(ctx, legacyMemoryProfileID); !errors.Is(err, memcore.ErrNotFound) {
			t.Fatalf("legacy PageIndex profile %s survived: %v", legacyID, err)
		}
		document, err := pageIndex.GetDocumentBinding(ctx, "legacy-memory-"+legacyID)
		if err != nil || document.ProfileID != canonicalMemoryProfileID {
			t.Fatalf("PageIndex document %s = %+v, %v", legacyID, document, err)
		}
		base, err := knowledge.GetKnowledgeBase(ctx, knowledgecore.KnowledgeBaseID("kb-legacy-"+legacyID))
		if err != nil || base.OwnerID != defaultBusinessOrganizationID {
			t.Fatalf("knowledge base %s = %+v, %v", legacyID, base, err)
		}
		binding, err := knowledge.GetBinding(ctx, knowledgecore.KnowledgeBindingID("binding-legacy-"+legacyID))
		if err != nil || binding.BoundToID != defaultBusinessOrganizationID {
			t.Fatalf("knowledge binding %s = %+v, %v", legacyID, binding, err)
		}
	}
	for name, state := range map[string]*recordingBusinessTenantState{
		"memory ontology": memoryOntology,
		"diary":           diary,
		"graph":           graph,
		"discussions":     discussions,
	} {
		if len(state.calls) != len(legacyBusinessOrganizationIDs()) {
			t.Fatalf("%s migration calls = %+v", name, state.calls)
		}
	}
}

func businessTenantTestKnowledgeBase(id knowledgecore.KnowledgeBaseID, ownerID string, now time.Time) knowledgecore.KnowledgeBase {
	return knowledgecore.KnowledgeBase{
		ID:            id,
		Name:          string(id),
		OwnerType:     knowledgecore.OwnerOrganization,
		OwnerID:       ownerID,
		Visibility:    knowledgecore.VisibilityScoped,
		VectorBackend: knowledgecore.VectorBackendLanceDB,
		ChunkPolicy:   doccore.ChunkPolicy{MaxRunes: 2400, OverlapRunes: 240, MinRunes: 400},
		Status:        knowledgecore.KnowledgeActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

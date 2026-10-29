package pageindex

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	memcore "orrery_v2/internal/memory/core"
	memtestkit "orrery_v2/internal/memory/testkit"
)

func TestServiceIndexesNaturalLanguageInProfileScopedFolders(t *testing.T) {
	ctx := context.Background()
	memory := memtestkit.NewFakeStore()
	personal := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-a"}, memcore.OwnerRef{AgentID: "agent-a"})
	organization := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-a"}, memcore.OwnerRef{OrganizationID: "org-a"})
	personalRecord := seedMemoryRecord(t, memory, personal, "memory-personal", "The user prefers to see the conclusion first.")
	organizationRecord := seedMemoryRecord(t, memory, organization, "memory-organization", "The team publishes a progress summary every Friday.")
	pageIndex := newFakeProvider()
	service := NewService(ServiceOptions{
		Memory: memory,
		State:  NewMemoryState(),
		Provider: func() Provider {
			return pageIndex
		},
		Now: func() time.Time { return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) },
	})

	for _, record := range []memcore.MemoryRecord{personalRecord, organizationRecord} {
		changed, deleted, err := service.SyncRecord(ctx, record)
		if err != nil {
			t.Fatalf("SyncRecord(%s): %v", record.ID, err)
		}
		if !changed || deleted {
			t.Fatalf("SyncRecord(%s) = changed %v deleted %v", record.ID, changed, deleted)
		}
	}

	if len(pageIndex.folders) != 2 || pageIndex.folders[string(personal.ID)] == pageIndex.folders[string(organization.ID)] {
		t.Fatalf("profile folders = %+v, want separate folders", pageIndex.folders)
	}
	if len(pageIndex.uploads) != 2 {
		t.Fatalf("uploads = %d, want 2", len(pageIndex.uploads))
	}
	for _, upload := range pageIndex.uploads {
		if strings.Contains(upload.content, upload.memoryID) || strings.Contains(upload.content, "candidate-") {
			t.Fatalf("indexed document exposed technical identity: %q", upload.content)
		}
		if !strings.Contains(upload.content, "conclusion first") && !strings.Contains(upload.content, "progress summary") {
			t.Fatalf("indexed document does not contain natural-language memory: %q", upload.content)
		}
	}
}

func TestServiceRecallRechecksProfileAuthorizationAndLifecycle(t *testing.T) {
	ctx := context.Background()
	memory := memtestkit.NewFakeStore()
	owned := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-a"}, memcore.OwnerRef{AgentID: "agent-a"})
	other := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-b"}, memcore.OwnerRef{AgentID: "agent-b"})
	ownedRecord := seedMemoryRecord(t, memory, owned, "memory-owned", "The user prefers concise plans.")
	otherRecord := seedMemoryRecord(t, memory, other, "memory-other", "Another user's private information.")
	state := NewMemoryState()
	pageIndex := newFakeProvider()
	service := NewService(ServiceOptions{Memory: memory, State: state, Provider: func() Provider { return pageIndex }})
	for _, record := range []memcore.MemoryRecord{ownedRecord, otherRecord} {
		if _, _, err := service.SyncRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	pageIndex.searchResults = []provider.SearchResult{
		{DocumentID: otherRecord.ID, Score: 0.99},
		{DocumentID: ownedRecord.ID, Score: 0.80},
	}

	recalled, err := service.Recall(ctx, RecallRequest{
		Query:       "planning preference",
		Subject:     memcore.AccessSubject{AgentID: "agent-a"},
		ProfileRefs: []agentcore.MemoryProfileRef{memcore.MemoryProfileRefForScope(owned), memcore.MemoryProfileRefForScope(other)},
		Limit:       10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled) != 1 || recalled[0].ID != ownedRecord.ID {
		t.Fatalf("recalled = %+v, want only owner-authorized record", recalled)
	}
	if pageIndex.lastSearch.RetrievalMode != provider.RetrievalModeAuto {
		t.Fatalf("retrieval mode = %q, want auto", pageIndex.lastSearch.RetrievalMode)
	}

	if err := memory.UpdateRecordLifecycle(ctx, ownedRecord.ID, memcore.MemoryLifecycleArchived); err != nil {
		t.Fatal(err)
	}
	archived, err := memory.GetRecord(ctx, ownedRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, deleted, err := service.SyncRecord(ctx, archived); err != nil || !deleted {
		t.Fatalf("archive sync = deleted %v err %v", deleted, err)
	}
	recalled, err = service.Recall(ctx, RecallRequest{
		Query:       "planning preference",
		Subject:     memcore.AccessSubject{AgentID: "agent-a"},
		ProfileRefs: []agentcore.MemoryProfileRef{memcore.MemoryProfileRefForScope(owned)},
		Limit:       10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled) != 0 {
		t.Fatalf("archived recall = %+v, want none", recalled)
	}
}

func TestServiceTracksAsynchronousProviderFailureInsteadOfClaimingIndexed(t *testing.T) {
	ctx := context.Background()
	memory := memtestkit.NewFakeStore()
	profile := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-a"}, memcore.OwnerRef{OrganizationID: "org-a"})
	record := seedMemoryRecord(t, memory, profile, "memory-async", "A reusable delivery practice was confirmed.")
	state := NewMemoryState()
	pageIndex := newFakeProvider()
	pageIndex.uploadStatus = knowledgecore.DocumentParsing
	pageIndex.refreshStatus = knowledgecore.DocumentFailed
	pageIndex.refreshError = "provider indexing failed"
	now := time.Date(2026, 8, 19, 4, 0, 0, 0, time.UTC)
	service := NewService(ServiceOptions{
		Memory: memory,
		State:  state,
		Provider: func() Provider {
			return pageIndex
		},
		Now: func() time.Time { return now },
	})

	if _, _, err := service.SyncRecord(ctx, record); err != nil {
		t.Fatalf("initial SyncRecord: %v", err)
	}
	binding, err := state.GetDocumentBinding(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Status != string(knowledgecore.DocumentParsing) || !binding.VerifiedAt.IsZero() {
		t.Fatalf("initial binding = %+v, want unverified parsing", binding)
	}

	if _, _, err := service.SyncRecord(ctx, record); err == nil || !strings.Contains(err.Error(), "provider indexing failed") {
		t.Fatalf("refresh SyncRecord err = %v, want provider failure", err)
	}
	binding, err = state.GetDocumentBinding(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Status != DocumentStatusFailed || binding.VerifiedAt.IsZero() || pageIndex.refreshCalls != 1 {
		t.Fatalf("refreshed binding = %+v calls=%d", binding, pageIndex.refreshCalls)
	}

	pageIndex.searchResults = []provider.SearchResult{{DocumentID: record.ID, Score: 1}}
	recalled, err := service.Recall(ctx, RecallRequest{
		Query:       "delivery practice",
		Subject:     memcore.AccessSubject{OrganizationIDs: []string{"org-a"}},
		ProfileRefs: []agentcore.MemoryProfileRef{memcore.MemoryProfileRefForScope(profile)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled) != 0 {
		t.Fatalf("failed provider document was recalled: %+v", recalled)
	}
}

func TestServiceReconcileDeletesOrphanProviderDocumentsAndBindings(t *testing.T) {
	ctx := context.Background()
	memory := memtestkit.NewFakeStore()
	profile := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "A000009"}, memcore.OwnerRef{AgentID: "A000009"})
	if err := memory.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	state := NewMemoryState()
	binding := DocumentBinding{
		MemoryID:           "memory-orphan",
		ProfileID:          profile.ID,
		FolderID:           "folder-A000009",
		ProviderDocumentID: "pageindex-orphan",
		Status:             DocumentStatusIndexed,
	}
	if err := state.SaveDocumentBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	pageIndex := newFakeProvider()
	service := NewService(ServiceOptions{Memory: memory, State: state, Provider: func() Provider { return pageIndex }})

	report, err := service.Reconcile(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.OrphansScanned != 1 || report.Deleted != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(pageIndex.deletes) != 1 || pageIndex.deletes[0] != binding.ProviderDocumentID {
		t.Fatalf("provider deletes = %+v", pageIndex.deletes)
	}
	if _, err := state.GetDocumentBinding(ctx, binding.MemoryID); !errors.Is(err, memcore.ErrNotFound) {
		t.Fatalf("orphan binding error = %v", err)
	}
}

type fakeProvider struct {
	folders       map[string]string
	uploads       []fakeUpload
	deletes       []string
	searchResults []provider.SearchResult
	lastSearch    provider.SearchRequest
	uploadStatus  knowledgecore.DocumentStatus
	refreshStatus knowledgecore.DocumentStatus
	refreshError  string
	refreshCalls  int
}

type fakeUpload struct {
	memoryID string
	folderID string
	content  string
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{folders: map[string]string{}}
}

func (p *fakeProvider) EnsureKnowledgeBase(_ context.Context, request provider.EnsureKnowledgeBaseRequest) (provider.EnsureKnowledgeBaseResult, error) {
	profileID := strings.TrimPrefix(request.Base.Name, "Memory ")
	folderID := "folder-" + shortHash(profileID)
	p.folders[profileID] = folderID
	return provider.EnsureKnowledgeBaseResult{ProviderResourceID: folderID}, nil
}

func (p *fakeProvider) UploadDocument(_ context.Context, request provider.UploadDocumentRequest) (provider.UploadDocumentResult, error) {
	content, err := io.ReadAll(request.Reader)
	if err != nil {
		return provider.UploadDocumentResult{}, err
	}
	memoryID := string(request.DocumentID)
	p.uploads = append(p.uploads, fakeUpload{memoryID: memoryID, folderID: request.Base.ProviderResourceID, content: string(content)})
	status := p.uploadStatus
	if status == "" {
		status = knowledgecore.DocumentIndexed
	}
	return provider.UploadDocumentResult{ProviderDocumentID: "pageindex-" + memoryID, Status: status}, nil
}

func (p *fakeProvider) DeleteDocument(_ context.Context, request provider.DeleteDocumentRequest) error {
	p.deletes = append(p.deletes, request.Document.ProviderDocumentID)
	return nil
}

func (p *fakeProvider) RefreshDocumentStatus(_ context.Context, request provider.DocumentStatusRequest) (provider.DocumentStatusResult, error) {
	p.refreshCalls++
	status := p.refreshStatus
	if status == "" {
		status = knowledgecore.DocumentIndexed
	}
	return provider.DocumentStatusResult{
		ProviderDocumentID: request.Document.ProviderDocumentID,
		Status:             status,
		ErrorMessage:       p.refreshError,
	}, nil
}

func (p *fakeProvider) Search(_ context.Context, request provider.SearchRequest) (provider.SearchResultPage, error) {
	p.lastSearch = request
	return provider.SearchResultPage{Results: append([]provider.SearchResult(nil), p.searchResults...)}, nil
}

func seedMemoryRecord(t *testing.T, memory *memtestkit.FakeStore, profile memcore.MemoryProfile, id, summary string) memcore.MemoryRecord {
	t.Helper()
	ctx := context.Background()
	candidateID := "candidate-" + id
	if err := memory.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if err := memory.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            candidateID,
		ProposedScope: profile.DefaultScope,
		ProposedKind:  profile.MemoryKind,
		Status:        memcore.CandidateAccepted,
	}); err != nil {
		t.Fatal(err)
	}
	record := memcore.MemoryRecord{
		ID:              id,
		ProfileID:       profile.ID,
		CandidateID:     candidateID,
		Scope:           profile.DefaultScope,
		Layer:           memcore.MemoryLayerFact,
		Topic:           "personal_assistant_exchange",
		Summary:         summary,
		Narrative:       summary + " Future answers should preserve this style.",
		Facts:           []string{summary},
		LifecycleStatus: memcore.MemoryLifecycleActive,
		CreatedBy:       agentcore.AgentID(profile.DefaultScope.ID),
		CreatedAt:       time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC),
	}
	if err := memory.SaveRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	return record
}

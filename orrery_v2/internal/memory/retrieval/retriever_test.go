package retrieval

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memtestkit "orrery_v2/internal/memory/testkit"
)

func TestRetrieverFiltersByVisibilityAndBuildsDigest(t *testing.T) {
	ctx := context.Background()
	repository := memtestkit.NewFakeStore()
	personalProfile := memcore.MemoryProfile{
		ID:               agentcore.MemoryProfileID("profile-agent-a"),
		OwnerRef:         memcore.OwnerRef{AgentID: agentcore.AgentID("agent-a")},
		MemoryKind:       agentcore.MemoryKindPersonal,
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}
	otherProfile := memcore.MemoryProfile{
		ID:               agentcore.MemoryProfileID("profile-agent-b"),
		OwnerRef:         memcore.OwnerRef{AgentID: agentcore.AgentID("agent-b")},
		MemoryKind:       agentcore.MemoryKindPersonal,
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}
	if err := repository.SaveProfile(ctx, personalProfile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	if err := repository.SaveProfile(ctx, otherProfile); err != nil {
		t.Fatalf("save other profile: %v", err)
	}
	if err := repository.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-visible", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatalf("save visible candidate: %v", err)
	}
	if err := repository.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-hidden", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatalf("save hidden candidate: %v", err)
	}
	if err := repository.SaveRecord(ctx, memcore.MemoryRecord{
		ID:          "mem-visible",
		ProfileID:   personalProfile.ID,
		CandidateID: "candidate-visible",
		Summary:     "Visible memory",
		Facts:       []string{"Visible fact"},
		Confidence:  0.8,
		Visibility:  memcore.Visibility{Mode: memcore.VisibilityOwnerOnly},
	}); err != nil {
		t.Fatalf("save visible: %v", err)
	}
	if err := repository.SaveRecord(ctx, memcore.MemoryRecord{
		ID:          "mem-hidden",
		ProfileID:   otherProfile.ID,
		CandidateID: "candidate-hidden",
		Summary:     "Hidden memory",
		Confidence:  0.8,
		Visibility:  memcore.Visibility{Mode: memcore.VisibilityOwnerOnly},
	}); err != nil {
		t.Fatalf("save hidden: %v", err)
	}
	retriever := NewRetriever(repository, NewCompressor(CompressionOptions{}))

	digest, err := retriever.Retrieve(ctx, RetrieveRequest{
		AgentID:     agentcore.AgentID("agent-a"),
		Subject:     memcore.AccessSubject{AgentID: agentcore.AgentID("agent-a")},
		ProfileRefs: []agentcore.MemoryProfileRef{{MemoryProfileID: personalProfile.ID, MemoryKind: agentcore.MemoryKindPersonal}},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(digest.SourceMemoryRefs) != 1 || digest.SourceMemoryRefs[0] != "mem-visible" {
		t.Fatalf("digest source refs = %+v", digest.SourceMemoryRefs)
	}
}

func TestRetrieverReturnsEmptyDigestWhenNoRequestedProfileIsReadable(t *testing.T) {
	ctx := context.Background()
	repository := memtestkit.NewFakeStore()
	privateProfile := memcore.MemoryProfile{
		ID:               "profile-agent-b",
		OwnerRef:         memcore.OwnerRef{AgentID: "agent-b"},
		MemoryKind:       agentcore.MemoryKindPersonal,
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}
	if err := repository.SaveProfile(ctx, privateProfile); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-private", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveRecord(ctx, memcore.MemoryRecord{
		ID: "memory-private", ProfileID: privateProfile.ID, CandidateID: "candidate-private",
		Summary: "Private information owned by another agent.",
	}); err != nil {
		t.Fatal(err)
	}

	digest, err := NewRetriever(repository, NewCompressor(CompressionOptions{})).Retrieve(ctx, RetrieveRequest{
		AgentID:     "agent-a",
		Subject:     memcore.AccessSubject{AgentID: "agent-a"},
		ProfileRefs: []agentcore.MemoryProfileRef{{MemoryProfileID: privateProfile.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(digest.SourceMemoryRefs) != 0 || strings.TrimSpace(digest.Summary) != "" {
		t.Fatalf("unauthorized digest = %+v, want empty", digest)
	}
}

func TestRetrieverRanksOrganizationWorldMemoryIntoCollectiveDigest(t *testing.T) {
	ctx := context.Background()
	repository := memtestkit.NewFakeStore()
	profile := memcore.MemoryProfile{
		ID:         agentcore.MemoryProfileID("profile-org"),
		MemoryKind: agentcore.MemoryKindOrganization,
		DefaultScope: memcore.MemoryScope{
			Kind: memcore.ScopeOrganization,
			ID:   "org-1",
		},
		RetrievalPolicy: memcore.RetrievalPolicy{
			MaxRecords: 2,
			Tags:       []string{"quality"},
		},
		VisibilityPolicy: memcore.VisibilityPolicy{
			Mode:      memcore.VisibilityScopedMembers,
			ScopeKind: memcore.ScopeOrganization,
			ScopeID:   "org-1",
		},
		Enabled: true,
	}
	if err := repository.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	for _, candidateID := range []string{"candidate-world-stale", "candidate-world-current", "candidate-experience", "candidate-fact"} {
		if err := repository.SaveCandidate(ctx, memcore.MemoryCandidate{ID: candidateID, Status: memcore.CandidateAccepted}); err != nil {
			t.Fatalf("save candidate %s: %v", candidateID, err)
		}
	}
	records := []memcore.MemoryRecord{
		{
			ID:              "mem-fact-noise",
			ProfileID:       profile.ID,
			CandidateID:     "candidate-fact",
			Scope:           profile.DefaultScope,
			Layer:           memcore.MemoryLayerFact,
			Topic:           "quality",
			Summary:         "Raw QA fact should not outrank accepted world memory.",
			Tags:            []string{"quality"},
			Confidence:      0.99,
			LifecycleStatus: memcore.MemoryLifecycleActive,
			CreatedAt:       now.Add(2 * time.Hour),
			UpdatedAt:       now.Add(2 * time.Hour),
		},
		{
			ID:              "mem-world-stale",
			ProfileID:       profile.ID,
			CandidateID:     "candidate-world-stale",
			Scope:           profile.DefaultScope,
			Layer:           memcore.MemoryLayerWorld,
			Topic:           "quality",
			Summary:         "Older world guidance that should remain below current guidance.",
			Tags:            []string{"quality"},
			LineageRefs:     []string{"mem-experience"},
			Confidence:      0.7,
			LifecycleStatus: memcore.MemoryLifecycleSettled,
			CreatedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:       now.Add(-48 * time.Hour),
		},
		{
			ID:               "mem-world-current",
			ProfileID:        profile.ID,
			CandidateID:      "candidate-world-current",
			Scope:            profile.DefaultScope,
			Layer:            memcore.MemoryLayerWorld,
			Topic:            "quality",
			Summary:          "Current org belief: API changes require OpenAPI contract tests.",
			Tags:             []string{"quality"},
			LineageRefs:      []string{"mem-experience"},
			Confidence:       0.91,
			LifecycleStatus:  memcore.MemoryLifecycleActive,
			ReferenceCount:   4,
			LastReferencedAt: now.Add(-time.Hour),
			CreatedAt:        now.Add(-24 * time.Hour),
			UpdatedAt:        now.Add(-time.Hour),
		},
		{
			ID:              "mem-experience",
			ProfileID:       profile.ID,
			CandidateID:     "candidate-experience",
			Scope:           profile.DefaultScope,
			Layer:           memcore.MemoryLayerExperience,
			Topic:           "quality",
			Summary:         "Experience lesson should be available after world memory.",
			Tags:            []string{"quality"},
			LineageRefs:     []string{"mem-fact-noise"},
			Confidence:      0.85,
			LifecycleStatus: memcore.MemoryLifecycleActive,
			CreatedAt:       now.Add(-2 * time.Hour),
			UpdatedAt:       now.Add(-2 * time.Hour),
		},
	}
	for _, record := range records {
		if err := repository.SaveRecord(ctx, record); err != nil {
			t.Fatalf("save record %s: %v", record.ID, err)
		}
	}
	retriever := NewRetriever(repository, NewCompressor(CompressionOptions{}))

	digest, err := retriever.Retrieve(ctx, RetrieveRequest{
		AgentID: agentcore.AgentID("agent-a"),
		Subject: memcore.AccessSubject{
			AgentID:         agentcore.AgentID("agent-a"),
			OrganizationIDs: []string{"org-1"},
		},
		ProfileRefs: []agentcore.MemoryProfileRef{{MemoryProfileID: profile.ID, MemoryKind: agentcore.MemoryKindOrganization}},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}

	if got, want := strings.Join(digest.SourceMemoryRefs, ","), "mem-world-current,mem-experience"; got != want {
		t.Fatalf("source memory refs = %s, want %s", got, want)
	}
	if len(digest.Layers) != 2 || digest.Layers[0].Layer != memcore.MemoryLayerExperience || digest.Layers[1].Layer != memcore.MemoryLayerWorld {
		t.Fatalf("layers = %+v, want experience then world", digest.Layers)
	}
	if !strings.Contains(digest.Summary, "Current org belief") || strings.Contains(digest.Summary, "Raw QA fact") {
		t.Fatalf("summary = %q", digest.Summary)
	}
}

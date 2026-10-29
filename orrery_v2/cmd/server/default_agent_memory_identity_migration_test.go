package main

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	mempageindex "orrery_v2/internal/memory/pageindex"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/testkit"
)

func TestMigrateCanonicalAgentMemoryProfilesPreservesRecordsAndPageIndexFolder(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	memory := memstore.NewPlatformStore(env.DB)
	pageIndex := mempageindex.NewPlatformState(env.DB)
	template, err := readDefaultAgentTemplate("bug-fixer.json")
	if err != nil {
		t.Fatal(err)
	}
	profile := defaultFreelanceAgentProfile(agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}, template)
	profile.ID = "A000001"
	profile.PersonalMemoryProfileRef = agentcore.MemoryProfileRef{
		MemoryProfileID: "memory-legacy", OwnerAgentID: "A000001", MemoryKind: agentcore.MemoryKindPersonal,
	}
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if err := memory.SaveProfile(ctx, memcore.MemoryProfile{ID: "memory-legacy", OwnerRef: memcore.OwnerRef{AgentID: "A000001"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := memory.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-1", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatal(err)
	}
	if err := memory.SaveRecord(ctx, memcore.MemoryRecord{ID: "memory-1", ProfileID: "memory-legacy", CandidateID: "candidate-1"}); err != nil {
		t.Fatal(err)
	}
	if err := pageIndex.SaveProfileBinding(ctx, mempageindex.ProfileBinding{ProfileID: "memory-legacy", FolderID: "folder-legacy"}); err != nil {
		t.Fatal(err)
	}
	if err := pageIndex.SaveDocumentBinding(ctx, mempageindex.DocumentBinding{MemoryID: "memory-1", ProfileID: "memory-legacy", FolderID: "folder-legacy"}); err != nil {
		t.Fatal(err)
	}

	report, err := migrateCanonicalAgentMemoryProfiles(ctx, profiles, memory, pageIndex)
	if err != nil {
		t.Fatal(err)
	}
	if report.ProfilesMoved != 1 || report.RecordsMoved != 1 || report.PageIndexBindingsMoved != 1 {
		t.Fatalf("report = %+v", report)
	}
	stored, err := profiles.GetProfile(ctx, profile.ID)
	if err != nil || stored.PersonalMemoryProfileRef.MemoryProfileID != "memory-A000001" {
		t.Fatalf("stored profile = %+v err=%v", stored, err)
	}
	record, err := memory.GetRecord(ctx, "memory-1")
	if err != nil || record.ProfileID != "memory-A000001" {
		t.Fatalf("record = %+v err=%v", record, err)
	}
	binding, err := pageIndex.GetProfileBinding(ctx, "memory-A000001")
	if err != nil || binding.FolderID != "folder-legacy" {
		t.Fatalf("binding = %+v err=%v", binding, err)
	}
	if _, err := memory.GetProfile(ctx, "memory-legacy"); !errors.Is(err, memcore.ErrNotFound) {
		t.Fatalf("legacy memory profile error = %v", err)
	}
}

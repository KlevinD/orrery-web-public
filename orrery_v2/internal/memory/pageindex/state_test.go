package pageindex

import (
	"context"
	"errors"
	"testing"
	"time"

	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform/testkit"
)

func TestPlatformStatePersistsAndResolvesPageIndexBindings(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	state := NewPlatformState(env.DB)
	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	profile := ProfileBinding{ProfileID: "memory-agent-a", FolderID: "folder-a", UpdatedAt: at}
	document := DocumentBinding{
		MemoryID:           "memory-a",
		ProfileID:          profile.ProfileID,
		FolderID:           profile.FolderID,
		ProviderDocumentID: "pageindex-document-a",
		ContentHash:        "hash-a",
		Status:             DocumentStatusIndexed,
		UpdatedAt:          at,
	}
	if err := state.SaveProfileBinding(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveDocumentBinding(ctx, document); err != nil {
		t.Fatal(err)
	}

	gotProfile, err := state.GetProfileBinding(ctx, profile.ProfileID)
	if err != nil || gotProfile != profile {
		t.Fatalf("profile binding = %+v, err = %v", gotProfile, err)
	}
	gotDocument, err := state.GetDocumentBinding(ctx, document.MemoryID)
	if err != nil || gotDocument != document {
		t.Fatalf("document binding = %+v, err = %v", gotDocument, err)
	}
	resolved, err := state.FindDocumentBindingByProviderID(ctx, document.ProviderDocumentID)
	if err != nil || resolved != document {
		t.Fatalf("resolved binding = %+v, err = %v", resolved, err)
	}
	bindings, err := state.ListDocumentBindings(ctx, 10)
	if err != nil || len(bindings) != 1 || bindings[0] != document {
		t.Fatalf("listed bindings = %+v, err = %v", bindings, err)
	}
	if err := state.DeleteDocumentBinding(ctx, document.MemoryID); err != nil {
		t.Fatal(err)
	}
	if _, err := state.GetDocumentBinding(ctx, document.MemoryID); !errors.Is(err, memcore.ErrNotFound) {
		t.Fatalf("deleted document binding error = %v", err)
	}
}

func TestPlatformStateReassignsOrganizationMemoryProfile(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	state := NewPlatformState(env.DB)
	if err := state.SaveProfileBinding(ctx, ProfileBinding{ProfileID: "memory-organization-legacy", FolderID: "folder-1"}); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveDocumentBinding(ctx, DocumentBinding{MemoryID: "memory-1", ProfileID: "memory-organization-legacy", FolderID: "folder-1"}); err != nil {
		t.Fatal(err)
	}
	if err := state.ReassignProfile(ctx, "memory-organization-legacy", "memory-organization-minvia"); err != nil {
		t.Fatalf("ReassignProfile: %v", err)
	}
	profile, err := state.GetProfileBinding(ctx, "memory-organization-minvia")
	if err != nil || profile.FolderID != "folder-1" {
		t.Fatalf("profile = %+v, %v", profile, err)
	}
	document, err := state.GetDocumentBinding(ctx, "memory-1")
	if err != nil || document.ProfileID != "memory-organization-minvia" {
		t.Fatalf("document = %+v, %v", document, err)
	}
}

func TestPlatformStateReassignProfilePreservesCanonicalFolder(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	state := NewPlatformState(env.DB)
	if err := state.SaveProfileBinding(ctx, ProfileBinding{ProfileID: "memory-organization-legacy", FolderID: "legacy-folder"}); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveProfileBinding(ctx, ProfileBinding{ProfileID: "memory-organization-minvia", FolderID: "canonical-folder"}); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveDocumentBinding(ctx, DocumentBinding{MemoryID: "memory-1", ProfileID: "memory-organization-legacy", FolderID: "legacy-folder"}); err != nil {
		t.Fatal(err)
	}
	if err := state.ReassignProfile(ctx, "memory-organization-legacy", "memory-organization-minvia"); err != nil {
		t.Fatalf("ReassignProfile: %v", err)
	}
	profile, err := state.GetProfileBinding(ctx, "memory-organization-minvia")
	if err != nil || profile.FolderID != "canonical-folder" {
		t.Fatalf("canonical profile = %+v, %v", profile, err)
	}
	document, err := state.GetDocumentBinding(ctx, "memory-1")
	if err != nil || document.ProfileID != "memory-organization-minvia" {
		t.Fatalf("document = %+v, %v", document, err)
	}
}

func TestPlatformStateConsolidateProfileRecreatesDocumentsInCanonicalFolder(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	state := NewPlatformState(env.DB)
	if err := state.SaveProfileBinding(ctx, ProfileBinding{ProfileID: "memory-legacy", FolderID: "legacy-folder"}); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveProfileBinding(ctx, ProfileBinding{ProfileID: "memory-A000001", FolderID: "canonical-folder"}); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveDocumentBinding(ctx, DocumentBinding{MemoryID: "memory-1", ProfileID: "memory-legacy", FolderID: "legacy-folder"}); err != nil {
		t.Fatal(err)
	}

	report, err := state.ConsolidateProfile(ctx, "memory-legacy", "memory-A000001")
	if err != nil {
		t.Fatal(err)
	}
	if report.SourceFolderID != "legacy-folder" || report.TargetFolderID != "canonical-folder" || report.DocumentBindingsRemoved != 1 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := state.GetProfileBinding(ctx, "memory-legacy"); !errors.Is(err, memcore.ErrNotFound) {
		t.Fatalf("legacy profile binding error = %v", err)
	}
	if _, err := state.GetDocumentBinding(ctx, "memory-1"); !errors.Is(err, memcore.ErrNotFound) {
		t.Fatalf("legacy document binding error = %v", err)
	}
}

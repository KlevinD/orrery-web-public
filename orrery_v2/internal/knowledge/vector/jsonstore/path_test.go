package jsonstore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/vector"
	"orrery_v2/internal/knowledge/vector/jsonstore"
)

func TestDescriptorForKnowledgeBaseResolvesUnderRoot(t *testing.T) {
	root := t.TempDir()
	descriptor, err := jsonstore.DescriptorForKnowledgeBase(root, core.KnowledgeBaseID("kb-1"), 1536)
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	want := filepath.Join(root, "kb_kb-1", "lancedb")
	if descriptor.StorePath != want {
		t.Fatalf("store path = %q, want %q", descriptor.StorePath, want)
	}
	if descriptor.TableName != "chunks" || descriptor.Dimensions != 1536 {
		t.Fatalf("descriptor mismatch: %#v", descriptor)
	}
}

func TestDescriptorRejectsTraversal(t *testing.T) {
	_, err := jsonstore.DescriptorForKnowledgeBase(t.TempDir(), core.KnowledgeBaseID("../outside"), 1536)
	if !errors.Is(err, vector.ErrUnsafePath) {
		t.Fatalf("expected unsafe path, got %v", err)
	}
}

func TestStoreEnsureAndDropKnowledgeBaseDirectory(t *testing.T) {
	root := t.TempDir()
	store := jsonstore.NewStore(root)
	descriptor, err := jsonstore.DescriptorForKnowledgeBase(root, core.KnowledgeBaseID("kb-1"), 1536)
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}

	if err := store.EnsureKnowledgeBase(context.Background(), descriptor); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := os.Stat(descriptor.StorePath); err != nil {
		t.Fatalf("expected store path created: %v", err)
	}
	if err := os.WriteFile(filepath.Join(descriptor.StorePath, "sentinel"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if err := store.DropKnowledgeBase(context.Background(), descriptor); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "kb_kb-1")); !os.IsNotExist(err) {
		t.Fatalf("expected per-base directory removed, got %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("root should remain: %v", err)
	}
}

func TestStoreRejectsDropOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	store := jsonstore.NewStore(root)
	err := store.DropKnowledgeBase(context.Background(), vector.Descriptor{
		KnowledgeBaseID: "kb-1",
		RootPath:        root,
		StorePath:       filepath.Join(outside, "lancedb"),
		TableName:       "chunks",
		Dimensions:      1536,
	})
	if !errors.Is(err, vector.ErrUnsafePath) {
		t.Fatalf("expected unsafe path, got %v", err)
	}
}

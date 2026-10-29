package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
)

func TestProductionSchedulerArtifactStoreRejectsSymlinkAndOversizedArtifact(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspaces", "A")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(workspace, "target.md")
	link := filepath.Join(workspace, "report.md")
	writeSchedulerArtifactTestFile(t, target, "report")
	if err := os.Symlink(target, link); err == nil {
		if _, _, err := openScheduledArtifactInRoot(workspace, link); !errors.Is(err, schedulerapp.ErrNotFound) {
			t.Fatalf("symlink error = %v, want ErrNotFound", err)
		}
	}
	large := filepath.Join(workspace, "large.bin")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxScheduledArtifactBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	if _, _, err := openScheduledArtifactInRoot(workspace, large); !errors.Is(err, schedulerapp.ErrNotFound) {
		t.Fatalf("large artifact error = %v, want ErrNotFound", err)
	}
}

func TestOpenScheduledArtifactRejectsSymlinkWorkspaceRoot(t *testing.T) {
	outside := t.TempDir()
	writeSchedulerArtifactTestFile(t, filepath.Join(outside, "private.md"), "private")
	rootParent := t.TempDir()
	rootLink := filepath.Join(rootParent, "workspace")
	if err := os.Symlink(outside, rootLink); err != nil {
		t.Skipf("create directory symlink: %v", err)
	}
	_, matched, err := openScheduledArtifactInRoot(rootLink, filepath.Join(rootLink, "private.md"))
	if !matched || !errors.Is(err, schedulerapp.ErrNotFound) {
		t.Fatalf("symlink root matched=%v error=%v, want matched ErrNotFound", matched, err)
	}
}

func TestOpenScheduledArtifactDetectsInPlaceRewrite(t *testing.T) {
	workspace := t.TempDir()
	artifactPath := filepath.Join(workspace, "report.md")
	writeSchedulerArtifactTestFile(t, artifactPath, "original")
	content, matched, err := openScheduledArtifactInRoot(workspace, artifactPath)
	if err != nil || !matched {
		t.Fatalf("open artifact matched=%v error=%v", matched, err)
	}
	defer content.Reader.Close()
	reader, ok := content.Reader.(*schedulerArtifactFileReadCloser)
	if !ok {
		t.Fatalf("reader type = %T", content.Reader)
	}
	if err := os.WriteFile(artifactPath, []byte("changed!"), 0o644); err != nil {
		t.Fatal(err)
	}
	changedAt := reader.snapshot.ModTime().Add(time.Second)
	if err := os.Chtimes(artifactPath, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(content.Reader); err != nil {
		t.Fatal(err)
	}
	if err := reader.VerifyUnchanged(); err == nil {
		t.Fatal("in-place rewrite was not detected")
	}
}

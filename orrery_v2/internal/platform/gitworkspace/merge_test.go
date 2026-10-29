package gitworkspace

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthoritativeBarePushPreviewAndCASMerge(t *testing.T) {
	authoritative, seed, base := initAuthoritativeRepository(t)
	manager := newTestManager(t, Config{})
	request := testProvisionRequest(authoritative, SourceManagedBare, base, "todo-merge")
	workspace, err := manager.Provision(context.Background(), request)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	writeTestFile(t, filepath.Join(workspace.WorktreePath, "src", "feature.txt"), "feature\n")
	commit, err := manager.CreateCommit(context.Background(), CommitRequest{
		ProvisionRequest: request, PlanID: "plan-merge", PlanDigest: "digest", ExecutionRunID: "run-merge", AgentID: "agent-merge",
		AllowedFileScopes: []string{"src"}, CheckCommands: []string{"git diff --check"},
	})
	if err != nil {
		t.Fatalf("CreateCommit: %v", err)
	}
	if err := manager.PushTodoBranch(context.Background(), request, commit.CommitSHA); err != nil {
		t.Fatalf("PushTodoBranch: %v", err)
	}
	remoteTodoHead := runTestGit(t, seed, "--git-dir", authoritative, "rev-parse", "refs/heads/"+workspace.BranchName)
	if remoteTodoHead != commit.CommitSHA {
		t.Fatalf("remote Todo head = %s, want %s", remoteTodoHead, commit.CommitSHA)
	}
	baseHead, err := manager.FetchBaseHead(context.Background(), request)
	if err != nil || baseHead != base {
		t.Fatalf("FetchBaseHead = %q, %v; want %q", baseHead, err, base)
	}
	preview, err := manager.PreviewMerge(context.Background(), MergePreviewRequest{
		ProvisionRequest: request, HeadCommit: commit.CommitSHA, ExpectedBaseHead: baseHead,
		MergeStrategy: MergeStrategySquash, CheckCommands: []string{"git diff --check"},
	})
	if err != nil {
		t.Fatalf("PreviewMerge: %v", err)
	}
	if !validObjectID(preview.TreeSHA) || len(preview.Checks) != 1 || !preview.Checks[0].Succeeded {
		t.Fatalf("preview = %+v", preview)
	}
	receipt, err := manager.MergeAuthoritative(context.Background(), MergeRequest{
		ProvisionRequest: request, HeadCommit: commit.CommitSHA, ExpectedBaseHead: baseHead,
		ExpectedTreeSHA: preview.TreeSHA, MergeStrategy: MergeStrategySquash,
	})
	if err != nil {
		t.Fatalf("MergeAuthoritative: %v", err)
	}
	if receipt.TargetHead != receipt.MergeCommitSHA || receipt.TreeSHA != preview.TreeSHA {
		t.Fatalf("merge receipt = %+v", receipt)
	}
	if head := runTestGit(t, seed, "--git-dir", authoritative, "rev-parse", "refs/heads/main"); head != receipt.MergeCommitSHA {
		t.Fatalf("authoritative main = %s, want %s", head, receipt.MergeCommitSHA)
	}
	parents := strings.Fields(runTestGit(t, seed, "--git-dir", authoritative, "show", "-s", "--format=%P", receipt.MergeCommitSHA))
	if len(parents) != 1 || parents[0] != base {
		t.Fatalf("squash merge parents = %v, want [%s]", parents, base)
	}
	if _, err := manager.MergeAuthoritative(context.Background(), MergeRequest{
		ProvisionRequest: request, HeadCommit: commit.CommitSHA, ExpectedBaseHead: baseHead,
		ExpectedTreeSHA: preview.TreeSHA, MergeStrategy: MergeStrategySquash,
	}); !errors.Is(err, ErrTargetMoved) {
		t.Fatalf("replayed raw CAS merge error = %v, want ErrTargetMoved", err)
	}
}

func TestAuthoritativeMergeRejectsTargetMovementWithoutChangingNewHead(t *testing.T) {
	authoritative, seed, base := initAuthoritativeRepository(t)
	manager := newTestManager(t, Config{})
	request := testProvisionRequest(authoritative, SourceManagedBare, base, "todo-race")
	workspace, err := manager.Provision(context.Background(), request)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	writeTestFile(t, filepath.Join(workspace.WorktreePath, "src", "candidate.txt"), "candidate\n")
	commit, err := manager.CreateCommit(context.Background(), CommitRequest{
		ProvisionRequest: request, PlanID: "plan", PlanDigest: "digest", ExecutionRunID: "run", AgentID: "agent",
		AllowedFileScopes: []string{"src"},
	})
	if err != nil {
		t.Fatalf("CreateCommit: %v", err)
	}
	if err := manager.PushTodoBranch(context.Background(), request, commit.CommitSHA); err != nil {
		t.Fatalf("PushTodoBranch: %v", err)
	}
	preview, err := manager.PreviewMerge(context.Background(), MergePreviewRequest{
		ProvisionRequest: request, HeadCommit: commit.CommitSHA, ExpectedBaseHead: base, MergeStrategy: MergeStrategySquash,
	})
	if err != nil {
		t.Fatalf("PreviewMerge: %v", err)
	}
	writeTestFile(t, filepath.Join(seed, "independent.txt"), "new target\n")
	runTestGit(t, seed, "add", "independent.txt")
	runTestGit(t, seed, "commit", "-m", "advance target")
	runTestGit(t, seed, "push", "origin", "main")
	advanced := runTestGit(t, seed, "rev-parse", "HEAD")
	_, err = manager.MergeAuthoritative(context.Background(), MergeRequest{
		ProvisionRequest: request, HeadCommit: commit.CommitSHA, ExpectedBaseHead: base,
		ExpectedTreeSHA: preview.TreeSHA, MergeStrategy: MergeStrategySquash,
	})
	if !errors.Is(err, ErrTargetMoved) {
		t.Fatalf("MergeAuthoritative target movement error = %v, want ErrTargetMoved", err)
	}
	if head := runTestGit(t, seed, "--git-dir", authoritative, "rev-parse", "refs/heads/main"); head != advanced {
		t.Fatalf("target movement was overwritten: main=%s want=%s", head, advanced)
	}
}

func TestPreviewDetectsConflictsAndLeavesNoPreviewWorktree(t *testing.T) {
	authoritative, seed, base := initAuthoritativeRepository(t)
	manager := newTestManager(t, Config{})
	request := testProvisionRequest(authoritative, SourceManagedBare, base, "todo-conflict")
	workspace, err := manager.Provision(context.Background(), request)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	writeTestFile(t, filepath.Join(workspace.WorktreePath, "src", "base.txt"), "candidate version\n")
	commit, err := manager.CreateCommit(context.Background(), CommitRequest{
		ProvisionRequest: request, PlanID: "plan", PlanDigest: "digest", ExecutionRunID: "run", AgentID: "agent",
		AllowedFileScopes: []string{"src"},
	})
	if err != nil {
		t.Fatalf("CreateCommit: %v", err)
	}
	writeTestFile(t, filepath.Join(seed, "src", "base.txt"), "target version\n")
	runTestGit(t, seed, "add", "src/base.txt")
	runTestGit(t, seed, "commit", "-m", "conflicting target")
	runTestGit(t, seed, "push", "origin", "main")
	newBase := runTestGit(t, seed, "rev-parse", "HEAD")
	_, err = manager.PreviewMerge(context.Background(), MergePreviewRequest{
		ProvisionRequest: request, HeadCommit: commit.CommitSHA, ExpectedBaseHead: newBase, MergeStrategy: MergeStrategySquash,
	})
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("PreviewMerge conflict error = %v, want ErrMergeConflict", err)
	}
	previewRoot := filepath.Join(filepath.Dir(filepath.Dir(workspace.WorktreePath)), ".previews")
	entries, readErr := os.ReadDir(previewRoot)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("preview worktree was not cleaned: entries=%v err=%v", entries, readErr)
	}
}

func TestReadOnlySourceExportsArtifactsWithoutMutatingCheckout(t *testing.T) {
	source, base := initSourceRepository(t)
	manager := newTestManager(t, Config{})
	request := testProvisionRequest(source, SourceManagedLocalReadOnly, base, "todo-export")
	workspace, err := manager.Provision(context.Background(), request)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	sourceBranch := runTestGit(t, source, "symbolic-ref", "--short", "HEAD")
	sourceStatus := runTestGit(t, source, "status", "--porcelain")
	writeTestFile(t, filepath.Join(workspace.WorktreePath, "src", "exported.txt"), "exported\n")
	commit, err := manager.CreateCommit(context.Background(), CommitRequest{
		ProvisionRequest: request, PlanID: "plan", PlanDigest: "digest", ExecutionRunID: "run", AgentID: "agent",
		AllowedFileScopes: []string{"src"},
	})
	if err != nil {
		t.Fatalf("CreateCommit: %v", err)
	}
	exported, err := manager.Export(context.Background(), request, base, commit.CommitSHA)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	for _, path := range []string{exported.PatchPath, exported.BundlePath} {
		info, statErr := os.Stat(path)
		if statErr != nil || info.Size() == 0 || !pathWithin(manager.root, path) {
			t.Fatalf("invalid export %q: info=%v err=%v", path, info, statErr)
		}
	}
	patch, err := manager.OpenExport(context.Background(), request, base, commit.CommitSHA, "patch")
	if err != nil {
		t.Fatalf("OpenExport patch: %v", err)
	}
	data, readErr := io.ReadAll(patch.Reader)
	_ = patch.Reader.Close()
	if readErr != nil || len(data) == 0 || filepath.IsAbs(patch.Name) || strings.Contains(patch.Name, string(filepath.Separator)) || patch.ContentType == "" {
		t.Fatalf("opened patch = name=%q type=%q bytes=%d err=%v", patch.Name, patch.ContentType, len(data), readErr)
	}
	if _, err := manager.OpenExport(context.Background(), request, base, commit.CommitSHA, "unknown"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("OpenExport invalid kind error = %v", err)
	}
	if branch := runTestGit(t, source, "symbolic-ref", "--short", "HEAD"); branch != sourceBranch {
		t.Fatalf("source checkout branch changed: got=%s want=%s", branch, sourceBranch)
	}
	if status := runTestGit(t, source, "status", "--porcelain"); status != sourceStatus {
		t.Fatalf("source checkout status changed: got=%q want=%q", status, sourceStatus)
	}
}

func initAuthoritativeRepository(t *testing.T) (bare, seed, base string) {
	t.Helper()
	bare = filepath.Join(t.TempDir(), "authoritative.git")
	runTestGit(t, "", "init", "--bare", "--initial-branch=main", bare)
	seed = t.TempDir()
	runTestGit(t, seed, "init", "-b", "main")
	runTestGit(t, seed, "config", "user.name", "Target User")
	runTestGit(t, seed, "config", "user.email", "target@example.test")
	writeTestFile(t, filepath.Join(seed, "README.md"), "baseline\n")
	writeTestFile(t, filepath.Join(seed, "src", "base.txt"), "base\n")
	runTestGit(t, seed, "add", "README.md", "src/base.txt")
	runTestGit(t, seed, "commit", "-m", "initial")
	runTestGit(t, seed, "remote", "add", "origin", bare)
	runTestGit(t, seed, "push", "-u", "origin", "main")
	base = runTestGit(t, seed, "rev-parse", "HEAD")
	return bare, seed, base
}

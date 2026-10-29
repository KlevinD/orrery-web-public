package gitworkspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProvisionIsolatesTodosAndIgnoresDirtySourceCheckout(t *testing.T) {
	source, base := initSourceRepository(t)
	writeTestFile(t, filepath.Join(source, "dirty-only.txt"), "not committed\n")
	manager := newTestManager(t, Config{})

	firstRequest := testProvisionRequest(source, SourceManagedLocalReadOnly, base, "todo-a")
	first, err := manager.Provision(context.Background(), firstRequest)
	if err != nil {
		t.Fatalf("Provision first Todo: %v", err)
	}
	secondRequest := testProvisionRequest(source, SourceManagedLocalReadOnly, base, "todo-b")
	second, err := manager.Provision(context.Background(), secondRequest)
	if err != nil {
		t.Fatalf("Provision second Todo: %v", err)
	}
	if first.WorktreePath == second.WorktreePath || first.BranchName == second.BranchName {
		t.Fatalf("Todo workspaces are not isolated: first=%+v second=%+v", first, second)
	}
	if _, err := os.Stat(filepath.Join(first.WorktreePath, "dirty-only.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dirty source checkout content entered baseline: %v", err)
	}
	reused, err := manager.Provision(context.Background(), firstRequest)
	if err != nil {
		t.Fatalf("Provision same Todo again: %v", err)
	}
	if reused.WorktreePath != first.WorktreePath || reused.BranchName != first.BranchName || reused.StorageKey != first.StorageKey {
		t.Fatalf("same Todo did not reuse workspace: first=%+v reused=%+v", first, reused)
	}
}

func TestProvisionIsolatesReopenedTodoAttempts(t *testing.T) {
	source, base := initSourceRepository(t)
	manager := newTestManager(t, Config{})
	request := testProvisionRequest(source, SourceManagedLocalReadOnly, base, "todo-reopened")
	request.ExecutionAttemptID = "mission-attempt-1"
	first, err := manager.Provision(context.Background(), request)
	if err != nil {
		t.Fatalf("Provision first attempt: %v", err)
	}
	request.ExecutionAttemptID = "mission-attempt-2"
	second, err := manager.Provision(context.Background(), request)
	if err != nil {
		t.Fatalf("Provision second attempt: %v", err)
	}
	if first.WorktreePath == second.WorktreePath || first.StorageKey == second.StorageKey || first.BranchName == second.BranchName {
		t.Fatalf("reopened Todo attempts share Git identity: first=%+v second=%+v", first, second)
	}
}

func TestProvisionRejectsManagedRootSymlinkEscape(t *testing.T) {
	source, base := initSourceRepository(t)
	root := t.TempDir()
	manager, err := NewManager(Config{Root: root, RequireSigning: false})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	outside := t.TempDir()
	parent := filepath.Join(root, "tenant-a", "project-a", "todos")
	if err := mkdirContained(root, parent); err != nil {
		t.Fatalf("create managed parent: %v", err)
	}
	link := filepath.Join(parent, "todo-a")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink privilege unavailable: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
	_, err = manager.Provision(context.Background(), testProvisionRequest(source, SourceManagedLocalReadOnly, base, "todo-a"))
	if !errors.Is(err, ErrPolicyViolation) {
		t.Fatalf("Provision symlink escape error = %v, want ErrPolicyViolation", err)
	}
	if entries, readErr := os.ReadDir(outside); readErr != nil || len(entries) != 0 {
		t.Fatalf("outside directory was modified: entries=%v err=%v", entries, readErr)
	}
}

func TestProvisionRejectsGitRefInjection(t *testing.T) {
	source, base := initSourceRepository(t)
	manager := newTestManager(t, Config{})
	request := testProvisionRequest(source, SourceManagedLocalReadOnly, base, "todo-a")
	request.DefaultBranch = "--upload-pack=evil"
	if _, err := manager.Provision(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Provision ref injection error = %v, want ErrInvalidRequest", err)
	}
	request = testProvisionRequest(source, SourceManagedLocalReadOnly, base, "todo-a")
	request.BranchPrefix = "orrery/todo/../../"
	if _, err := manager.Provision(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Provision branch injection error = %v, want ErrInvalidRequest", err)
	}
}

func TestControlledCommitRecordsVerifiableMetadata(t *testing.T) {
	source, base := initSourceRepository(t)
	manager := newTestManager(t, Config{CommitterName: "Orrery Test Service", CommitterEmail: "git-service@example.test"})
	request := testProvisionRequest(source, SourceManagedLocalReadOnly, base, "todo-commit")
	workspace, err := manager.Provision(context.Background(), request)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	writeTestFile(t, filepath.Join(workspace.WorktreePath, "src", "app.txt"), "approved change\n")
	result, err := manager.CreateCommit(context.Background(), CommitRequest{
		ProvisionRequest: request, PlanID: "plan-1", PlanDigest: "sha256:plan", ExecutionRunID: "run-1", AgentID: "agent-1",
		AuthorName: "Agent One", AuthorEmail: "agent-1@example.test", Summary: "apply approved change",
		AllowedFileScopes: []string{"src"}, CheckCommands: []string{"git diff --check"},
	})
	if err != nil {
		t.Fatalf("CreateCommit: %v", err)
	}
	if !validObjectID(result.CommitSHA) || !validObjectID(result.TreeSHA) || result.DiffDigest == "" || result.SignatureStatus != "unsigned" {
		t.Fatalf("commit result is incomplete: %+v", result)
	}
	if len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != "src/app.txt" || len(result.Checks) != 1 || !result.Checks[0].Succeeded {
		t.Fatalf("commit evidence = %+v", result)
	}
	metadata := runTestGit(t, workspace.WorktreePath, "show", "-s", "--format=%an%n%ae%n%cn%n%ce%n%B", result.CommitSHA)
	for _, expected := range []string{"Agent One", "agent-1@example.test", "Orrery Test Service", "git-service@example.test", "Orrery-Todo: todo-commit", "Orrery-Plan: plan-1@sha256:plan", "Orrery-Run: run-1", "Orrery-Agent: agent-1"} {
		if !strings.Contains(metadata, expected) {
			t.Fatalf("commit metadata %q does not contain %q", metadata, expected)
		}
	}
	if status := runTestGit(t, workspace.WorktreePath, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("workspace remains dirty after controlled commit: %q", status)
	}
}

func TestControlledCommitRejectsPolicyViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, workspace Workspace, base string)
		config Config
	}{
		{name: "outside scope", mutate: func(t *testing.T, workspace Workspace, _ string) {
			writeTestFile(t, filepath.Join(workspace.WorktreePath, "docs", "outside.txt"), "outside\n")
		}},
		{name: "secret", mutate: func(t *testing.T, workspace Workspace, _ string) {
			writeTestFile(t, filepath.Join(workspace.WorktreePath, "src", "secret.txt"), "api_key=abcdefghijklmnopqrstuvwxyz123456\n")
		}},
		{name: "binary", mutate: func(t *testing.T, workspace Workspace, _ string) {
			writeTestBytes(t, filepath.Join(workspace.WorktreePath, "src", "binary.dat"), []byte{'a', 0, 'b'})
		}},
		{name: "oversize", config: Config{MaxFileBytes: 8}, mutate: func(t *testing.T, workspace Workspace, _ string) {
			writeTestFile(t, filepath.Join(workspace.WorktreePath, "src", "large.txt"), "more than eight bytes\n")
		}},
		{name: "symlink", mutate: func(t *testing.T, workspace Workspace, _ string) {
			target := filepath.Join(workspace.WorktreePath, "src", "target.txt")
			writeTestFile(t, target, "target\n")
			if err := os.Symlink(target, filepath.Join(workspace.WorktreePath, "src", "link.txt")); err != nil {
				t.Skipf("symlink privilege unavailable: %v", err)
			}
		}},
		{name: "submodule index", mutate: func(t *testing.T, workspace Workspace, base string) {
			runTestGit(t, workspace.WorktreePath, "update-index", "--add", "--cacheinfo", "160000,"+base+",src/vendor")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, base := initSourceRepository(t)
			manager := newTestManager(t, test.config)
			request := testProvisionRequest(source, SourceManagedLocalReadOnly, base, "todo-policy")
			workspace, err := manager.Provision(context.Background(), request)
			if err != nil {
				t.Fatalf("Provision: %v", err)
			}
			test.mutate(t, workspace, base)
			_, err = manager.CreateCommit(context.Background(), CommitRequest{
				ProvisionRequest: request, PlanID: "plan-1", PlanDigest: "digest", ExecutionRunID: "run-1", AgentID: "agent-1",
				AllowedFileScopes: []string{"src"},
			})
			if !errors.Is(err, ErrPolicyViolation) {
				t.Fatalf("CreateCommit policy error = %v, want ErrPolicyViolation", err)
			}
		})
	}
}

func TestControlledCommitRequiresAndCreatesSSHSignature(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is unavailable")
	}
	source, base := initSourceRepository(t)
	keyPath := filepath.Join(t.TempDir(), "signing-key")
	runExternal(t, "", "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", keyPath)
	privateKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read signing key: %v", err)
	}
	root := t.TempDir()
	manager, err := NewManager(Config{Root: root, SigningKey: string(privateKey), SigningFormat: "ssh", RequireSigning: true})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	materialized := manager.ephemeralSigningKey
	request := testProvisionRequest(source, SourceManagedLocalReadOnly, base, "todo-signed")
	workspace, err := manager.Provision(context.Background(), request)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	writeTestFile(t, filepath.Join(workspace.WorktreePath, "src", "signed.txt"), "signed\n")
	result, err := manager.CreateCommit(context.Background(), CommitRequest{
		ProvisionRequest: request, PlanID: "plan-1", PlanDigest: "digest", ExecutionRunID: "run-1", AgentID: "agent-1",
		AllowedFileScopes: []string{"src"},
	})
	if err != nil {
		t.Fatalf("CreateCommit signed: %v", err)
	}
	if result.SignatureStatus != "signed" || !strings.Contains(runTestGit(t, workspace.WorktreePath, "cat-file", "commit", result.CommitSHA), "gpgsig ") {
		t.Fatalf("commit is not signed: %+v", result)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(materialized); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ephemeral signing key still exists after Close: %v", err)
	}
}

func newTestManager(t *testing.T, overrides Config) *Manager {
	t.Helper()
	overrides.Root = t.TempDir()
	overrides.RequireSigning = false
	if overrides.CommandTimeout == 0 {
		overrides.CommandTimeout = 30 * time.Second
	}
	manager, err := NewManager(overrides)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func initSourceRepository(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	runTestGit(t, dir, "init", "-b", "main")
	runTestGit(t, dir, "config", "user.name", "Source User")
	runTestGit(t, dir, "config", "user.email", "source@example.test")
	writeTestFile(t, filepath.Join(dir, "README.md"), "baseline\n")
	writeTestFile(t, filepath.Join(dir, "src", "base.txt"), "base\n")
	runTestGit(t, dir, "add", "README.md", "src/base.txt")
	runTestGit(t, dir, "commit", "-m", "initial")
	return dir, strings.TrimSpace(runTestGit(t, dir, "rev-parse", "HEAD"))
}

func testProvisionRequest(source, sourceKind, base, todoID string) ProvisionRequest {
	return ProvisionRequest{
		TenantID: "tenant-a", DevelopmentProjectID: "project-a", TodoID: todoID, RepositoryBindingID: "binding-a",
		SourcePath: source, SourceKind: sourceKind, DefaultBranch: "main", BranchPrefix: "orrery/todo/", BranchSlug: todoID, BaseCommit: base,
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	writeTestBytes(t, path, []byte(content))
}

func writeTestBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create test file parent: %v", err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func runTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return runExternal(t, dir, "git", args...)
}

func runExternal(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	if dir != "" {
		command.Dir = dir
	}
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
	return strings.TrimSpace(string(output))
}

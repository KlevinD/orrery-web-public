package scmprovider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/scmconnection"
)

func TestManagedLocalVerifierReadsRepositoryMetadataWithoutExposingPath(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	repositoryPath := initializeGitRepository(t, gitPath)
	now := time.Date(2026, 7, 25, 3, 0, 0, 0, time.UTC)
	verifier := NewManagedLocalVerifier(gitPath, func() time.Time { return now })

	result, err := verifier.Verify(context.Background(), scmconnection.Connection{
		Provider: scmconnection.ProviderManagedLocal,
		BaseURL:  repositoryPath,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !result.VerifiedAt.Equal(now) || result.Repository.DefaultBranch != "main" || !result.Repository.ReadOnly {
		t.Fatalf("verification = %+v", result)
	}
	if !isGitObjectID(result.Repository.HeadCommit) {
		t.Fatalf("head commit = %q", result.Repository.HeadCommit)
	}
	serialized := result.Repository.RepositoryLocator + " " + result.Repository.DisplayName
	if strings.Contains(strings.ToLower(serialized), strings.ToLower(filepath.Clean(repositoryPath))) {
		t.Fatalf("metadata exposed absolute repository path: %+v", result.Repository)
	}
	if !strings.HasPrefix(result.Repository.RepositoryLocator, "managed-local:") {
		t.Fatalf("repository locator = %q", result.Repository.RepositoryLocator)
	}
}

func TestManagedLocalVerifierRejectsInvalidRepositoryWithoutPathDisclosure(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	missing := filepath.Join(t.TempDir(), "sensitive", "missing")
	verifier := NewManagedLocalVerifier(gitPath, nil)

	_, err = verifier.Verify(context.Background(), scmconnection.Connection{
		Provider: scmconnection.ProviderManagedLocal,
		BaseURL:  missing,
	})
	if !errors.Is(err, ErrInvalidRepository) {
		t.Fatalf("Verify error = %v, want ErrInvalidRepository", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(missing)) {
		t.Fatalf("error exposed repository path: %v", err)
	}
}

func initializeGitRepository(t *testing.T, gitPath string) string {
	t.Helper()
	repositoryPath := filepath.Join(t.TempDir(), "source-repository")
	runGit(t, gitPath, "init", "--initial-branch=main", repositoryPath)
	runGit(t, gitPath, "-C", repositoryPath, "config", "user.name", "Orrery Test")
	runGit(t, gitPath, "-C", repositoryPath, "config", "user.email", "orrery-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README.md"), []byte("test repository\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runGit(t, gitPath, "-C", repositoryPath, "add", "README.md")
	runGit(t, gitPath, "-C", repositoryPath, "commit", "-m", "test: initialize repository")
	return repositoryPath
}

func runGit(t *testing.T, gitPath string, args ...string) {
	t.Helper()
	cmd := exec.Command(gitPath, args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

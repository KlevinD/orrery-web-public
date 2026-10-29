package scmprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"orrery_v2/internal/app/scmconnection"
)

const gitOutputLimit = 64 * 1024

var (
	ErrUnsupportedProvider = errors.New("SCM provider is not supported")
	ErrInvalidRepository   = errors.New("managed local repository is invalid")
	ErrGitUnavailable      = errors.New("git executable is unavailable")
)

type ManagedLocalVerifier struct {
	gitPath string
	now     func() time.Time
}

func NewManagedLocalVerifier(gitPath string, now func() time.Time) *ManagedLocalVerifier {
	gitPath = strings.TrimSpace(gitPath)
	if gitPath == "" {
		gitPath = "git"
	}
	if now == nil {
		now = time.Now
	}
	return &ManagedLocalVerifier{gitPath: gitPath, now: now}
}

func (v *ManagedLocalVerifier) SupportsProvider(provider string) bool {
	return v != nil && strings.EqualFold(strings.TrimSpace(provider), scmconnection.ProviderManagedLocal)
}

func (v *ManagedLocalVerifier) Verify(ctx context.Context, connection scmconnection.Connection) (scmconnection.Verification, error) {
	metadata, err := v.repositoryMetadata(ctx, connection)
	if err != nil {
		return scmconnection.Verification{}, err
	}
	return scmconnection.Verification{VerifiedAt: v.now().UTC(), Repository: metadata}, nil
}

func (v *ManagedLocalVerifier) ListRepositories(ctx context.Context, connection scmconnection.Connection) ([]scmconnection.RepositoryMetadata, error) {
	metadata, err := v.repositoryMetadata(ctx, connection)
	if err != nil {
		return nil, err
	}
	return []scmconnection.RepositoryMetadata{metadata}, nil
}

func (v *ManagedLocalVerifier) repositoryMetadata(ctx context.Context, connection scmconnection.Connection) (scmconnection.RepositoryMetadata, error) {
	if err := ctx.Err(); err != nil {
		return scmconnection.RepositoryMetadata{}, err
	}
	if v == nil || strings.TrimSpace(v.gitPath) == "" {
		return scmconnection.RepositoryMetadata{}, ErrGitUnavailable
	}
	if connection.Provider != scmconnection.ProviderManagedLocal {
		return scmconnection.RepositoryMetadata{}, ErrUnsupportedProvider
	}
	repositoryPath := filepath.Clean(strings.TrimSpace(connection.BaseURL))
	if repositoryPath == "." || !filepath.IsAbs(repositoryPath) {
		return scmconnection.RepositoryMetadata{}, ErrInvalidRepository
	}
	info, err := os.Stat(repositoryPath)
	if err != nil || !info.IsDir() {
		return scmconnection.RepositoryMetadata{}, ErrInvalidRepository
	}

	if _, err := v.git(ctx, repositoryPath, "rev-parse", "--git-dir"); err != nil {
		return scmconnection.RepositoryMetadata{}, ErrInvalidRepository
	}
	bare, err := v.git(ctx, repositoryPath, "rev-parse", "--is-bare-repository")
	if err != nil || (bare != "true" && bare != "false") {
		return scmconnection.RepositoryMetadata{}, ErrInvalidRepository
	}
	branch, err := v.git(ctx, repositoryPath, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch == "" || strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, "\r\n\x00") {
		return scmconnection.RepositoryMetadata{}, fmt.Errorf("%w: default branch is unavailable", ErrInvalidRepository)
	}
	if _, err := v.git(ctx, repositoryPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return scmconnection.RepositoryMetadata{}, fmt.Errorf("%w: default branch is unavailable", ErrInvalidRepository)
	}
	headCommit, err := v.git(ctx, repositoryPath, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil || !isGitObjectID(headCommit) {
		return scmconnection.RepositoryMetadata{}, fmt.Errorf("%w: head commit is unavailable", ErrInvalidRepository)
	}

	sourceKind := "managed_local_source"
	readOnly, supportsPush, supportsMerge := true, false, false
	if bare == "true" {
		sourceKind = "managed_bare_authoritative"
		readOnly, supportsPush, supportsMerge = false, true, true
	}
	return scmconnection.RepositoryMetadata{
		RepositoryLocator: managedLocalLocator(repositoryPath),
		DisplayName:       filepath.Base(repositoryPath),
		DefaultBranch:     branch,
		HeadCommit:        headCommit,
		ReadOnly:          readOnly,
		SourceKind:        sourceKind,
		WorkspaceWritable: true,
		SupportsPush:      supportsPush,
		SupportsMerge:     supportsMerge,
	}, nil
}

func (v *ManagedLocalVerifier) git(ctx context.Context, repositoryPath string, args ...string) (string, error) {
	commandArgs := make([]string, 0, len(args)+3)
	commandArgs = append(commandArgs, "--no-optional-locks", "-C", repositoryPath)
	commandArgs = append(commandArgs, args...)
	cmd := exec.CommandContext(ctx, v.gitPath, commandArgs...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	var stdout limitedBuffer
	stdout.remaining = gitOutputLimit
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", ErrGitUnavailable
		}
		return "", err
	}
	if stdout.exceeded {
		return "", fmt.Errorf("git output exceeded limit")
	}
	return strings.TrimSpace(stdout.String()), nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	exceeded  bool
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	originalLength := len(p)
	if len(p) > w.remaining {
		p = p[:w.remaining]
		w.exceeded = true
	}
	if len(p) > 0 {
		_, _ = w.buffer.Write(p)
		w.remaining -= len(p)
	}
	return originalLength, nil
}

func (w *limitedBuffer) String() string { return w.buffer.String() }

func managedLocalLocator(repositoryPath string) string {
	canonicalPath := filepath.Clean(repositoryPath)
	if resolved, err := filepath.EvalSymlinks(canonicalPath); err == nil {
		canonicalPath = resolved
	}
	digest := sha256.Sum256([]byte(strings.ToLower(filepath.ToSlash(canonicalPath))))
	return "managed-local:" + hex.EncodeToString(digest[:16])
}

func isGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

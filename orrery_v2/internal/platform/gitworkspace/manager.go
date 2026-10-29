package gitworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	safeSegmentPattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	objectIDPattern    = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
)

type Manager struct {
	root                string
	git                 gitRunner
	cfg                 Config
	now                 func() time.Time
	ephemeralSigningKey string
	checkHome           string
}

func NewManager(cfg Config) (*Manager, error) {
	root := filepath.Clean(strings.TrimSpace(cfg.Root))
	if root == "." || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("%w: managed workspace root must be absolute", ErrInvalidRequest)
	}
	gitPath := strings.TrimSpace(cfg.GitPath)
	if gitPath == "" {
		gitPath = "git"
	}
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = 5 * 1024 * 1024
	}
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = 30 * time.Minute
	}
	if cfg.FetchLockTimeout <= 0 {
		cfg.FetchLockTimeout = 2 * time.Minute
	}
	if strings.TrimSpace(cfg.CommitterName) == "" {
		cfg.CommitterName = "Orrery Git Service"
	}
	if strings.TrimSpace(cfg.CommitterEmail) == "" {
		cfg.CommitterEmail = "orrery@localhost"
	}
	if strings.TrimSpace(cfg.SigningFormat) == "" {
		cfg.SigningFormat = "ssh"
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create managed workspace root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve managed workspace root: %w", err)
	}
	hooks := filepath.Join(canonical, ".disabled-hooks")
	if err := mkdirContained(canonical, hooks); err != nil {
		return nil, fmt.Errorf("create disabled hooks directory: %w", err)
	}
	checkHome := filepath.Join(canonical, ".check-home")
	if err := mkdirContained(canonical, checkHome); err != nil {
		return nil, fmt.Errorf("create isolated check home: %w", err)
	}
	if err := mkdirContained(canonical, filepath.Join(checkHome, "cache")); err != nil {
		return nil, fmt.Errorf("create isolated check cache: %w", err)
	}
	ephemeralSigningKey := ""
	if key := strings.TrimSpace(cfg.SigningKey); strings.Contains(key, "PRIVATE KEY") {
		signingDir := filepath.Join(canonical, ".signing")
		if err := mkdirContained(canonical, signingDir); err != nil {
			return nil, fmt.Errorf("create signing key directory: %w", err)
		}
		keyFile, err := os.CreateTemp(signingDir, "git-signing-key-*")
		if err != nil {
			return nil, fmt.Errorf("create git signing key: %w", err)
		}
		keyPath := keyFile.Name()
		if err := keyFile.Chmod(0o600); err != nil {
			_ = keyFile.Close()
			_ = os.Remove(keyPath)
			return nil, fmt.Errorf("protect git signing key: %w", err)
		}
		if _, err := keyFile.WriteString(key + "\n"); err != nil {
			_ = keyFile.Close()
			_ = os.Remove(keyPath)
			return nil, fmt.Errorf("materialize git signing key: %w", err)
		}
		if err := keyFile.Close(); err != nil {
			_ = os.Remove(keyPath)
			return nil, fmt.Errorf("close git signing key: %w", err)
		}
		cfg.SigningKey = keyPath
		ephemeralSigningKey = keyPath
	}
	return &Manager{
		root: canonical, cfg: cfg,
		git: gitRunner{path: gitPath, hooks: hooks, timeout: cfg.CommandTimeout},
		now: time.Now, ephemeralSigningKey: ephemeralSigningKey, checkHome: checkHome,
	}, nil
}

func (m *Manager) Close() error {
	if m == nil || m.ephemeralSigningKey == "" {
		return nil
	}
	path := m.ephemeralSigningKey
	m.ephemeralSigningKey = ""
	if !pathWithin(m.root, path) {
		return ErrPolicyViolation
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (m *Manager) Provision(ctx context.Context, req ProvisionRequest) (Workspace, error) {
	paths, req, err := m.resolve(req)
	if err != nil {
		return Workspace{}, err
	}
	if err := validateSource(req); err != nil {
		return Workspace{}, err
	}
	if err := mkdirContained(m.root, filepath.Dir(paths.mirror)); err != nil {
		return Workspace{}, fmt.Errorf("create mirror parent: %w", err)
	}
	if err := mkdirContained(m.root, filepath.Dir(paths.worktree)); err != nil {
		return Workspace{}, fmt.Errorf("create worktree parent: %w", err)
	}
	if err := m.ensureContained(paths.mirror, false); err != nil {
		return Workspace{}, err
	}
	if err := m.ensureContained(paths.worktree, false); err != nil {
		return Workspace{}, err
	}
	unlock, err := m.acquireFetchLock(ctx, paths.mirror+".fetch.lock")
	if err != nil {
		return Workspace{}, err
	}
	defer unlock()
	if err := m.ensureMirror(ctx, req, paths.mirror); err != nil {
		return Workspace{}, err
	}
	if err := m.ensureCommit(ctx, paths.mirror, req.BaseCommit); err != nil {
		return Workspace{}, fmt.Errorf("%w: approved base commit is unavailable", ErrWorkspaceConflict)
	}
	branch := todoBranch(req)
	if err := m.ensureWorktree(ctx, paths.mirror, paths.worktree, branch, req.BaseCommit); err != nil {
		return Workspace{}, err
	}
	if err := m.ensureContained(paths.worktree, true); err != nil {
		return Workspace{}, err
	}
	head, err := m.git.output(ctx, paths.worktree, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !validObjectID(head) {
		return Workspace{}, fmt.Errorf("%w: workspace head is unavailable", ErrRepository)
	}
	return Workspace{
		StorageKey: storageKey(req), BranchName: branch, BaseCommit: req.BaseCommit,
		HeadCommit: head, WorktreePath: paths.worktree, MirrorPath: paths.mirror,
	}, nil
}

func mkdirContained(root, target string) error {
	root, target = filepath.Clean(root), filepath.Clean(target)
	if !pathWithin(root, target) {
		return ErrPolicyViolation
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return ErrPolicyViolation
	}
	current := root
	if relative == "." {
		return nil
	}
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		if segment == "" || segment == "." || segment == ".." {
			return ErrPolicyViolation
		}
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil || info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrPolicyViolation
		}
		canonical, err := filepath.EvalSymlinks(current)
		if err != nil || !pathWithin(root, canonical) {
			return ErrPolicyViolation
		}
	}
	return nil
}

func (m *Manager) Resolve(req ProvisionRequest) (Workspace, error) {
	paths, req, err := m.resolve(req)
	if err != nil {
		return Workspace{}, err
	}
	if err := m.ensureContained(paths.worktree, true); err != nil {
		return Workspace{}, err
	}
	return Workspace{StorageKey: storageKey(req), BranchName: todoBranch(req), BaseCommit: req.BaseCommit, WorktreePath: paths.worktree, MirrorPath: paths.mirror}, nil
}

func (m *Manager) FetchBaseHead(ctx context.Context, req ProvisionRequest) (string, error) {
	paths, req, err := m.resolve(req)
	if err != nil {
		return "", err
	}
	if err := validateSource(req); err != nil {
		return "", err
	}
	unlock, err := m.acquireFetchLock(ctx, paths.mirror+".fetch.lock")
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := m.ensureMirror(ctx, req, paths.mirror); err != nil {
		return "", err
	}
	ref := "refs/remotes/origin/" + req.DefaultBranch
	head, err := m.git.output(ctx, m.root, "--git-dir", paths.mirror, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		// A newly cloned mirror stores the source branch as refs/heads/*.
		head, err = m.git.output(ctx, m.root, "--git-dir", paths.mirror, "rev-parse", "--verify", "refs/heads/"+req.DefaultBranch+"^{commit}")
	}
	if err != nil || !validObjectID(head) {
		return "", fmt.Errorf("%w: default branch head is unavailable", ErrRepository)
	}
	return head, nil
}

func (m *Manager) RemoveWorktree(ctx context.Context, req ProvisionRequest) error {
	paths, _, err := m.resolve(req)
	if err != nil {
		return err
	}
	if _, err := os.Stat(paths.worktree); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := m.ensureContained(paths.worktree, true); err != nil {
		return err
	}
	_, err = m.git.run(ctx, m.root, nil, "--git-dir", paths.mirror, "worktree", "remove", "--force", paths.worktree)
	if err != nil {
		return err
	}
	return nil
}

type managedPaths struct {
	mirror   string
	worktree string
}

func (m *Manager) resolve(req ProvisionRequest) (managedPaths, ProvisionRequest, error) {
	if m == nil || m.root == "" {
		return managedPaths{}, req, ErrInvalidRequest
	}
	req = normalizeProvisionRequest(req)
	for _, value := range []string{req.TenantID, req.DevelopmentProjectID, req.TodoID, req.ExecutionAttemptID, req.RepositoryBindingID, req.DefaultBranch, req.BaseCommit} {
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return managedPaths{}, req, ErrInvalidRequest
		}
	}
	if !validObjectID(req.BaseCommit) || !validGitRefPart(req.DefaultBranch) || !validBranchPrefix(req.BranchPrefix) {
		return managedPaths{}, req, ErrInvalidRequest
	}
	projectRoot := filepath.Join(m.root, safeSegment(req.TenantID), safeSegment(req.DevelopmentProjectID))
	paths := managedPaths{
		mirror:   filepath.Join(projectRoot, "mirrors", safeSegment(req.RepositoryBindingID)+".git"),
		worktree: filepath.Join(projectRoot, "todos", safeSegment(req.TodoID), safeSegment(req.ExecutionAttemptID), safeSegment(req.RepositoryBindingID)),
	}
	if !pathWithin(m.root, paths.mirror) || !pathWithin(m.root, paths.worktree) {
		return managedPaths{}, req, ErrInvalidRequest
	}
	return paths, req, nil
}

func normalizeProvisionRequest(req ProvisionRequest) ProvisionRequest {
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.DevelopmentProjectID = strings.TrimSpace(req.DevelopmentProjectID)
	req.TodoID = strings.TrimSpace(req.TodoID)
	req.ExecutionAttemptID = strings.TrimSpace(req.ExecutionAttemptID)
	if req.ExecutionAttemptID == "" {
		req.ExecutionAttemptID = req.TodoID
	}
	req.RepositoryBindingID = strings.TrimSpace(req.RepositoryBindingID)
	req.SourcePath = filepath.Clean(strings.TrimSpace(req.SourcePath))
	req.SourceKind = strings.TrimSpace(req.SourceKind)
	req.DefaultBranch = strings.TrimSpace(req.DefaultBranch)
	req.BranchPrefix = strings.TrimSpace(req.BranchPrefix)
	req.BranchSlug = strings.TrimSpace(req.BranchSlug)
	req.BaseCommit = strings.ToLower(strings.TrimSpace(req.BaseCommit))
	return req
}

func validateSource(req ProvisionRequest) error {
	if req.SourceKind != SourceManagedLocalReadOnly && req.SourceKind != SourceManagedBare {
		return ErrInvalidRequest
	}
	if req.SourcePath == "." || !filepath.IsAbs(req.SourcePath) {
		return ErrInvalidRequest
	}
	info, err := os.Stat(req.SourcePath)
	if err != nil || !info.IsDir() {
		return ErrInvalidRequest
	}
	return nil
}

func (m *Manager) ensureMirror(ctx context.Context, req ProvisionRequest, mirror string) error {
	if _, err := os.Stat(mirror); errors.Is(err, os.ErrNotExist) {
		if _, err := m.git.run(ctx, m.root, nil, "clone", "--mirror", "--no-hardlinks", "--", req.SourcePath, mirror); err != nil {
			return err
		}
		return m.ensureContained(mirror, true)
	} else if err != nil {
		return err
	}
	bare, err := m.git.output(ctx, m.root, "--git-dir", mirror, "rev-parse", "--is-bare-repository")
	if err != nil || bare != "true" {
		return ErrWorkspaceConflict
	}
	remote, err := m.git.output(ctx, m.root, "--git-dir", mirror, "remote", "get-url", "origin")
	if err != nil || !samePath(remote, req.SourcePath) {
		return ErrWorkspaceConflict
	}
	_, err = m.git.run(ctx, m.root, nil, "--git-dir", mirror, "fetch", "--no-tags", "--prune", "--", req.SourcePath, "+refs/heads/*:refs/remotes/origin/*")
	return err
}

func (m *Manager) ensureCommit(ctx context.Context, mirror, commit string) error {
	_, err := m.git.run(ctx, m.root, nil, "--git-dir", mirror, "cat-file", "-e", commit+"^{commit}")
	return err
}

func (m *Manager) ensureWorktree(ctx context.Context, mirror, worktree, branch, base string) error {
	if _, err := os.Stat(worktree); err == nil {
		common, commonErr := m.git.output(ctx, worktree, "rev-parse", "--git-common-dir")
		if commonErr != nil {
			return ErrWorkspaceConflict
		}
		if !filepath.IsAbs(common) {
			common = filepath.Join(worktree, common)
		}
		canonicalCommon, commonErr := filepath.EvalSymlinks(filepath.Clean(common))
		if commonErr != nil || !samePath(canonicalCommon, mirror) {
			return ErrWorkspaceConflict
		}
		currentBranch, branchErr := m.git.output(ctx, worktree, "symbolic-ref", "--quiet", "--short", "HEAD")
		if branchErr != nil || currentBranch != branch {
			return ErrWorkspaceConflict
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := m.git.run(ctx, m.root, nil, "--git-dir", mirror, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err = m.git.run(ctx, m.root, nil, "--git-dir", mirror, "worktree", "add", "--", worktree, branch)
		return err
	}
	_, err := m.git.run(ctx, m.root, nil, "--git-dir", mirror, "worktree", "add", "-b", branch, "--", worktree, base)
	return err
}

func (m *Manager) acquireFetchLock(ctx context.Context, lockPath string) (func(), error) {
	deadline := m.now().Add(m.cfg.FetchLockTimeout)
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n", m.now().Add(m.cfg.FetchLockTimeout).Unix())
			_ = file.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if data, readErr := os.ReadFile(lockPath); readErr == nil {
			expiry, parseErr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
			if parseErr == nil && expiry < m.now().Unix() {
				_ = os.Remove(lockPath)
				continue
			}
		}
		if !m.now().Before(deadline) {
			return nil, fmt.Errorf("%w: fetch lock timed out", ErrWorkspaceConflict)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (m *Manager) ensureContained(path string, mustExist bool) error {
	path = filepath.Clean(path)
	if !pathWithin(m.root, path) {
		return ErrPolicyViolation
	}
	candidate := path
	if !mustExist {
		for {
			if _, err := os.Lstat(candidate); err == nil {
				break
			}
			parent := filepath.Dir(candidate)
			if parent == candidate {
				return ErrPolicyViolation
			}
			candidate = parent
		}
	}
	canonical, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return err
	}
	if !pathWithin(m.root, canonical) {
		return ErrPolicyViolation
	}
	return nil
}

func todoBranch(req ProvisionRequest) string {
	prefix := strings.TrimSpace(req.BranchPrefix)
	if prefix == "" {
		prefix = "orrery/todo/"
	}
	slug := safeSegment(req.BranchSlug)
	if slug == "item" {
		slug = safeSegment(req.TodoID)
	}
	shortID := branchIdentityToken(req.TodoID)
	if len(slug) > 40 {
		slug = slug[:40]
	}
	attemptID := strings.TrimSpace(req.ExecutionAttemptID)
	if attemptID == "" {
		attemptID = req.TodoID
	}
	shortAttempt := branchIdentityToken(attemptID)
	return prefix + shortID + "-" + shortAttempt + "-" + slug
}

func branchIdentityToken(value string) string {
	token := safeSegment(value)
	if len(token) <= 12 {
		return token
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return token[:8] + "-" + hex.EncodeToString(digest[:3])
}

func storageKey(req ProvisionRequest) string {
	attemptID := strings.TrimSpace(req.ExecutionAttemptID)
	if attemptID == "" {
		attemptID = req.TodoID
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{req.TenantID, req.DevelopmentProjectID, req.TodoID, attemptID, req.RepositoryBindingID}, "\x00")))
	return "workspace:" + hex.EncodeToString(digest[:16])
}

func safeSegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(safeSegmentPattern.ReplaceAllString(value, "-"), ".-_")
	if value == "" || value == "." || value == ".." {
		return "item"
	}
	if len(value) > 80 {
		digest := sha256.Sum256([]byte(value))
		value = value[:48] + "-" + hex.EncodeToString(digest[:8])
	}
	return value
}

func validObjectID(value string) bool {
	return objectIDPattern.MatchString(strings.ToLower(strings.TrimSpace(value)))
}

func validGitRefPart(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 200 && !strings.HasPrefix(value, "-") && !strings.Contains(value, "..") &&
		!strings.ContainsAny(value, " ~^:?*[\\\x00\r\n") && !strings.HasSuffix(value, ".") && !strings.HasSuffix(value, ".lock") &&
		!strings.HasPrefix(value, "/") && !strings.HasSuffix(value, "/") && !strings.Contains(value, "//")
}

func validBranchPrefix(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || (strings.HasSuffix(value, "/") && validGitRefPart(strings.TrimSuffix(value, "/")))
}

func pathWithin(root, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(child))
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)))
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(strings.TrimSpace(left)), filepath.Clean(strings.TrimSpace(right))
	if resolved, err := filepath.EvalSymlinks(left); err == nil {
		left = resolved
	}
	if resolved, err := filepath.EvalSymlinks(right); err == nil {
		right = resolved
	}
	return strings.EqualFold(left, right)
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

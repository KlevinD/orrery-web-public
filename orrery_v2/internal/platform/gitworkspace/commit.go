package gitworkspace

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)-----BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|client[_-]?secret|password)\s*[:=]\s*["']?[A-Za-z0-9_./+\-]{20,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
}

func (m *Manager) CreateCommit(ctx context.Context, req CommitRequest) (CommitResult, error) {
	workspace, err := m.Provision(ctx, req.ProvisionRequest)
	if err != nil {
		return CommitResult{}, err
	}
	if strings.TrimSpace(req.PlanID) == "" || strings.TrimSpace(req.PlanDigest) == "" || strings.TrimSpace(req.ExecutionRunID) == "" || strings.TrimSpace(req.AgentID) == "" {
		return CommitResult{}, ErrInvalidRequest
	}
	if m.cfg.RequireSigning && strings.TrimSpace(m.cfg.SigningKey) == "" {
		return CommitResult{}, ErrSigningRequired
	}
	headBefore, err := m.git.output(ctx, workspace.WorktreePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !validObjectID(headBefore) {
		return CommitResult{}, ErrRepository
	}
	currentChanges, err := m.changedFiles(ctx, workspace.WorktreePath, "HEAD")
	if err != nil {
		return CommitResult{}, err
	}
	if len(currentChanges) == 0 {
		return CommitResult{}, fmt.Errorf("%w: no changed files", ErrInvalidRequest)
	}
	changed, err := m.changedFiles(ctx, workspace.WorktreePath, req.BaseCommit)
	if err != nil {
		return CommitResult{}, err
	}
	if err := m.validateChangedFiles(ctx, workspace.WorktreePath, uniqueSorted(append(changed, currentChanges...)), req.AllowedFileScopes, "HEAD", req.BaseCommit); err != nil {
		return CommitResult{}, err
	}
	checkDir, err := resolveSubdirectory(workspace.WorktreePath, req.SourceSubdirectory)
	if err != nil {
		return CommitResult{}, err
	}
	checks := make([]CheckResult, 0, len(req.CheckCommands))
	for _, command := range req.CheckCommands {
		result, checkErr := runCheck(ctx, checkDir, command, m.cfg.CommandTimeout, m.checkHome)
		checks = append(checks, result)
		if checkErr != nil {
			return CommitResult{Checks: checks, HeadBefore: headBefore}, checkErr
		}
	}
	pathspecs := make([]string, 0, len(currentChanges)+2)
	pathspecs = append(pathspecs, "add", "--")
	pathspecs = append(pathspecs, currentChanges...)
	if _, err := m.git.run(ctx, workspace.WorktreePath, nil, pathspecs...); err != nil {
		return CommitResult{}, err
	}
	staged, err := m.git.output(ctx, workspace.WorktreePath, "diff", "--cached", "--name-only", "-z", "HEAD")
	if err != nil || !sameStrings(currentChanges, splitNUL(staged)) {
		return CommitResult{}, fmt.Errorf("%w: staged paths differ from validated paths", ErrWorkspaceDirty)
	}
	message := commitMessage(req)
	authorName := firstNonBlank(req.AuthorName, req.AgentID)
	authorEmail := strings.TrimSpace(req.AuthorEmail)
	if authorEmail == "" {
		authorEmail = safeEmailLocal(req.AgentID) + "@agents.orrery.local"
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + authorName,
		"GIT_AUTHOR_EMAIL=" + authorEmail,
		"GIT_COMMITTER_NAME=" + m.cfg.CommitterName,
		"GIT_COMMITTER_EMAIL=" + m.cfg.CommitterEmail,
	}
	args := []string{"-c", "user.name=" + m.cfg.CommitterName, "-c", "user.email=" + m.cfg.CommitterEmail}
	signatureStatus := "unsigned"
	if key := strings.TrimSpace(m.cfg.SigningKey); key != "" {
		args = append(args, "-c", "gpg.format="+m.cfg.SigningFormat, "-c", "user.signingkey="+key)
		args = append(args, "commit", "-S", "--no-verify", "-m", message)
		signatureStatus = "signed"
	} else {
		args = append(args, "commit", "--no-gpg-sign", "--no-verify", "-m", message)
	}
	if _, err := m.git.run(ctx, workspace.WorktreePath, env, args...); err != nil {
		return CommitResult{}, err
	}
	commitSHA, err := m.git.output(ctx, workspace.WorktreePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !validObjectID(commitSHA) || commitSHA == headBefore {
		return CommitResult{}, ErrVerificationFailed
	}
	commitObject, err := m.git.output(ctx, workspace.WorktreePath, "cat-file", "commit", commitSHA)
	if err != nil {
		return CommitResult{}, ErrVerificationFailed
	}
	hasSignature := strings.HasPrefix(commitObject, "gpgsig ") || strings.Contains(commitObject, "\ngpgsig ")
	if signatureStatus == "signed" && !hasSignature {
		return CommitResult{}, ErrVerificationFailed
	}
	if signatureStatus == "unsigned" && hasSignature {
		signatureStatus = "signed"
	}
	if status, statusErr := m.git.output(ctx, workspace.WorktreePath, "status", "--porcelain=v1", "-z"); statusErr != nil || status != "" {
		return CommitResult{}, fmt.Errorf("%w: uncommitted files remain after controlled commit", ErrWorkspaceDirty)
	}
	result, err := m.commitResult(ctx, workspace.WorktreePath, req.BaseCommit, commitSHA)
	if err != nil {
		return CommitResult{}, err
	}
	result.Message = message
	result.SignatureStatus = signatureStatus
	result.Checks = checks
	result.HeadBefore = headBefore
	result.CreatedAt = m.now().UTC()
	return result, nil
}

func (m *Manager) SnapshotSubmission(ctx context.Context, req SubmissionSnapshotRequest) (SubmissionSnapshot, error) {
	workspace, err := m.Resolve(req.ProvisionRequest)
	if err != nil {
		return SubmissionSnapshot{}, err
	}
	head := strings.ToLower(strings.TrimSpace(req.HeadCommit))
	if !validObjectID(head) {
		return SubmissionSnapshot{}, ErrInvalidRequest
	}
	actual, err := m.git.output(ctx, workspace.WorktreePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || actual != head {
		return SubmissionSnapshot{}, ErrWorkspaceConflict
	}
	commit, err := m.commitResult(ctx, workspace.WorktreePath, req.BaseCommit, head)
	if err != nil {
		return SubmissionSnapshot{}, err
	}
	commitsRaw, err := m.git.output(ctx, workspace.WorktreePath, "rev-list", "--reverse", req.BaseCommit+".."+head)
	if err != nil {
		return SubmissionSnapshot{}, err
	}
	commits := strings.Fields(commitsRaw)
	if len(commits) == 0 {
		return SubmissionSnapshot{}, ErrInvalidRequest
	}
	return SubmissionSnapshot{
		BaseCommit: req.BaseCommit, HeadCommit: head, TreeSHA: commit.TreeSHA,
		CommitSHAs: commits, ChangedFiles: commit.ChangedFiles, Additions: commit.Additions,
		Deletions: commit.Deletions, DiffDigest: commit.DiffDigest,
	}, nil
}

func (m *Manager) changedFiles(ctx context.Context, worktree, base string) ([]string, error) {
	tracked, err := m.git.output(ctx, worktree, "diff", "--name-only", "-z", base)
	if err != nil {
		return nil, err
	}
	staged, err := m.git.output(ctx, worktree, "diff", "--cached", "--name-only", "-z", base)
	if err != nil {
		return nil, err
	}
	untracked, err := m.git.output(ctx, worktree, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	return uniqueSorted(append(append(splitNUL(tracked), splitNUL(staged)...), splitNUL(untracked)...)), nil
}

func (m *Manager) validateChangedFiles(ctx context.Context, worktree string, files, scopes []string, historicalRefs ...string) error {
	if len(scopes) == 0 {
		return fmt.Errorf("%w: no approved file scopes", ErrPolicyViolation)
	}
	normalizedScopes := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = normalizeRepoPath(scope)
		if !validRepoPath(scope) {
			return ErrPolicyViolation
		}
		normalizedScopes = append(normalizedScopes, strings.TrimSuffix(scope, "/"))
	}
	for _, name := range files {
		name = normalizeRepoPath(name)
		if !validRepoPath(name) || !pathAllowed(name, normalizedScopes) || forbiddenPath(name) {
			return fmt.Errorf("%w: changed path is not approved: %s", ErrPolicyViolation, name)
		}
		absolute := filepath.Join(worktree, filepath.FromSlash(name))
		if !pathWithin(worktree, absolute) {
			return ErrPolicyViolation
		}
		stage, err := m.git.output(ctx, worktree, "ls-files", "--stage", "--", name)
		if err != nil {
			return err
		}
		if strings.HasPrefix(stage, "160000 ") || strings.HasPrefix(stage, "120000 ") {
			return fmt.Errorf("%w: submodule or symbolic link changes are not allowed: %s", ErrPolicyViolation, name)
		}
		info, err := os.Lstat(absolute)
		if os.IsNotExist(err) {
			for _, ref := range historicalRefs {
				entry, entryErr := m.git.output(ctx, worktree, "ls-tree", ref, "--", name)
				if entryErr != nil || strings.TrimSpace(entry) == "" {
					continue
				}
				mode := strings.Fields(entry)
				if len(mode) > 0 && (mode[0] == "160000" || mode[0] == "120000") {
					return fmt.Errorf("%w: submodule or symbolic link changes are not allowed: %s", ErrPolicyViolation, name)
				}
				break
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() || info.Size() > m.cfg.MaxFileBytes {
			return fmt.Errorf("%w: unsupported changed file: %s", ErrPolicyViolation, name)
		}
		canonical, err := filepath.EvalSymlinks(absolute)
		if err != nil || !pathWithin(worktree, canonical) {
			return fmt.Errorf("%w: changed file escapes workspace: %s", ErrPolicyViolation, name)
		}
		content, err := os.ReadFile(absolute)
		if err != nil {
			return err
		}
		if bytes.IndexByte(content, 0) >= 0 {
			return fmt.Errorf("%w: binary changes are not allowed: %s", ErrPolicyViolation, name)
		}
		for _, pattern := range secretPatterns {
			if pattern.Match(content) {
				return fmt.Errorf("%w: secret-like content detected: %s", ErrPolicyViolation, name)
			}
		}
	}
	return nil
}

func (m *Manager) commitResult(ctx context.Context, worktree, base, commitSHA string) (CommitResult, error) {
	tree, err := m.git.output(ctx, worktree, "rev-parse", "--verify", commitSHA+"^{tree}")
	if err != nil || !validObjectID(tree) {
		return CommitResult{}, ErrVerificationFailed
	}
	parentsRaw, err := m.git.output(ctx, worktree, "show", "-s", "--format=%P", commitSHA)
	if err != nil {
		return CommitResult{}, err
	}
	filesRaw, err := m.git.output(ctx, worktree, "diff", "--name-only", "-z", base, commitSHA)
	if err != nil {
		return CommitResult{}, err
	}
	numstat, err := m.git.output(ctx, worktree, "diff", "--numstat", base, commitSHA)
	if err != nil {
		return CommitResult{}, err
	}
	additions, deletions := 0, 0
	for _, line := range strings.Split(numstat, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] == "-" || fields[1] == "-" {
			continue
		}
		added, addErr := strconv.Atoi(fields[0])
		deleted, deleteErr := strconv.Atoi(fields[1])
		if addErr == nil && deleteErr == nil {
			additions += added
			deletions += deleted
		}
	}
	diff, err := m.git.output(ctx, worktree, "diff", "--binary", "--full-index", base, commitSHA)
	if err != nil {
		return CommitResult{}, err
	}
	digest := sha256.Sum256([]byte(diff))
	return CommitResult{
		CommitSHA: commitSHA, ParentSHAs: strings.Fields(parentsRaw), TreeSHA: tree,
		ChangedFiles: uniqueSorted(splitNUL(filesRaw)), Additions: additions, Deletions: deletions,
		DiffDigest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func resolveSubdirectory(root, subdirectory string) (string, error) {
	subdirectory = normalizeRepoPath(subdirectory)
	if subdirectory == "" || subdirectory == "." {
		return root, nil
	}
	if !validRepoPath(subdirectory) {
		return "", ErrPolicyViolation
	}
	dir := filepath.Join(root, filepath.FromSlash(subdirectory))
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil || !pathWithin(root, canonical) {
		return "", ErrPolicyViolation
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", ErrPolicyViolation
	}
	return canonical, nil
}

func commitMessage(req CommitRequest) string {
	summary := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(req.Summary, "\r", " "), "\n", " "))
	if summary == "" {
		summary = "apply approved changes"
	}
	if len(summary) > 72 {
		summary = summary[:72]
	}
	return fmt.Sprintf("todo(%s): %s\n\nOrrery-Todo: %s\nOrrery-Plan: %s@%s\nOrrery-Run: %s\nOrrery-Agent: %s",
		safeSegment(req.TodoID), summary, req.TodoID, req.PlanID, req.PlanDigest, req.ExecutionRunID, req.AgentID)
}

func splitNUL(value string) []string {
	if value == "" {
		return nil
	}
	return strings.FieldsFunc(value, func(r rune) bool { return r == 0 })
}

func sameStrings(left, right []string) bool {
	left, right = uniqueSorted(left), uniqueSorted(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func normalizeRepoPath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return ""
	}
	return path.Clean(value)
}

func validRepoPath(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.HasPrefix(value, "../") &&
		!strings.HasPrefix(value, "/") && !strings.Contains(value, ":") && !strings.ContainsAny(value, "\x00\r\n")
}

func pathAllowed(name string, scopes []string) bool {
	for _, scope := range scopes {
		if scope == "*" || name == scope || strings.HasPrefix(name, scope+"/") {
			return true
		}
	}
	return false
}

func forbiddenPath(name string) bool {
	base := strings.ToLower(path.Base(name))
	if base == ".env" || strings.HasPrefix(base, ".env.") || base == "id_rsa" || base == "id_ed25519" || base == ".npmrc" || base == ".pypirc" {
		return true
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".kdbx"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

func safeEmailLocal(value string) string {
	value = strings.ToLower(safeSegment(value))
	if value == "item" {
		return "agent"
	}
	return value
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func scanLines(value string) []string {
	scanner := bufio.NewScanner(strings.NewReader(value))
	var out []string
	for scanner.Scan() {
		out = append(out, scanner.Text())
	}
	return out
}

var _ = time.Time{}

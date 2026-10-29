package gitworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) PreviewMerge(ctx context.Context, req MergePreviewRequest) (MergePreview, error) {
	if !validObjectID(req.HeadCommit) || !validObjectID(req.ExpectedBaseHead) ||
		(req.MergeStrategy != MergeStrategySquash && req.MergeStrategy != MergeStrategyMergeCommit) {
		return MergePreview{}, ErrInvalidRequest
	}
	workspace, err := m.Provision(ctx, req.ProvisionRequest)
	if err != nil {
		return MergePreview{}, err
	}
	for _, commit := range []string{req.HeadCommit, req.ExpectedBaseHead} {
		if err := m.ensureCommit(ctx, workspace.MirrorPath, commit); err != nil {
			return MergePreview{}, ErrWorkspaceConflict
		}
	}
	previewID := sha256.Sum256([]byte(req.ExpectedBaseHead + "\x00" + req.HeadCommit + "\x00" + req.MergeStrategy))
	previewPath := filepath.Join(filepath.Dir(filepath.Dir(workspace.WorktreePath)), ".previews", hex.EncodeToString(previewID[:12]))
	if !pathWithin(m.root, previewPath) {
		return MergePreview{}, ErrPolicyViolation
	}
	if err := mkdirContained(m.root, filepath.Dir(previewPath)); err != nil {
		return MergePreview{}, err
	}
	if _, statErr := os.Stat(previewPath); statErr == nil {
		_, _ = m.git.run(ctx, m.root, nil, "--git-dir", workspace.MirrorPath, "worktree", "remove", "--force", previewPath)
	}
	if _, err := m.git.run(ctx, m.root, nil, "--git-dir", workspace.MirrorPath, "worktree", "add", "--detach", "--", previewPath, req.ExpectedBaseHead); err != nil {
		return MergePreview{}, err
	}
	defer func() {
		_, _ = m.git.run(context.WithoutCancel(ctx), m.root, nil, "--git-dir", workspace.MirrorPath, "worktree", "remove", "--force", previewPath)
	}()
	mergeArgs := []string{"merge", "--no-commit", "--no-ff", req.HeadCommit}
	if req.MergeStrategy == MergeStrategySquash {
		mergeArgs = []string{"merge", "--squash", "--no-commit", req.HeadCommit}
	}
	mergeEnv := []string{
		"GIT_AUTHOR_NAME=" + m.cfg.CommitterName, "GIT_AUTHOR_EMAIL=" + m.cfg.CommitterEmail,
		"GIT_COMMITTER_NAME=" + m.cfg.CommitterName, "GIT_COMMITTER_EMAIL=" + m.cfg.CommitterEmail,
	}
	if _, err := m.git.run(ctx, previewPath, mergeEnv, mergeArgs...); err != nil {
		conflicts, _ := m.git.output(ctx, previewPath, "diff", "--name-only", "--diff-filter=U")
		if strings.TrimSpace(conflicts) != "" {
			return MergePreview{}, fmt.Errorf("%w: %s", ErrMergeConflict, strings.Join(strings.Fields(conflicts), ", "))
		}
		return MergePreview{}, err
	}
	tree, err := m.git.output(ctx, previewPath, "write-tree")
	if err != nil || !validObjectID(tree) {
		return MergePreview{}, ErrVerificationFailed
	}
	checkDir, err := resolveSubdirectory(previewPath, req.SourceSubdirectory)
	if err != nil {
		return MergePreview{}, err
	}
	checks := make([]CheckResult, 0, len(req.CheckCommands))
	for _, command := range req.CheckCommands {
		result, checkErr := runCheck(ctx, checkDir, command, m.cfg.CommandTimeout, m.checkHome)
		checks = append(checks, result)
		if checkErr != nil {
			return MergePreview{ExpectedBaseHead: req.ExpectedBaseHead, HeadCommit: req.HeadCommit, TreeSHA: tree, MergeStrategy: req.MergeStrategy, Checks: checks}, checkErr
		}
	}
	return MergePreview{ExpectedBaseHead: req.ExpectedBaseHead, HeadCommit: req.HeadCommit, TreeSHA: tree, MergeStrategy: req.MergeStrategy, Checks: checks}, nil
}

func (m *Manager) MergeAuthoritative(ctx context.Context, req MergeRequest) (MergeReceipt, error) {
	if req.SourceKind != SourceManagedBare {
		return MergeReceipt{}, ErrAuthoritativeSource
	}
	if !validObjectID(req.HeadCommit) || !validObjectID(req.ExpectedBaseHead) || !validObjectID(req.ExpectedTreeSHA) {
		return MergeReceipt{}, ErrInvalidRequest
	}
	_, err := m.Provision(ctx, req.ProvisionRequest)
	if err != nil {
		return MergeReceipt{}, err
	}
	current, err := m.git.output(ctx, m.root, "--git-dir", req.SourcePath, "rev-parse", "--verify", "refs/heads/"+req.DefaultBranch+"^{commit}")
	if err != nil {
		return MergeReceipt{}, err
	}
	if current != req.ExpectedBaseHead {
		return MergeReceipt{}, ErrTargetMoved
	}
	preview, err := m.PreviewMerge(ctx, MergePreviewRequest{
		ProvisionRequest: req.ProvisionRequest, HeadCommit: req.HeadCommit, ExpectedBaseHead: req.ExpectedBaseHead,
		MergeStrategy: req.MergeStrategy,
	})
	if err != nil {
		return MergeReceipt{}, err
	}
	if preview.TreeSHA != req.ExpectedTreeSHA {
		return MergeReceipt{}, ErrVerificationFailed
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		message = "Merge Orrery Todo " + req.TodoID
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + firstNonBlank(req.ActorName, "Orrery Merge Approver"),
		"GIT_AUTHOR_EMAIL=" + firstNonBlank(req.ActorEmail, "merge-approver@orrery.local"),
		"GIT_COMMITTER_NAME=" + m.cfg.CommitterName,
		"GIT_COMMITTER_EMAIL=" + m.cfg.CommitterEmail,
	}
	args := []string{"--git-dir", req.SourcePath, "commit-tree", req.ExpectedTreeSHA, "-p", req.ExpectedBaseHead}
	if req.MergeStrategy == MergeStrategyMergeCommit {
		args = append(args, "-p", req.HeadCommit)
	}
	if key := strings.TrimSpace(m.cfg.SigningKey); key != "" {
		args = append([]string{"-c", "gpg.format=" + m.cfg.SigningFormat, "-c", "user.signingkey=" + key}, args...)
		args = append(args, "-S")
	} else if m.cfg.RequireSigning {
		return MergeReceipt{}, ErrSigningRequired
	}
	args = append(args, "-m", message)
	created, err := m.git.run(ctx, m.root, env, args...)
	if err != nil || !validObjectID(created.stdout) {
		return MergeReceipt{}, errors.Join(ErrVerificationFailed, err)
	}
	mergeSHA := created.stdout
	commitObject, err := m.git.output(ctx, m.root, "--git-dir", req.SourcePath, "cat-file", "commit", mergeSHA)
	if err != nil {
		return MergeReceipt{}, ErrVerificationFailed
	}
	hasSignature := strings.HasPrefix(commitObject, "gpgsig ") || strings.Contains(commitObject, "\ngpgsig ")
	if strings.TrimSpace(m.cfg.SigningKey) != "" && !hasSignature {
		return MergeReceipt{}, ErrVerificationFailed
	}
	if _, err := m.git.run(ctx, m.root, nil, "--git-dir", req.SourcePath, "update-ref", "refs/heads/"+req.DefaultBranch, mergeSHA, req.ExpectedBaseHead); err != nil {
		latest, _ := m.git.output(ctx, m.root, "--git-dir", req.SourcePath, "rev-parse", "--verify", "refs/heads/"+req.DefaultBranch+"^{commit}")
		if latest != req.ExpectedBaseHead {
			return MergeReceipt{}, ErrTargetMoved
		}
		return MergeReceipt{}, err
	}
	finalSHA, err := m.git.output(ctx, m.root, "--git-dir", req.SourcePath, "rev-parse", "--verify", "refs/heads/"+req.DefaultBranch+"^{commit}")
	if err != nil || finalSHA != mergeSHA {
		return MergeReceipt{}, ErrVerificationFailed
	}
	finalTree, err := m.git.output(ctx, m.root, "--git-dir", req.SourcePath, "rev-parse", "--verify", finalSHA+"^{tree}")
	if err != nil || finalTree != req.ExpectedTreeSHA {
		return MergeReceipt{}, ErrVerificationFailed
	}
	return MergeReceipt{MergeCommitSHA: mergeSHA, TreeSHA: finalTree, TargetHead: finalSHA, MergedAt: m.now().UTC()}, nil
}

func (m *Manager) PushTodoBranch(ctx context.Context, req ProvisionRequest, headCommit string) error {
	if req.SourceKind != SourceManagedBare || !validObjectID(headCommit) {
		return ErrAuthoritativeSource
	}
	workspace, err := m.Resolve(req)
	if err != nil {
		return err
	}
	branch := todoBranch(req)
	refspec := headCommit + ":refs/heads/" + branch
	if _, err := m.git.run(ctx, m.root, nil, "--git-dir", workspace.MirrorPath, "push", "--porcelain", "--", req.SourcePath, refspec); err != nil {
		return err
	}
	remoteHead, err := m.git.output(ctx, m.root, "--git-dir", req.SourcePath, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil || remoteHead != headCommit {
		return ErrVerificationFailed
	}
	return nil
}

func (m *Manager) Export(ctx context.Context, req ProvisionRequest, baseCommit, headCommit string) (ExportResult, error) {
	workspace, err := m.Resolve(req)
	if err != nil {
		return ExportResult{}, err
	}
	if !validObjectID(baseCommit) || !validObjectID(headCommit) {
		return ExportResult{}, ErrInvalidRequest
	}
	exports := filepath.Join(filepath.Dir(workspace.WorktreePath), "exports")
	if !pathWithin(m.root, exports) {
		return ExportResult{}, ErrPolicyViolation
	}
	if err := mkdirContained(m.root, exports); err != nil {
		return ExportResult{}, err
	}
	name := safeSegment(req.TodoID) + "-" + headCommit[:12]
	patchPath := filepath.Join(exports, name+".patch")
	bundlePath := filepath.Join(exports, name+".bundle")
	patch, err := m.git.output(ctx, workspace.WorktreePath, "format-patch", "--stdout", "--binary", baseCommit+".."+headCommit)
	if err != nil {
		return ExportResult{}, err
	}
	if err := os.WriteFile(patchPath, []byte(patch), 0o600); err != nil {
		return ExportResult{}, err
	}
	branchRef := "refs/heads/" + workspace.BranchName
	branchHead, err := m.git.output(ctx, workspace.WorktreePath, "rev-parse", "--verify", branchRef+"^{commit}")
	if err != nil || branchHead != headCommit {
		_ = os.Remove(patchPath)
		return ExportResult{}, ErrWorkspaceConflict
	}
	if _, err := m.git.run(ctx, workspace.WorktreePath, nil, "bundle", "create", bundlePath, branchRef, "^"+baseCommit); err != nil {
		_ = os.Remove(patchPath)
		return ExportResult{}, err
	}
	return ExportResult{PatchPath: patchPath, BundlePath: bundlePath}, nil
}

func (m *Manager) OpenExport(ctx context.Context, req ProvisionRequest, baseCommit, headCommit, kind string) (ExportFile, error) {
	result, err := m.Export(ctx, req, baseCommit, headCommit)
	if err != nil {
		return ExportFile{}, err
	}
	var path, contentType string
	switch strings.TrimSpace(kind) {
	case "patch":
		path, contentType = result.PatchPath, "text/x-patch; charset=utf-8"
	case "bundle":
		path, contentType = result.BundlePath, "application/octet-stream"
	default:
		return ExportFile{}, ErrInvalidRequest
	}
	if !pathWithin(m.root, path) {
		return ExportFile{}, ErrPolicyViolation
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ExportFile{}, ErrPolicyViolation
	}
	file, err := os.Open(path)
	if err != nil {
		return ExportFile{}, err
	}
	return ExportFile{Name: filepath.Base(path), ContentType: contentType, Size: info.Size(), Reader: file}, nil
}

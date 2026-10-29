package main

import (
	"context"
	"errors"
	"path/filepath"

	"orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/platform/gitworkspace"
)

type productionGitWorkspace struct{ manager *gitworkspace.Manager }

func (p productionGitWorkspace) Provision(ctx context.Context, req developmentproject.GitProvisionRequest) (developmentproject.GitWorkspaceResult, error) {
	result, err := p.manager.Provision(ctx, mapProvisionRequest(req))
	return developmentproject.GitWorkspaceResult{StorageKey: result.StorageKey, BranchName: result.BranchName, BaseCommit: result.BaseCommit, HeadCommit: result.HeadCommit, WorktreePath: result.WorktreePath}, mapGitWorkspaceError(err)
}

func (p productionGitWorkspace) Resolve(ctx context.Context, req developmentproject.GitProvisionRequest) (developmentproject.GitWorkspaceResult, error) {
	result, err := p.manager.Resolve(mapProvisionRequest(req))
	return developmentproject.GitWorkspaceResult{StorageKey: result.StorageKey, BranchName: result.BranchName, BaseCommit: result.BaseCommit, HeadCommit: result.HeadCommit, WorktreePath: result.WorktreePath}, mapGitWorkspaceError(err)
}

func (p productionGitWorkspace) CreateCommit(ctx context.Context, req developmentproject.GitCommitRequest) (developmentproject.GitCommitResult, error) {
	result, err := p.manager.CreateCommit(ctx, gitworkspace.CommitRequest{
		ProvisionRequest: mapProvisionRequest(req.GitProvisionRequest), PlanID: req.PlanID, PlanDigest: req.PlanDigest,
		ExecutionRunID: req.ExecutionRunID, AgentID: req.AgentID, AuthorName: req.AuthorName, AuthorEmail: req.AuthorEmail,
		Summary: req.Summary, AllowedFileScopes: req.AllowedFileScopes, SourceSubdirectory: req.SourceSubdirectory, CheckCommands: req.CheckCommands,
	})
	return developmentproject.GitCommitResult{
		CommitSHA: result.CommitSHA, ParentSHAs: result.ParentSHAs, TreeSHA: result.TreeSHA, Message: result.Message,
		SignatureStatus: result.SignatureStatus, ChangedFiles: result.ChangedFiles, Additions: result.Additions,
		Deletions: result.Deletions, DiffDigest: result.DiffDigest, Checks: mapCheckEvidence(result.Checks), HeadBefore: result.HeadBefore, CreatedAt: result.CreatedAt,
	}, mapGitWorkspaceError(err)
}

func (p productionGitWorkspace) SnapshotSubmission(ctx context.Context, req developmentproject.GitSubmissionSnapshotRequest) (developmentproject.GitSubmissionSnapshot, error) {
	result, err := p.manager.SnapshotSubmission(ctx, gitworkspace.SubmissionSnapshotRequest{ProvisionRequest: mapProvisionRequest(req.GitProvisionRequest), HeadCommit: req.HeadCommit})
	return developmentproject.GitSubmissionSnapshot{BaseCommit: result.BaseCommit, HeadCommit: result.HeadCommit, TreeSHA: result.TreeSHA, CommitSHAs: result.CommitSHAs, ChangedFiles: result.ChangedFiles, Additions: result.Additions, Deletions: result.Deletions, DiffDigest: result.DiffDigest}, mapGitWorkspaceError(err)
}

func (p productionGitWorkspace) FetchBaseHead(ctx context.Context, req developmentproject.GitProvisionRequest) (string, error) {
	value, err := p.manager.FetchBaseHead(ctx, mapProvisionRequest(req))
	return value, mapGitWorkspaceError(err)
}

func (p productionGitWorkspace) PushTodoBranch(ctx context.Context, req developmentproject.GitProvisionRequest, head string) error {
	return mapGitWorkspaceError(p.manager.PushTodoBranch(ctx, mapProvisionRequest(req), head))
}

func (p productionGitWorkspace) PreviewMerge(ctx context.Context, req developmentproject.GitMergePreviewRequest) (developmentproject.GitMergePreview, error) {
	result, err := p.manager.PreviewMerge(ctx, gitworkspace.MergePreviewRequest{
		ProvisionRequest: mapProvisionRequest(req.GitProvisionRequest), HeadCommit: req.HeadCommit,
		ExpectedBaseHead: req.ExpectedBaseHead, MergeStrategy: req.MergeStrategy,
		SourceSubdirectory: req.SourceSubdirectory, CheckCommands: req.CheckCommands,
	})
	return developmentproject.GitMergePreview{ExpectedBaseHead: result.ExpectedBaseHead, HeadCommit: result.HeadCommit, TreeSHA: result.TreeSHA, MergeStrategy: result.MergeStrategy, Checks: mapCheckEvidence(result.Checks)}, mapGitWorkspaceError(err)
}

func (p productionGitWorkspace) MergeAuthoritative(ctx context.Context, req developmentproject.GitMergeRequest) (developmentproject.GitMergeReceipt, error) {
	result, err := p.manager.MergeAuthoritative(ctx, gitworkspace.MergeRequest{
		ProvisionRequest: mapProvisionRequest(req.GitProvisionRequest), HeadCommit: req.HeadCommit,
		ExpectedBaseHead: req.ExpectedBaseHead, ExpectedTreeSHA: req.ExpectedTreeSHA, MergeStrategy: req.MergeStrategy,
		Message: req.Message, ActorName: req.ActorName, ActorEmail: req.ActorEmail,
	})
	return developmentproject.GitMergeReceipt{MergeCommitSHA: result.MergeCommitSHA, TreeSHA: result.TreeSHA, TargetHead: result.TargetHead, MergedAt: result.MergedAt}, mapGitWorkspaceError(err)
}

func (p productionGitWorkspace) Export(ctx context.Context, req developmentproject.GitProvisionRequest, base, head string) (developmentproject.GitExportResult, error) {
	result, err := p.manager.Export(ctx, mapProvisionRequest(req), base, head)
	return developmentproject.GitExportResult{PatchRef: filepath.Base(result.PatchPath), BundleRef: filepath.Base(result.BundlePath)}, mapGitWorkspaceError(err)
}

func (p productionGitWorkspace) OpenExport(ctx context.Context, req developmentproject.GitProvisionRequest, base, head, kind string) (developmentproject.GitExportFile, error) {
	result, err := p.manager.OpenExport(ctx, mapProvisionRequest(req), base, head, kind)
	return developmentproject.GitExportFile{Name: result.Name, ContentType: result.ContentType, Size: result.Size, Reader: result.Reader}, mapGitWorkspaceError(err)
}

func mapProvisionRequest(req developmentproject.GitProvisionRequest) gitworkspace.ProvisionRequest {
	return gitworkspace.ProvisionRequest{
		TenantID: req.TenantID, DevelopmentProjectID: req.DevelopmentProjectID, TodoID: req.TodoID, ExecutionAttemptID: req.ExecutionAttemptID,
		RepositoryBindingID: req.RepositoryBindingID, SourcePath: req.SourcePath, SourceKind: req.SourceKind,
		DefaultBranch: req.DefaultBranch, BranchPrefix: req.BranchPrefix, BranchSlug: req.BranchSlug, BaseCommit: req.BaseCommit,
	}
}

func mapCheckEvidence(values []gitworkspace.CheckResult) []developmentproject.CheckEvidence {
	out := make([]developmentproject.CheckEvidence, len(values))
	for index, value := range values {
		out[index] = developmentproject.CheckEvidence{Command: append([]string(nil), value.Command...), ExitCode: value.ExitCode, Succeeded: value.Succeeded, Summary: value.Summary, StartedAt: value.StartedAt, EndedAt: value.EndedAt}
	}
	return out
}

func mapGitWorkspaceError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, gitworkspace.ErrTargetMoved):
		return developmentproject.ErrApprovalStale
	case errors.Is(err, gitworkspace.ErrMergeConflict):
		return developmentproject.ErrConflict
	case errors.Is(err, gitworkspace.ErrPolicyViolation), errors.Is(err, gitworkspace.ErrWorkspaceDirty):
		return developmentproject.ErrRepositoryPolicy
	case errors.Is(err, gitworkspace.ErrAuthoritativeSource):
		return developmentproject.ErrMergeForbidden
	case errors.Is(err, gitworkspace.ErrWorkspaceConflict):
		return developmentproject.ErrWorkspaceConflict
	case errors.Is(err, gitworkspace.ErrInvalidRequest):
		return developmentproject.ErrInvalidRequest
	case errors.Is(err, gitworkspace.ErrVerificationFailed):
		return developmentproject.ErrInvalidState
	default:
		return err
	}
}

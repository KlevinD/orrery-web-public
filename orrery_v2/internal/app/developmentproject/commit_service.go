package developmentproject

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Service) CreateControlledCommit(ctx context.Context, viewer ViewerRef, todoID string, cmd CreateCommitCommand) (TodoCommitRecord, error) {
	if s == nil || s.executionRepo == nil || s.gitWorkspace == nil {
		return TodoCommitRecord{}, ErrDependency
	}
	cmd.IdempotencyKey, cmd.ExecutionRunID = strings.TrimSpace(cmd.IdempotencyKey), strings.TrimSpace(cmd.ExecutionRunID)
	if !validIdempotencyKey(cmd.IdempotencyKey) || !validResourceID(cmd.ExecutionRunID) || strings.TrimSpace(todoID) == "" {
		return TodoCommitRecord{}, ErrInvalidRequest
	}
	recordID := deterministicResourceID("commitrec", viewerTenant(viewer), cmd.IdempotencyKey)
	if existing, err := s.executionRepo.GetCommit(ctx, viewerTenant(viewer), recordID); err == nil {
		if existing.ExecutionRunID != cmd.ExecutionRunID || existing.TodoID != strings.TrimSpace(todoID) {
			return TodoCommitRecord{}, ErrIdempotency
		}
		if err := s.recordExecutionAudit(ctx, existing.TenantID, existing.AuthorID, existing.CommitRecordID, "todo.commit.created", "succeeded"); err != nil {
			return TodoCommitRecord{}, err
		}
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return TodoCommitRecord{}, err
	}
	run, err := s.executionRepo.GetRun(ctx, viewerTenant(viewer), cmd.ExecutionRunID)
	if err != nil {
		return TodoCommitRecord{}, err
	}
	if run.TodoID != strings.TrimSpace(todoID) {
		return TodoCommitRecord{}, ErrNotFound
	}
	if cmd.ExpectedRunVersion <= 0 || run.Version != cmd.ExpectedRunVersion {
		return TodoCommitRecord{}, ErrConflict
	}
	if strings.ToLower(strings.TrimSpace(viewer.ViewerKind)) != "agent" || viewerSubjectID(viewer) != run.AgentID {
		return TodoCommitRecord{}, ErrNotFound
	}
	project, err := s.getAuthorized(ctx, viewer, run.DevelopmentProjectID, "development_project.run")
	if err != nil {
		return TodoCommitRecord{}, err
	}
	workspace, err := s.executionRepo.GetWorkspace(ctx, project.TenantID, run.WorkspaceID)
	if err != nil {
		return TodoCommitRecord{}, err
	}
	if workspace.Status != WorkspaceStatusLeased || workspace.LeaseOwnerRunID != run.ExecutionRunID || workspace.LeaseExpiresAt.Before(s.now()) {
		return TodoCommitRecord{}, ErrWorkspaceLease
	}
	project, binding, request, err := s.executionGitRequest(ctx, project.TenantID, workspace, todoID)
	if err != nil {
		return TodoCommitRecord{}, err
	}
	result, err := s.gitWorkspace.CreateCommit(ctx, GitCommitRequest{
		GitProvisionRequest: request, PlanID: run.PlanID, PlanDigest: run.PlanDigest, ExecutionRunID: run.ExecutionRunID,
		AgentID: run.AgentID, AuthorName: run.AgentID, Summary: cmd.Summary,
		AllowedFileScopes: append([]string(nil), run.AllowedFileScopes...), SourceSubdirectory: binding.SourceSubdirectory,
		CheckCommands: append([]string(nil), run.PlannedCommands...),
	})
	if err != nil {
		expected := run.Version
		run.Status, run.FailureReason, run.UpdatedAt, run.Version = ExecutionRunStatusFailed, publicExecutionFailure(err), s.now(), run.Version+1
		_ = s.executionRepo.UpdateRun(context.WithoutCancel(ctx), run, expected)
		_ = s.recordExecutionAudit(context.WithoutCancel(ctx), project.TenantID, run.AgentID, run.ExecutionRunID, "todo.commit.rejected", "failed")
		return TodoCommitRecord{}, err
	}
	record := TodoCommitRecord{
		TenantID: project.TenantID, CommitRecordID: recordID, DevelopmentProjectID: project.DevelopmentProjectID,
		TodoID: run.TodoID, ExecutionRunID: run.ExecutionRunID, PlanID: run.PlanID, RepositoryBindingID: binding.BindingID,
		CommitSHA: result.CommitSHA, ParentSHAs: append([]string(nil), result.ParentSHAs...), TreeSHA: result.TreeSHA,
		AuthorKind: "agent", AuthorID: run.AgentID, CommitterID: "orrery-git-service", Message: result.Message,
		SignatureStatus: result.SignatureStatus, ChangedFiles: append([]string(nil), result.ChangedFiles...),
		Additions: result.Additions, Deletions: result.Deletions, DiffDigest: result.DiffDigest, CreatedAt: result.CreatedAt,
	}
	created, err := s.executionRepo.CreateCommit(ctx, record)
	if err != nil {
		return TodoCommitRecord{}, err
	}
	if !created {
		existing, getErr := s.executionRepo.GetCommit(ctx, project.TenantID, recordID)
		if getErr != nil {
			return TodoCommitRecord{}, getErr
		}
		if existing.CommitSHA != record.CommitSHA {
			return TodoCommitRecord{}, ErrIdempotency
		}
		return existing, nil
	}
	expectedRun := run.Version
	run.Status, run.CommitAfter, run.TestResults, run.UpdatedAt, run.Version = ExecutionRunStatusReview, result.CommitSHA, cloneChecks(result.Checks), s.now(), run.Version+1
	run.DiffSummary = fmt.Sprintf("%d files, +%d/-%d, %s", len(result.ChangedFiles), result.Additions, result.Deletions, result.DiffDigest)
	if err := s.executionRepo.UpdateRun(ctx, run, expectedRun); err != nil {
		return TodoCommitRecord{}, err
	}
	expectedWorkspace := workspace.Version
	workspace.HeadCommit, workspace.Status, workspace.LastActiveAt, workspace.Version = result.CommitSHA, WorkspaceStatusReview, s.now(), workspace.Version+1
	workspace.LeaseOwnerRunID, workspace.LeaseExpiresAt = "", time.Time{}
	if err := s.executionRepo.UpdateWorkspace(ctx, workspace, expectedWorkspace); err != nil {
		return TodoCommitRecord{}, err
	}
	if err := s.recordExecutionAudit(ctx, project.TenantID, run.AgentID, record.CommitRecordID, "todo.commit.created", "succeeded"); err != nil {
		return TodoCommitRecord{}, err
	}
	return record, nil
}

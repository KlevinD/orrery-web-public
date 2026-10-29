package developmentproject

import (
	"context"
	"errors"
	"strings"
)

func (s *Service) CreateCodeSubmission(ctx context.Context, viewer ViewerRef, cmd CreateSubmissionCommand) (TodoCodeSubmission, error) {
	if s == nil || s.executionRepo == nil || s.gitWorkspace == nil {
		return TodoCodeSubmission{}, ErrDependency
	}
	cmd.IdempotencyKey, cmd.TodoID, cmd.ExecutionRunID, cmd.HeadCommit = strings.TrimSpace(cmd.IdempotencyKey), strings.TrimSpace(cmd.TodoID), strings.TrimSpace(cmd.ExecutionRunID), strings.TrimSpace(cmd.HeadCommit)
	if !validIdempotencyKey(cmd.IdempotencyKey) || !validResourceID(cmd.TodoID) || !validResourceID(cmd.ExecutionRunID) || !validGitObjectID(cmd.HeadCommit) {
		return TodoCodeSubmission{}, ErrInvalidRequest
	}
	if cmd.MergeStrategy == "" {
		cmd.MergeStrategy = "squash"
	}
	if cmd.MergeStrategy != "squash" && cmd.MergeStrategy != "merge_commit" {
		return TodoCodeSubmission{}, ErrInvalidRequest
	}
	operation, replay, err := s.beginSubmissionMutation(ctx, viewerTenant(viewer), "create", cmd.IdempotencyKey, cmd)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if replay != nil {
		return *replay, nil
	}
	completed := false
	defer func() {
		if !completed {
			_ = s.failSubmissionOperation(context.WithoutCancel(ctx), operation)
		}
	}()
	run, err := s.executionRepo.GetRun(ctx, viewerTenant(viewer), cmd.ExecutionRunID)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if run.TodoID != cmd.TodoID || run.CommitAfter != cmd.HeadCommit || run.Status != ExecutionRunStatusReview {
		return TodoCodeSubmission{}, ErrInvalidState
	}
	project, err := s.getAuthorized(ctx, viewer, run.DevelopmentProjectID, "development_project.submit")
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	workspace, err := s.executionRepo.GetWorkspace(ctx, project.TenantID, run.WorkspaceID)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	_, binding, request, err := s.executionGitRequest(ctx, project.TenantID, workspace, cmd.TodoID)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	snapshot, err := s.gitWorkspace.SnapshotSubmission(ctx, GitSubmissionSnapshotRequest{GitProvisionRequest: request, HeadCommit: cmd.HeadCommit})
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	submissionID := deterministicResourceID("submission", project.TenantID, cmd.TodoID+"\x00"+workspace.WorkspaceID+"\x00"+binding.BindingID)
	now := s.now()
	existing, getErr := s.executionRepo.GetSubmission(ctx, project.TenantID, submissionID)
	if getErr == nil {
		if existing.WorkspaceID != workspace.WorkspaceID || existing.RepositoryBindingID != binding.BindingID || existing.PlanDigest != run.PlanDigest {
			return TodoCodeSubmission{}, ErrIdempotency
		}
		if existing.HeadCommit == snapshot.HeadCommit && existing.DiffDigest == snapshot.DiffDigest {
			if err := s.stageSubmissionOperation(ctx, &operation, existing, viewerSubjectID(viewer), "todo.code_submission.created"); err != nil {
				return TodoCodeSubmission{}, err
			}
			if err := s.completeSubmissionOperation(ctx, &operation, existing); err != nil {
				return TodoCodeSubmission{}, err
			}
			completed = true
			return cloneSubmission(existing), nil
		}
		if existing.Status == SubmissionStatusMerged || existing.Status == SubmissionStatusClosed || existing.Status == SubmissionStatusMerging {
			return TodoCodeSubmission{}, ErrInvalidState
		}
		expected := existing.Version
		appendSubmissionRevision(&existing, now)
		if existing.MergeApproval != nil && existing.MergeApproval.Status == MergeApprovalStatusApproved {
			existing.MergeApproval.Status, existing.MergeApproval.StaleAt = MergeApprovalStatusStale, now
		}
		existing.Revision++
		existing.Version++
		existing.HeadCommit, existing.TreeSHA, existing.CommitSHAs = snapshot.HeadCommit, snapshot.TreeSHA, append([]string(nil), snapshot.CommitSHAs...)
		existing.DiffDigest, existing.ChangedFiles, existing.Additions, existing.Deletions = snapshot.DiffDigest, append([]string(nil), snapshot.ChangedFiles...), snapshot.Additions, snapshot.Deletions
		existing.Status, existing.ExpectedBaseHead, existing.MergePreviewTreeSHA, existing.MergeApproval = SubmissionStatusLocalCandidate, "", "", nil
		existing.RequiredChecksSnapshot, existing.ReviewComment, existing.UpdatedAt = nil, "", now
		if err := s.executionRepo.UpdateSubmission(ctx, existing, expected); err != nil {
			return TodoCodeSubmission{}, err
		}
		if err := s.stageSubmissionOperation(ctx, &operation, existing, viewerSubjectID(viewer), "todo.code_submission.created"); err != nil {
			return TodoCodeSubmission{}, err
		}
		if err := s.completeSubmissionOperation(ctx, &operation, existing); err != nil {
			return TodoCodeSubmission{}, err
		}
		completed = true
		return cloneSubmission(existing), nil
	}
	if !errors.Is(getErr, ErrNotFound) {
		return TodoCodeSubmission{}, getErr
	}
	submission := TodoCodeSubmission{
		TenantID: project.TenantID, SubmissionID: submissionID, Revision: 1, Version: 1, DevelopmentProjectID: project.DevelopmentProjectID,
		TodoID: cmd.TodoID, PlanID: run.PlanID, PlanDigest: run.PlanDigest, WorkspaceID: workspace.WorkspaceID,
		RepositoryBindingID: binding.BindingID, BranchName: workspace.BranchName, TargetBranch: binding.DefaultBranch,
		BaseCommit: snapshot.BaseCommit, HeadCommit: snapshot.HeadCommit, TreeSHA: snapshot.TreeSHA, CommitSHAs: append([]string(nil), snapshot.CommitSHAs...),
		DiffDigest: snapshot.DiffDigest, ChangedFiles: append([]string(nil), snapshot.ChangedFiles...), Additions: snapshot.Additions, Deletions: snapshot.Deletions,
		Status: SubmissionStatusLocalCandidate, MergeStrategy: cmd.MergeStrategy, CreatedBy: viewerSubjectID(viewer), CreatedAt: now, UpdatedAt: now,
	}
	created, err := s.executionRepo.CreateSubmission(ctx, submission)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if !created {
		existing, getErr := s.executionRepo.GetSubmission(ctx, project.TenantID, submissionID)
		if getErr != nil {
			return TodoCodeSubmission{}, getErr
		}
		if existing.HeadCommit != submission.HeadCommit || existing.PlanDigest != submission.PlanDigest {
			return TodoCodeSubmission{}, ErrIdempotency
		}
		submission = existing
	}
	if err := s.stageSubmissionOperation(ctx, &operation, submission, viewerSubjectID(viewer), "todo.code_submission.created"); err != nil {
		return TodoCodeSubmission{}, err
	}
	if err := s.completeSubmissionOperation(ctx, &operation, submission); err != nil {
		return TodoCodeSubmission{}, err
	}
	completed = true
	return cloneSubmission(submission), nil
}

func (s *Service) acquireSubmissionOperation(ctx context.Context, tenantID, action, idempotencyKey string, request any) (SubmissionOperation, bool, error) {
	now := s.now()
	operation := SubmissionOperation{
		TenantID: tenantID, OperationID: deterministicResourceID("submissionop", tenantID, action+"\x00"+idempotencyKey),
		Action: action, IdempotencyKey: idempotencyKey, RequestHash: hashValue(request), Status: "processing",
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	return s.executionRepo.AcquireSubmissionOperation(ctx, operation)
}

func (s *Service) stageSubmissionOperation(ctx context.Context, operation *SubmissionOperation, submission TodoCodeSubmission, actorID, action string) error {
	if operation == nil {
		return ErrDependency
	}
	copy := cloneSubmission(submission)
	expected := operation.Version
	operation.Submission = &copy
	operation.Audit = &AuditEvent{
		AuditID: deterministicResourceID("audit", operation.TenantID, operation.OperationID+"\x00"+action), TenantID: operation.TenantID,
		ActorID: strings.TrimSpace(actorID), EntityID: submission.SubmissionID, Action: action, Result: "succeeded", Summary: action, OccurredAt: s.now(),
	}
	operation.UpdatedAt, operation.Version = s.now(), operation.Version+1
	return s.executionRepo.CompleteSubmissionOperation(ctx, *operation, expected)
}

func (s *Service) completeSubmissionOperation(ctx context.Context, operation *SubmissionOperation, submission TodoCodeSubmission) error {
	if operation == nil {
		return ErrDependency
	}
	if operation.Submission == nil {
		if err := s.stageSubmissionOperation(ctx, operation, submission, "", "todo.code_submission.created"); err != nil {
			return err
		}
	}
	if operation.Audit != nil {
		if s.audit == nil {
			return ErrDependency
		}
		if err := s.audit.RecordDevelopmentProjectAudit(ctx, cloneAuditEvent(*operation.Audit)); err != nil {
			return err
		}
	}
	expected := operation.Version
	operation.Status, operation.UpdatedAt, operation.Version = "completed", s.now(), operation.Version+1
	return s.executionRepo.CompleteSubmissionOperation(ctx, *operation, expected)
}

func (s *Service) failSubmissionOperation(ctx context.Context, operation SubmissionOperation) error {
	if operation.Submission != nil {
		return nil
	}
	expected := operation.Version
	operation.Status, operation.FailureReason, operation.UpdatedAt, operation.Version = "failed", "submission operation did not complete", s.now(), operation.Version+1
	return s.executionRepo.CompleteSubmissionOperation(ctx, operation, expected)
}

func (s *Service) beginSubmissionMutation(ctx context.Context, tenantID, action, idempotencyKey string, request any) (SubmissionOperation, *TodoCodeSubmission, error) {
	operation, acquired, err := s.acquireSubmissionOperation(ctx, tenantID, action, idempotencyKey, request)
	if err != nil {
		return SubmissionOperation{}, nil, err
	}
	if acquired {
		return operation, nil, nil
	}
	if operation.Submission == nil {
		return SubmissionOperation{}, nil, ErrConflict
	}
	if operation.Status == "processing" {
		target := cloneSubmission(*operation.Submission)
		current, getErr := s.executionRepo.GetSubmission(ctx, tenantID, target.SubmissionID)
		switch {
		case errors.Is(getErr, ErrNotFound):
			created, createErr := s.executionRepo.CreateSubmission(ctx, target)
			if createErr != nil || !created {
				return SubmissionOperation{}, nil, firstNonNilError(createErr, ErrConflict)
			}
		case getErr != nil:
			return SubmissionOperation{}, nil, getErr
		case current.Version+1 == target.Version:
			if err := s.executionRepo.UpdateSubmission(ctx, target, current.Version); err != nil {
				return SubmissionOperation{}, nil, err
			}
		case current.Version < target.Version:
			return SubmissionOperation{}, nil, ErrConflict
		}
		if err := s.completeSubmissionOperation(ctx, &operation, *operation.Submission); err != nil {
			return SubmissionOperation{}, nil, err
		}
	}
	copy := cloneSubmission(*operation.Submission)
	return operation, &copy, nil
}

func firstNonNilError(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func (s *Service) PushCodeSubmission(ctx context.Context, viewer ViewerRef, cmd SubmissionCommand) (TodoCodeSubmission, error) {
	project, submission, err := s.authorizedSubmission(ctx, viewer, cmd.TodoID, cmd.SubmissionID, "development_project.submit")
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if !validIdempotencyKey(strings.TrimSpace(cmd.IdempotencyKey)) {
		return TodoCodeSubmission{}, ErrInvalidRequest
	}
	operation, replay, err := s.beginSubmissionMutation(ctx, project.TenantID, "push", cmd.IdempotencyKey, cmd)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if replay != nil {
		return *replay, nil
	}
	completed := false
	defer func() {
		if !completed {
			_ = s.failSubmissionOperation(context.WithoutCancel(ctx), operation)
		}
	}()
	if err := validateSubmissionCommand(submission, cmd); err != nil {
		return TodoCodeSubmission{}, err
	}
	if submission.Status != SubmissionStatusLocalCandidate && submission.Status != SubmissionStatusChangesRequested && submission.Status != SubmissionStatusPushPending {
		return TodoCodeSubmission{}, ErrInvalidState
	}
	workspace, err := s.executionRepo.GetWorkspace(ctx, project.TenantID, submission.WorkspaceID)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	_, binding, request, err := s.executionGitRequest(ctx, project.TenantID, workspace, submission.TodoID)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if !binding.SupportsPush || binding.SourceKind != RepositorySourceManagedBare {
		return TodoCodeSubmission{}, ErrMergeForbidden
	}
	if err := s.gitWorkspace.PushTodoBranch(ctx, request, submission.HeadCommit); err != nil {
		return TodoCodeSubmission{}, err
	}
	expected := submission.Version
	submission.Status, submission.RemoteBranchRef, submission.UpdatedAt, submission.Version = SubmissionStatusPushed, "refs/heads/"+submission.BranchName, s.now(), submission.Version+1
	if err := s.stageSubmissionOperation(ctx, &operation, submission, viewerSubjectID(viewer), "todo.code_submission.pushed"); err != nil {
		return TodoCodeSubmission{}, err
	}
	if err := s.executionRepo.UpdateSubmission(ctx, submission, expected); err != nil {
		return TodoCodeSubmission{}, err
	}
	if err := s.completeSubmissionOperation(ctx, &operation, submission); err != nil {
		return TodoCodeSubmission{}, err
	}
	completed = true
	return cloneSubmission(submission), nil
}

func (s *Service) RequestSubmissionChanges(ctx context.Context, viewer ViewerRef, cmd RequestChangesCommand) (TodoCodeSubmission, error) {
	project, submission, err := s.authorizedSubmission(ctx, viewer, cmd.TodoID, cmd.SubmissionID, "development_project.approve")
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if !humanExecutionViewer(viewer) || strings.TrimSpace(cmd.Comment) == "" || len(strings.TrimSpace(cmd.Comment)) > 4000 {
		return TodoCodeSubmission{}, ErrInvalidRequest
	}
	if !validIdempotencyKey(strings.TrimSpace(cmd.IdempotencyKey)) {
		return TodoCodeSubmission{}, ErrInvalidRequest
	}
	operation, replay, err := s.beginSubmissionMutation(ctx, project.TenantID, "request_changes", cmd.IdempotencyKey, cmd)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if replay != nil {
		return *replay, nil
	}
	completed := false
	defer func() {
		if !completed {
			_ = s.failSubmissionOperation(context.WithoutCancel(ctx), operation)
		}
	}()
	if err := validateSubmissionCommand(submission, cmd.SubmissionCommand); err != nil {
		return TodoCodeSubmission{}, err
	}
	if submission.Status == SubmissionStatusMerged || submission.Status == SubmissionStatusClosed {
		return TodoCodeSubmission{}, ErrInvalidState
	}
	expected := submission.Version
	submission.Status, submission.ReviewComment, submission.UpdatedAt, submission.Version = SubmissionStatusChangesRequested, strings.TrimSpace(cmd.Comment), s.now(), submission.Version+1
	if submission.MergeApproval != nil && submission.MergeApproval.Status == MergeApprovalStatusApproved {
		submission.MergeApproval.Status, submission.MergeApproval.StaleAt = MergeApprovalStatusStale, s.now()
	}
	if err := s.stageSubmissionOperation(ctx, &operation, submission, viewerSubjectID(viewer), "todo.pull_request.changes_requested"); err != nil {
		return TodoCodeSubmission{}, err
	}
	if err := s.executionRepo.UpdateSubmission(ctx, submission, expected); err != nil {
		return TodoCodeSubmission{}, err
	}
	if err := s.completeSubmissionOperation(ctx, &operation, submission); err != nil {
		return TodoCodeSubmission{}, err
	}
	completed = true
	return cloneSubmission(submission), nil
}

func (s *Service) CloseCodeSubmission(ctx context.Context, viewer ViewerRef, cmd SubmissionCommand) (TodoCodeSubmission, error) {
	project, submission, err := s.authorizedSubmission(ctx, viewer, cmd.TodoID, cmd.SubmissionID, "development_project.submit")
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if !validIdempotencyKey(strings.TrimSpace(cmd.IdempotencyKey)) {
		return TodoCodeSubmission{}, ErrInvalidRequest
	}
	operation, replay, err := s.beginSubmissionMutation(ctx, project.TenantID, "close", cmd.IdempotencyKey, cmd)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if replay != nil {
		return *replay, nil
	}
	completed := false
	defer func() {
		if !completed {
			_ = s.failSubmissionOperation(context.WithoutCancel(ctx), operation)
		}
	}()
	if err := validateSubmissionCommand(submission, cmd); err != nil {
		return TodoCodeSubmission{}, err
	}
	if submission.Status == SubmissionStatusMerged {
		return TodoCodeSubmission{}, ErrInvalidState
	}
	expected := submission.Version
	submission.Status, submission.UpdatedAt, submission.Version = SubmissionStatusClosed, s.now(), submission.Version+1
	if submission.MergeApproval != nil && submission.MergeApproval.Status == MergeApprovalStatusApproved {
		submission.MergeApproval.Status, submission.MergeApproval.StaleAt = MergeApprovalStatusStale, s.now()
	}
	if err := s.stageSubmissionOperation(ctx, &operation, submission, viewerSubjectID(viewer), "todo.code_submission.closed"); err != nil {
		return TodoCodeSubmission{}, err
	}
	if err := s.executionRepo.UpdateSubmission(ctx, submission, expected); err != nil {
		return TodoCodeSubmission{}, err
	}
	if err := s.completeSubmissionOperation(ctx, &operation, submission); err != nil {
		return TodoCodeSubmission{}, err
	}
	completed = true
	return cloneSubmission(submission), nil
}

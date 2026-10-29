package developmentproject

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Service) PreviewCodeSubmissionMerge(ctx context.Context, viewer ViewerRef, cmd SubmissionCommand) (TodoCodeSubmission, error) {
	project, submission, err := s.authorizedSubmission(ctx, viewer, cmd.TodoID, cmd.SubmissionID, "development_project.merge")
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if !humanExecutionViewer(viewer) {
		return TodoCodeSubmission{}, ErrNotFound
	}
	if !validIdempotencyKey(strings.TrimSpace(cmd.IdempotencyKey)) {
		return TodoCodeSubmission{}, ErrInvalidRequest
	}
	operation, replay, err := s.beginSubmissionMutation(ctx, project.TenantID, "merge_preview", cmd.IdempotencyKey, cmd)
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
	if submission.Status != SubmissionStatusPushed && submission.Status != SubmissionStatusPROpen && submission.Status != SubmissionStatusChecksRunning && submission.Status != SubmissionStatusMergeReady {
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
	if !binding.SupportsMerge || binding.SourceKind != RepositorySourceManagedBare {
		return TodoCodeSubmission{}, ErrMergeForbidden
	}
	baseHead, err := s.gitWorkspace.FetchBaseHead(ctx, request)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	checks := append(append([]string(nil), binding.BuildCommands...), binding.TestCommands...)
	preview, err := s.gitWorkspace.PreviewMerge(ctx, GitMergePreviewRequest{
		GitProvisionRequest: request, HeadCommit: submission.HeadCommit, ExpectedBaseHead: baseHead,
		MergeStrategy: submission.MergeStrategy, SourceSubdirectory: binding.SourceSubdirectory, CheckCommands: checks,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			expected := submission.Version
			appendSubmissionRevision(&submission, s.now())
			submission.Revision++
			submission.Version++
			submission.Status, submission.UpdatedAt = SubmissionStatusConflicted, s.now()
			_ = s.executionRepo.UpdateSubmission(context.WithoutCancel(ctx), submission, expected)
		}
		return TodoCodeSubmission{}, err
	}
	expected := submission.Version
	appendSubmissionRevision(&submission, s.now())
	if submission.MergeApproval != nil && submission.MergeApproval.Status == MergeApprovalStatusApproved {
		submission.MergeApproval.Status, submission.MergeApproval.StaleAt = MergeApprovalStatusStale, s.now()
	}
	submission.Revision++
	submission.Version++
	submission.ExpectedBaseHead, submission.MergePreviewTreeSHA = preview.ExpectedBaseHead, preview.TreeSHA
	submission.RequiredChecksSnapshot, submission.MergeApproval, submission.Status, submission.UpdatedAt = cloneChecks(preview.Checks), nil, SubmissionStatusMergeReady, s.now()
	if err := s.stageSubmissionOperation(ctx, &operation, submission, viewerSubjectID(viewer), "todo.merge_preview.created"); err != nil {
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

func (s *Service) ApproveCodeSubmissionMerge(ctx context.Context, viewer ViewerRef, cmd MergeApprovalCommand) (TodoCodeSubmission, error) {
	project, submission, err := s.authorizedSubmission(ctx, viewer, cmd.TodoID, cmd.SubmissionID, "development_project.merge")
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if !humanExecutionViewer(viewer) {
		return TodoCodeSubmission{}, ErrNotFound
	}
	if !validIdempotencyKey(strings.TrimSpace(cmd.IdempotencyKey)) {
		return TodoCodeSubmission{}, ErrInvalidRequest
	}
	operation, replay, err := s.beginSubmissionMutation(ctx, project.TenantID, "merge_approval", cmd.IdempotencyKey, cmd)
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
	if submission.Status != SubmissionStatusMergeReady || submission.ExpectedBaseHead == "" || submission.MergePreviewTreeSHA == "" {
		return TodoCodeSubmission{}, ErrInvalidState
	}
	if submission.ExpectedBaseHead != strings.TrimSpace(cmd.ExpectedBaseHead) || submission.MergePreviewTreeSHA != strings.TrimSpace(cmd.ExpectedTreeSHA) || submission.DiffDigest != strings.TrimSpace(cmd.DiffDigest) || checksDigest(submission.RequiredChecksSnapshot) != strings.TrimSpace(cmd.ChecksDigest) {
		return TodoCodeSubmission{}, ErrApprovalStale
	}
	approverID := viewerSubjectID(viewer)
	if approverID == submission.CreatedBy {
		return TodoCodeSubmission{}, fmt.Errorf("%w: submitter cannot approve their own merge", ErrMergeForbidden)
	}
	runs, err := s.executionRepo.ListRuns(ctx, project.TenantID)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	for _, run := range runs {
		if run.WorkspaceID == submission.WorkspaceID && run.AgentID == approverID {
			return TodoCodeSubmission{}, fmt.Errorf("%w: execution agent cannot approve merge", ErrMergeForbidden)
		}
	}
	approvalID := deterministicResourceID("mergeapproval", project.TenantID, cmd.IdempotencyKey)
	approval := MergeApproval{
		MergeApprovalID: approvalID, SubmissionID: submission.SubmissionID, SubmissionRevision: submission.Revision,
		ApprovedBy: approverID, TargetBranch: submission.TargetBranch, ExpectedBaseHead: submission.ExpectedBaseHead,
		HeadCommit: submission.HeadCommit, TreeSHA: submission.MergePreviewTreeSHA, DiffDigest: submission.DiffDigest,
		ChecksDigest: checksDigest(submission.RequiredChecksSnapshot), PlanDigest: submission.PlanDigest,
		MergeStrategy: submission.MergeStrategy, Status: MergeApprovalStatusApproved, ApprovedAt: s.now(),
	}
	expected := submission.Version
	submission.MergeApproval, submission.Status, submission.UpdatedAt, submission.Version = &approval, SubmissionStatusMergeApprovalPending, s.now(), submission.Version+1
	if err := s.stageSubmissionOperation(ctx, &operation, submission, approverID, "development_merge.approved"); err != nil {
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

func (s *Service) MergeCodeSubmission(ctx context.Context, viewer ViewerRef, cmd SubmissionCommand) (TodoCodeSubmission, error) {
	project, submission, err := s.authorizedSubmission(ctx, viewer, cmd.TodoID, cmd.SubmissionID, "development_project.merge")
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if !humanExecutionViewer(viewer) {
		return TodoCodeSubmission{}, ErrNotFound
	}
	if submission.MergeReceipt != nil && submission.MergeReceipt.IdempotencyKey == cmd.IdempotencyKey {
		if submission.Revision != cmd.ExpectedRevision || submission.HeadCommit != strings.TrimSpace(cmd.ExpectedHeadCommit) {
			return TodoCodeSubmission{}, ErrIdempotency
		}
		return cloneSubmission(submission), nil
	}
	if operation, operationErr := s.executionRepo.GetMergeOperation(ctx, project.TenantID, cmd.IdempotencyKey); operationErr == nil && operation.Status == "completed" && operation.Receipt != nil {
		if operation.Receipt.SubmissionID != submission.SubmissionID || operation.Receipt.SubmissionRevision != cmd.ExpectedRevision || submission.HeadCommit != strings.TrimSpace(cmd.ExpectedHeadCommit) {
			return TodoCodeSubmission{}, ErrIdempotency
		}
		return s.applyMergeReceiptState(ctx, viewer, submission, *operation.Receipt)
	} else if operationErr != nil && !errors.Is(operationErr, ErrNotFound) {
		return TodoCodeSubmission{}, operationErr
	}
	if err := validateSubmissionCommand(submission, cmd); err != nil {
		return TodoCodeSubmission{}, err
	}
	approval := submission.MergeApproval
	if submission.Status != SubmissionStatusMergeApprovalPending || approval == nil || approval.Status != MergeApprovalStatusApproved || approval.SubmissionRevision != submission.Revision ||
		approval.ExpectedBaseHead != submission.ExpectedBaseHead || approval.HeadCommit != submission.HeadCommit || approval.TreeSHA != submission.MergePreviewTreeSHA || approval.DiffDigest != submission.DiffDigest || approval.ChecksDigest != checksDigest(submission.RequiredChecksSnapshot) {
		return TodoCodeSubmission{}, ErrApprovalStale
	}
	workspace, err := s.executionRepo.GetWorkspace(ctx, project.TenantID, submission.WorkspaceID)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	_, binding, request, err := s.executionGitRequest(ctx, project.TenantID, workspace, submission.TodoID)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if !binding.SupportsMerge || binding.SourceKind != RepositorySourceManagedBare {
		return TodoCodeSubmission{}, ErrMergeForbidden
	}
	operationKey := "merge:" + project.TenantID + ":" + submission.SubmissionID + ":" + fmt.Sprint(submission.Revision) + ":" + approval.MergeApprovalID
	if cmd.IdempotencyKey != operationKey {
		return TodoCodeSubmission{}, fmt.Errorf("%w: merge idempotency key must bind the exact approval", ErrInvalidRequest)
	}
	requestHash := hashValue(struct {
		SubmissionID                       string
		Revision                           int
		ApprovalID, Base, Head, Tree, Diff string
	}{
		submission.SubmissionID, submission.Revision, approval.MergeApprovalID, approval.ExpectedBaseHead, approval.HeadCommit, approval.TreeSHA, approval.DiffDigest,
	})
	operation, acquired, err := s.executionRepo.AcquireMergeOperation(ctx, MergeOperation{TenantID: project.TenantID, IdempotencyKey: operationKey, RequestHash: requestHash, Status: "processing", CreatedAt: s.now(), UpdatedAt: s.now(), Version: 1})
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if !acquired {
		if operation.Status == "completed" && operation.Receipt != nil {
			latest, getErr := s.executionRepo.GetSubmission(ctx, project.TenantID, submission.SubmissionID)
			if getErr != nil {
				return TodoCodeSubmission{}, getErr
			}
			return cloneSubmission(latest), nil
		}
		return TodoCodeSubmission{}, ErrConflict
	}
	expected := submission.Version
	submission.Status, submission.UpdatedAt, submission.Version = SubmissionStatusMerging, s.now(), submission.Version+1
	if err := s.executionRepo.UpdateSubmission(ctx, submission, expected); err != nil {
		operation.Status, operation.FailureReason, operation.UpdatedAt, operation.Version = "failed", "submission changed before merge started", s.now(), operation.Version+1
		_ = s.executionRepo.CompleteMergeOperation(context.WithoutCancel(ctx), operation, 1)
		return TodoCodeSubmission{}, err
	}
	gitReceipt, mergeErr := s.gitWorkspace.MergeAuthoritative(ctx, GitMergeRequest{
		GitProvisionRequest: request, HeadCommit: submission.HeadCommit, ExpectedBaseHead: approval.ExpectedBaseHead,
		ExpectedTreeSHA: approval.TreeSHA, MergeStrategy: approval.MergeStrategy,
		Message: "Merge Todo " + submission.TodoID, ActorName: viewer.ViewerID, ActorEmail: "merge-approver@orrery.local",
	})
	if mergeErr != nil {
		operation.Status, operation.FailureReason, operation.UpdatedAt, operation.Version = "failed", publicExecutionFailure(mergeErr), s.now(), operation.Version+1
		_ = s.executionRepo.CompleteMergeOperation(context.WithoutCancel(ctx), operation, 1)
		expected = submission.Version
		if errors.Is(mergeErr, ErrApprovalStale) {
			submission.Status = SubmissionStatusPushed
			approval.Status, approval.StaleAt = MergeApprovalStatusStale, s.now()
			submission.ExpectedBaseHead, submission.MergePreviewTreeSHA = "", ""
			submission.RequiredChecksSnapshot = nil
		} else {
			submission.Status = SubmissionStatusMergeVerificationFailed
		}
		submission.UpdatedAt, submission.Version = s.now(), submission.Version+1
		_ = s.executionRepo.UpdateSubmission(context.WithoutCancel(ctx), submission, expected)
		return TodoCodeSubmission{}, mergeErr
	}
	receiptID := deterministicResourceID("mergereceipt", project.TenantID, operationKey)
	receipt := DevelopmentMergeReceipt{
		ReceiptID: receiptID, IdempotencyKey: operationKey, SubmissionID: submission.SubmissionID, SubmissionRevision: submission.Revision,
		ApprovalID: approval.MergeApprovalID, MergeCommitSHA: gitReceipt.MergeCommitSHA, TreeSHA: gitReceipt.TreeSHA,
		TargetHead: gitReceipt.TargetHead, MergedBy: viewerSubjectID(viewer), MergedAt: gitReceipt.MergedAt,
	}
	operation.Status, operation.Receipt, operation.UpdatedAt, operation.Version = "completed", &receipt, s.now(), operation.Version+1
	if err := s.executionRepo.CompleteMergeOperation(ctx, operation, 1); err != nil {
		return TodoCodeSubmission{}, err
	}
	return s.applyMergeReceiptState(ctx, viewer, submission, receipt)
}

func (s *Service) applyMergeReceiptState(ctx context.Context, viewer ViewerRef, submission TodoCodeSubmission, receipt DevelopmentMergeReceipt) (TodoCodeSubmission, error) {
	if submission.SubmissionID != receipt.SubmissionID || submission.Revision != receipt.SubmissionRevision {
		return TodoCodeSubmission{}, ErrIdempotency
	}
	if submission.Status != SubmissionStatusMerged || submission.MergeReceipt == nil {
		expected := submission.Version
		if submission.MergeApproval != nil && submission.MergeApproval.MergeApprovalID == receipt.ApprovalID {
			submission.MergeApproval.Status = MergeApprovalStatusConsumed
		}
		submission.Status, submission.MergeReceipt, submission.MergedBy, submission.MergedAt = SubmissionStatusMerged, &receipt, receipt.MergedBy, receipt.MergedAt
		submission.UpdatedAt, submission.Version = s.now(), submission.Version+1
		if err := s.executionRepo.UpdateSubmission(ctx, submission, expected); err != nil {
			return TodoCodeSubmission{}, err
		}
	}
	workspace, err := s.executionRepo.GetWorkspace(ctx, submission.TenantID, submission.WorkspaceID)
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if workspace.Status != WorkspaceStatusMerged || workspace.HeadCommit != receipt.MergeCommitSHA {
		expectedWorkspace := workspace.Version
		workspace.Status, workspace.HeadCommit, workspace.LeaseOwnerRunID, workspace.LeaseExpiresAt = WorkspaceStatusMerged, receipt.MergeCommitSHA, "", time.Time{}
		workspace.LastActiveAt, workspace.CleanupAfter, workspace.Version = s.now(), s.now().Add(s.workspaceRetention), workspace.Version+1
		if err := s.executionRepo.UpdateWorkspace(ctx, workspace, expectedWorkspace); err != nil {
			return TodoCodeSubmission{}, err
		}
	}
	if err := s.recordExecutionAudit(ctx, submission.TenantID, viewerSubjectID(viewer), submission.SubmissionID, "development_merge.completed", "succeeded"); err != nil {
		return TodoCodeSubmission{}, err
	}
	return cloneSubmission(submission), nil
}

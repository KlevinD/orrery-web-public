package developmentproject

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/scmconnection"
)

const executionTestBase = "0123456789012345678901234567890123456789"

type executionTestEnvironment struct {
	testEnvironment
	executions *ExecutionRepository
	git        *fakeGitWorkspace
	project    Project
	snapshot   ExecutionPlanSnapshot
}

func TestExecutionRepositoryIsolatesTenantsAndEnforcesCAS(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	repository := NewExecutionRepository(env.router)
	for _, tenantID := range []string{"tenant-a", "tenant-b"} {
		created, err := repository.CreateWorkspace(context.Background(), DevelopmentWorkspace{
			TenantID: tenantID, WorkspaceID: "workspace-shared", DevelopmentProjectID: "project-a", TodoID: "todo-a",
			RepositoryBindingID: "binding-a", BaseCommit: executionTestBase, Status: WorkspaceStatusReady, Version: 1,
		})
		if err != nil || !created {
			t.Fatalf("CreateWorkspace(%s) = %v, %v", tenantID, created, err)
		}
	}
	a, err := repository.GetWorkspace(context.Background(), "tenant-a", "workspace-shared")
	if err != nil || a.TenantID != "tenant-a" {
		t.Fatalf("GetWorkspace tenant-a = %+v, %v", a, err)
	}
	if _, err := repository.GetWorkspace(context.Background(), "tenant-c", "workspace-shared"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant GetWorkspace error = %v, want ErrNotFound", err)
	}
	a.Status, a.Version = WorkspaceStatusLeased, 2
	if err := repository.UpdateWorkspace(context.Background(), a, 99); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale workspace CAS error = %v, want ErrConflict", err)
	}
	if err := repository.UpdateWorkspace(context.Background(), a, 1); err != nil {
		t.Fatalf("UpdateWorkspace: %v", err)
	}
}

func TestProvisionAndPrepareAgentRunUseApprovedResourceSnapshot(t *testing.T) {
	env := newExecutionTestEnvironment(t)
	defer env.close()
	command := env.provisionCommand("mission-a", []string{"task-a", "task-b"}, []string{"agent-a", "agent-b"})
	result, err := env.service.ProvisionTodoWorkspace(context.Background(), command)
	if err != nil {
		t.Fatalf("ProvisionTodoWorkspace: %v", err)
	}
	if result.Workspace.Status != WorkspaceStatusReady || result.Workspace.LeaseOwnerRunID != "" || len(result.Runs) != 2 {
		t.Fatalf("provision result = %+v", result)
	}
	for _, run := range result.Runs {
		if run.ResourceSnapshotDigest != env.snapshot.SnapshotDigest || len(run.AllowedFileScopes) != 1 || run.AllowedFileScopes[0] != "src" || len(run.PlannedCommands) != 1 || run.PlannedCommands[0] != "go test ./..." {
			t.Fatalf("run does not retain approved policy: %+v", run)
		}
	}
	prepared, found, err := env.service.PrepareAgentRun(context.Background(), env.viewer.TenantID, "mission-a", "task-a", "agent-a", "runtime-a")
	if err != nil || !found || prepared.ExecutionRunID != result.Runs[0].ExecutionRunID || prepared.WorkspaceID != result.Workspace.WorkspaceID {
		t.Fatalf("PrepareAgentRun = %+v, %v, %v", prepared, found, err)
	}
	if _, found, err := env.service.PrepareAgentRun(context.Background(), env.viewer.TenantID, "mission-a", "task-b", "agent-b", "runtime-b"); !found || !errors.Is(err, ErrWorkspaceLease) {
		t.Fatalf("concurrent same-Todo PrepareAgentRun = found %v, err %v; want lease conflict", found, err)
	}
	if err := env.service.FailAgentRun(context.Background(), env.viewer.TenantID, "mission-a", "task-a", "agent-a", "runtime-a", errors.New("runtime failed")); err != nil {
		t.Fatalf("FailAgentRun: %v", err)
	}
	if _, found, err := env.service.PrepareAgentRun(context.Background(), env.viewer.TenantID, "mission-a", "task-b", "agent-b", "runtime-b"); err != nil || !found {
		t.Fatalf("PrepareAgentRun after lease release = found %v, err %v", found, err)
	}
	if _, found, err := env.service.PrepareAgentRun(context.Background(), "tenant-b", "mission-a", "task-a", "agent-a", "runtime-a"); err != nil || found {
		t.Fatalf("cross-tenant PrepareAgentRun = found %v, err %v", found, err)
	}
}

func TestProvisionRejectsChangedRepositorySnapshot(t *testing.T) {
	env := newExecutionTestEnvironment(t)
	defer env.close()
	project, err := env.repo.Get(context.Background(), env.viewer.TenantID, env.project.DevelopmentProjectID)
	if err != nil {
		t.Fatalf("Get project: %v", err)
	}
	expected := project.Version
	project.RepositoryBindings[0].LastFetchedCommit = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	project.RepositoryBindings[0].Version++
	project.Version++
	if err := env.repo.Update(context.Background(), project.TenantID, project, expected); err != nil {
		t.Fatalf("Update project: %v", err)
	}
	if _, err := env.service.ProvisionTodoWorkspace(context.Background(), env.provisionCommand("mission-a", []string{"task-a"}, []string{"agent-a"})); !errors.Is(err, ErrApprovalStale) {
		t.Fatalf("Provision stale resource snapshot error = %v, want ErrApprovalStale", err)
	}
}

func TestProvisionCreatesNewWorkspaceForReopenedTodoMission(t *testing.T) {
	env := newExecutionTestEnvironment(t)
	defer env.close()
	first, err := env.service.ProvisionTodoWorkspace(context.Background(), env.provisionCommand("mission-attempt-1", []string{"task-attempt-1"}, []string{"agent-a"}))
	if err != nil {
		t.Fatalf("Provision first attempt: %v", err)
	}
	second, err := env.service.ProvisionTodoWorkspace(context.Background(), env.provisionCommand("mission-attempt-2", []string{"task-attempt-2"}, []string{"agent-a"}))
	if err != nil {
		t.Fatalf("Provision reopened attempt: %v", err)
	}
	if first.Workspace.WorkspaceID == second.Workspace.WorkspaceID || first.Workspace.ExecutionAttemptID != "mission-attempt-1" || second.Workspace.ExecutionAttemptID != "mission-attempt-2" {
		t.Fatalf("reopened workspaces = first=%+v second=%+v", first.Workspace, second.Workspace)
	}
}

func TestAgentResultCommitIsAuthorizedIdempotentAndUsesApprovedPolicy(t *testing.T) {
	env := newExecutionTestEnvironment(t)
	defer env.close()
	result, err := env.service.ProvisionTodoWorkspace(context.Background(), env.provisionCommand("mission-a", []string{"task-a"}, []string{"agent-a"}))
	if err != nil {
		t.Fatalf("ProvisionTodoWorkspace: %v", err)
	}
	if _, found, err := env.service.PrepareAgentRun(context.Background(), env.viewer.TenantID, "mission-a", "task-a", "agent-a", "runtime-a"); err != nil || !found {
		t.Fatalf("PrepareAgentRun = %v, %v", found, err)
	}
	if _, found, err := env.service.CommitAgentRunResult(context.Background(), env.viewer.TenantID, "mission-a", "task-a", "agent-other", "runtime-a", "done"); !found || !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong Agent commit = found %v, err %v", found, err)
	}
	record, found, err := env.service.CommitAgentRunResult(context.Background(), env.viewer.TenantID, "mission-a", "task-a", "agent-a", "runtime-a", "done")
	if err != nil || !found || record.ExecutionRunID != result.Runs[0].ExecutionRunID {
		t.Fatalf("CommitAgentRunResult = %+v, %v, %v", record, found, err)
	}
	if env.git.commitCalls != 1 || len(env.git.lastCommit.AllowedFileScopes) != 1 || env.git.lastCommit.AllowedFileScopes[0] != "src" || len(env.git.lastCommit.CheckCommands) != 1 || env.git.lastCommit.CheckCommands[0] != "go test ./..." {
		t.Fatalf("controlled commit request = %+v calls=%d", env.git.lastCommit, env.git.commitCalls)
	}
	replayed, found, err := env.service.CommitAgentRunResult(context.Background(), env.viewer.TenantID, "mission-a", "task-a", "agent-a", "runtime-a", "done")
	if err != nil || !found || replayed.CommitRecordID != record.CommitRecordID || env.git.commitCalls != 1 {
		t.Fatalf("idempotent commit replay = %+v, %v, %v calls=%d", replayed, found, err, env.git.commitCalls)
	}
}

func TestCodeSubmissionCreateUsesDurableIdempotencyFingerprint(t *testing.T) {
	env := newExecutionTestEnvironment(t)
	defer env.close()
	result, err := env.service.ProvisionTodoWorkspace(context.Background(), env.provisionCommand("mission-a", []string{"task-a"}, []string{"agent-a"}))
	if err != nil {
		t.Fatalf("ProvisionTodoWorkspace: %v", err)
	}
	if _, found, err := env.service.PrepareAgentRun(context.Background(), env.viewer.TenantID, "mission-a", "task-a", "agent-a", "runtime-a"); err != nil || !found {
		t.Fatalf("PrepareAgentRun = %v, %v", found, err)
	}
	record, _, err := env.service.CommitAgentRunResult(context.Background(), env.viewer.TenantID, "mission-a", "task-a", "agent-a", "runtime-a", "done")
	if err != nil {
		t.Fatalf("CommitAgentRunResult: %v", err)
	}
	command := CreateSubmissionCommand{IdempotencyKey: "submission-create-a", TodoID: "todo-a", ExecutionRunID: result.Runs[0].ExecutionRunID, HeadCommit: record.CommitSHA, MergeStrategy: "squash"}
	first, err := env.service.CreateCodeSubmission(context.Background(), env.viewer, command)
	if err != nil {
		t.Fatalf("CreateCodeSubmission: %v", err)
	}
	second, err := env.service.CreateCodeSubmission(context.Background(), env.viewer, command)
	if err != nil || second.SubmissionID != first.SubmissionID || second.Revision != first.Revision || env.git.snapshotCalls != 1 {
		t.Fatalf("submission replay = %+v, %v snapshots=%d", second, err, env.git.snapshotCalls)
	}
	command.HeadCommit = "9999999999999999999999999999999999999999"
	if _, err := env.service.CreateCodeSubmission(context.Background(), env.viewer, command); !errors.Is(err, ErrIdempotency) {
		t.Fatalf("submission idempotency mismatch error = %v, want ErrIdempotency", err)
	}
}

func TestSubmissionMutationsAreIdempotentAndDoNotRepeatGitOperations(t *testing.T) {
	env := newExecutionTestEnvironment(t)
	defer env.close()
	submission := env.createLocalSubmission(t)
	push := SubmissionCommand{
		IdempotencyKey: "push-idempotent", TodoID: submission.TodoID, SubmissionID: submission.SubmissionID,
		ExpectedVersion: submission.Version, ExpectedRevision: submission.Revision, ExpectedHeadCommit: submission.HeadCommit,
	}
	pushed, err := env.service.PushCodeSubmission(context.Background(), env.viewer, push)
	if err != nil {
		t.Fatalf("PushCodeSubmission: %v", err)
	}
	replayed, err := env.service.PushCodeSubmission(context.Background(), env.viewer, push)
	if err != nil || replayed.Version != pushed.Version || env.git.pushCalls != 1 {
		t.Fatalf("push replay = %+v, %v; push calls=%d", replayed, err, env.git.pushCalls)
	}
	conflicting := push
	conflicting.ExpectedHeadCommit = executionTestBase
	if _, err := env.service.PushCodeSubmission(context.Background(), env.viewer, conflicting); !errors.Is(err, ErrIdempotency) {
		t.Fatalf("push key reuse error = %v, want ErrIdempotency", err)
	}

	previewCommand := SubmissionCommand{
		IdempotencyKey: "preview-idempotent", TodoID: pushed.TodoID, SubmissionID: pushed.SubmissionID,
		ExpectedVersion: pushed.Version, ExpectedRevision: pushed.Revision, ExpectedHeadCommit: pushed.HeadCommit,
	}
	previewed, err := env.service.PreviewCodeSubmissionMerge(context.Background(), env.viewer, previewCommand)
	if err != nil {
		t.Fatalf("PreviewCodeSubmissionMerge: %v", err)
	}
	previewReplay, err := env.service.PreviewCodeSubmissionMerge(context.Background(), env.viewer, previewCommand)
	if err != nil || previewReplay.Version != previewed.Version || env.git.previewCalls != 1 {
		t.Fatalf("preview replay = %+v, %v; preview calls=%d", previewReplay, err, env.git.previewCalls)
	}
}

func TestSubmissionAuditFailureRecoversWithoutRepeatingGitPush(t *testing.T) {
	env := newExecutionTestEnvironment(t)
	defer env.close()
	submission := env.createLocalSubmission(t)
	command := SubmissionCommand{
		IdempotencyKey: "push-audit-recovery", TodoID: submission.TodoID, SubmissionID: submission.SubmissionID,
		ExpectedVersion: submission.Version, ExpectedRevision: submission.Revision, ExpectedHeadCommit: submission.HeadCommit,
	}
	auditFailure := errors.New("audit unavailable")
	env.service.SetAuditRecorder(&auditRecorder{err: auditFailure})
	if _, err := env.service.PushCodeSubmission(context.Background(), env.viewer, command); !errors.Is(err, auditFailure) {
		t.Fatalf("PushCodeSubmission audit error = %v", err)
	}
	recoveredAudit := &auditRecorder{}
	env.service.SetAuditRecorder(recoveredAudit)
	recovered, err := env.service.PushCodeSubmission(context.Background(), env.viewer, command)
	if err != nil || recovered.Status != SubmissionStatusPushed || env.git.pushCalls != 1 || len(recoveredAudit.events) != 1 || recoveredAudit.events[0].AuditID == "" {
		t.Fatalf("recovered push = %+v, %v; calls=%d audits=%+v", recovered, err, env.git.pushCalls, recoveredAudit.events)
	}
}

func TestReviewApprovalAndCloseMutationsReplayOriginalReceipts(t *testing.T) {
	t.Run("request changes and close", func(t *testing.T) {
		env := newExecutionTestEnvironment(t)
		defer env.close()
		submission := env.createLocalSubmission(t)
		request := RequestChangesCommand{SubmissionCommand: SubmissionCommand{
			IdempotencyKey: "changes-idempotent", TodoID: submission.TodoID, SubmissionID: submission.SubmissionID,
			ExpectedVersion: submission.Version, ExpectedRevision: submission.Revision, ExpectedHeadCommit: submission.HeadCommit,
		}, Comment: "Please add a regression test."}
		changed, err := env.service.RequestSubmissionChanges(context.Background(), env.viewer, request)
		if err != nil {
			t.Fatalf("RequestSubmissionChanges: %v", err)
		}
		replayed, err := env.service.RequestSubmissionChanges(context.Background(), env.viewer, request)
		if err != nil || replayed.Version != changed.Version || replayed.ReviewComment != changed.ReviewComment {
			t.Fatalf("request changes replay = %+v, %v", replayed, err)
		}
		closeCommand := SubmissionCommand{
			IdempotencyKey: "close-idempotent", TodoID: changed.TodoID, SubmissionID: changed.SubmissionID,
			ExpectedVersion: changed.Version, ExpectedRevision: changed.Revision, ExpectedHeadCommit: changed.HeadCommit,
		}
		closed, err := env.service.CloseCodeSubmission(context.Background(), env.viewer, closeCommand)
		if err != nil {
			t.Fatalf("CloseCodeSubmission: %v", err)
		}
		closeReplay, err := env.service.CloseCodeSubmission(context.Background(), env.viewer, closeCommand)
		if err != nil || closeReplay.Version != closed.Version || closeReplay.Status != SubmissionStatusClosed {
			t.Fatalf("close replay = %+v, %v", closeReplay, err)
		}
	})

	t.Run("merge approval", func(t *testing.T) {
		env := newExecutionTestEnvironment(t)
		defer env.close()
		submission := env.createPushedSubmission(t)
		stored, err := env.executions.GetSubmission(context.Background(), env.viewer.TenantID, submission.SubmissionID)
		if err != nil {
			t.Fatalf("GetSubmission: %v", err)
		}
		expected := stored.Version
		stored.CreatedBy, stored.Version = "independent-submitter", stored.Version+1
		if err := env.executions.UpdateSubmission(context.Background(), stored, expected); err != nil {
			t.Fatalf("set submitter: %v", err)
		}
		preview, err := env.service.PreviewCodeSubmissionMerge(context.Background(), env.viewer, SubmissionCommand{
			IdempotencyKey: "approval-preview", TodoID: stored.TodoID, SubmissionID: stored.SubmissionID,
			ExpectedVersion: stored.Version, ExpectedRevision: stored.Revision, ExpectedHeadCommit: stored.HeadCommit,
		})
		if err != nil {
			t.Fatalf("PreviewCodeSubmissionMerge: %v", err)
		}
		command := MergeApprovalCommand{SubmissionCommand: SubmissionCommand{
			IdempotencyKey: "approval-idempotent", TodoID: preview.TodoID, SubmissionID: preview.SubmissionID,
			ExpectedVersion: preview.Version, ExpectedRevision: preview.Revision, ExpectedHeadCommit: preview.HeadCommit,
		}, ExpectedBaseHead: preview.ExpectedBaseHead, ExpectedTreeSHA: preview.MergePreviewTreeSHA,
			DiffDigest: preview.DiffDigest, ChecksDigest: checksDigest(preview.RequiredChecksSnapshot)}
		approved, err := env.service.ApproveCodeSubmissionMerge(context.Background(), env.viewer, command)
		if err != nil {
			t.Fatalf("ApproveCodeSubmissionMerge: %v", err)
		}
		replayed, err := env.service.ApproveCodeSubmissionMerge(context.Background(), env.viewer, command)
		if err != nil || replayed.Version != approved.Version || replayed.MergeApproval == nil || replayed.MergeApproval.MergeApprovalID != approved.MergeApproval.MergeApprovalID {
			t.Fatalf("approval replay = %+v, %v", replayed, err)
		}
	})
}

func TestMergeRequiresIndependentHumanApprovalAndInvalidatesMovedTarget(t *testing.T) {
	env := newExecutionTestEnvironment(t)
	defer env.close()
	submission := env.createPushedSubmission(t)
	stored, err := env.executions.GetSubmission(context.Background(), env.viewer.TenantID, submission.SubmissionID)
	if err != nil {
		t.Fatalf("GetSubmission: %v", err)
	}
	expected := stored.Version
	stored.CreatedBy, stored.Version = "submitter-user", stored.Version+1
	if err := env.executions.UpdateSubmission(context.Background(), stored, expected); err != nil {
		t.Fatalf("set independent submitter: %v", err)
	}
	submission = stored
	preview, err := env.service.PreviewCodeSubmissionMerge(context.Background(), env.viewer, SubmissionCommand{
		IdempotencyKey: "preview-a", TodoID: submission.TodoID, SubmissionID: submission.SubmissionID,
		ExpectedVersion: submission.Version, ExpectedRevision: submission.Revision, ExpectedHeadCommit: submission.HeadCommit,
	})
	if err != nil {
		t.Fatalf("PreviewCodeSubmissionMerge: %v", err)
	}
	agentViewer := ViewerRef{ViewerID: "agent-a", ViewerKind: identity.ViewerKindAgent, TenantID: env.viewer.TenantID, AuthorizationSubjectID: "agent-a", PermissionScope: []string{"development.merge"}}
	if err := env.authorizer.WriteRelations(context.Background(), []authorization.RelationTuple{{
		TenantID: env.viewer.TenantID, Subject: authorization.SubjectRef{Kind: identity.ViewerKindAgent, ID: "agent-a"}, Relation: "merger",
		Object: identity.ObjectRef{Type: "development_project", ID: env.project.DevelopmentProjectID, TenantID: env.viewer.TenantID},
	}}); err != nil {
		t.Fatalf("write Agent merger relation: %v", err)
	}
	approvalCommand := MergeApprovalCommand{
		SubmissionCommand: SubmissionCommand{IdempotencyKey: "approval-a", TodoID: preview.TodoID, SubmissionID: preview.SubmissionID, ExpectedVersion: preview.Version, ExpectedRevision: preview.Revision, ExpectedHeadCommit: preview.HeadCommit},
		ExpectedBaseHead:  preview.ExpectedBaseHead, ExpectedTreeSHA: preview.MergePreviewTreeSHA,
		DiffDigest: preview.DiffDigest, ChecksDigest: checksDigest(preview.RequiredChecksSnapshot),
	}
	if _, err := env.service.ApproveCodeSubmissionMerge(context.Background(), agentViewer, approvalCommand); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Agent merge approval error = %v, want fail-closed ErrNotFound", err)
	}
	approved, err := env.service.ApproveCodeSubmissionMerge(context.Background(), env.viewer, approvalCommand)
	if err != nil {
		t.Fatalf("ApproveCodeSubmissionMerge: %v", err)
	}
	if approved.MergeApproval == nil || approved.MergeApproval.ApprovedBy != env.viewer.ViewerID {
		t.Fatalf("merge approval = %+v", approved.MergeApproval)
	}
	env.git.mergeErr = ErrApprovalStale
	mergeKey := fmt.Sprintf("merge:%s:%s:%d:%s", env.viewer.TenantID, approved.SubmissionID, approved.Revision, approved.MergeApproval.MergeApprovalID)
	_, err = env.service.MergeCodeSubmission(context.Background(), env.viewer, SubmissionCommand{
		IdempotencyKey: mergeKey, TodoID: approved.TodoID, SubmissionID: approved.SubmissionID,
		ExpectedVersion: approved.Version, ExpectedRevision: approved.Revision, ExpectedHeadCommit: approved.HeadCommit,
	})
	if !errors.Is(err, ErrApprovalStale) {
		t.Fatalf("MergeCodeSubmission target movement error = %v, want ErrApprovalStale", err)
	}
	latest, err := env.executions.GetSubmission(context.Background(), env.viewer.TenantID, approved.SubmissionID)
	if err != nil {
		t.Fatalf("GetSubmission after target movement: %v", err)
	}
	if latest.Status != SubmissionStatusPushed || latest.ExpectedBaseHead != "" || latest.MergePreviewTreeSHA != "" || latest.MergeApproval == nil || latest.MergeApproval.Status != MergeApprovalStatusStale {
		t.Fatalf("stale merge state = %+v", latest)
	}
}

func TestMergeReceiptIsIdempotentAndVerifiesExactApproval(t *testing.T) {
	env := newExecutionTestEnvironment(t)
	defer env.close()
	submission := env.createPushedSubmission(t)
	stored, _ := env.executions.GetSubmission(context.Background(), env.viewer.TenantID, submission.SubmissionID)
	expected := stored.Version
	stored.CreatedBy, stored.Version = "submitter-user", stored.Version+1
	if err := env.executions.UpdateSubmission(context.Background(), stored, expected); err != nil {
		t.Fatalf("set independent submitter: %v", err)
	}
	preview, err := env.service.PreviewCodeSubmissionMerge(context.Background(), env.viewer, SubmissionCommand{
		IdempotencyKey: "preview-b", TodoID: stored.TodoID, SubmissionID: stored.SubmissionID,
		ExpectedVersion: stored.Version, ExpectedRevision: stored.Revision, ExpectedHeadCommit: stored.HeadCommit,
	})
	if err != nil {
		t.Fatalf("PreviewCodeSubmissionMerge: %v", err)
	}
	approved, err := env.service.ApproveCodeSubmissionMerge(context.Background(), env.viewer, MergeApprovalCommand{
		SubmissionCommand: SubmissionCommand{IdempotencyKey: "approval-b", TodoID: preview.TodoID, SubmissionID: preview.SubmissionID, ExpectedVersion: preview.Version, ExpectedRevision: preview.Revision, ExpectedHeadCommit: preview.HeadCommit},
		ExpectedBaseHead:  preview.ExpectedBaseHead, ExpectedTreeSHA: preview.MergePreviewTreeSHA,
		DiffDigest: preview.DiffDigest, ChecksDigest: checksDigest(preview.RequiredChecksSnapshot),
	})
	if err != nil {
		t.Fatalf("ApproveCodeSubmissionMerge: %v", err)
	}
	mergeKey := fmt.Sprintf("merge:%s:%s:%d:%s", env.viewer.TenantID, approved.SubmissionID, approved.Revision, approved.MergeApproval.MergeApprovalID)
	command := SubmissionCommand{IdempotencyKey: mergeKey, TodoID: approved.TodoID, SubmissionID: approved.SubmissionID, ExpectedVersion: approved.Version, ExpectedRevision: approved.Revision, ExpectedHeadCommit: approved.HeadCommit}
	merged, err := env.service.MergeCodeSubmission(context.Background(), env.viewer, command)
	if err != nil || merged.Status != SubmissionStatusMerged || merged.MergeReceipt == nil {
		t.Fatalf("MergeCodeSubmission = %+v, %v", merged, err)
	}
	replayed, err := env.service.MergeCodeSubmission(context.Background(), env.viewer, command)
	if err != nil || replayed.MergeReceipt == nil || replayed.MergeReceipt.ReceiptID != merged.MergeReceipt.ReceiptID || env.git.mergeCalls != 1 {
		t.Fatalf("merge replay = %+v, %v calls=%d", replayed, err, env.git.mergeCalls)
	}
}

func newExecutionTestEnvironment(t *testing.T) executionTestEnvironment {
	t.Helper()
	base := newCreatedEnvironment(t, "project-execution")
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(filepath.Join(worktree, "src"), 0o755); err != nil {
		base.close()
		t.Fatalf("create fake worktree: %v", err)
	}
	git := &fakeGitWorkspace{worktree: worktree, head: executionTestBase}
	project, err := base.repo.Get(context.Background(), base.viewer.TenantID, "project-execution")
	if err != nil {
		base.close()
		t.Fatalf("Get project: %v", err)
	}
	expected := project.Version
	project.ExecutionPolicy = ExecutionPolicy{ApprovalRequired: true, NetworkMode: "none", AllowedFileScopes: []string{"src"}, AllowedCommandPrefixes: []string{"go test"}}
	project.RepositoryBindings = []RepositoryBinding{{
		BindingID: "binding-a", ConnectionID: "connection-a", RepositoryLocator: "repo-a", DisplayName: "Repository A",
		Role: RepositoryRolePrimary, AccessMode: AccessReadWrite, DefaultBranch: "main",
		TestCommands: []string{"go test ./..."}, BranchPrefix: "orrery/todo/", Status: BindingStatusActive,
		LastFetchedCommit: executionTestBase, SourceKind: RepositorySourceManagedBare, WorkspaceWritable: true,
		SupportsPush: true, SupportsMerge: true, LastVerifiedAt: time.Now().UTC(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Version: 1,
	}}
	project.Version++
	if err := base.repo.Update(context.Background(), project.TenantID, project, expected); err != nil {
		base.close()
		t.Fatalf("Update project fixture: %v", err)
	}
	base.service.SetSCMConnections(connectionCatalog{connection: scmconnection.Connection{
		TenantID: project.TenantID, ConnectionID: "connection-a", Provider: scmconnection.ProviderManagedLocal,
		BaseURL: filepath.Dir(worktree), Status: scmconnection.StatusActive,
	}})
	executions := NewExecutionRepository(base.router)
	base.service.SetExecutionResources(executions, git, time.Hour, 24*time.Hour)
	if err := base.authorizer.WriteRelations(context.Background(), []authorization.RelationTuple{{
		TenantID: project.TenantID, Subject: authorization.SubjectRef{Kind: identity.ViewerKindAgent, ID: "agent-a"}, Relation: "executor",
		Object: identity.ObjectRef{Type: "development_project", ID: project.DevelopmentProjectID, TenantID: project.TenantID},
	}, {
		TenantID: project.TenantID, Subject: authorization.SubjectRef{Kind: identity.ViewerKindAgent, ID: "agent-b"}, Relation: "executor",
		Object: identity.ObjectRef{Type: "development_project", ID: project.DevelopmentProjectID, TenantID: project.TenantID},
	}}); err != nil {
		base.close()
		t.Fatalf("write executor relations: %v", err)
	}
	snapshot, err := base.service.ResolveExecutionPlanSnapshot(context.Background(), project.TenantID, project.DevelopmentProjectID)
	if err != nil {
		base.close()
		t.Fatalf("ResolveExecutionPlanSnapshot: %v", err)
	}
	return executionTestEnvironment{testEnvironment: base, executions: executions, git: git, project: project, snapshot: snapshot}
}

func (e executionTestEnvironment) provisionCommand(missionID string, taskIDs, agentIDs []string) ProvisionTodoWorkspaceCommand {
	return ProvisionTodoWorkspaceCommand{
		TenantID: e.viewer.TenantID, DevelopmentProjectID: e.project.DevelopmentProjectID, TodoID: "todo-a", TodoTitle: "Todo A",
		PlanID: "plan-a", PlanDigest: "sha256:approved-plan", ApprovalID: "approval-a", MissionID: missionID,
		ResourceSnapshotDigest: e.snapshot.SnapshotDigest, RepositoryBindingID: "binding-a", BaseCommit: executionTestBase,
		AllowedFileScopes: []string{"src"}, PlannedCommands: []string{"go test ./..."}, NetworkMode: "none",
		MissionTaskIDs: append([]string(nil), taskIDs...), AgentIDs: append([]string(nil), agentIDs...),
	}
}

type fakeGitWorkspace struct {
	mu            sync.Mutex
	worktree      string
	head          string
	commitCalls   int
	snapshotCalls int
	lastCommit    GitCommitRequest
	mergeErr      error
	mergeCalls    int
	pushCalls     int
	previewCalls  int
}

func (f *fakeGitWorkspace) Provision(_ context.Context, request GitProvisionRequest) (GitWorkspaceResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return GitWorkspaceResult{StorageKey: "storage-a", BranchName: "orrery/todo/todo-a", BaseCommit: request.BaseCommit, HeadCommit: f.head, WorktreePath: f.worktree}, nil
}

func (f *fakeGitWorkspace) Resolve(_ context.Context, request GitProvisionRequest) (GitWorkspaceResult, error) {
	return GitWorkspaceResult{StorageKey: "storage-a", BranchName: "orrery/todo/todo-a", BaseCommit: request.BaseCommit, HeadCommit: f.head, WorktreePath: f.worktree}, nil
}

func (f *fakeGitWorkspace) CreateCommit(_ context.Context, request GitCommitRequest) (GitCommitResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCalls++
	f.lastCommit = request
	f.head = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	return GitCommitResult{
		CommitSHA: f.head, ParentSHAs: []string{executionTestBase}, TreeSHA: "1111111111111111111111111111111111111111",
		Message: "controlled", SignatureStatus: "signed", ChangedFiles: []string{"src/app.go"}, Additions: 2,
		DiffDigest: "sha256:diff", Checks: []CheckEvidence{{Command: []string{"go", "test", "./..."}, Succeeded: true}}, CreatedAt: time.Now().UTC(),
	}, nil
}

func (f *fakeGitWorkspace) SnapshotSubmission(_ context.Context, request GitSubmissionSnapshotRequest) (GitSubmissionSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshotCalls++
	return GitSubmissionSnapshot{BaseCommit: request.BaseCommit, HeadCommit: request.HeadCommit, TreeSHA: "1111111111111111111111111111111111111111", CommitSHAs: []string{request.HeadCommit}, ChangedFiles: []string{"src/app.go"}, Additions: 2, DiffDigest: "sha256:diff"}, nil
}

func (f *fakeGitWorkspace) FetchBaseHead(context.Context, GitProvisionRequest) (string, error) {
	return executionTestBase, nil
}
func (f *fakeGitWorkspace) PushTodoBranch(context.Context, GitProvisionRequest, string) error {
	f.pushCalls++
	return nil
}
func (f *fakeGitWorkspace) PreviewMerge(_ context.Context, request GitMergePreviewRequest) (GitMergePreview, error) {
	f.previewCalls++
	return GitMergePreview{ExpectedBaseHead: request.ExpectedBaseHead, HeadCommit: request.HeadCommit, TreeSHA: "2222222222222222222222222222222222222222", MergeStrategy: request.MergeStrategy}, nil
}
func (f *fakeGitWorkspace) MergeAuthoritative(context.Context, GitMergeRequest) (GitMergeReceipt, error) {
	f.mergeCalls++
	if f.mergeErr != nil {
		return GitMergeReceipt{}, f.mergeErr
	}
	return GitMergeReceipt{MergeCommitSHA: "3333333333333333333333333333333333333333", TreeSHA: "2222222222222222222222222222222222222222", TargetHead: "3333333333333333333333333333333333333333", MergedAt: time.Now().UTC()}, nil
}
func (f *fakeGitWorkspace) Export(context.Context, GitProvisionRequest, string, string) (GitExportResult, error) {
	return GitExportResult{PatchRef: "change.patch", BundleRef: "change.bundle"}, nil
}
func (f *fakeGitWorkspace) OpenExport(context.Context, GitProvisionRequest, string, string, string) (GitExportFile, error) {
	return GitExportFile{}, ErrMergeForbidden
}

func (e executionTestEnvironment) createPushedSubmission(t *testing.T) TodoCodeSubmission {
	t.Helper()
	submission := e.createLocalSubmission(t)
	resultSubmission, err := e.service.PushCodeSubmission(context.Background(), e.viewer, SubmissionCommand{
		IdempotencyKey: "push-for-merge", TodoID: submission.TodoID, SubmissionID: submission.SubmissionID,
		ExpectedVersion: submission.Version, ExpectedRevision: submission.Revision, ExpectedHeadCommit: submission.HeadCommit,
	})
	if err != nil {
		t.Fatalf("PushCodeSubmission: %v", err)
	}
	return resultSubmission
}

func (e executionTestEnvironment) createLocalSubmission(t *testing.T) TodoCodeSubmission {
	t.Helper()
	result, err := e.service.ProvisionTodoWorkspace(context.Background(), e.provisionCommand("mission-a", []string{"task-a"}, []string{"agent-a"}))
	if err != nil {
		t.Fatalf("ProvisionTodoWorkspace: %v", err)
	}
	if _, found, err := e.service.PrepareAgentRun(context.Background(), e.viewer.TenantID, "mission-a", "task-a", "agent-a", "runtime-a"); err != nil || !found {
		t.Fatalf("PrepareAgentRun = %v, %v", found, err)
	}
	record, _, err := e.service.CommitAgentRunResult(context.Background(), e.viewer.TenantID, "mission-a", "task-a", "agent-a", "runtime-a", "done")
	if err != nil {
		t.Fatalf("CommitAgentRunResult: %v", err)
	}
	submission, err := e.service.CreateCodeSubmission(context.Background(), e.viewer, CreateSubmissionCommand{
		IdempotencyKey: "submission-for-merge", TodoID: "todo-a", ExecutionRunID: result.Runs[0].ExecutionRunID, HeadCommit: record.CommitSHA, MergeStrategy: "squash",
	})
	if err != nil {
		t.Fatalf("CreateCodeSubmission: %v", err)
	}
	return submission
}

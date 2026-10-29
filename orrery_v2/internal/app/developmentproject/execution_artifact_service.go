package developmentproject

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (s *Service) ListWorkspaces(ctx context.Context, viewer ViewerRef, projectID string, query ListExecutionArtifactsQuery) (WorkspacePage, error) {
	project, err := s.getAuthorized(ctx, viewer, projectID, "development_project.view")
	if err != nil {
		return WorkspacePage{}, err
	}
	if s.executionRepo == nil {
		return WorkspacePage{}, ErrDependency
	}
	items, err := s.executionRepo.ListWorkspaces(ctx, project.TenantID)
	if err != nil {
		return WorkspacePage{}, err
	}
	filtered := make([]DevelopmentWorkspace, 0)
	for _, item := range items {
		if item.DevelopmentProjectID != project.DevelopmentProjectID || (query.TodoID != "" && item.TodoID != strings.TrimSpace(query.TodoID)) || (query.Status != "" && item.Status != strings.TrimSpace(query.Status)) {
			continue
		}
		item.StorageKey = ""
		filtered = append(filtered, item)
	}
	start, count := pageBounds(query.Offset, query.Limit, len(filtered))
	end := start + count
	return WorkspacePage{Items: filtered[start:end], Page: PageDTO{Limit: count, Offset: start, Total: len(filtered), Returned: count, HasMore: end < len(filtered)}}, nil
}

func (s *Service) ListRuns(ctx context.Context, viewer ViewerRef, projectID string, query ListExecutionArtifactsQuery) (ExecutionRunPage, error) {
	project, err := s.getAuthorized(ctx, viewer, projectID, "development_project.monitor")
	if err != nil {
		return ExecutionRunPage{}, err
	}
	if s.executionRepo == nil {
		return ExecutionRunPage{}, ErrDependency
	}
	items, err := s.executionRepo.ListRuns(ctx, project.TenantID)
	if err != nil {
		return ExecutionRunPage{}, err
	}
	filtered := make([]TodoExecutionRun, 0)
	for _, item := range items {
		if item.DevelopmentProjectID != project.DevelopmentProjectID || (query.TodoID != "" && item.TodoID != strings.TrimSpace(query.TodoID)) || (query.Status != "" && item.Status != strings.TrimSpace(query.Status)) {
			continue
		}
		item.RequiredSecretRefs = nil
		filtered = append(filtered, item)
	}
	start, count := pageBounds(query.Offset, query.Limit, len(filtered))
	end := start + count
	return ExecutionRunPage{Items: filtered[start:end], Page: PageDTO{Limit: count, Offset: start, Total: len(filtered), Returned: count, HasMore: end < len(filtered)}}, nil
}

func (s *Service) ListTodoCommits(ctx context.Context, viewer ViewerRef, todoID string, query ListExecutionArtifactsQuery) (CommitPage, error) {
	if s == nil || s.executionRepo == nil {
		return CommitPage{}, ErrDependency
	}
	items, err := s.executionRepo.ListCommits(ctx, viewerTenant(viewer))
	if err != nil {
		return CommitPage{}, err
	}
	filtered := make([]TodoCommitRecord, 0)
	for _, item := range items {
		if item.TodoID != strings.TrimSpace(todoID) {
			continue
		}
		if _, authErr := s.getAuthorized(ctx, viewer, item.DevelopmentProjectID, "development_project.view"); authErr != nil {
			if errors.Is(authErr, ErrNotFound) {
				continue
			}
			return CommitPage{}, authErr
		}
		filtered = append(filtered, item)
	}
	start, count := pageBounds(query.Offset, query.Limit, len(filtered))
	end := start + count
	return CommitPage{Items: filtered[start:end], Page: PageDTO{Limit: count, Offset: start, Total: len(filtered), Returned: count, HasMore: end < len(filtered)}}, nil
}

func (s *Service) ListTodoSubmissions(ctx context.Context, viewer ViewerRef, todoID string, query ListExecutionArtifactsQuery) (CodeSubmissionPage, error) {
	if s == nil || s.executionRepo == nil {
		return CodeSubmissionPage{}, ErrDependency
	}
	items, err := s.executionRepo.ListSubmissions(ctx, viewerTenant(viewer))
	if err != nil {
		return CodeSubmissionPage{}, err
	}
	filtered := make([]TodoCodeSubmission, 0)
	for _, item := range items {
		if item.TodoID != strings.TrimSpace(todoID) || (query.Status != "" && item.Status != strings.TrimSpace(query.Status)) {
			continue
		}
		if _, authErr := s.getAuthorized(ctx, viewer, item.DevelopmentProjectID, "development_project.view"); authErr != nil {
			if errors.Is(authErr, ErrNotFound) {
				continue
			}
			return CodeSubmissionPage{}, authErr
		}
		filtered = append(filtered, cloneSubmission(item))
	}
	start, count := pageBounds(query.Offset, query.Limit, len(filtered))
	end := start + count
	return CodeSubmissionPage{Items: filtered[start:end], Page: PageDTO{Limit: count, Offset: start, Total: len(filtered), Returned: count, HasMore: end < len(filtered)}}, nil
}

func (s *Service) GetTodoSubmission(ctx context.Context, viewer ViewerRef, todoID, submissionID string) (TodoCodeSubmission, error) {
	if s == nil || s.executionRepo == nil {
		return TodoCodeSubmission{}, ErrDependency
	}
	submission, err := s.executionRepo.GetSubmission(ctx, viewerTenant(viewer), strings.TrimSpace(submissionID))
	if err != nil {
		return TodoCodeSubmission{}, err
	}
	if submission.TodoID != strings.TrimSpace(todoID) {
		return TodoCodeSubmission{}, ErrNotFound
	}
	if _, err := s.getAuthorized(ctx, viewer, submission.DevelopmentProjectID, "development_project.view"); err != nil {
		return TodoCodeSubmission{}, err
	}
	return cloneSubmission(submission), nil
}

func (s *Service) ExportTodoSubmission(ctx context.Context, viewer ViewerRef, todoID, submissionID, kind string) (CodeExportDownload, error) {
	project, submission, err := s.authorizedSubmission(ctx, viewer, todoID, submissionID, "development_project.submit")
	if err != nil {
		return CodeExportDownload{}, err
	}
	kind = strings.TrimSpace(kind)
	if kind != "patch" && kind != "bundle" {
		return CodeExportDownload{}, ErrInvalidRequest
	}
	workspace, err := s.executionRepo.GetWorkspace(ctx, project.TenantID, submission.WorkspaceID)
	if err != nil {
		return CodeExportDownload{}, err
	}
	_, binding, request, err := s.executionGitRequest(ctx, project.TenantID, workspace, submission.TodoID)
	if err != nil {
		return CodeExportDownload{}, err
	}
	if binding.SourceKind != RepositorySourceManagedLocal {
		return CodeExportDownload{}, ErrMergeForbidden
	}
	file, err := s.gitWorkspace.OpenExport(ctx, request, submission.BaseCommit, submission.HeadCommit, kind)
	if err != nil {
		return CodeExportDownload{}, err
	}
	if file.Reader == nil || strings.TrimSpace(file.Name) == "" || file.Size < 0 {
		if file.Reader != nil {
			_ = file.Reader.Close()
		}
		return CodeExportDownload{}, ErrDependency
	}
	return CodeExportDownload{Name: file.Name, ContentType: file.ContentType, Size: file.Size, Reader: file.Reader}, nil
}

func (s *Service) executionGitRequest(ctx context.Context, tenantID string, workspace DevelopmentWorkspace, todoTitle string) (Project, RepositoryBinding, GitProvisionRequest, error) {
	project, err := s.repo.Get(ctx, tenantID, workspace.DevelopmentProjectID)
	if err != nil {
		return Project{}, RepositoryBinding{}, GitProvisionRequest{}, err
	}
	binding, err := repositoryBindingByID(project, workspace.RepositoryBindingID)
	if err != nil {
		return Project{}, RepositoryBinding{}, GitProvisionRequest{}, err
	}
	connections, ok := s.connections.(executionSCMConnections)
	if !ok {
		return Project{}, RepositoryBinding{}, GitProvisionRequest{}, ErrDependency
	}
	connection, err := connections.GetConnectionForExecution(ctx, tenantID, binding.ConnectionID)
	if err != nil {
		return Project{}, RepositoryBinding{}, GitProvisionRequest{}, err
	}
	attemptID := firstNonBlank(workspace.ExecutionAttemptID, workspace.TodoID)
	request, err := gitRequestForExecution(project, binding, connection, workspace.TodoID, attemptID, todoTitle, workspace.BaseCommit)
	return project, binding, request, err
}

func validateSubmissionCommand(submission TodoCodeSubmission, cmd SubmissionCommand) error {
	if cmd.ExpectedVersion <= 0 || submission.Version != cmd.ExpectedVersion || cmd.ExpectedRevision <= 0 || submission.Revision != cmd.ExpectedRevision {
		return ErrConflict
	}
	if strings.TrimSpace(cmd.ExpectedHeadCommit) == "" || submission.HeadCommit != strings.TrimSpace(cmd.ExpectedHeadCommit) {
		return ErrConflict
	}
	if cmd.TodoID != "" && submission.TodoID != strings.TrimSpace(cmd.TodoID) {
		return ErrNotFound
	}
	if !validIdempotencyKey(cmd.IdempotencyKey) {
		return ErrInvalidRequest
	}
	return nil
}

func checksDigest(checks []CheckEvidence) string { return hashValue(checks) }

func appendSubmissionRevision(submission *TodoCodeSubmission, now time.Time) {
	if submission == nil {
		return
	}
	var approval *MergeApproval
	if submission.MergeApproval != nil {
		copy := *submission.MergeApproval
		approval = &copy
	}
	submission.PriorRevisions = append(submission.PriorRevisions, CodeSubmissionRevision{
		Revision: submission.Revision, HeadCommit: submission.HeadCommit, TreeSHA: submission.TreeSHA,
		DiffDigest: submission.DiffDigest, Status: submission.Status, ExpectedBaseHead: submission.ExpectedBaseHead,
		MergePreviewTreeSHA: submission.MergePreviewTreeSHA, RequiredChecksSnapshot: cloneChecks(submission.RequiredChecksSnapshot),
		MergeApproval: approval, RecordedAt: now,
	})
}

func cloneChecks(values []CheckEvidence) []CheckEvidence {
	out := make([]CheckEvidence, len(values))
	for i, value := range values {
		value.Command = append([]string(nil), value.Command...)
		out[i] = value
	}
	return out
}

func cloneSubmission(value TodoCodeSubmission) TodoCodeSubmission {
	value.CommitSHAs = append([]string(nil), value.CommitSHAs...)
	value.ChangedFiles = append([]string(nil), value.ChangedFiles...)
	value.RequiredChecksSnapshot = cloneChecks(value.RequiredChecksSnapshot)
	if value.MergeApproval != nil {
		copy := *value.MergeApproval
		value.MergeApproval = &copy
	}
	if value.MergeReceipt != nil {
		copy := *value.MergeReceipt
		value.MergeReceipt = &copy
	}
	value.PriorRevisions = append([]CodeSubmissionRevision(nil), value.PriorRevisions...)
	return value
}

func (s *Service) authorizedSubmission(ctx context.Context, viewer ViewerRef, todoID, submissionID, action string) (Project, TodoCodeSubmission, error) {
	if s == nil || s.executionRepo == nil {
		return Project{}, TodoCodeSubmission{}, ErrDependency
	}
	submission, err := s.executionRepo.GetSubmission(ctx, viewerTenant(viewer), strings.TrimSpace(submissionID))
	if err != nil {
		return Project{}, TodoCodeSubmission{}, err
	}
	if submission.TodoID != strings.TrimSpace(todoID) {
		return Project{}, TodoCodeSubmission{}, ErrNotFound
	}
	project, err := s.getAuthorized(ctx, viewer, submission.DevelopmentProjectID, action)
	if err != nil {
		return Project{}, TodoCodeSubmission{}, err
	}
	return project, submission, nil
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

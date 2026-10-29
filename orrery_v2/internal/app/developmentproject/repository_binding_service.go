package developmentproject

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"orrery_v2/internal/app/scmconnection"
)

var branchPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,79}/$`)

func (s *Service) ListRepositories(ctx context.Context, viewer ViewerRef, projectID string) ([]RepositoryBinding, error) {
	project, err := s.getAuthorized(ctx, viewer, projectID, "development_project.view")
	if err != nil {
		return nil, err
	}
	capabilities, err := s.capabilities(ctx, viewer, project)
	if err != nil {
		return nil, err
	}
	bindings := cloneRepositoryBindings(project.RepositoryBindings)
	if !capabilities.CanConfigure {
		for index := range bindings {
			bindings[index].BuildCommands = nil
			bindings[index].TestCommands = nil
		}
	}
	return bindings, nil
}

func (s *Service) BindRepository(ctx context.Context, viewer ViewerRef, cmd BindRepositoryCommand) (RepositoryBinding, ProjectDTO, error) {
	if err := s.requireMutationDependencies(true); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	cmd = normalizeBindRepositoryCommand(cmd)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return RepositoryBinding{}, ProjectDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := projectMutationRequestHash("repository.bind", hashCommand)
	if err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	project, err := s.getAuthorized(ctx, viewer, cmd.DevelopmentProjectID, "development_project.configure")
	if err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	if receipt, replay, err := projectMutationReplay(project, cmd.IdempotencyKey, "repository.bind", requestHash); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	} else if replay {
		project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
		if err != nil {
			return RepositoryBinding{}, ProjectDTO{}, err
		}
		if receipt.Repository == nil {
			return RepositoryBinding{}, ProjectDTO{}, ErrDependency
		}
		dto, dtoErr := s.dto(ctx, viewer, project)
		return *receipt.Repository, dto, dtoErr
	}
	if project.Status != StatusActive {
		return RepositoryBinding{}, ProjectDTO{}, ErrArchived
	}
	if cmd.ExpectedProjectVersion <= 0 || project.Version != cmd.ExpectedProjectVersion {
		return RepositoryBinding{}, ProjectDTO{}, ErrConflict
	}
	if err := validateRepositoryCommand(cmd); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	if err := validateRepositoryInvariant(project.RepositoryBindings, RepositoryBinding{Role: cmd.Role, AccessMode: cmd.AccessMode}, ""); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	for _, binding := range project.RepositoryBindings {
		if binding.ConnectionID == cmd.ConnectionID && binding.RepositoryLocator == cmd.RepositoryLocator {
			return RepositoryBinding{}, ProjectDTO{}, ErrConflict
		}
	}
	connection, metadata, err := s.resolveRepositoryForUse(ctx, viewer, cmd.ConnectionID, cmd.RepositoryLocator)
	if err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	if err := validateRepositoryMetadata(metadata); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	if !repositoryAllowed(connection.AllowedRepositoryPatterns, metadata.RepositoryLocator) {
		return RepositoryBinding{}, ProjectDTO{}, fmt.Errorf("%w: repository is outside the connection allowlist", ErrRepositoryPolicy)
	}
	if cmd.AccessMode == AccessReadWrite && metadata.ReadOnly && !metadata.WorkspaceWritable {
		return RepositoryBinding{}, ProjectDTO{}, fmt.Errorf("%w: repository connection does not support an isolated writable workspace", ErrRepositoryPolicy)
	}
	if cmd.DefaultBranch == "" {
		cmd.DefaultBranch = metadata.DefaultBranch
	}
	if cmd.DefaultBranch != metadata.DefaultBranch {
		return RepositoryBinding{}, ProjectDTO{}, fmt.Errorf("%w: default branch was not verified", ErrRepositoryPolicy)
	}
	bindingID := cmd.BindingID
	if bindingID == "" {
		bindingID, err = s.newID("repository")
		if err != nil {
			return RepositoryBinding{}, ProjectDTO{}, err
		}
	}
	if !validResourceID(bindingID) {
		return RepositoryBinding{}, ProjectDTO{}, fmt.Errorf("%w: binding_id is invalid", ErrInvalidRequest)
	}
	now := s.now()
	binding := RepositoryBinding{
		BindingID: bindingID, ConnectionID: cmd.ConnectionID, RepositoryLocator: metadata.RepositoryLocator,
		DisplayName: metadata.DisplayName, Role: cmd.Role, AccessMode: cmd.AccessMode,
		DefaultBranch: cmd.DefaultBranch, SourceSubdirectory: cmd.SourceSubdirectory,
		BuildCommands: append([]string(nil), cmd.BuildCommands...), TestCommands: append([]string(nil), cmd.TestCommands...),
		BranchPrefix: cmd.BranchPrefix, Status: BindingStatusActive, LastFetchedCommit: metadata.HeadCommit,
		SourceKind: metadata.SourceKind, WorkspaceWritable: metadata.WorkspaceWritable, SupportsPush: metadata.SupportsPush,
		SupportsPullRequest: metadata.SupportsPullRequest, SupportsMerge: metadata.SupportsMerge,
		LastVerifiedAt: now, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	project.RepositoryBindings = append(project.RepositoryBindings, binding)
	project.Version++
	project.UpdatedAt = now
	appendProjectMutationReceipt(&project, MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "repository.bind", RequestHash: requestHash, Repository: &binding})
	appendProjectEffects(&project,
		projectConnectionEffect(project, effectWriteConnectionUser, connection.ConnectionID),
		s.projectAuditEffect(project, viewer, "development_project.repository_bound", "succeeded", "bound development project repository", map[string]string{"binding_id": binding.BindingID, "connection_id": binding.ConnectionID}),
	)
	if err := s.repo.Update(ctx, project.TenantID, project, cmd.ExpectedProjectVersion); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
	if err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	dto, err := s.dto(ctx, viewer, project)
	return binding, dto, err
}

func (s *Service) UpdateRepository(ctx context.Context, viewer ViewerRef, cmd UpdateRepositoryCommand) (RepositoryBinding, ProjectDTO, error) {
	if err := s.requireMutationDependencies(false); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.DevelopmentProjectID = strings.TrimSpace(cmd.DevelopmentProjectID)
	cmd.BindingID = strings.TrimSpace(cmd.BindingID)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return RepositoryBinding{}, ProjectDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := projectMutationRequestHash("repository.update", hashCommand)
	if err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	project, err := s.getAuthorized(ctx, viewer, cmd.DevelopmentProjectID, "development_project.configure")
	if err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	if receipt, replay, err := projectMutationReplay(project, cmd.IdempotencyKey, "repository.update", requestHash); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	} else if replay {
		project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
		if err != nil {
			return RepositoryBinding{}, ProjectDTO{}, err
		}
		if receipt.Repository == nil {
			return RepositoryBinding{}, ProjectDTO{}, ErrDependency
		}
		dto, dtoErr := s.dto(ctx, viewer, project)
		return *receipt.Repository, dto, dtoErr
	}
	if project.Status != StatusActive {
		return RepositoryBinding{}, ProjectDTO{}, ErrArchived
	}
	if cmd.ExpectedProjectVersion <= 0 || project.Version != cmd.ExpectedProjectVersion {
		return RepositoryBinding{}, ProjectDTO{}, ErrConflict
	}
	index := repositoryIndex(project.RepositoryBindings, cmd.BindingID)
	if index < 0 {
		return RepositoryBinding{}, ProjectDTO{}, ErrNotFound
	}
	binding := project.RepositoryBindings[index]
	if cmd.ExpectedBindingVersion <= 0 || binding.Version != cmd.ExpectedBindingVersion {
		return RepositoryBinding{}, ProjectDTO{}, ErrConflict
	}
	if cmd.SourceSubdirectory != nil {
		binding.SourceSubdirectory = normalizeRelativePath(*cmd.SourceSubdirectory)
	}
	if cmd.BuildCommands != nil {
		binding.BuildCommands = normalizeCommandList(*cmd.BuildCommands)
	}
	if cmd.TestCommands != nil {
		binding.TestCommands = normalizeCommandList(*cmd.TestCommands)
	}
	if cmd.BranchPrefix != nil {
		binding.BranchPrefix = strings.TrimSpace(*cmd.BranchPrefix)
	}
	validation := BindRepositoryCommand{
		ConnectionID: binding.ConnectionID, RepositoryLocator: binding.RepositoryLocator,
		Role: binding.Role, AccessMode: binding.AccessMode, DefaultBranch: binding.DefaultBranch,
		SourceSubdirectory: binding.SourceSubdirectory, BuildCommands: binding.BuildCommands,
		TestCommands: binding.TestCommands, BranchPrefix: binding.BranchPrefix,
	}
	if err := validateRepositoryCommand(validation); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	binding.Version++
	binding.UpdatedAt = s.now()
	project.RepositoryBindings[index] = binding
	project.Version++
	project.UpdatedAt = s.now()
	appendProjectMutationReceipt(&project, MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "repository.update", RequestHash: requestHash, Repository: &binding})
	appendProjectEffects(&project, s.projectAuditEffect(project, viewer, "development_project.repository_updated", "succeeded", "updated development project repository", map[string]string{"binding_id": binding.BindingID}))
	if err := s.repo.Update(ctx, project.TenantID, project, cmd.ExpectedProjectVersion); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
	if err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	dto, err := s.dto(ctx, viewer, project)
	return binding, dto, err
}

func (s *Service) VerifyRepository(ctx context.Context, viewer ViewerRef, cmd VerifyRepositoryCommand) (RepositoryBinding, ProjectDTO, error) {
	if err := s.requireMutationDependencies(true); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.DevelopmentProjectID = strings.TrimSpace(cmd.DevelopmentProjectID)
	cmd.BindingID = strings.TrimSpace(cmd.BindingID)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return RepositoryBinding{}, ProjectDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := projectMutationRequestHash("repository.verify", hashCommand)
	if err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	project, err := s.getAuthorized(ctx, viewer, cmd.DevelopmentProjectID, "development_project.configure")
	if err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	if receipt, replay, err := projectMutationReplay(project, cmd.IdempotencyKey, "repository.verify", requestHash); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	} else if replay {
		project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
		if err != nil {
			return RepositoryBinding{}, ProjectDTO{}, err
		}
		if receipt.Repository == nil {
			return RepositoryBinding{}, ProjectDTO{}, ErrDependency
		}
		dto, dtoErr := s.dto(ctx, viewer, project)
		if receipt.VerificationFail != "" {
			failure, failureErr := repositoryVerificationError(receipt.VerificationFailureCode, receipt.VerificationFail)
			if failureErr != nil {
				return RepositoryBinding{}, ProjectDTO{}, failureErr
			}
			return *receipt.Repository, dto, errors.Join(failure, dtoErr)
		}
		return *receipt.Repository, dto, dtoErr
	}
	if project.Status != StatusActive {
		return RepositoryBinding{}, ProjectDTO{}, ErrArchived
	}
	if cmd.ExpectedProjectVersion <= 0 || project.Version != cmd.ExpectedProjectVersion {
		return RepositoryBinding{}, ProjectDTO{}, ErrConflict
	}
	index := repositoryIndex(project.RepositoryBindings, cmd.BindingID)
	if index < 0 {
		return RepositoryBinding{}, ProjectDTO{}, ErrNotFound
	}
	binding := project.RepositoryBindings[index]
	if cmd.ExpectedBindingVersion <= 0 || binding.Version != cmd.ExpectedBindingVersion {
		return RepositoryBinding{}, ProjectDTO{}, ErrConflict
	}
	_, metadata, verifyErr := s.resolveRepositoryForUse(ctx, viewer, binding.ConnectionID, binding.RepositoryLocator)
	if verifyErr == nil {
		verifyErr = validateRepositoryMetadata(metadata)
	}
	if verifyErr == nil && metadata.DefaultBranch != binding.DefaultBranch {
		verifyErr = fmt.Errorf("%w: default branch no longer matches the verified repository", ErrRepositoryPolicy)
	}
	verificationFailureCode := ""
	if verifyErr != nil {
		verificationFailureCode, verifyErr = normalizeRepositoryVerificationError(verifyErr)
	}
	binding.Version++
	binding.UpdatedAt = s.now()
	if verifyErr != nil {
		binding.Status = BindingStatusError
	} else {
		binding.Status = BindingStatusActive
		binding.DisplayName = metadata.DisplayName
		binding.LastFetchedCommit = metadata.HeadCommit
		binding.LastVerifiedAt = s.now()
		binding.SourceKind = metadata.SourceKind
		binding.WorkspaceWritable = metadata.WorkspaceWritable
		binding.SupportsPush = metadata.SupportsPush
		binding.SupportsPullRequest = metadata.SupportsPullRequest
		binding.SupportsMerge = metadata.SupportsMerge
	}
	project.RepositoryBindings[index] = binding
	project.Version++
	project.UpdatedAt = s.now()
	receipt := MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "repository.verify", RequestHash: requestHash, Repository: &binding}
	if verifyErr != nil {
		receipt.VerificationFailureCode = verificationFailureCode
		receipt.VerificationFail = verifyErr.Error()
	}
	appendProjectMutationReceipt(&project, receipt)
	result := "succeeded"
	if verifyErr != nil {
		result = "failed"
	}
	appendProjectEffects(&project, s.projectAuditEffect(project, viewer, "development_project.repository_verified", result, "verified development project repository", map[string]string{"binding_id": binding.BindingID}))
	if err := s.repo.Update(ctx, project.TenantID, project, cmd.ExpectedProjectVersion); err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
	if err != nil {
		return RepositoryBinding{}, ProjectDTO{}, err
	}
	dto, dtoErr := s.dto(ctx, viewer, project)
	if verifyErr != nil {
		return binding, dto, errors.Join(verifyErr, dtoErr)
	}
	return binding, dto, dtoErr
}

const (
	verificationFailureRepositoryPolicy  = "repository_policy"
	verificationFailureSCMNotFound       = "scm_not_found"
	verificationFailureSCMInvalidRequest = "scm_invalid_request"
	verificationFailureSCMDependency     = "scm_dependency"
	verificationFailureSCMConflict       = "scm_conflict"
	verificationFailureSCMVerification   = "scm_verification_failed"
)

func normalizeRepositoryVerificationError(err error) (string, error) {
	switch {
	case errors.Is(err, ErrRepositoryPolicy):
		return verificationFailureRepositoryPolicy, err
	case errors.Is(err, scmconnection.ErrNotFound):
		return verificationFailureSCMNotFound, err
	case errors.Is(err, scmconnection.ErrInvalidRequest):
		return verificationFailureSCMInvalidRequest, err
	case errors.Is(err, scmconnection.ErrDependency):
		return verificationFailureSCMDependency, err
	case errors.Is(err, scmconnection.ErrConflict):
		return verificationFailureSCMConflict, err
	case errors.Is(err, scmconnection.ErrVerificationFailed):
		return verificationFailureSCMVerification, err
	default:
		return verificationFailureSCMDependency, fmt.Errorf("%w: repository verification failed: %v", scmconnection.ErrDependency, err)
	}
}

func repositoryVerificationError(code, message string) (error, error) {
	var sentinel error
	switch code {
	case verificationFailureRepositoryPolicy:
		sentinel = ErrRepositoryPolicy
	case verificationFailureSCMNotFound:
		sentinel = scmconnection.ErrNotFound
	case verificationFailureSCMInvalidRequest:
		sentinel = scmconnection.ErrInvalidRequest
	case verificationFailureSCMDependency:
		sentinel = scmconnection.ErrDependency
	case verificationFailureSCMConflict:
		sentinel = scmconnection.ErrConflict
	case verificationFailureSCMVerification:
		sentinel = scmconnection.ErrVerificationFailed
	default:
		return nil, fmt.Errorf("%w: unsupported repository verification failure code %q", ErrDependency, code)
	}
	return fmt.Errorf("%w: %s", sentinel, message), nil
}

func (s *Service) RemoveRepository(ctx context.Context, viewer ViewerRef, cmd RemoveRepositoryCommand) (ProjectDTO, error) {
	if err := s.requireMutationDependencies(false); err != nil {
		return ProjectDTO{}, err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.DevelopmentProjectID = strings.TrimSpace(cmd.DevelopmentProjectID)
	cmd.BindingID = strings.TrimSpace(cmd.BindingID)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return ProjectDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := projectMutationRequestHash("repository.remove", hashCommand)
	if err != nil {
		return ProjectDTO{}, err
	}
	project, err := s.getAuthorized(ctx, viewer, cmd.DevelopmentProjectID, "development_project.configure")
	if err != nil {
		return ProjectDTO{}, err
	}
	if _, replay, err := projectMutationReplay(project, cmd.IdempotencyKey, "repository.remove", requestHash); err != nil {
		return ProjectDTO{}, err
	} else if replay {
		project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
		if err != nil {
			return ProjectDTO{}, err
		}
		return s.dto(ctx, viewer, project)
	}
	if project.Status != StatusActive {
		return ProjectDTO{}, ErrArchived
	}
	if cmd.ExpectedProjectVersion <= 0 || project.Version != cmd.ExpectedProjectVersion {
		return ProjectDTO{}, ErrConflict
	}
	index := repositoryIndex(project.RepositoryBindings, cmd.BindingID)
	if index < 0 {
		return ProjectDTO{}, ErrNotFound
	}
	binding := project.RepositoryBindings[index]
	if cmd.ExpectedBindingVersion <= 0 || binding.Version != cmd.ExpectedBindingVersion {
		return ProjectDTO{}, ErrConflict
	}
	project.RepositoryBindings = append(project.RepositoryBindings[:index:index], project.RepositoryBindings[index+1:]...)
	project.Version++
	project.UpdatedAt = s.now()
	appendProjectMutationReceipt(&project, MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "repository.remove", RequestHash: requestHash})
	if !projectUsesConnection(project, binding.ConnectionID) {
		appendProjectEffects(&project, projectConnectionEffect(project, effectDeleteConnectionUser, binding.ConnectionID))
	}
	appendProjectEffects(&project, s.projectAuditEffect(project, viewer, "development_project.repository_unbound", "succeeded", "unbound development project repository", map[string]string{"binding_id": binding.BindingID}))
	if err := s.repo.Update(ctx, project.TenantID, project, cmd.ExpectedProjectVersion); err != nil {
		return ProjectDTO{}, err
	}
	project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
	if err != nil {
		return ProjectDTO{}, err
	}
	return s.dto(ctx, viewer, project)
}

func (s *Service) resolveRepositoryForUse(ctx context.Context, viewer ViewerRef, connectionID, repositoryLocator string) (scmconnection.Connection, scmconnection.RepositoryMetadata, error) {
	connection, err := s.connections.GetConnectionForUse(ctx, viewerForSCM(viewer), connectionID)
	if err != nil {
		return scmconnection.Connection{}, scmconnection.RepositoryMetadata{}, err
	}
	if connection.Status != scmconnection.StatusActive {
		return scmconnection.Connection{}, scmconnection.RepositoryMetadata{}, fmt.Errorf("%w: SCM connection is not active", ErrRepositoryPolicy)
	}
	repositories, err := s.connections.ListRepositories(ctx, viewerForSCM(viewer), connectionID)
	if err != nil {
		return scmconnection.Connection{}, scmconnection.RepositoryMetadata{}, err
	}
	for _, repository := range repositories {
		if repository.RepositoryLocator == repositoryLocator {
			return connection, repository, nil
		}
	}
	return scmconnection.Connection{}, scmconnection.RepositoryMetadata{}, fmt.Errorf("%w: repository was not verified for the connection", ErrRepositoryPolicy)
}

func normalizeBindRepositoryCommand(cmd BindRepositoryCommand) BindRepositoryCommand {
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.DevelopmentProjectID = strings.TrimSpace(cmd.DevelopmentProjectID)
	cmd.BindingID = strings.TrimSpace(cmd.BindingID)
	cmd.ConnectionID = strings.TrimSpace(cmd.ConnectionID)
	cmd.RepositoryLocator = strings.TrimSpace(cmd.RepositoryLocator)
	cmd.Role = strings.ToLower(strings.TrimSpace(cmd.Role))
	cmd.AccessMode = strings.ToLower(strings.TrimSpace(cmd.AccessMode))
	cmd.DefaultBranch = strings.TrimSpace(cmd.DefaultBranch)
	cmd.SourceSubdirectory = normalizeRelativePath(cmd.SourceSubdirectory)
	cmd.BuildCommands = normalizeCommandList(cmd.BuildCommands)
	cmd.TestCommands = normalizeCommandList(cmd.TestCommands)
	cmd.BranchPrefix = strings.TrimSpace(cmd.BranchPrefix)
	if cmd.BranchPrefix == "" {
		cmd.BranchPrefix = "orrery/todo/"
	}
	return cmd
}

func validateRepositoryCommand(cmd BindRepositoryCommand) error {
	if !validResourceID(cmd.ConnectionID) || cmd.RepositoryLocator == "" || len(cmd.RepositoryLocator) > 256 || containsControl(cmd.RepositoryLocator) || strings.ContainsAny(cmd.RepositoryLocator, "?@") || strings.Contains(cmd.RepositoryLocator, "://") {
		return ErrInvalidRequest
	}
	if !oneOf(cmd.Role, RepositoryRolePrimary, RepositoryRoleDependency) || !oneOf(cmd.AccessMode, AccessReadOnly, AccessReadWrite) {
		return ErrInvalidRequest
	}
	if cmd.Role == RepositoryRolePrimary && cmd.AccessMode != AccessReadWrite {
		return fmt.Errorf("%w: primary repository must be read-write", ErrRepositoryPolicy)
	}
	if cmd.Role == RepositoryRoleDependency && cmd.AccessMode != AccessReadOnly {
		return fmt.Errorf("%w: dependency repository must be read-only", ErrRepositoryPolicy)
	}
	if cmd.DefaultBranch != "" && (!validGitRefPart(cmd.DefaultBranch) || strings.Contains(cmd.DefaultBranch, "..")) {
		return ErrInvalidRequest
	}
	if cmd.SourceSubdirectory != "" && !validRelativePath(cmd.SourceSubdirectory) {
		return fmt.Errorf("%w: source subdirectory must be relative", ErrInvalidRequest)
	}
	if !branchPrefixPattern.MatchString(cmd.BranchPrefix) || !validGitRefPart(strings.TrimSuffix(cmd.BranchPrefix, "/")) {
		return fmt.Errorf("%w: branch prefix is invalid", ErrInvalidRequest)
	}
	if len(cmd.BuildCommands) > 32 || len(cmd.TestCommands) > 32 {
		return fmt.Errorf("%w: too many repository commands", ErrInvalidRequest)
	}
	for _, command := range append(append([]string(nil), cmd.BuildCommands...), cmd.TestCommands...) {
		if command == "" || len(command) > 2048 || strings.ContainsAny(command, "\r\n\x00") {
			return fmt.Errorf("%w: repository command is invalid", ErrInvalidRequest)
		}
	}
	return nil
}

func normalizeRelativePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return path.Clean(strings.ReplaceAll(value, "\\", "/"))
}

func validateRepositoryInvariant(bindings []RepositoryBinding, candidate RepositoryBinding, ignoredBindingID string) error {
	if candidate.Role != RepositoryRolePrimary || candidate.AccessMode != AccessReadWrite {
		return nil
	}
	for _, binding := range bindings {
		if binding.BindingID != ignoredBindingID && binding.Role == RepositoryRolePrimary && binding.AccessMode == AccessReadWrite {
			return fmt.Errorf("%w: only one writable primary repository is allowed", ErrRepositoryPolicy)
		}
	}
	return nil
}

func repositoryAllowed(patterns []string, locator string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == locator || (strings.HasSuffix(pattern, "*") && strings.HasPrefix(locator, strings.TrimSuffix(pattern, "*"))) {
			return true
		}
	}
	return false
}

func validGitRefPart(value string) bool {
	if value == "" || len(value) > 255 || strings.HasPrefix(value, "-") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || strings.HasSuffix(strings.ToLower(value), ".lock") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.Contains(value, "//") || strings.ContainsAny(value, " ~^:?*[\\\x00") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".") || strings.HasSuffix(strings.ToLower(component), ".lock") {
			return false
		}
	}
	return true
}

func validateRepositoryMetadata(metadata scmconnection.RepositoryMetadata) error {
	if metadata.RepositoryLocator == "" || len(metadata.RepositoryLocator) > 256 || containsControl(metadata.RepositoryLocator) ||
		metadata.DisplayName == "" || len(metadata.DisplayName) > 256 || containsControl(metadata.DisplayName) ||
		!validGitRefPart(metadata.DefaultBranch) || !validGitObjectID(metadata.HeadCommit) {
		return fmt.Errorf("%w: SCM provider returned invalid repository metadata", ErrRepositoryPolicy)
	}
	return nil
}

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func normalizeCommandList(commands []string) []string {
	out := make([]string, 0, len(commands))
	for _, command := range commands {
		if command = strings.TrimSpace(command); command != "" {
			out = append(out, command)
		}
	}
	return out
}

func repositoryIndex(bindings []RepositoryBinding, bindingID string) int {
	bindingID = strings.TrimSpace(bindingID)
	for index := range bindings {
		if bindings[index].BindingID == bindingID {
			return index
		}
	}
	return -1
}

func projectUsesConnection(project Project, connectionID string) bool {
	for _, binding := range project.RepositoryBindings {
		if binding.ConnectionID == connectionID {
			return true
		}
	}
	return false
}

func cloneRepositoryBindings(bindings []RepositoryBinding) []RepositoryBinding {
	out := make([]RepositoryBinding, len(bindings))
	for index, binding := range bindings {
		binding.BuildCommands = append([]string(nil), binding.BuildCommands...)
		binding.TestCommands = append([]string(nil), binding.TestCommands...)
		out[index] = binding
	}
	return out
}

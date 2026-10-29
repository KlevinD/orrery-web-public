package developmentproject

import (
	"context"
	"errors"
	"strings"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/scmconnection"
	platformrealtime "orrery_v2/internal/platform/realtime"
)

func (s *Service) getAuthorized(ctx context.Context, viewer ViewerRef, projectID, action string) (Project, error) {
	if s == nil || s.repo == nil || s.access == nil {
		return Project{}, ErrDependency
	}
	if err := validateViewer(viewer); err != nil {
		return Project{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if !validResourceID(projectID) {
		return Project{}, ErrInvalidRequest
	}
	project, err := s.repo.Get(ctx, viewerTenant(viewer), projectID)
	if err != nil {
		return Project{}, err
	}
	if err := s.access.Can(ctx, viewerPrincipal(viewer), action, projectObject(project)); err != nil {
		if errors.Is(err, authorization.ErrDenied) {
			return Project{}, ErrNotFound
		}
		return Project{}, err
	}
	return project, nil
}

func (s *Service) authorizeCreate(ctx context.Context, viewer ViewerRef, organizationID string) error {
	object := identity.ObjectRef{Type: "platform", ID: "global", TenantID: viewerTenant(viewer)}
	if organizationID = strings.TrimSpace(organizationID); organizationID != "" {
		object = identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: viewerTenant(viewer)}
	}
	return s.access.Can(ctx, viewerPrincipal(viewer), "development_project.create", object)
}

func (s *Service) dto(ctx context.Context, viewer ViewerRef, project Project) (ProjectDTO, error) {
	capabilities, err := s.capabilities(ctx, viewer, project)
	if err != nil {
		return ProjectDTO{}, err
	}
	if !capabilities.CanView {
		return ProjectDTO{}, ErrNotFound
	}
	policy := project.ExecutionPolicy
	if !capabilities.CanConfigure {
		policy.AllowedHosts = nil
		policy.AllowedCommandPrefixes = nil
		policy.AllowedFileScopes = nil
	}
	executionRealtimeChannel, err := platformrealtime.ProjectChannel(project.TenantID, project.DevelopmentProjectID)
	if err != nil {
		return ProjectDTO{}, ErrDependency
	}
	return ProjectDTO{
		DevelopmentProjectID: project.DevelopmentProjectID, ExecutionRealtimeChannel: executionRealtimeChannel, OrganizationID: project.OrganizationID,
		Name: project.Name, Description: project.Description, Status: project.Status,
		DefaultAutomationMode: project.DefaultAutomationMode, MaxConcurrentRuns: project.MaxConcurrentRuns,
		ExecutionPolicy: ExecutionPolicyDTO{
			ApprovalRequired: policy.ApprovalRequired, NetworkMode: policy.NetworkMode,
			AllowedHosts:           append([]string(nil), policy.AllowedHosts...),
			AllowedCommandPrefixes: append([]string(nil), policy.AllowedCommandPrefixes...),
			AllowedFileScopes:      append([]string(nil), policy.AllowedFileScopes...),
			AllowedSecretCount:     len(policy.AllowedSecretRefs),
		},
		CreatedBy: project.CreatedBy, CreatedAt: project.CreatedAt, UpdatedAt: project.UpdatedAt,
		Version: project.Version, MemberCount: len(project.Members), RepositoryCount: len(project.RepositoryBindings),
		Capabilities: capabilities,
	}, nil
}

func (s *Service) capabilities(ctx context.Context, viewer ViewerRef, project Project) (Capabilities, error) {
	actions := []string{
		"development_project.view", "development_project.monitor", "development_project.edit", "development_project.configure", "development_project.manage",
		"development_project.run", "development_project.submit", "development_project.approve", "development_project.merge",
	}
	checks := make([]authorization.CheckRequest, 0, len(actions))
	for _, action := range actions {
		checks = append(checks, authorization.CheckRequest{Principal: viewerPrincipal(viewer), Action: action, Object: projectObject(project)})
	}
	decisions, err := s.access.BatchCan(ctx, checks)
	if err != nil {
		return Capabilities{}, err
	}
	if len(decisions) != len(checks) {
		return Capabilities{}, ErrDependency
	}
	active := project.Status == StatusActive
	return Capabilities{
		CanView: decisions[0].Allowed, CanMonitor: decisions[1].Allowed, CanUpdate: active && decisions[2].Allowed,
		CanConfigure:     active && decisions[3].Allowed,
		CanManageMembers: active && decisions[4].Allowed, CanBindRepository: active && decisions[3].Allowed,
		CanRequestExecution: active && decisions[5].Allowed, CanSubmit: active && decisions[6].Allowed,
		CanApprove: active && decisions[7].Allowed, CanMerge: active && decisions[8].Allowed,
		CanArchive: active && decisions[4].Allowed,
	}, nil
}

func projectObject(project Project) identity.ObjectRef {
	return identity.ObjectRef{Type: "development_project", ID: project.DevelopmentProjectID, TenantID: project.TenantID}
}

func viewerPrincipal(viewer ViewerRef) identity.Principal {
	subjectID := viewerSubjectID(viewer)
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: identity.ProviderLocal, Subject: subjectID, UserID: subjectID, Username: subjectID,
		ViewerKind: viewer.ViewerKind, TenantID: viewerTenant(viewer), CoarseScopes: append([]string(nil), viewer.PermissionScope...),
	})
}

func viewerSubject(viewer ViewerRef) authorization.SubjectRef {
	return authorization.SubjectRef{Kind: viewer.ViewerKind, ID: viewerSubjectID(viewer)}
}

func viewerSubjectID(viewer ViewerRef) string {
	if value := strings.TrimSpace(viewer.AuthorizationSubjectID); value != "" {
		return value
	}
	return strings.TrimSpace(viewer.ViewerID)
}

func viewerTenant(viewer ViewerRef) string { return normalizeTenantID(viewer.TenantID) }

func validateViewer(viewer ViewerRef) error {
	if !oneOf(strings.ToLower(strings.TrimSpace(viewer.ViewerKind)), "user", "agent") ||
		!validSubjectID(strings.TrimSpace(viewer.ViewerID)) ||
		!validSubjectID(viewerSubjectID(viewer)) ||
		!validTenantKeySegment(viewerTenant(viewer)) {
		return ErrInvalidRequest
	}
	return nil
}

func viewerForSCM(viewer ViewerRef) scmconnection.ViewerRef {
	return scmconnection.ViewerRef{
		ViewerID: viewer.ViewerID, ViewerKind: viewer.ViewerKind, TenantID: viewerTenant(viewer),
		AuthorizationSubjectID: viewer.AuthorizationSubjectID, PermissionScope: append([]string(nil), viewer.PermissionScope...),
	}
}

func (s *Service) requireMutationDependencies(requireConnections bool) error {
	if s == nil || s.repo == nil || s.access == nil || s.relations == nil || s.audit == nil {
		return ErrDependency
	}
	if requireConnections && s.connections == nil {
		return ErrDependency
	}
	return nil
}

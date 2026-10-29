package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/permissionscope"
	"orrery_v2/internal/app/projectadmin"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/db"
	toolres "orrery_v2/internal/resource/tool"
)

type productionOntologyScopeState struct {
	Store      *ontologyscope.PlatformBindingStore
	Resolver   *ontologyscope.Resolver
	Gate       *ontologyscope.ScopeAccessGate
	Snapshots  authorization.SnapshotProvider
	Authorizer productionScopeAccessAuthorizer
}

func newProductionOntologyScopeState(
	core *db.Store,
	organizations *operations.OrganizationCatalogStore,
	projects projectadmin.Repository,
	missions missioncontrol.ProjectionRepository,
	tools toolres.Registry,
	authorizer *authorization.Service,
	snapshots authorization.SnapshotProvider,
	roleAuthorizers ...productionRolePermissionAuthorizer,
) (*productionOntologyScopeState, error) {
	if core == nil || organizations == nil || projects == nil || missions == nil || tools == nil || authorizer == nil || snapshots == nil {
		return nil, fmt.Errorf("ontology scope binding dependencies are required")
	}
	store, resolver, err := newProductionOntologyBindingResolver(core, organizations, projects, missions, tools)
	if err != nil {
		return nil, err
	}
	var roleAuthorizer productionRolePermissionAuthorizer
	if len(roleAuthorizers) > 0 {
		roleAuthorizer = roleAuthorizers[0]
	}
	access := productionScopeAccessAuthorizer{service: authorizer, roles: roleAuthorizer}
	return &productionOntologyScopeState{
		Store:      store,
		Resolver:   resolver,
		Gate:       ontologyscope.NewScopeAccessGate(resolver, access, snapshots),
		Snapshots:  snapshots,
		Authorizer: access,
	}, nil
}

func newProductionOntologyBindingResolver(
	core *db.Store,
	organizations *operations.OrganizationCatalogStore,
	projects projectadmin.Repository,
	missions missioncontrol.ProjectionRepository,
	tools toolres.Registry,
) (*ontologyscope.PlatformBindingStore, *ontologyscope.Resolver, error) {
	if core == nil || organizations == nil || projects == nil || missions == nil || tools == nil {
		return nil, nil, fmt.Errorf("ontology scope binding resolver dependencies are required")
	}
	store := ontologyscope.NewPlatformBindingStore(core)
	resolver := ontologyscope.NewResolver(store,
		organizationBindingSource(organizations),
		projectBindingSource(projects),
		missionBindingSource(missions),
		resourceBindingSource(tools, "tool:"),
		resourceBindingSource(tools, "resource:"),
	)
	return store, resolver, nil
}

func organizationBindingSource(catalog *operations.OrganizationCatalogStore) ontologyscope.BindingSource {
	return ontologyscope.BindingSourceFunc(func(ctx context.Context, tenantID string) (ontologyscope.ScopeBinding, bool, error) {
		organization, found, err := catalog.GetOrganization(ctx, tenantID)
		if err != nil {
			return ontologyscope.ScopeBinding{}, false, err
		}
		if !found {
			return ontologyscope.ScopeBinding{}, false, nil
		}
		if strings.TrimSpace(organization.OrganizationID) != tenantID {
			return ontologyscope.ScopeBinding{}, false, ontologyscope.ErrBindingConflict
		}
		return canonicalProductionBinding(tenantID, ontologyscope.KindOrganization, "organization", organization.OrganizationID, organization.OrganizationID, 1), true, nil
	})
}

func projectBindingSource(repository projectadmin.Repository) ontologyscope.BindingSource {
	return ontologyscope.BindingSourceFunc(func(ctx context.Context, tenantID string) (ontologyscope.ScopeBinding, bool, error) {
		projectTenant, projectID, ok := canonicalProjectTenantSource(tenantID)
		if !ok {
			return ontologyscope.ScopeBinding{}, false, nil
		}
		project, err := repository.GetProject(ctx, projectTenant, projectID)
		if errors.Is(err, projectadmin.ErrNotFound) {
			return ontologyscope.ScopeBinding{}, false, nil
		}
		if err != nil {
			return ontologyscope.ScopeBinding{}, false, err
		}
		if strings.TrimSpace(project.ProjectID) != projectID {
			return ontologyscope.ScopeBinding{}, false, ontologyscope.ErrBindingConflict
		}
		return canonicalProductionBinding(tenantID, ontologyscope.KindProject, "project", projectID, project.TenantID, 1), true, nil
	})
}

func missionBindingSource(repository missioncontrol.ProjectionRepository) ontologyscope.BindingSource {
	return ontologyscope.BindingSourceFunc(func(ctx context.Context, tenantID string) (ontologyscope.ScopeBinding, bool, error) {
		missionTenant, missionID, ok := canonicalMissionTenantSource(tenantID)
		if !ok {
			return ontologyscope.ScopeBinding{}, false, nil
		}
		tenantRepo, ok := repository.(missioncontrol.TenantProjectionRepository)
		if !ok {
			return ontologyscope.ScopeBinding{}, false, ontologyscope.ErrDependencyUnavailable
		}
		model, err := tenantRepo.GetMissionControlForTenant(ctx, missionTenant, missioncore.MissionID(missionID))
		if errors.Is(err, missioncontrol.ErrProjectionNotFound) {
			return ontologyscope.ScopeBinding{}, false, nil
		}
		if err != nil {
			return ontologyscope.ScopeBinding{}, false, err
		}
		if model.Mission.MissionID.String() != missionID {
			return ontologyscope.ScopeBinding{}, false, ontologyscope.ErrBindingConflict
		}
		return canonicalProductionBinding(tenantID, ontologyscope.KindMission, "mission", missionID, missionTenant, 1), true, nil
	})
}

type productionResourceBindingSource struct {
	registry toolres.Registry
	prefix   string
}

type productionToolBatchGetter interface {
	GetMany(context.Context, []agentcore.ToolID) (map[agentcore.ToolID]toolres.ToolDefinition, error)
}

func resourceBindingSource(registry toolres.Registry, prefix string) ontologyscope.BindingSource {
	return productionResourceBindingSource{registry: registry, prefix: prefix}
}

func (s productionResourceBindingSource) ResolveBinding(ctx context.Context, tenantID string) (ontologyscope.ScopeBinding, bool, error) {
	resourceID, ok := canonicalTenantSourceID(tenantID, s.prefix)
	if !ok {
		return ontologyscope.ScopeBinding{}, false, nil
	}
	definition, err := s.registry.Get(ctx, agentcore.ToolID(resourceID))
	if errors.Is(err, toolres.ErrNotFound) {
		return ontologyscope.ScopeBinding{}, false, nil
	}
	if err != nil {
		return ontologyscope.ScopeBinding{}, false, err
	}
	if strings.TrimSpace(string(definition.ID)) != resourceID {
		return ontologyscope.ScopeBinding{}, false, ontologyscope.ErrBindingConflict
	}
	return canonicalProductionBinding(tenantID, ontologyscope.KindResource, "resource", resourceID, "global", 1), true, nil
}

func (s productionResourceBindingSource) ResolveBindings(ctx context.Context, tenantIDs []string) (map[string]ontologyscope.ScopeBinding, error) {
	matched := make(map[string]agentcore.ToolID)
	ids := make([]agentcore.ToolID, 0, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		resourceID, ok := canonicalTenantSourceID(tenantID, s.prefix)
		if !ok {
			continue
		}
		id := agentcore.ToolID(resourceID)
		matched[tenantID] = id
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return map[string]ontologyscope.ScopeBinding{}, nil
	}
	definitions := make(map[agentcore.ToolID]toolres.ToolDefinition, len(ids))
	if batch, ok := s.registry.(productionToolBatchGetter); ok {
		var err error
		definitions, err = batch.GetMany(ctx, ids)
		if err != nil {
			return nil, err
		}
	} else {
		for _, id := range ids {
			definition, err := s.registry.Get(ctx, id)
			if errors.Is(err, toolres.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			definitions[id] = definition
		}
	}
	out := make(map[string]ontologyscope.ScopeBinding, len(definitions))
	for tenantID, id := range matched {
		definition, found := definitions[id]
		if !found {
			continue
		}
		resourceID := strings.TrimSpace(string(definition.ID))
		if resourceID != string(id) {
			return nil, ontologyscope.ErrBindingConflict
		}
		out[tenantID] = canonicalProductionBinding(tenantID, ontologyscope.KindResource, "resource", resourceID, "global", 1)
	}
	return out, nil
}

func canonicalProductionBinding(tenantID string, kind ontologyscope.Kind, objectType, objectID, authorizationTenantID string, version int64) ontologyscope.ScopeBinding {
	readAction := objectType + ".view"
	if objectType == "organization" {
		readAction = "organization.read"
	}
	return ontologyscope.ScopeBinding{
		TenantID:              strings.TrimSpace(tenantID),
		Kind:                  kind,
		SourceObject:          identity.ObjectRef{Type: objectType, ID: strings.TrimSpace(objectID)},
		AuthorizationTenantID: strings.TrimSpace(authorizationTenantID),
		ReadAction:            readAction,
		ManageAction:          objectType + ".manage",
		LabelRef:              identity.ObjectRef{Type: objectType, ID: strings.TrimSpace(objectID), TenantID: strings.TrimSpace(authorizationTenantID)},
		BindingVersion:        version,
		State:                 ontologyscope.BindingStateActive,
	}
}

func canonicalTenantSourceID(tenantID, prefix string) (string, bool) {
	tenantID = strings.TrimSpace(tenantID)
	if !strings.HasPrefix(tenantID, prefix) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimPrefix(tenantID, prefix))
	return id, id != "" && tenantID == prefix+id
}

func projectScopeTenantID(tenantID, projectID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		tenantID = "global"
	}
	return "project:" + tenantID + "/" + strings.TrimSpace(projectID)
}

func canonicalProjectTenantSource(value string) (string, string, bool) {
	raw, ok := canonicalTenantSourceID(value, "project:")
	if !ok {
		return "", "", false
	}
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	tenantID := strings.TrimSpace(parts[0])
	projectID := strings.TrimSpace(parts[1])
	return tenantID, projectID, value == projectScopeTenantID(tenantID, projectID)
}

func missionScopeTenantID(tenantID, missionID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		tenantID = "global"
	}
	return "mission:" + tenantID + "/" + strings.TrimSpace(missionID)
}

func canonicalMissionTenantSource(value string) (string, string, bool) {
	raw, ok := canonicalTenantSourceID(value, "mission:")
	if !ok {
		return "", "", false
	}
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	tenantID := strings.TrimSpace(parts[0])
	missionID := strings.TrimSpace(parts[1])
	return tenantID, missionID, value == missionScopeTenantID(tenantID, missionID)
}

type productionScopeAccessAuthorizer struct {
	service *authorization.Service
	roles   productionRolePermissionAuthorizer
}

type productionRolePermissionAuthorizer interface {
	AuthorizeRolePermission(ctx context.Context, principal identity.Principal, permission string, objects []identity.ObjectRef) (bool, error)
}

func (a productionScopeAccessAuthorizer) AuthorizeFeature(ctx context.Context, principal identity.Principal, action string) error {
	if a.service == nil {
		return authorization.ErrServiceDependency
	}
	object := identity.ObjectRef{Type: "platform", ID: "global", TenantID: "global"}
	decision, err := a.service.Check(ctx, authorization.CheckRequest{Principal: principal, Action: action, Object: object, TenantID: "global"})
	if err != nil {
		return err
	}
	if !decision.Allowed {
		if identity.TrustedInternalServiceFromContext(ctx) {
			if roleErr := a.authorizeRolePermission(ctx, principal, action, object); roleErr == nil {
				return nil
			} else if !errors.Is(roleErr, authorization.ErrDenied) {
				return roleErr
			}
		}
		return fmt.Errorf("%w: %s", authorization.ErrDenied, decision.Reason)
	}
	switch strings.TrimSpace(decision.Source) {
	case authorization.DecisionSourceScope:
		if ontologyCoarseReadAllowed(principal, action) {
			return nil
		}
		return a.authorizeRolePermission(ctx, principal, action, object)
	case authorization.DecisionSourceReBAC, authorization.DecisionSourceOpenFGA:
		return nil
	default:
		return authorization.ErrServiceDependency
	}
}

func (a productionScopeAccessAuthorizer) AuthorizeObject(ctx context.Context, principal identity.Principal, action string, object identity.ObjectRef, tenantID string) error {
	if a.service == nil {
		return authorization.ErrServiceDependency
	}
	decision, err := a.service.Check(ctx, productionOntologyObjectAuthorizationCheck(principal, action, object, tenantID))
	if err != nil {
		return err
	}
	return a.authorizeObjectDecision(ctx, principal, action, object, decision)
}

func (a productionScopeAccessAuthorizer) AuthorizeObjects(ctx context.Context, principal identity.Principal, requests []ontologyscope.ObjectAuthorizationRequest) ([]bool, error) {
	if a.service == nil {
		return nil, authorization.ErrServiceDependency
	}
	checks := make([]authorization.CheckRequest, len(requests))
	for index, request := range requests {
		checks[index] = productionOntologyObjectAuthorizationCheck(principal, request.Action, request.Object, request.AuthorizationTenantID)
	}
	decisions, err := a.service.BatchCan(ctx, checks)
	if err != nil {
		return nil, err
	}
	if len(decisions) != len(requests) {
		return nil, authorization.ErrServiceDependency
	}
	allowed := make([]bool, len(requests))
	for index, request := range requests {
		err := a.authorizeObjectDecision(ctx, principal, request.Action, request.Object, decisions[index])
		if errors.Is(err, authorization.ErrDenied) {
			continue
		}
		if err != nil {
			return nil, err
		}
		allowed[index] = true
	}
	return allowed, nil
}

func (a productionScopeAccessAuthorizer) authorizeObjectDecision(ctx context.Context, principal identity.Principal, action string, object identity.ObjectRef, decision authorization.Decision) error {
	source := strings.TrimSpace(decision.Source)
	if !decision.Allowed {
		if identity.TrustedInternalServiceFromContext(ctx) {
			if roleErr := a.authorizeLocalObjectRole(ctx, principal, action, object); roleErr == nil {
				return nil
			} else if !errors.Is(roleErr, authorization.ErrDenied) {
				return roleErr
			}
		}
		return fmt.Errorf("%w: %s", authorization.ErrDenied, decision.Reason)
	}
	if source == authorization.DecisionSourceScope {
		if ontologySourceReadAction(action) && identity.NormalizePrincipal(principal).ViewerKind != identity.ViewerKindSystem {
			return nil
		}
		return a.authorizeLocalObjectRole(ctx, principal, action, object)
	}
	if source != authorization.DecisionSourceReBAC && source != authorization.DecisionSourceOpenFGA {
		return fmt.Errorf("%w: source-object authorization decision source is unavailable", authorization.ErrServiceDependency)
	}
	return nil
}

func productionOntologyObjectAuthorizationCheck(principal identity.Principal, action string, object identity.ObjectRef, tenantID string) authorization.CheckRequest {
	check := authorization.CheckRequest{Principal: principal, Action: action, Object: object, TenantID: tenantID}
	if ontologySourceReadAction(action) {
		check.CoarseScopeAlternatives = []string{permissionscope.EnterpriseGraphRead}
	}
	return check
}

func ontologyCoarseReadAllowed(principal identity.Principal, action string) bool {
	principal = identity.NormalizePrincipal(principal)
	return principal.ViewerKind != identity.ViewerKindSystem &&
		strings.EqualFold(strings.TrimSpace(action), ontologyscope.FeatureReadAction) &&
		permissionscope.AnyAllows(principal.CoarseScopes, permissionscope.OntologyRead)
}

func ontologyReadScopeGranted(principal identity.Principal) bool {
	principal = identity.NormalizePrincipal(principal)
	return principal.ViewerKind != identity.ViewerKindSystem &&
		permissionscope.AnyAllows(principal.CoarseScopes, permissionscope.EnterpriseGraphRead)
}

func ontologySourceReadAction(action string) bool {
	action = strings.ToLower(strings.TrimSpace(action))
	return strings.HasSuffix(action, ".read") || strings.HasSuffix(action, ".view")
}

func (a productionScopeAccessAuthorizer) authorizeLocalObjectRole(ctx context.Context, principal identity.Principal, action string, object identity.ObjectRef) error {
	permission := ""
	switch strings.ToLower(strings.TrimSpace(object.Type)) + ":" + strings.ToLower(strings.TrimSpace(action)) {
	case "organization:organization.read", "department:department.read":
		permission = operations.PermissionOrgRead
	case "organization:organization.manage", "department:department.manage":
		permission = operations.PermissionOrgManage
	case "platform:operations.read":
		permission = operations.PermissionOperationsRead
	case "platform:operations.manage":
		permission = operations.PermissionOperationsManage
	default:
		// The local adapter cannot prove object-level access for these scopes.
		// Treat that as a denial so catalog enumeration can omit the object
		// without turning one inaccessible scope into a service-wide outage.
		return fmt.Errorf("%w: source-object authorization requires a relation decision", authorization.ErrDenied)
	}
	return a.authorizeRolePermission(ctx, principal, permission, object)
}

func (a productionScopeAccessAuthorizer) authorizeRolePermission(ctx context.Context, principal identity.Principal, permission string, object identity.ObjectRef) error {
	principal = identity.NormalizePrincipal(principal)
	if principal.ViewerKind == identity.ViewerKindSystem {
		return authorization.ErrDenied
	}
	if a.roles == nil {
		return authorization.ErrServiceDependency
	}
	allowed, err := a.roles.AuthorizeRolePermission(ctx, principal, permission, []identity.ObjectRef{object})
	if err != nil {
		return err
	}
	if !allowed {
		return authorization.ErrDenied
	}
	return nil
}

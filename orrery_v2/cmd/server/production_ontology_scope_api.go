package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	missioncore "orrery_v2/internal/mission/core"
	toolres "orrery_v2/internal/resource/tool"
)

type productionScopeCatalogSource struct {
	kind    ontologyscope.Kind
	list    func(context.Context, string, int) (ontologyscope.CandidatePage, error)
	get     func(context.Context, ontologyscope.ScopeBinding) (ontologyscope.CatalogCandidate, bool, error)
	resolve func(context.Context, string) (ontologyscope.ScopeBinding, ontologyscope.CatalogCandidate, bool, error)
}

func (s productionScopeCatalogSource) Kind() ontologyscope.Kind { return s.kind }

func (s productionScopeCatalogSource) ListCandidates(ctx context.Context, after string, limit int) (ontologyscope.CandidatePage, error) {
	if s.list == nil {
		return ontologyscope.CandidatePage{}, ontologyscope.ErrDependencyUnavailable
	}
	return s.list(ctx, after, limit)
}

func (s productionScopeCatalogSource) GetCandidate(ctx context.Context, binding ontologyscope.ScopeBinding) (ontologyscope.CatalogCandidate, bool, error) {
	if s.get == nil {
		return ontologyscope.CatalogCandidate{}, false, ontologyscope.ErrDependencyUnavailable
	}
	return s.get(ctx, binding)
}

func (s productionScopeCatalogSource) ResolveCatalogCandidate(ctx context.Context, tenantID string) (ontologyscope.ScopeBinding, ontologyscope.CatalogCandidate, bool, error) {
	if s.resolve == nil {
		return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, ontologyscope.ErrDependencyUnavailable
	}
	return s.resolve(ctx, strings.TrimSpace(tenantID))
}

func newProductionOntologyScopeAPI(
	scopeState *productionOntologyScopeState,
	cursors *productionOntologyCursorState,
	organizations *operations.OrganizationCatalogStore,
	projects *projectadmin.PlatformRepository,
	missions *missioncontrol.DBProjectionRepository,
	tools *toolres.PlatformRegistry,
) (*ontologyscope.Catalog, error) {
	if scopeState == nil || scopeState.Gate == nil || scopeState.Store == nil || cursors == nil || cursors.Stateful == nil || organizations == nil || projects == nil || missions == nil || tools == nil {
		return nil, fmt.Errorf("ontology scope API dependencies are required")
	}
	return ontologyscope.NewCatalog(
		scopeState.Gate,
		ontologyscope.CatalogCursorSet{Encrypted: cursors.Encrypted, Stateful: cursors.Stateful},
		nil,
		productionOrganizationCatalogSource(organizations),
		productionProjectCatalogSource(projects),
		productionMissionCatalogSource(missions),
		productionResourceCatalogSource(tools),
		productionCustomCatalogSource(scopeState.Store),
	)
}

func productionOrganizationCatalogSource(catalog *operations.OrganizationCatalogStore) ontologyscope.CatalogSource {
	return productionScopeCatalogSource{
		kind: ontologyscope.KindOrganization,
		list: func(ctx context.Context, after string, limit int) (ontologyscope.CandidatePage, error) {
			organizations, hasMore, err := catalog.ListOrganizationCatalog(ctx, after, limit)
			if err != nil {
				return ontologyscope.CandidatePage{}, err
			}
			items := make([]ontologyscope.CatalogCandidate, 0, len(organizations))
			for _, organization := range organizations {
				binding := canonicalProductionBinding(organization.OrganizationID, ontologyscope.KindOrganization, "organization", organization.OrganizationID, organization.OrganizationID, 1)
				items = append(items, ontologyscope.CatalogCandidate{
					TenantID: organization.OrganizationID,
					Kind:     ontologyscope.KindOrganization,
					Seek:     organization.OrganizationID,
					Label:    organization.Name,
					Binding:  binding,
				})
			}
			return productionCandidatePage(items, after, hasMore), nil
		},
		get: func(ctx context.Context, binding ontologyscope.ScopeBinding) (ontologyscope.CatalogCandidate, bool, error) {
			organization, found, err := catalog.GetOrganization(ctx, binding.SourceObject.ID)
			if err != nil || !found {
				return ontologyscope.CatalogCandidate{}, found, err
			}
			return ontologyscope.CatalogCandidate{TenantID: binding.TenantID, Kind: binding.Kind, Seek: organization.OrganizationID, Label: organization.Name}, true, nil
		},
		resolve: func(ctx context.Context, tenantID string) (ontologyscope.ScopeBinding, ontologyscope.CatalogCandidate, bool, error) {
			if strings.HasPrefix(tenantID, "project:") || strings.HasPrefix(tenantID, "mission:") || strings.HasPrefix(tenantID, "resource:") || strings.HasPrefix(tenantID, "tool:") {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, nil
			}
			organization, found, err := catalog.GetOrganization(ctx, tenantID)
			if err != nil || !found {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, err
			}
			binding := canonicalProductionBinding(tenantID, ontologyscope.KindOrganization, "organization", organization.OrganizationID, organization.OrganizationID, 1)
			candidate := ontologyscope.CatalogCandidate{TenantID: tenantID, Kind: ontologyscope.KindOrganization, Seek: organization.OrganizationID, Label: organization.Name}
			return binding, candidate, true, nil
		},
	}
}

func productionProjectCatalogSource(repository *projectadmin.PlatformRepository) ontologyscope.CatalogSource {
	return productionScopeCatalogSource{
		kind: ontologyscope.KindProject,
		list: func(ctx context.Context, after string, limit int) (ontologyscope.CandidatePage, error) {
			projects, hasMore, err := repository.ListProjectCatalog(ctx, after, limit)
			if err != nil {
				return ontologyscope.CandidatePage{}, err
			}
			items := make([]ontologyscope.CatalogCandidate, 0, len(projects))
			for _, project := range projects {
				tenantID := projectScopeTenantID(project.TenantID, project.ProjectID)
				binding := canonicalProductionBinding(tenantID, ontologyscope.KindProject, "project", project.ProjectID, project.TenantID, 1)
				items = append(items, ontologyscope.CatalogCandidate{
					TenantID:       tenantID,
					Kind:           ontologyscope.KindProject,
					Seek:           project.ProjectID,
					Label:          project.Title,
					ParentTenantID: project.OrganizationID,
					ActivityAt:     project.UpdatedAt,
					Binding:        binding,
				})
			}
			return productionCandidatePage(items, after, hasMore), nil
		},
		get: func(ctx context.Context, binding ontologyscope.ScopeBinding) (ontologyscope.CatalogCandidate, bool, error) {
			project, err := repository.GetProject(ctx, binding.AuthorizationTenantID, binding.SourceObject.ID)
			if errors.Is(err, projectadmin.ErrNotFound) {
				return ontologyscope.CatalogCandidate{}, false, nil
			}
			if err != nil {
				return ontologyscope.CatalogCandidate{}, false, err
			}
			return ontologyscope.CatalogCandidate{TenantID: binding.TenantID, Kind: binding.Kind, Seek: project.ProjectID, Label: project.Title, ParentTenantID: project.OrganizationID, ActivityAt: project.UpdatedAt}, true, nil
		},
		resolve: func(ctx context.Context, tenantID string) (ontologyscope.ScopeBinding, ontologyscope.CatalogCandidate, bool, error) {
			projectTenant, projectID, ok := canonicalProjectTenantSource(tenantID)
			if !ok {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, nil
			}
			project, err := repository.GetProject(ctx, projectTenant, projectID)
			if errors.Is(err, projectadmin.ErrNotFound) {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, nil
			}
			if err != nil {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, err
			}
			binding := canonicalProductionBinding(tenantID, ontologyscope.KindProject, "project", projectID, project.TenantID, 1)
			candidate := ontologyscope.CatalogCandidate{TenantID: tenantID, Kind: ontologyscope.KindProject, Seek: project.ProjectID, Label: project.Title, ParentTenantID: project.OrganizationID, ActivityAt: project.UpdatedAt}
			return binding, candidate, true, nil
		},
	}
}

func productionMissionCatalogSource(repository *missioncontrol.DBProjectionRepository) ontologyscope.CatalogSource {
	return productionScopeCatalogSource{
		kind: ontologyscope.KindMission,
		list: func(ctx context.Context, after string, limit int) (ontologyscope.CandidatePage, error) {
			entries, hasMore, err := repository.ListMissionCatalogEntries(ctx, after, limit)
			if err != nil {
				return ontologyscope.CandidatePage{}, err
			}
			items := make([]ontologyscope.CatalogCandidate, 0, len(entries))
			for _, entry := range entries {
				id := entry.MissionID.String()
				tenantID := missionScopeTenantID(entry.TenantID, id)
				binding := canonicalProductionBinding(tenantID, ontologyscope.KindMission, "mission", id, entry.TenantID, 1)
				items = append(items, ontologyscope.CatalogCandidate{TenantID: tenantID, Kind: ontologyscope.KindMission, Seek: entry.Seek, Label: entry.Title, ActivityAt: entry.UpdatedAt, Binding: binding})
			}
			return productionCandidatePage(items, after, hasMore), nil
		},
		get: func(ctx context.Context, binding ontologyscope.ScopeBinding) (ontologyscope.CatalogCandidate, bool, error) {
			model, err := repository.GetMissionControlForTenant(ctx, binding.AuthorizationTenantID, missioncore.MissionID(binding.SourceObject.ID))
			if errors.Is(err, missioncontrol.ErrProjectionNotFound) {
				return ontologyscope.CatalogCandidate{}, false, nil
			}
			if err != nil {
				return ontologyscope.CatalogCandidate{}, false, err
			}
			return ontologyscope.CatalogCandidate{TenantID: binding.TenantID, Kind: binding.Kind, Seek: model.Mission.MissionID.String(), Label: model.Mission.Title, ActivityAt: model.UpdatedAt}, true, nil
		},
		resolve: func(ctx context.Context, tenantID string) (ontologyscope.ScopeBinding, ontologyscope.CatalogCandidate, bool, error) {
			missionTenant, missionID, ok := canonicalMissionTenantSource(tenantID)
			if !ok {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, nil
			}
			model, err := repository.GetMissionControlForTenant(ctx, missionTenant, missioncore.MissionID(missionID))
			if errors.Is(err, missioncontrol.ErrProjectionNotFound) {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, nil
			}
			if err != nil {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, err
			}
			binding := canonicalProductionBinding(tenantID, ontologyscope.KindMission, "mission", missionID, missionTenant, 1)
			candidate := ontologyscope.CatalogCandidate{TenantID: tenantID, Kind: ontologyscope.KindMission, Seek: missionID, Label: model.Mission.Title, ActivityAt: model.UpdatedAt}
			return binding, candidate, true, nil
		},
	}
}

func productionResourceCatalogSource(registry *toolres.PlatformRegistry) ontologyscope.CatalogSource {
	return productionScopeCatalogSource{
		kind: ontologyscope.KindResource,
		list: func(ctx context.Context, after string, limit int) (ontologyscope.CandidatePage, error) {
			page, err := registry.ListCatalogEntries(ctx, after, limit)
			if err != nil {
				return ontologyscope.CandidatePage{}, err
			}
			items := make([]ontologyscope.CatalogCandidate, 0, len(page.Entries))
			for _, entry := range page.Entries {
				id := string(entry.ID)
				tenantID := "resource:" + id
				binding := canonicalProductionBinding(tenantID, ontologyscope.KindResource, "resource", id, "global", 1)
				items = append(items, ontologyscope.CatalogCandidate{TenantID: tenantID, Kind: ontologyscope.KindResource, Seek: id, Label: entry.Name, ActivityAt: entry.UpdatedAt, Binding: binding})
			}
			return ontologyscope.CandidatePage{Items: items, NextAfter: page.NextAfterID, Scanned: page.Scanned, HasMore: page.HasMore}, nil
		},
		get: func(ctx context.Context, binding ontologyscope.ScopeBinding) (ontologyscope.CatalogCandidate, bool, error) {
			definition, err := registry.Get(ctx, agentcore.ToolID(binding.SourceObject.ID))
			if errors.Is(err, toolres.ErrNotFound) {
				return ontologyscope.CatalogCandidate{}, false, nil
			}
			if err != nil {
				return ontologyscope.CatalogCandidate{}, false, err
			}
			return ontologyscope.CatalogCandidate{TenantID: binding.TenantID, Kind: binding.Kind, Seek: string(definition.ID), Label: definition.Name, ActivityAt: definition.UpdatedAt}, true, nil
		},
		resolve: func(ctx context.Context, tenantID string) (ontologyscope.ScopeBinding, ontologyscope.CatalogCandidate, bool, error) {
			resourceID, ok := canonicalTenantSourceID(tenantID, "resource:")
			if !ok {
				resourceID, ok = canonicalTenantSourceID(tenantID, "tool:")
			}
			if !ok {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, nil
			}
			definition, err := registry.Get(ctx, agentcore.ToolID(resourceID))
			if errors.Is(err, toolres.ErrNotFound) {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, nil
			}
			if err != nil {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, err
			}
			binding := canonicalProductionBinding(tenantID, ontologyscope.KindResource, "resource", resourceID, "global", 1)
			candidate := ontologyscope.CatalogCandidate{TenantID: tenantID, Kind: ontologyscope.KindResource, Seek: resourceID, Label: definition.Name, ActivityAt: definition.UpdatedAt}
			return binding, candidate, true, nil
		},
	}
}

func productionCustomCatalogSource(store *ontologyscope.PlatformBindingStore) ontologyscope.CatalogSource {
	return productionScopeCatalogSource{
		kind: ontologyscope.KindCustom,
		list: func(ctx context.Context, after string, limit int) (ontologyscope.CandidatePage, error) {
			bindings, err := store.ListBindings(ctx, ontologyscope.BindingQuery{Kind: ontologyscope.KindCustom, State: ontologyscope.BindingStateActive, AfterTenantID: after, Limit: limit + 1})
			if err != nil {
				return ontologyscope.CandidatePage{}, err
			}
			hasMore := len(bindings) > limit
			scanned := len(bindings)
			if hasMore {
				bindings = bindings[:limit]
			}
			items := make([]ontologyscope.CatalogCandidate, 0, len(bindings))
			for _, binding := range bindings {
				items = append(items, ontologyscope.CatalogCandidate{TenantID: binding.TenantID, Kind: binding.Kind, Seek: binding.TenantID, Label: binding.TenantID, ActivityAt: binding.UpdatedAt, Binding: binding})
			}
			next := after
			if len(items) > 0 {
				next = items[len(items)-1].Seek
			}
			return ontologyscope.CandidatePage{Items: items, NextAfter: next, Scanned: scanned, HasMore: hasMore}, nil
		},
		get: func(_ context.Context, binding ontologyscope.ScopeBinding) (ontologyscope.CatalogCandidate, bool, error) {
			if binding.Kind != ontologyscope.KindCustom || binding.State != ontologyscope.BindingStateActive {
				return ontologyscope.CatalogCandidate{}, false, nil
			}
			return ontologyscope.CatalogCandidate{TenantID: binding.TenantID, Kind: binding.Kind, Seek: binding.TenantID, Label: binding.TenantID, ActivityAt: binding.UpdatedAt}, true, nil
		},
		resolve: func(ctx context.Context, tenantID string) (ontologyscope.ScopeBinding, ontologyscope.CatalogCandidate, bool, error) {
			binding, err := store.GetBinding(ctx, tenantID)
			if errors.Is(err, ontologyscope.ErrBindingNotFound) {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, nil
			}
			if err != nil {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, err
			}
			if binding.Kind != ontologyscope.KindCustom || binding.State != ontologyscope.BindingStateActive {
				return ontologyscope.ScopeBinding{}, ontologyscope.CatalogCandidate{}, false, nil
			}
			candidate := ontologyscope.CatalogCandidate{TenantID: binding.TenantID, Kind: binding.Kind, Seek: binding.TenantID, Label: binding.TenantID, ActivityAt: binding.UpdatedAt}
			return binding, candidate, true, nil
		},
	}
}

func productionCandidatePage(items []ontologyscope.CatalogCandidate, after string, hasMore bool) ontologyscope.CandidatePage {
	next := strings.TrimSpace(after)
	if len(items) > 0 {
		next = items[len(items)-1].Seek
	}
	scanned := len(items)
	if hasMore {
		scanned++
	}
	return ontologyscope.CandidatePage{Items: items, NextAfter: next, Scanned: scanned, HasMore: hasMore}
}

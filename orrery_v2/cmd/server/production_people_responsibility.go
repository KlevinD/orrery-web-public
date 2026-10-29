package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	agentidentity "orrery_v2/internal/agent/identity"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform"
)

const (
	peopleResponsibilityPulseVersion = "v1"
	peopleResponsibilityBatchVersion = "v1"
)

type peopleResponsibilityProfileDirectory interface {
	responsibility.AgentProfilePort
	QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

type peopleResponsibilityProfileMutator interface {
	MutateProfile(context.Context, agentcore.AgentProfileID, agentcore.ProfileMutation) (agentcore.AgentProfile, bool, error)
}

type productionPeopleResponsibilityRoleSync struct {
	profiles peopleResponsibilityProfileMutator
}

func configureProductionPeopleResponsibilityReconciler(
	ctx context.Context,
	rt *platform.Runtime,
	profiles responsibility.AgentProfilePort,
	repository *responsibility.Repository,
	scheduledTasks *schedulerapp.Repository,
	scheduler *schedulerapp.Service,
	target *productionSchedulerResponsibilityPort,
) error {
	if target == nil {
		return schedulerapp.ErrServiceDependency
	}
	reconciler, err := newProductionPeopleResponsibilityReconciler(ctx, rt, profiles, repository, scheduledTasks, scheduler)
	if err != nil {
		return err
	}
	target.people = reconciler
	return nil
}

func newProductionPeopleResponsibilityReconciler(
	ctx context.Context,
	rt *platform.Runtime,
	profiles responsibility.AgentProfilePort,
	repository *responsibility.Repository,
	scheduledTasks *schedulerapp.Repository,
	scheduler *schedulerapp.Service,
) (*responsibility.PeopleResponsibilityReconciler, error) {
	if rt == nil {
		return nil, schedulerapp.ErrServiceDependency
	}
	cfg := rt.Config.Integrations.People
	if !cfg.Enabled {
		return nil, nil
	}
	if profiles == nil || repository == nil || scheduledTasks == nil || scheduler == nil {
		return nil, schedulerapp.ErrServiceDependency
	}
	if _, err := repository.BackfillPeopleResponsibilityPortfolioVersions(ctx, rt.Clock.Now().UTC()); err != nil {
		return nil, fmt.Errorf("backfill People responsibility portfolio versions: %w", err)
	}
	token, err := (platformSecretResolver{provider: rt.Secrets}).ResolveSecret(ctx, cfg.TokenRef)
	if err != nil {
		return nil, fmt.Errorf("resolve People responsibility token: %w", err)
	}
	source, err := responsibility.NewHTTPPeopleResponsibilitySource(cfg.BaseURL, token, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	agentBindings, err := canonicalPeopleResponsibilityAgentBindings(ctx, profiles, cfg.PeopleTenantID, cfg.AgentBindings)
	if err != nil {
		return nil, fmt.Errorf("resolve People responsibility Agent bindings: %w", err)
	}
	var roleSync responsibility.PeopleResponsibilityRoleSyncPort
	if len(cfg.HumanRoleMappings) > 0 {
		profileMutator, ok := profiles.(peopleResponsibilityProfileMutator)
		if !ok {
			return nil, fmt.Errorf("configure People responsibility role sync: %w", schedulerapp.ErrServiceDependency)
		}
		roleSync = productionPeopleResponsibilityRoleSync{profiles: profileMutator}
	}
	return responsibility.NewPeopleResponsibilityReconciler(responsibility.PeopleResponsibilityReconcilerOptions{
		Repository: repository, Source: source, Profiles: profiles,
		RoleSync: roleSync, Clock: rt.Clock, PeopleTenantID: cfg.PeopleTenantID, OrreryTenantID: cfg.OrreryTenantID,
		AgentBindings: agentBindings, HumanRoleMappings: cfg.HumanRoleMappings, ReconcileInterval: cfg.ReconcileInterval,
	})
}

func (s productionPeopleResponsibilityRoleSync) SyncPeopleResponsibilityRoles(
	ctx context.Context,
	input responsibility.PeopleResponsibilityRoleSyncInput,
) (bool, error) {
	if s.profiles == nil || strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.PeopleTenantID) == "" ||
		strings.TrimSpace(input.SubjectID) == "" || strings.TrimSpace(input.AgentProfileID) == "" {
		return false, responsibility.ErrInvalidContract
	}
	managed, err := canonicalPeopleRoleIDs(input.ManagedRoleIDs)
	if err != nil || len(managed) == 0 {
		return false, responsibility.ErrInvalidContract
	}
	desired, err := canonicalPeopleRoleIDs(input.DesiredRoleIDs)
	if err != nil {
		return false, responsibility.ErrInvalidContract
	}
	managedSet := make(map[string]struct{}, len(managed))
	for _, roleID := range managed {
		managedSet[roleID] = struct{}{}
	}
	desiredSet := make(map[string]struct{}, len(desired))
	for _, roleID := range desired {
		if _, ok := managedSet[roleID]; !ok {
			return false, responsibility.ErrInvalidContract
		}
		desiredSet[roleID] = struct{}{}
	}

	profileID := agentcore.AgentProfileID(strings.TrimSpace(input.AgentProfileID))
	_, changed, err := s.profiles.MutateProfile(ctx, profileID, func(current agentcore.AgentProfile) (agentcore.AgentProfile, bool, error) {
		if current.AgentKind != agentcore.AgentKindHuman {
			if len(desired) > 0 {
				return current, false, fmt.Errorf("People responsibility human role sync rejected non-human profile %s", profileID)
			}
			return current, false, nil
		}
		if !samePeopleProfileSource(current.PersonSource, strings.TrimSpace(input.PeopleTenantID), strings.TrimSpace(input.SubjectID)) {
			return current, false, fmt.Errorf("People responsibility role sync profile source mismatch for %s", profileID)
		}
		roleRefs := make([]agentcore.RoleRef, 0, len(current.OrgIdentity.RoleRefs)+len(desired))
		seenManaged := make(map[string]struct{}, len(desired))
		for _, roleRef := range current.OrgIdentity.RoleRefs {
			roleID := strings.TrimSpace(string(roleRef.ID))
			if _, isManaged := managedSet[roleID]; isManaged {
				if _, isDesired := desiredSet[roleID]; !isDesired {
					continue
				}
				if _, duplicate := seenManaged[roleID]; duplicate {
					continue
				}
				seenManaged[roleID] = struct{}{}
				roleRefs = append(roleRefs, agentcore.RoleRef{ID: agentcore.RoleID(roleID)})
				continue
			}
			roleRefs = append(roleRefs, roleRef)
		}
		for _, roleID := range desired {
			if _, exists := seenManaged[roleID]; exists {
				continue
			}
			seenManaged[roleID] = struct{}{}
			roleRefs = append(roleRefs, agentcore.RoleRef{ID: agentcore.RoleID(roleID)})
		}
		if samePeopleRoleRefs(current.OrgIdentity.RoleRefs, roleRefs) {
			return current, false, nil
		}
		current.OrgIdentity.RoleRefs = roleRefs
		return current, true, nil
	})
	if errors.Is(err, agentcore.ErrNotFound) {
		return false, nil
	}
	return changed, err
}

func samePeopleRoleRefs(left, right []agentcore.RoleRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID {
			return false
		}
	}
	return true
}

func canonicalPeopleRoleIDs(roleIDs []string) ([]string, error) {
	unique := make(map[string]struct{}, len(roleIDs))
	for _, value := range roleIDs {
		roleID := strings.TrimSpace(value)
		if roleID == "" {
			return nil, responsibility.ErrInvalidContract
		}
		unique[roleID] = struct{}{}
	}
	out := make([]string, 0, len(unique))
	for roleID := range unique {
		out = append(out, roleID)
	}
	sort.Strings(out)
	return out, nil
}

var _ responsibility.PeopleResponsibilityRoleSyncPort = productionPeopleResponsibilityRoleSync{}

func canonicalPeopleResponsibilityAgentBindings(
	ctx context.Context,
	profiles responsibility.AgentProfilePort,
	peopleTenantID string,
	configured map[string]string,
) (map[string]string, error) {
	bindings := make(map[string]string, len(configured))
	for subjectID, profileID := range configured {
		bindings[strings.TrimSpace(subjectID)] = agentidentity.CanonicalProfileID(profileID)
	}
	directory, ok := profiles.(peopleResponsibilityProfileDirectory)
	if !ok {
		return bindings, nil
	}

	const pageLimit = 200
	canonicalBySubject := make(map[string]string)
	for offset := 0; ; offset += pageLimit {
		page, err := directory.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
			AgentKind:     agentcore.AgentKindHuman,
			IncludeHumans: true,
			Placement:     agentcore.TalentPlacementFilterAll,
			Page:          agentcore.Page{Limit: pageLimit, Offset: offset},
		})
		if err != nil {
			return nil, err
		}
		for _, entry := range page.Entries {
			profile, err := directory.GetProfile(ctx, entry.AgentProfileID)
			if err != nil {
				return nil, err
			}
			subjectID := strings.TrimSpace(profile.PersonSource.SourceRecordID)
			if !samePeopleProfileSource(profile.PersonSource, strings.TrimSpace(peopleTenantID), subjectID) {
				continue
			}
			employeeNumber := strings.ToUpper(strings.TrimSpace(profile.PersonSource.EmployeeNumber))
			if employeeNumber == "" {
				continue
			}
			if profile.ID.String() != employeeNumber {
				return nil, fmt.Errorf("People worker_number %q does not match Orrery profile id %q", employeeNumber, profile.ID)
			}
			if existing := canonicalBySubject[subjectID]; existing != "" && existing != employeeNumber {
				return nil, fmt.Errorf("People subject %q maps to multiple employee profiles", subjectID)
			}
			canonicalBySubject[subjectID] = employeeNumber
		}
		if len(page.Entries) < pageLimit || offset+len(page.Entries) >= page.Total {
			break
		}
	}
	for subjectID := range bindings {
		if profileID := canonicalBySubject[subjectID]; profileID != "" {
			bindings[subjectID] = profileID
		}
	}
	return bindings, nil
}

func managedTaskViewer(task schedulerapp.ScheduledTask) schedulerapp.ViewerRef {
	return schedulerapp.ViewerRef{
		TenantID: strings.TrimSpace(task.TenantID), ViewerID: strings.TrimSpace(task.CreatedBy),
		ViewerKind: schedulerapp.ViewerKindSystem, ProfileID: strings.TrimSpace(task.CreatorProfileID),
		DisplayName: "People responsibility schedule manager",
	}
}

func productionResponsibilityBatchTaskIdentity(task schedulerapp.ScheduledTask) bool {
	tenantID := strings.TrimSpace(task.TenantID)
	peopleTenantID := strings.TrimSpace(task.Target.SourceScopeID)
	return tenantID != "" && peopleTenantID != "" &&
		task.Target.Mode == schedulerapp.TargetModeResponsibilityEvaluation &&
		task.Target.SourceScopeKind == schedulerapp.ResponsibilityEvaluationPeoplePortfoliosScope &&
		task.CreatedByKind == schedulerapp.ViewerKindSystem &&
		strings.TrimSpace(task.CreatedBy) == "system:people-responsibility-batch:"+peopleTenantID &&
		strings.TrimSpace(task.CreatorProfileID) == "system:people-responsibility-batch" &&
		strings.TrimSpace(task.IdempotencyKey) == "people-responsibility-evaluation-batch:"+tenantID+":"+peopleTenantID+":"+peopleResponsibilityBatchVersion
}

func legacyResponsibilityPulseIdentity(task schedulerapp.ScheduledTask) bool {
	bindingID := strings.TrimSpace(task.Target.SourceScopeID)
	agentID := strings.TrimSpace(task.Target.AgentID)
	return strings.TrimSpace(task.TenantID) != "" &&
		task.Target.Mode == schedulerapp.TargetModeResponsibilityEvaluation &&
		task.Target.SourceScopeKind == schedulerapp.ResponsibilityEvaluationPeoplePortfolioScope &&
		bindingID != "" && agentID != "" && task.CreatedByKind == schedulerapp.ViewerKindSystem &&
		strings.TrimSpace(task.CreatedBy) == "system:people-responsibility:"+bindingID+":"+agentID &&
		strings.TrimSpace(task.CreatorProfileID) == agentID &&
		strings.TrimSpace(task.IdempotencyKey) == "people-responsibility-pulse:"+bindingID+":"+agentID+":"+peopleResponsibilityPulseVersion
}

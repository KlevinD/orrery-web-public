package responsibility

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/clock"
)

type PeopleResponsibilityReconcilerOptions struct {
	Repository        *Repository
	Source            PeopleResponsibilitySource
	Profiles          AgentProfilePort
	RoleSync          PeopleResponsibilityRoleSyncPort
	Clock             clock.Clock
	PeopleTenantID    string
	OrreryTenantID    string
	AgentBindings     map[string]string
	HumanRoleMappings map[string]string
	ReconcileInterval time.Duration
	MaxBindings       int
}

type PeopleResponsibilityReconciler struct {
	repository        *Repository
	source            PeopleResponsibilitySource
	profiles          AgentProfilePort
	roleSync          PeopleResponsibilityRoleSyncPort
	clock             clock.Clock
	peopleTenantID    string
	orreryTenantID    string
	agentBindings     map[string]string
	humanRoleMappings map[string]string
	managedRoleIDs    []string
	reconcileInterval time.Duration
	maxBindings       int
}

func NewPeopleResponsibilityReconciler(opts PeopleResponsibilityReconcilerOptions) (*PeopleResponsibilityReconciler, error) {
	peopleTenantID, orreryTenantID := strings.TrimSpace(opts.PeopleTenantID), strings.TrimSpace(opts.OrreryTenantID)
	if opts.Repository == nil || opts.Source == nil || opts.Profiles == nil || peopleTenantID == "" || orreryTenantID == "" || opts.ReconcileInterval <= 0 || len(opts.AgentBindings) == 0 {
		return nil, ErrInvalidContract
	}
	bindings := make(map[string]string, len(opts.AgentBindings))
	for subjectID, profileID := range opts.AgentBindings {
		subjectID, profileID = strings.TrimSpace(subjectID), strings.TrimSpace(profileID)
		if subjectID == "" || profileID == "" {
			return nil, ErrInvalidContract
		}
		bindings[subjectID] = profileID
	}
	maxBindings := opts.MaxBindings
	if maxBindings <= 0 {
		maxBindings = 1000
	}
	if len(bindings) > maxBindings {
		return nil, ErrInvalidContract
	}
	humanRoleMappings := make(map[string]string, len(opts.HumanRoleMappings))
	managedRoleSet := make(map[string]struct{}, len(opts.HumanRoleMappings))
	for responsibilityCode, roleID := range opts.HumanRoleMappings {
		responsibilityCode, roleID = strings.TrimSpace(responsibilityCode), strings.TrimSpace(roleID)
		if responsibilityCode == "" || roleID == "" {
			return nil, ErrInvalidContract
		}
		humanRoleMappings[responsibilityCode] = roleID
		managedRoleSet[roleID] = struct{}{}
	}
	if len(humanRoleMappings) > 0 && opts.RoleSync == nil {
		return nil, ErrInvalidContract
	}
	managedRoleIDs := make([]string, 0, len(managedRoleSet))
	for roleID := range managedRoleSet {
		managedRoleIDs = append(managedRoleIDs, roleID)
	}
	sort.Strings(managedRoleIDs)
	platformClock := opts.Clock
	if platformClock == nil {
		platformClock = clock.Real{}
	}
	return &PeopleResponsibilityReconciler{
		repository: opts.Repository, source: opts.Source, profiles: opts.Profiles, roleSync: opts.RoleSync, clock: platformClock,
		peopleTenantID: peopleTenantID, orreryTenantID: orreryTenantID, agentBindings: bindings,
		humanRoleMappings: humanRoleMappings, managedRoleIDs: managedRoleIDs,
		reconcileInterval: opts.ReconcileInterval, maxBindings: maxBindings,
	}, nil
}

func (r *PeopleResponsibilityReconciler) Reconcile(ctx context.Context) (stats PeopleResponsibilityReconcileStats, runErr error) {
	if r == nil || r.repository == nil {
		return stats, ErrInvalidContract
	}
	now := r.clock.Now().UTC()
	claimed, err := r.repository.ClaimPeopleResponsibilityReconcile(ctx, r.orreryTenantID, r.peopleTenantID, now, r.reconcileInterval)
	if err != nil {
		return stats, err
	}
	if !claimed {
		stats.SourceThrottled = true
		return stats, nil
	}
	stats.SourceAttempted = true
	defer func() {
		completeErr := r.repository.CompletePeopleResponsibilityReconcile(ctx, r.orreryTenantID, r.peopleTenantID, r.clock.Now().UTC(), runErr)
		if runErr == nil && completeErr != nil {
			runErr = completeErr
		}
	}()

	existing, err := r.repository.ListPeopleAgentBindings(ctx)
	if err != nil {
		return stats, err
	}
	desired := make(map[string]struct{}, len(r.agentBindings))
	subjects := make([]string, 0, len(r.agentBindings))
	for subjectID := range r.agentBindings {
		subjects = append(subjects, subjectID)
	}
	sort.Strings(subjects)
	var failures []error
	for _, subjectID := range subjects {
		profileID := r.agentBindings[subjectID]
		if profile, profileErr := r.profiles.GetProfile(ctx, agentcore.AgentProfileID(profileID)); profileErr == nil && profile.IsPersonalAssistant() {
			continue
		}
		binding := PeopleAgentBindingRecord{
			ID: PeopleAgentBindingID(r.orreryTenantID, r.peopleTenantID, subjectID), TenantID: r.orreryTenantID,
			PeopleTenantID: r.peopleTenantID, SubjectID: subjectID, AgentProfileID: profileID, Source: "people", Status: peopleBindingStatusActive,
		}
		desired[binding.ID] = struct{}{}
		stored, changed, err := r.repository.UpsertPeopleAgentBinding(ctx, binding, now)
		if err != nil {
			stats.Failed++
			failures = append(failures, err)
			continue
		}
		stats.BindingsScanned++
		if changed {
			stats.BindingsChanged++
		}
		if err := r.reconcileBinding(ctx, stored, now, &stats); err != nil {
			stats.Failed++
			failures = append(failures, err)
		}
	}
	for _, binding := range existing {
		if binding.TenantID != r.orreryTenantID || binding.PeopleTenantID != r.peopleTenantID {
			continue
		}
		if binding.Status == peopleBindingStatusDetached {
			continue
		}
		if _, ok := desired[binding.ID]; ok {
			continue
		}
		if err := r.detachBinding(ctx, binding, now, &stats); err != nil {
			stats.Failed++
			failures = append(failures, err)
		}
	}
	return stats, errors.Join(failures...)
}

func (r *PeopleResponsibilityReconciler) reconcileBinding(ctx context.Context, binding PeopleAgentBindingRecord, now time.Time, stats *PeopleResponsibilityReconcileStats) error {
	resolution, err := r.source.ResolveResponsibilities(ctx, binding.SubjectID)
	if err != nil {
		return r.pauseForGap(ctx, binding, "people_source_unavailable", err.Error(), now, stats)
	}
	if strings.TrimSpace(resolution.SubjectID) != binding.SubjectID {
		return r.pauseForGap(ctx, binding, "people_source_unavailable", "People responsibility subject mismatch", now, stats)
	}
	resolution.Items = canonicalPeopleResponsibilities(resolution.Items)
	if len(resolution.Items) == 0 {
		if err := r.syncHumanRoles(ctx, binding, resolution.SourceRevision, nil, stats); err != nil {
			return r.pauseForGap(ctx, binding, "responsibility_role_sync_failed", err.Error(), now, stats)
		}
		return r.pauseForGap(ctx, binding, "responsibility_unassigned", "People returned no active responsibilities", now, stats)
	}
	profile, err := r.profiles.GetProfile(ctx, agentcore.AgentProfileID(binding.AgentProfileID))
	if err != nil {
		return r.pauseForGap(ctx, binding, "agent_profile_not_runnable", err.Error(), now, stats)
	}
	if err := r.syncHumanRoles(ctx, binding, resolution.SourceRevision, resolution.Items, stats); err != nil {
		return r.pauseForGap(ctx, binding, "responsibility_role_sync_failed", err.Error(), now, stats)
	}
	if !profile.Enabled {
		return r.pauseForGap(ctx, binding, "agent_profile_not_runnable", "Orrery Agent profile is disabled", now, stats)
	}
	digest, err := peopleResponsibilitiesDigest(resolution)
	if err != nil {
		return r.pauseForGap(ctx, binding, "people_source_invalid", err.Error(), now, stats)
	}
	portfolioID := PeopleResponsibilityPortfolioID(binding.ID)
	current, found, err := r.repository.GetPeopleResponsibilityPortfolio(ctx, portfolioID)
	if err != nil {
		return err
	}
	pulseID := ""
	createdAt := time.Time{}
	if found {
		pulseID, createdAt = current.PulseScheduledTaskID, current.CreatedAt
	}
	portfolio := PeopleResponsibilityPortfolio{
		ID: portfolioID, TenantID: binding.TenantID, BindingID: binding.ID, PeopleTenantID: binding.PeopleTenantID,
		SubjectID: binding.SubjectID, AgentProfileID: binding.AgentProfileID, SourceRevision: resolution.SourceRevision,
		SourceDigest: digest, Responsibilities: resolution.Items, Status: peoplePortfolioStatusActive,
		PulseScheduledTaskID: pulseID, SourceAsOf: resolution.AsOf, CreatedAt: createdAt,
	}
	_, changed, err := r.repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, now)
	if err != nil {
		return err
	}
	if changed {
		stats.PortfoliosChanged++
	}
	if resolved, err := r.repository.ResolvePeopleResponsibilityGap(ctx, PeopleResponsibilityGapID(binding.ID), now); err != nil {
		return err
	} else if resolved {
		stats.GapsResolved++
	}
	return nil
}

func (r *PeopleResponsibilityReconciler) pauseForGap(ctx context.Context, binding PeopleAgentBindingRecord, code, message string, now time.Time, stats *PeopleResponsibilityReconcileStats) error {
	portfolioID := PeopleResponsibilityPortfolioID(binding.ID)
	portfolio, found, err := r.repository.GetPeopleResponsibilityPortfolio(ctx, portfolioID)
	if err != nil {
		return err
	}
	if found {
		portfolio.Status = peoplePortfolioStatusPaused
		if _, changed, err := r.repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, now); err != nil {
			return err
		} else if changed {
			stats.PortfoliosChanged++
		}
	}
	gap := PeopleResponsibilityGap{
		ID: PeopleResponsibilityGapID(binding.ID), TenantID: binding.TenantID, BindingID: binding.ID,
		SubjectID: binding.SubjectID, AgentProfileID: binding.AgentProfileID, Code: code, Message: strings.TrimSpace(message),
	}
	if changed, err := r.repository.OpenPeopleResponsibilityGap(ctx, gap, now); err != nil {
		return err
	} else if changed {
		stats.GapsOpen++
	}
	return fmt.Errorf("%s: %s", code, strings.TrimSpace(message))
}

func (r *PeopleResponsibilityReconciler) detachBinding(ctx context.Context, binding PeopleAgentBindingRecord, now time.Time, stats *PeopleResponsibilityReconcileStats) error {
	if err := r.syncHumanRoles(ctx, binding, "", nil, stats); err != nil {
		return err
	}
	portfolio, found, err := r.repository.GetPeopleResponsibilityPortfolio(ctx, PeopleResponsibilityPortfolioID(binding.ID))
	if err != nil {
		return err
	}
	if found {
		portfolio.Status = peoplePortfolioStatusPaused
		if _, changed, err := r.repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, now); err != nil {
			return err
		} else if changed {
			stats.PortfoliosChanged++
		}
	}
	if changed, err := r.repository.DetachPeopleAgentBinding(ctx, binding.ID, now); err != nil {
		return err
	} else if changed {
		stats.BindingsChanged++
	}
	return nil
}

func (r *PeopleResponsibilityReconciler) syncHumanRoles(
	ctx context.Context,
	binding PeopleAgentBindingRecord,
	sourceRevision string,
	items []PeopleResolvedResponsibility,
	stats *PeopleResponsibilityReconcileStats,
) error {
	if len(r.humanRoleMappings) == 0 {
		return nil
	}
	desiredSet := make(map[string]struct{}, len(items))
	for _, item := range items {
		if roleID := r.humanRoleMappings[strings.TrimSpace(item.Code)]; roleID != "" {
			desiredSet[roleID] = struct{}{}
		}
	}
	desiredRoleIDs := make([]string, 0, len(desiredSet))
	for roleID := range desiredSet {
		desiredRoleIDs = append(desiredRoleIDs, roleID)
	}
	sort.Strings(desiredRoleIDs)
	changed, err := r.roleSync.SyncPeopleResponsibilityRoles(ctx, PeopleResponsibilityRoleSyncInput{
		TenantID:       binding.TenantID,
		PeopleTenantID: binding.PeopleTenantID,
		SubjectID:      binding.SubjectID,
		AgentProfileID: binding.AgentProfileID,
		SourceRevision: strings.TrimSpace(sourceRevision),
		ManagedRoleIDs: append([]string(nil), r.managedRoleIDs...),
		DesiredRoleIDs: desiredRoleIDs,
	})
	if err != nil {
		return err
	}
	if changed {
		stats.RoleProfilesChanged++
	}
	return nil
}

func peopleResponsibilitiesDigest(resolution PeopleResponsibilityResolution) (string, error) {
	payload, err := json.Marshal(struct {
		SubjectID      string                         `json:"subject_id"`
		SourceRevision string                         `json:"source_revision"`
		Items          []PeopleResolvedResponsibility `json:"items"`
	}{
		SubjectID: strings.TrimSpace(resolution.SubjectID), SourceRevision: strings.TrimSpace(resolution.SourceRevision),
		Items: canonicalPeopleResponsibilities(resolution.Items),
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalPeopleResponsibilities(items []PeopleResolvedResponsibility) []PeopleResolvedResponsibility {
	out := append([]PeopleResolvedResponsibility(nil), items...)
	for index := range out {
		out[index].Sources = append([]PeopleResponsibilityBinding(nil), out[index].Sources...)
		sort.Slice(out[index].Sources, func(i, j int) bool {
			left, right := out[index].Sources[i], out[index].Sources[j]
			if strings.TrimSpace(left.TargetType) != strings.TrimSpace(right.TargetType) {
				return strings.TrimSpace(left.TargetType) < strings.TrimSpace(right.TargetType)
			}
			if strings.TrimSpace(left.TargetID) != strings.TrimSpace(right.TargetID) {
				return strings.TrimSpace(left.TargetID) < strings.TrimSpace(right.TargetID)
			}
			return strings.TrimSpace(left.BindingID) < strings.TrimSpace(right.BindingID)
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if strings.TrimSpace(out[i].Code) != strings.TrimSpace(out[j].Code) {
			return strings.TrimSpace(out[i].Code) < strings.TrimSpace(out[j].Code)
		}
		return strings.TrimSpace(out[i].ID) < strings.TrimSpace(out[j].ID)
	})
	return out
}

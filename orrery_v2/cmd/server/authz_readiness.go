package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/rebac"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/health"
)

func authorizationServiceFromRuntime(rt *platform.Runtime) (*authorizationapp.Service, *authorizationapp.LoggingShadowRecorder, error) {
	if rt == nil {
		return nil, nil, fmt.Errorf("authorization service requires platform runtime")
	}
	mode, _ := rt.Config.String("authz.mode")
	mode = authorizationModeFromConfig(mode)
	if mode == "" {
		mode = authorizationModeLocal
	}
	if mode == authorizationModeLocal || mode == authorizationModeOpenFGAShadow {
		if rt.Databases == nil {
			return nil, nil, fmt.Errorf("local authorization requires database registry")
		}
		core, err := rt.Databases.Store(db.RoleCore)
		if err != nil {
			return nil, nil, err
		}
		local := authorizationapp.NewReBACAdapter(rebac.NewPlatformStore(core), authorizationapp.NewScopeAdapter(nil))
		if mode == authorizationModeLocal {
			return authorizationapp.NewService(local), nil, nil
		}
		service, recorder, err := authorizationServiceWithPrimaryFromPlatform(rt.Config, local)
		if err != nil {
			return nil, nil, err
		}
		if err := registerAuthorizationShadowReadiness(rt, recorder); err != nil {
			return nil, nil, err
		}
		return service, recorder, nil
	}
	service, recorder, err := authorizationServiceWithShadowStatsFromPlatform(rt.Config)
	if err != nil {
		return nil, nil, err
	}
	if err := registerAuthorizationShadowReadiness(rt, recorder); err != nil {
		return nil, nil, err
	}
	return service, recorder, nil
}

func registerAuthorizationShadowReadiness(rt *platform.Runtime, recorder *authorizationapp.LoggingShadowRecorder) error {
	if recorder == nil {
		return nil
	}
	if rt == nil || rt.Health == nil {
		return fmt.Errorf("authorization shadow readiness requires runtime health checker")
	}
	rt.Health.WithReadyCheck(func(ctx context.Context) health.Check {
		_ = ctx
		stats := recorder.Snapshot()
		return health.Check{
			Name: "authorization.openfga_shadow",
			OK:   stats.Mismatches == 0 && stats.PrimaryErrors == 0 && stats.ShadowErrors == 0,
			Message: fmt.Sprintf(
				"total=%d mismatches=%d primary_allow_shadow_deny=%d primary_deny_shadow_allow=%d primary_errors=%d shadow_errors=%d object_types=%s actions=%s",
				stats.Total,
				stats.Mismatches,
				stats.PrimaryAllowShadowDeny,
				stats.PrimaryDenyShadowAllow,
				stats.PrimaryErrors,
				stats.ShadowErrors,
				authorizationShadowBucketSummary(stats.ByObjectType),
				authorizationShadowBucketSummary(stats.ByAction),
			),
		}
	})
	return nil
}

func registerAuthorizationRoleScopeTransitionReadiness(
	rt *platform.Runtime,
	resolver *operations.RolePermissionScopeResolver,
	profiles operations.RoleScopeTransitionProfileReader,
) error {
	if rt == nil || rt.Health == nil {
		return fmt.Errorf("authorization role scope transition readiness requires runtime health checker")
	}
	if resolver == nil || profiles == nil {
		return fmt.Errorf("authorization role scope transition readiness requires resolver and profile reader")
	}
	rt.Health.WithReadyCheck(func(ctx context.Context) health.Check {
		diagnostics, err := resolver.DiagnoseTransition(ctx, profiles)
		if err != nil {
			return health.Check{
				Name:    "authorization.role_scope_transition",
				OK:      false,
				Message: fmt.Sprintf("mode=%s diagnostics_error=true", resolver.Mode()),
			}
		}
		return health.Check{
			Name: "authorization.role_scope_transition",
			OK:   diagnostics.Ready,
			Message: fmt.Sprintf(
				"mode=%s transition_ready=%t principals=%d break_glass_principals=%d legacy_dependent_principals=%d account_scopes=%d effective_role_scopes=%d dropped_scopes=%d break_glass_retained_scopes=%d object_scope_conflicts=%d",
				diagnostics.Mode,
				diagnostics.TransitionReady,
				diagnostics.Principals,
				diagnostics.BreakGlassPrincipals,
				diagnostics.LegacyDependentPrincipals,
				diagnostics.AccountScopes,
				diagnostics.EffectiveRoleScopes,
				diagnostics.DroppedScopes,
				diagnostics.BreakGlassRetainedScopes,
				diagnostics.ObjectScopeConflicts,
			),
		}
	})
	return nil
}

func registerAuthorizationRelationBackfillReadiness(rt *platform.Runtime, result authorizationapp.RelationBackfillResult) error {
	if rt == nil || rt.Health == nil {
		return fmt.Errorf("authorization relation backfill readiness requires runtime health checker")
	}
	rt.Health.WithReadyCheck(func(ctx context.Context) health.Check {
		_ = ctx
		ok := !result.Disabled && result.Diff.Errors == 0 && result.Diff.Missing == 0 && result.Diff.Extra == 0
		return health.Check{
			Name: "authorization.relation_backfill",
			OK:   ok,
			Message: fmt.Sprintf(
				"mode=%s disabled=%t dry_run=%t projects=%d development_projects=%d scm_connections=%d todos=%d missions=%d discussions=%d organizations=%d departments=%d workforce_members=%d role_assignments=%d resource_bindings=%d tuples=%d pruned=%d diff_checked=%d diff_existing=%d diff_missing=%d diff_extra=%d diff_errors=%d",
				result.Mode,
				result.Disabled,
				result.DryRun,
				result.Projects,
				result.DevelopmentProjects,
				result.SCMConnections,
				result.Todos,
				result.Missions,
				result.Discussions,
				result.Organizations,
				result.Departments,
				result.WorkforceMembers,
				result.RoleAssignments,
				result.ResourceBindings,
				result.Tuples,
				result.Pruned,
				result.Diff.Checked,
				result.Diff.Existing,
				result.Diff.Missing,
				result.Diff.Extra,
				result.Diff.Errors,
			),
		}
	})
	return nil
}

func registerAuthorizationEnforceTransitionReadiness(rt *platform.Runtime, recorder *authorizationapp.LoggingShadowRecorder, result authorizationapp.RelationBackfillResult) error {
	if recorder == nil {
		return nil
	}
	if rt == nil || rt.Health == nil {
		return fmt.Errorf("authorization enforce transition readiness requires runtime health checker")
	}
	rt.Health.WithReadyCheck(func(ctx context.Context) health.Check {
		_ = ctx
		diagnostics := authorizationapp.NewEnforceTransitionDiagnostics(recorder.Snapshot(), result)
		stats := diagnostics.Shadow
		return health.Check{
			Name: "authorization.enforce_transition",
			OK:   diagnostics.Ready,
			Message: fmt.Sprintf(
				"shadow_total=%d shadow_mismatches=%d primary_allow_shadow_deny=%d primary_deny_shadow_allow=%d primary_errors=%d shadow_errors=%d shadow_object_types=%s shadow_actions=%s backfill_mode=%s backfill_disabled=%t backfill_dry_run=%t backfill_validated=%t diff_checked=%d diff_existing=%d diff_missing=%d diff_extra=%d diff_errors=%d",
				stats.Total,
				stats.Mismatches,
				stats.PrimaryAllowShadowDeny,
				stats.PrimaryDenyShadowAllow,
				stats.PrimaryErrors,
				stats.ShadowErrors,
				authorizationShadowBucketSummary(stats.ByObjectType),
				authorizationShadowBucketSummary(stats.ByAction),
				result.Mode,
				result.Disabled,
				result.DryRun,
				diagnostics.BackfillValidated,
				result.Diff.Checked,
				result.Diff.Existing,
				result.Diff.Missing,
				result.Diff.Extra,
				result.Diff.Errors,
			),
		}
	})
	return nil
}

func authorizationShadowBucketSummary(buckets map[string]authorizationapp.ShadowDecisionCounter) string {
	if len(buckets) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		parts = append(parts, fmt.Sprintf("%s(total=%d,mismatches=%d,primary_errors=%d,shadow_errors=%d)", key, bucket.Total, bucket.Mismatches, bucket.PrimaryErrors, bucket.ShadowErrors))
	}
	return strings.Join(parts, ",")
}

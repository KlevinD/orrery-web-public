package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/health"
)

func TestAuthorizationRuntimeShadowKeepsReBACAsPrimary(t *testing.T) {
	ctx := context.Background()
	openFGACalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		openFGACalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer server.Close()
	cfg := loadAuthorizationTestConfig(t, map[string]string{
		"authz.mode":                            authorizationModeOpenFGAShadow,
		"authz.openfga.api_url":                 server.URL,
		"authz.openfga.store_id":                "store-1",
		"authz.openfga.authorization_model_id":  "model-1",
		"authz.openfga.ignore_duplicate_writes": "true",
	})
	rt, err := platform.OpenWithModules(ctx, cfg, OntologyModule())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	service, recorder, err := authorizationServiceFromRuntime(rt)
	if err != nil {
		t.Fatalf("authorizationServiceFromRuntime: %v", err)
	}
	decision, err := service.Check(ctx, authorizationapp.CheckRequest{
		Principal:               authorizationTestPrincipal("enterprise_graph.read"),
		Action:                  "mission.view",
		Object:                  identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "global"},
		TenantID:                "global",
		CoarseScopeAlternatives: []string{"enterprise_graph.read"},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if decision.Allowed || decision.Source != authorizationapp.DecisionSourceReBAC {
		t.Fatalf("shadow primary decision = %+v", decision)
	}
	if !openFGACalled || recorder == nil || recorder.Snapshot().Total != 1 {
		t.Fatalf("shadow observation called=%t recorder=%+v", openFGACalled, recorder)
	}
}

func TestRegisterAuthorizationShadowReadinessReportsStats(t *testing.T) {
	recorder := authorizationapp.NewLoggingShadowRecorder(authzReadinessTestLogger{})
	recorder.RecordAuthorizationShadowDecision(context.Background(), authorizationapp.ShadowDecisionRecord{
		Request:  authorizationapp.CheckRequest{Principal: identity.Principal{UserID: "planner", ViewerKind: identity.ViewerKindUser}, Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: "mission-1"}},
		Primary:  authorizationapp.Decision{Allowed: true},
		Shadow:   authorizationapp.Decision{Allowed: false},
		Mismatch: true,
	})
	rt := &platform.Runtime{Health: health.NewChecker(nil, nil, time.Second)}

	if err := registerAuthorizationShadowReadiness(rt, recorder); err != nil {
		t.Fatalf("registerAuthorizationShadowReadiness: %v", err)
	}

	report := rt.Health.Ready(context.Background())
	for _, check := range report.Checks {
		if check.Name == "authorization.openfga_shadow" {
			if check.OK || !strings.Contains(check.Message, "mismatches=1") || !strings.Contains(check.Message, "primary_allow_shadow_deny=1") || !strings.Contains(check.Message, "object_types=mission(total=1,mismatches=1") || !strings.Contains(check.Message, "actions=mission.view(total=1,mismatches=1") {
				t.Fatalf("authorization readiness check = %+v", check)
			}
			return
		}
	}
	t.Fatalf("authorization readiness check missing: %+v", report.Checks)
}

func TestRegisterAuthorizationRelationBackfillReadinessReportsDiffStatus(t *testing.T) {
	rt := &platform.Runtime{Health: health.NewChecker(nil, nil, time.Second)}
	result := authorizationapp.RelationBackfillResult{
		Mode:                authorizationRelationBackfillModeDryRun,
		DryRun:              true,
		Projects:            1,
		DevelopmentProjects: 2,
		SCMConnections:      3,
		Todos:               4,
		Discussions:         1,
		Tuples:              2,
		Diff: authorizationapp.RelationBackfillDiff{
			Checked: 2,
			Missing: 1,
		},
	}

	if err := registerAuthorizationRelationBackfillReadiness(rt, result); err != nil {
		t.Fatalf("registerAuthorizationRelationBackfillReadiness: %v", err)
	}

	report := rt.Health.Ready(context.Background())
	for _, check := range report.Checks {
		if check.Name == "authorization.relation_backfill" {
			if check.OK || !strings.Contains(check.Message, "mode=dry_run") || !strings.Contains(check.Message, "development_projects=2") || !strings.Contains(check.Message, "scm_connections=3") || !strings.Contains(check.Message, "todos=4") || !strings.Contains(check.Message, "discussions=1") || !strings.Contains(check.Message, "diff_missing=1") {
				t.Fatalf("relation backfill readiness check = %+v", check)
			}
			return
		}
	}
	t.Fatalf("relation backfill readiness check missing: %+v", report.Checks)
}

func TestRegisterAuthorizationRoleScopeTransitionReadinessHonorsMigrationMode(t *testing.T) {
	profiles := authzRoleScopeProfileReader{profile: agentcore.AgentProfile{
		ID:        "human-operator",
		AgentKind: agentcore.AgentKindHuman,
		Enabled:   true,
		UserAccount: agentcore.UserAccountSpec{
			Username:        "operator",
			AccountEnabled:  true,
			PermissionScope: []string{"operations.read"},
		},
	}}
	for _, tc := range []struct {
		name string
		mode operations.RolePermissionScopeMode
		ok   bool
	}{
		{name: "hybrid reports gap without failing readiness", mode: operations.RolePermissionScopeModeHybrid, ok: true},
		{name: "authoritative fails readiness while gap remains", mode: operations.RolePermissionScopeModeAuthoritative, ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &platform.Runtime{Health: health.NewChecker(nil, nil, time.Millisecond)}
			resolver := operations.NewRolePermissionScopeResolver(authzRoleScopeStore{}).WithMode(tc.mode)
			if err := registerAuthorizationRoleScopeTransitionReadiness(rt, resolver, profiles); err != nil {
				t.Fatalf("registerAuthorizationRoleScopeTransitionReadiness: %v", err)
			}

			report := rt.Health.Ready(context.Background())
			for _, check := range report.Checks {
				if check.Name != "authorization.role_scope_transition" {
					continue
				}
				if check.OK != tc.ok || !strings.Contains(check.Message, "transition_ready=false") || !strings.Contains(check.Message, "legacy_dependent_principals=1") || !strings.Contains(check.Message, "dropped_scopes=1") {
					t.Fatalf("role scope readiness check = %+v", check)
				}
				return
			}
			t.Fatalf("role scope readiness check missing: %+v", report.Checks)
		})
	}
}

func TestRegisterAuthorizationEnforceTransitionReadinessCombinesShadowAndBackfill(t *testing.T) {
	recorder := authorizationapp.NewLoggingShadowRecorder(authzReadinessTestLogger{})
	recorder.RecordAuthorizationShadowDecision(context.Background(), authorizationapp.ShadowDecisionRecord{
		Request:  authorizationapp.CheckRequest{Principal: identity.Principal{UserID: "planner", ViewerKind: identity.ViewerKindUser}, Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: "mission-1"}},
		Primary:  authorizationapp.Decision{Allowed: true},
		Shadow:   authorizationapp.Decision{Allowed: false},
		Mismatch: true,
	})
	rt := &platform.Runtime{Health: health.NewChecker(nil, nil, time.Second)}
	result := authorizationapp.RelationBackfillResult{
		Mode:   authorizationRelationBackfillModeDryRun,
		DryRun: true,
		Tuples: 2,
		Diff: authorizationapp.RelationBackfillDiff{
			Checked: 2,
			Missing: 1,
		},
	}

	if err := registerAuthorizationEnforceTransitionReadiness(rt, recorder, result); err != nil {
		t.Fatalf("registerAuthorizationEnforceTransitionReadiness: %v", err)
	}

	report := rt.Health.Ready(context.Background())
	for _, check := range report.Checks {
		if check.Name == "authorization.enforce_transition" {
			if check.OK || !strings.Contains(check.Message, "shadow_mismatches=1") || !strings.Contains(check.Message, "shadow_object_types=mission(total=1,mismatches=1") || !strings.Contains(check.Message, "diff_missing=1") || !strings.Contains(check.Message, "backfill_validated=false") {
				t.Fatalf("enforce transition readiness check = %+v", check)
			}
			return
		}
	}
	t.Fatalf("enforce transition readiness check missing: %+v", report.Checks)
}

func TestRegisterAuthorizationEnforceTransitionReadinessAcceptsCleanWriteBackfillDiffEvidence(t *testing.T) {
	recorder := authorizationapp.NewLoggingShadowRecorder(authzReadinessTestLogger{})
	rt := &platform.Runtime{Health: health.NewChecker(nil, nil, time.Second)}
	result := authorizationapp.RelationBackfillResult{
		Mode:   authorizationRelationBackfillModeWrite,
		Tuples: 2,
		Diff: authorizationapp.RelationBackfillDiff{
			Checked: 2,
		},
	}

	if err := registerAuthorizationEnforceTransitionReadiness(rt, recorder, result); err != nil {
		t.Fatalf("registerAuthorizationEnforceTransitionReadiness: %v", err)
	}

	report := rt.Health.Ready(context.Background())
	for _, check := range report.Checks {
		if check.Name == "authorization.enforce_transition" {
			if !check.OK || !strings.Contains(check.Message, "backfill_validated=true") {
				t.Fatalf("enforce transition readiness check = %+v", check)
			}
			return
		}
	}
	t.Fatalf("enforce transition readiness check missing: %+v", report.Checks)
}

func TestRegisterAuthorizationEnforceTransitionReadinessReportsCleanTransition(t *testing.T) {
	recorder := authorizationapp.NewLoggingShadowRecorder(authzReadinessTestLogger{})
	rt := &platform.Runtime{Health: health.NewChecker(nil, nil, time.Second)}
	result := authorizationapp.RelationBackfillResult{
		Mode:   authorizationRelationBackfillModeDryRun,
		DryRun: true,
		Tuples: 2,
		Diff: authorizationapp.RelationBackfillDiff{
			Checked: 2,
		},
	}

	if err := registerAuthorizationEnforceTransitionReadiness(rt, recorder, result); err != nil {
		t.Fatalf("registerAuthorizationEnforceTransitionReadiness: %v", err)
	}

	report := rt.Health.Ready(context.Background())
	for _, check := range report.Checks {
		if check.Name == "authorization.enforce_transition" {
			if !check.OK || !strings.Contains(check.Message, "shadow_mismatches=0") || !strings.Contains(check.Message, "backfill_validated=true") {
				t.Fatalf("enforce transition readiness check = %+v", check)
			}
			return
		}
	}
	t.Fatalf("enforce transition readiness check missing: %+v", report.Checks)
}

type authzReadinessTestLogger struct{}

func (authzReadinessTestLogger) Printf(string, ...any) {}

type authzRoleScopeStore struct{}

func (authzRoleScopeStore) ListRoleDefinitions(context.Context) ([]operations.RoleDefinitionDTO, error) {
	return nil, nil
}

func (authzRoleScopeStore) SaveRoleDefinition(context.Context, operations.RoleDefinitionDTO) error {
	return nil
}

func (authzRoleScopeStore) ListRoleAssignments(context.Context, operations.RoleAssignmentQuery) ([]operations.RoleAssignmentDTO, error) {
	return nil, nil
}

func (authzRoleScopeStore) SaveRoleAssignment(context.Context, operations.RoleAssignmentDTO) error {
	return nil
}

func (authzRoleScopeStore) DeleteRoleAssignment(context.Context, string) error {
	return nil
}

type authzRoleScopeProfileReader struct {
	profile agentcore.AgentProfile
}

func (r authzRoleScopeProfileReader) QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: r.profile.ID,
		AgentKind:      r.profile.AgentKind,
		Enabled:        r.profile.Enabled,
	}}}, nil
}

func (r authzRoleScopeProfileReader) GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return r.profile, nil
}

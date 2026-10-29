package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/config"
)

func TestAuthorizationServiceConfigDefaultsToLocalScopeAdapter(t *testing.T) {
	cfg := loadAuthorizationTestConfig(t, nil)
	service, err := authorizationServiceFromPlatform(cfg)
	if err != nil {
		t.Fatalf("authorizationServiceFromPlatform: %v", err)
	}

	err = service.Can(context.Background(), authorizationTestPrincipal("mission.write"), "mission.edit", identity.ObjectRef{Type: "mission", ID: "mission-1"})

	if err != nil {
		t.Fatalf("Can mission.edit: %v", err)
	}
}

func TestAuthorizationRelationsConfigDefaultsToLocalProjector(t *testing.T) {
	cfg := loadAuthorizationTestConfig(t, nil)
	service, err := authorizationServiceFromPlatform(cfg)
	if err != nil {
		t.Fatalf("authorizationServiceFromPlatform: %v", err)
	}
	if projector := authorizationRelationsFromPlatform(cfg, service); projector == nil {
		t.Fatal("default local authorization must configure relation projection")
	}
}

func TestAuthorizationServiceConfigEnablesOpenFGAShadow(t *testing.T) {
	openFGACalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		openFGACalled = true
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer server.Close()
	cfg := loadAuthorizationTestConfig(t, map[string]string{
		"authz.mode":                            "openfga_shadow",
		"authz.openfga.api_url":                 server.URL,
		"authz.openfga.store_id":                "store-1",
		"authz.openfga.authorization_model_id":  "model-1",
		"authz.openfga.ignore_duplicate_writes": "true",
	})
	service, err := authorizationServiceFromPlatform(cfg)
	if err != nil {
		t.Fatalf("authorizationServiceFromPlatform: %v", err)
	}

	err = service.Can(context.Background(), authorizationTestPrincipal("mission.write"), "mission.edit", identity.ObjectRef{Type: "mission", ID: "mission-1"})

	if err != nil {
		t.Fatalf("shadow mode should return primary local decision: %v", err)
	}
	if !openFGACalled {
		t.Fatalf("openfga shadow check was not called")
	}
}

func TestAuthorizationServiceConfigRejectsNonIdempotentOpenFGAWrites(t *testing.T) {
	for _, mode := range []string{"openfga_shadow", "openfga_enforce"} {
		t.Run(mode, func(t *testing.T) {
			cfg := loadAuthorizationTestConfig(t, map[string]string{
				"authz.mode":                            mode,
				"authz.openfga.api_url":                 "https://fga.example",
				"authz.openfga.store_id":                "store-1",
				"authz.openfga.authorization_model_id":  "model-1",
				"authz.openfga.ignore_duplicate_writes": "false",
			})

			_, err := authorizationServiceFromPlatform(cfg)

			if err == nil || !strings.Contains(err.Error(), "ignore_duplicate_writes must be true") {
				t.Fatalf("non-idempotent OpenFGA config error = %v", err)
			}
		})
	}
}

func TestAuthorizationServiceConfigEnforcesOpenFGA(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(`{"allowed":false}`))
	}))
	defer server.Close()
	cfg := loadAuthorizationTestConfig(t, map[string]string{
		"authz.mode":                           "openfga_enforce",
		"authz.openfga.api_url":                server.URL,
		"authz.openfga.store_id":               "store-1",
		"authz.openfga.authorization_model_id": "model-1",
	})
	service, err := authorizationServiceFromPlatform(cfg)
	if err != nil {
		t.Fatalf("authorizationServiceFromPlatform: %v", err)
	}

	err = service.Can(context.Background(), authorizationTestPrincipal("mission.write"), "mission.edit", identity.ObjectRef{Type: "mission", ID: "mission-1"})

	if !errors.Is(err, authorizationapp.ErrDenied) {
		t.Fatalf("enforce error = %v, want ErrDenied", err)
	}
}

func TestAuthorizationServiceConfigRejectsOpenFGAEnforceObjectTypeRollout(t *testing.T) {
	cfg := loadAuthorizationTestConfig(t, map[string]string{
		"authz.mode":                           "openfga_enforce",
		"authz.openfga.api_url":                "https://fga.example",
		"authz.openfga.store_id":               "store-1",
		"authz.openfga.authorization_model_id": "model-1",
		"authz.openfga.enforce_object_types":   "mission",
	})

	_, err := authorizationServiceFromPlatform(cfg)
	if err == nil || !strings.Contains(err.Error(), "authz.openfga.enforce_object_types") {
		t.Fatalf("partial enforce object type error = %v", err)
	}
}

func TestAuthorizationServiceConfigRejectsOpenFGAFailOpen(t *testing.T) {
	cfg := loadAuthorizationTestConfig(t, map[string]string{
		"authz.mode":                           "openfga_enforce",
		"authz.openfga.api_url":                "http://127.0.0.1:1",
		"authz.openfga.store_id":               "store-1",
		"authz.openfga.authorization_model_id": "model-1",
		"authz.openfga.fail_open_dev":          "true",
	})

	_, err := authorizationServiceFromPlatform(cfg)
	if err == nil || !strings.Contains(err.Error(), "authz.openfga.fail_open_dev") {
		t.Fatalf("fail-open error = %v", err)
	}
}

func TestAuthorizationServiceConfigRequiresProductionEnforceReady(t *testing.T) {
	base := map[string]string{
		"authz.mode":                           "openfga_enforce",
		"authz.openfga.api_url":                "https://fga.example",
		"authz.openfga.store_id":               "store-1",
		"authz.openfga.authorization_model_id": "model-1",
	}
	cfg := loadAuthorizationTestConfig(t, base)
	cfg.Environment = "production"

	_, err := authorizationServiceFromPlatform(cfg)
	if err == nil || !strings.Contains(err.Error(), "authz.openfga.enforce_ready") {
		t.Fatalf("enforce readiness error = %v", err)
	}

	base["authz.openfga.enforce_ready"] = "true"
	cfg = loadAuthorizationTestConfig(t, base)
	cfg.Environment = "production"
	if _, err := authorizationServiceFromPlatform(cfg); err != nil {
		t.Fatalf("authorizationServiceFromPlatform with enforce ready: %v", err)
	}
}

func TestRejectAuthorizationEnforceObjectTypeRolloutFromPlatform(t *testing.T) {
	cfg := loadAuthorizationTestConfig(t, nil)
	if err := rejectAuthorizationEnforceObjectTypeRolloutFromPlatform(cfg); err != nil {
		t.Fatalf("rejectAuthorizationEnforceObjectTypeRolloutFromPlatform default: %v", err)
	}

	cfg = loadAuthorizationTestConfig(t, map[string]string{"authz.openfga.enforce_object_types": "mission, project mission"})
	if err := rejectAuthorizationEnforceObjectTypeRolloutFromPlatform(cfg); err == nil || !strings.Contains(err.Error(), "partial rollout") {
		t.Fatalf("partial object type rollout error = %v", err)
	}

	cfg = loadAuthorizationTestConfig(t, map[string]string{"authz.openfga.enforce_object_types": "all"})
	if err := rejectAuthorizationEnforceObjectTypeRolloutFromPlatform(cfg); err != nil {
		t.Fatalf("rejectAuthorizationEnforceObjectTypeRolloutFromPlatform all: %v", err)
	}

	cfg = loadAuthorizationTestConfig(t, map[string]string{"authz.openfga.enforce_object_types": "mission/project"})
	err := rejectAuthorizationEnforceObjectTypeRolloutFromPlatform(cfg)
	if err == nil || !strings.Contains(err.Error(), "authz.openfga.enforce_object_types") {
		t.Fatalf("invalid object type error = %v", err)
	}
}

func TestAuthorizationServiceConfigRequiresOpenFGASettingsForShadow(t *testing.T) {
	cfg := loadAuthorizationTestConfig(t, map[string]string{
		"authz.mode": "openfga_shadow",
	})

	_, err := authorizationServiceFromPlatform(cfg)

	if err == nil || !strings.Contains(err.Error(), "openfga api_url is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthorizationServiceConfigRejectsInvalidOpenFGATimeout(t *testing.T) {
	cfg := loadAuthorizationTestConfig(t, map[string]string{
		"authz.mode":                           "openfga_shadow",
		"authz.openfga.api_url":                "https://fga.example",
		"authz.openfga.store_id":               "store-1",
		"authz.openfga.authorization_model_id": "model-1",
		"authz.openfga.timeout":                "forever",
	})

	_, err := authorizationServiceFromPlatform(cfg)

	if err == nil || !strings.Contains(err.Error(), "authz.openfga.timeout is invalid") {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthorizationRelationBackfillOptionsFromPlatform(t *testing.T) {
	cfg := loadAuthorizationTestConfig(t, nil)
	opts, err := authorizationRelationBackfillOptionsFromPlatform(cfg)
	if err != nil {
		t.Fatalf("authorizationRelationBackfillOptionsFromPlatform default: %v", err)
	}
	if opts.Mode != authorizationRelationBackfillModeWrite || opts.DryRun || !opts.Diff || !opts.Prune || opts.Disabled {
		t.Fatalf("default options = %+v", opts)
	}

	cfg = loadAuthorizationTestConfig(t, map[string]string{"authz.relation_backfill.mode": "dry_run"})
	opts, err = authorizationRelationBackfillOptionsFromPlatform(cfg)
	if err != nil {
		t.Fatalf("authorizationRelationBackfillOptionsFromPlatform dry_run: %v", err)
	}
	if opts.Mode != authorizationRelationBackfillModeDryRun || !opts.DryRun || !opts.Diff || opts.Disabled {
		t.Fatalf("dry-run options = %+v", opts)
	}

	cfg = loadAuthorizationTestConfig(t, map[string]string{"authz.relation_backfill.mode": "never"})
	_, err = authorizationRelationBackfillOptionsFromPlatform(cfg)
	if err == nil || !strings.Contains(err.Error(), "authz.relation_backfill.mode") {
		t.Fatalf("invalid mode error = %v", err)
	}
}

func TestAuthorizationRoleScopeModeFromPlatform(t *testing.T) {
	cfg := loadAuthorizationTestConfig(t, nil)
	mode, err := authorizationRoleScopeModeFromPlatform(cfg)
	if err != nil {
		t.Fatalf("authorizationRoleScopeModeFromPlatform default: %v", err)
	}
	if mode != operations.RolePermissionScopeModeAuthoritative {
		t.Fatalf("default role scope mode = %q", mode)
	}

	cfg = loadAuthorizationTestConfig(t, map[string]string{"authz.role_scope.mode": "authoritative"})
	mode, err = authorizationRoleScopeModeFromPlatform(cfg)
	if err != nil {
		t.Fatalf("authorizationRoleScopeModeFromPlatform authoritative: %v", err)
	}
	if mode != operations.RolePermissionScopeModeAuthoritative {
		t.Fatalf("authoritative role scope mode = %q", mode)
	}

	cfg = loadAuthorizationTestConfig(t, map[string]string{"authz.role_scope.mode": "legacy"})
	if _, err := authorizationRoleScopeModeFromPlatform(cfg); err == nil || !strings.Contains(err.Error(), "authz.role_scope.mode") {
		t.Fatalf("invalid role scope mode error = %v", err)
	}
}

func loadAuthorizationTestConfig(t *testing.T, env map[string]string) config.Config {
	t.Helper()
	return loadTestConfig(t, env)
}

func authorizationTestPrincipal(scopes ...string) identity.Principal {
	return identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "operator",
		UserID:          "operator",
		Username:        "operator",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    scopes,
	}
}

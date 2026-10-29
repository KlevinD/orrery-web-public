package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/config"
)

const (
	authorizationModeLocal          = "local"
	authorizationModeOpenFGAShadow  = "openfga_shadow"
	authorizationModeOpenFGAEnforce = "openfga_enforce"

	authorizationRelationBackfillModeWrite  = "write"
	authorizationRelationBackfillModeDryRun = "dry_run"
	authorizationRelationBackfillModeOff    = "off"
)

func authorizationServiceFromPlatform(cfg config.Config) (*authorizationapp.Service, error) {
	service, _, err := authorizationServiceWithShadowStatsFromPlatform(cfg)
	return service, err
}

func authorizationServiceWithShadowStatsFromPlatform(cfg config.Config) (*authorizationapp.Service, *authorizationapp.LoggingShadowRecorder, error) {
	return authorizationServiceWithPrimaryFromPlatform(cfg, authorizationapp.NewScopeAdapter(nil))
}

func authorizationServiceWithPrimaryFromPlatform(cfg config.Config, local authorizationapp.Adapter) (*authorizationapp.Service, *authorizationapp.LoggingShadowRecorder, error) {
	mode, _ := cfg.String("authz.mode")
	mode = authorizationModeFromConfig(mode)
	if mode == "" {
		mode = authorizationModeLocal
	}
	if err := validateAuthorizationEnforceReadiness(cfg, mode); err != nil {
		return nil, nil, err
	}
	if err := rejectAuthorizationOpenFGAFailOpenDevFromPlatform(cfg); err != nil {
		return nil, nil, err
	}
	if local == nil {
		return nil, nil, fmt.Errorf("local authorization adapter is required")
	}
	switch mode {
	case authorizationModeLocal:
		return authorizationapp.NewService(local), nil, nil
	case authorizationModeOpenFGAShadow:
		openfga, err := openFGAAdapterFromPlatform(cfg)
		if err != nil {
			return nil, nil, err
		}
		recorder := authorizationapp.NewLoggingShadowRecorder(log.Default())
		return authorizationapp.NewService(authorizationapp.NewShadowAdapter(local, openfga, recorder)), recorder, nil
	case authorizationModeOpenFGAEnforce:
		openfga, err := openFGAAdapterFromPlatform(cfg)
		if err != nil {
			return nil, nil, err
		}
		if err := rejectAuthorizationEnforceObjectTypeRolloutFromPlatform(cfg); err != nil {
			return nil, nil, err
		}
		return authorizationapp.NewService(openfga), nil, nil
	default:
		return nil, nil, fmt.Errorf("authz.mode %q is not supported", mode)
	}
}

func rejectAuthorizationOpenFGAFailOpenDevFromPlatform(cfg config.Config) error {
	enabled, ok := cfg.Bool("authz.openfga.fail_open_dev")
	if !ok || !enabled {
		return nil
	}
	return fmt.Errorf("authz.openfga.fail_open_dev is not supported; OpenFGA authorization must fail closed")
}

func authorizationRelationsFromPlatform(cfg config.Config, service *authorizationapp.Service) *authorizationapp.RelationProjector {
	if service == nil {
		return nil
	}
	mode, _ := cfg.String("authz.mode")
	mode = authorizationModeFromConfig(mode)
	if mode == "" {
		mode = authorizationModeLocal
	}
	switch mode {
	case authorizationModeLocal:
		return authorizationapp.NewRelationProjector(service)
	case authorizationModeOpenFGAShadow:
		return authorizationapp.NewRelationProjector(service)
	case authorizationModeOpenFGAEnforce:
		return authorizationapp.NewRelationProjector(service)
	default:
		return nil
	}
}

func authorizationModeFromConfig(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

func authorizationRoleScopeModeFromPlatform(cfg config.Config) (operations.RolePermissionScopeMode, error) {
	raw, _ := cfg.String("authz.role_scope.mode")
	mode, err := operations.ParseRolePermissionScopeMode(raw)
	if err != nil {
		return "", fmt.Errorf("authz.role_scope.mode: %w", err)
	}
	return mode, nil
}

func validateAuthorizationEnforceReadiness(cfg config.Config, mode string) error {
	if mode != authorizationModeOpenFGAEnforce || strings.ToLower(strings.TrimSpace(cfg.Environment)) != "production" {
		return nil
	}
	ready, _ := cfg.Bool("authz.openfga.enforce_ready")
	if !ready {
		return fmt.Errorf("authz.openfga.enforce_ready must be true before enabling %s in production", authorizationModeOpenFGAEnforce)
	}
	backfillMode, _ := cfg.String("authz.relation_backfill.mode")
	backfillMode = strings.ToLower(strings.TrimSpace(backfillMode))
	if backfillMode == "" {
		backfillMode = authorizationRelationBackfillModeWrite
	}
	if backfillMode != authorizationRelationBackfillModeWrite {
		return fmt.Errorf("authz.relation_backfill.mode must be write before enabling %s in production", authorizationModeOpenFGAEnforce)
	}
	return nil
}

func rejectAuthorizationEnforceObjectTypeRolloutFromPlatform(cfg config.Config) error {
	raw, ok := cfg.String("authz.openfga.enforce_object_types")
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	for _, field := range fields {
		objectType := strings.ToLower(strings.TrimSpace(field))
		if objectType == "" {
			continue
		}
		if objectType == "*" || objectType == "all" {
			return nil
		}
		if !validAuthorizationObjectType(objectType) {
			return fmt.Errorf("authz.openfga.enforce_object_types contains invalid object type %q", field)
		}
	}
	return fmt.Errorf("authz.openfga.enforce_object_types no longer supports partial rollout; use empty or all for fail-closed enforcement")
}

func validAuthorizationObjectType(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func authorizationRelationBackfillOptionsFromPlatform(cfg config.Config) (authorizationRelationBackfillOptions, error) {
	mode, _ := cfg.String("authz.relation_backfill.mode")
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = authorizationRelationBackfillModeWrite
	}
	switch mode {
	case authorizationRelationBackfillModeWrite:
		return authorizationRelationBackfillOptions{Mode: mode, Diff: true, Prune: true}, nil
	case authorizationRelationBackfillModeDryRun:
		return authorizationRelationBackfillOptions{Mode: mode, DryRun: true, Diff: true}, nil
	case authorizationRelationBackfillModeOff:
		return authorizationRelationBackfillOptions{Mode: mode, Disabled: true}, nil
	default:
		return authorizationRelationBackfillOptions{}, fmt.Errorf("authz.relation_backfill.mode %q is not supported", mode)
	}
}

func openFGAAdapterFromPlatform(cfg config.Config) (*authorizationapp.OpenFGAAdapter, error) {
	apiURL, _ := cfg.String("authz.openfga.api_url")
	storeID, _ := cfg.String("authz.openfga.store_id")
	modelID, _ := cfg.String("authz.openfga.authorization_model_id")
	token, _ := cfg.String("authz.openfga.token")
	timeout := 10 * time.Second
	if rawTimeout, ok := cfg.String("authz.openfga.timeout"); ok && strings.TrimSpace(rawTimeout) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(rawTimeout))
		if err != nil {
			return nil, fmt.Errorf("authz.openfga.timeout is invalid: %w", err)
		}
		timeout = parsed
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("authz.openfga.timeout must be positive")
	}
	ignoreDuplicateWrites := true
	if rawIgnore, ok := cfg.String("authz.openfga.ignore_duplicate_writes"); ok && strings.TrimSpace(rawIgnore) != "" {
		parsed, err := strconv.ParseBool(strings.TrimSpace(rawIgnore))
		if err != nil {
			return nil, fmt.Errorf("authz.openfga.ignore_duplicate_writes is invalid: %w", err)
		}
		ignoreDuplicateWrites = parsed
	}
	if !ignoreDuplicateWrites {
		return nil, fmt.Errorf("authz.openfga.ignore_duplicate_writes must be true because authorization relation effects are replayable")
	}
	return authorizationapp.NewOpenFGAAdapter(authorizationapp.OpenFGAConfig{
		APIURL:                apiURL,
		StoreID:               storeID,
		AuthorizationModelID:  modelID,
		Token:                 token,
		HTTPClient:            &http.Client{Timeout: timeout},
		IgnoreDuplicateWrites: ignoreDuplicateWrites,
	})
}

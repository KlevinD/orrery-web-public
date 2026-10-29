package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type SecretValues struct {
	DatabasePassword string
	LocalMFATOTP     string
	// FeishuAppID is not secret, but it belongs to the same deployment
	// credential set as FeishuAppSecret so Orrery and Kratos cannot drift.
	FeishuAppID     string
	FeishuAppSecret string
	FeishuState     string
	OIDCClient      string
	KratosAdmin     string
	HydraAdmin      string
	OpenFGAToken    string
}

func defaultConfig() Config {
	return Config{
		Environment:  "development",
		BaseDir:      ".",
		DatabaseURLs: map[string]string{},
		Server: ServerConfig{
			Addr:              "127.0.0.1:8080",
			TrustedProxyCIDRs: []string{"127.0.0.1/8", "::1/128"},
		},
		DeviceDelivery: DeviceDeliveryConfig{
			DefaultTenantID:  "global",
			MaxArtifactBytes: 10 * 1024 * 1024,
		},
		Runtime: RuntimeConfig{
			ExecEnvRoot: "./data/execenv",
			MCP: RuntimeMCPConfig{
				URL:        "http://127.0.0.1:8080/mcp",
				ServerName: "orrery",
				Transport:  "streamable-http",
			},
		},
		Development: DevelopmentConfig{
			WorkspaceRoot: "./data/managed-workspaces", WorkspaceLeaseTTL: 2 * time.Hour,
			WorkspaceRetention: 14 * 24 * time.Hour, GitCommitterName: "Orrery Git Service",
			GitCommitterEmail: "orrery@localhost", GitSigningFormat: "ssh", RequireSignedCommits: true,
			MaxChangedFileBytes: 5 * 1024 * 1024,
		},
		Integrations: IntegrationsConfig{
			LegalMCPURL: "http://127.0.0.1:8092/mcp",
			People: PeopleIntegrationConfig{
				OrreryTenantID: "system", ReconcileInterval: 15 * time.Minute,
				PlannerCron: "0 9 * * *", PlannerTimezone: "Asia/Shanghai",
				AgentBindings: map[string]string{}, HumanRoleMappings: map[string]string{},
			},
		},
		Bootstrap:         BootstrapConfig{PageIndexEnabled: true},
		FileStoreRoot:     "./data/artifacts",
		FileStore:         FileStoreConfig{Backend: "local", Root: "./data/artifacts"},
		ReadinessCacheTTL: time.Second,
		Logging:           LoggingConfig{Level: "info", Console: true, File: "./data/logs/platform.jsonl"},
		Queue:             QueueConfig{ClaimTTL: 30 * time.Second, MaxAttempts: 3, SweepBatchSize: 100, WorkerConcurrency: 4},
		Lock:              LockConfig{DefaultTTL: 30 * time.Second},
		Liveness:          LivenessConfig{HeartbeatTTL: time.Minute},
		Metrics:           MetricsConfig{BufferSize: 1024, WarnInterval: time.Minute},
		Secret:            SecretConfig{Root: "./secrets"},
		Sweeper:           SweeperConfig{Timeout: 30 * time.Second, BatchSize: 100},
		Tracing:           TracingConfig{BufferSize: 1024},
		raw:               map[string]any{},
	}
}

func loadYAML(path string, secrets SecretValues) (Config, error) {
	cfg := defaultConfig()
	parsed, raw, err := loadYAMLFile(path)
	if err != nil {
		return Config{}, err
	}
	hasDatabaseConfig := parsed.Database.DefaultURL != "" || len(parsed.Database.URLs) > 0
	hasSplitDatabaseConfig := len(parsed.Database.URLs) > 0
	if err := applyYAML(&cfg, parsed, raw); err != nil {
		return Config{}, err
	}
	if err := requireExplicitDatabaseConfig(&cfg, hasDatabaseConfig, hasSplitDatabaseConfig); err != nil {
		return Config{}, err
	}
	if cfg.ReadinessCacheTTL <= 0 {
		return Config{}, fmt.Errorf("readiness cache ttl must be positive")
	}
	if err := validateProfileConfig(cfg); err != nil {
		return Config{}, err
	}
	if err := validatePeopleIntegration(cfg.Integrations.People); err != nil {
		return Config{}, err
	}
	if localEnabled, _ := cfg.Bool("auth.local.enabled"); cfg.Development.AllowOwnerSelfApproval && !localEnabled {
		return Config{}, fmt.Errorf("development.allow_owner_self_approval requires explicit local authentication mode")
	}
	if err := applyDatabasePassword(&cfg, secrets.DatabasePassword); err != nil {
		return Config{}, err
	}
	applySecretValues(&cfg.raw, secrets)
	resolveRelativePaths(&cfg)
	cfg.raw = rawFromConfig(cfg, cfg.raw)
	return cfg, nil
}

func applySecretValues(raw *map[string]any, secrets SecretValues) {
	for _, secret := range []struct {
		value string
		path  []string
	}{
		{secrets.LocalMFATOTP, []string{"auth", "local", "mfa_totp_secret"}},
		{secrets.FeishuAppID, []string{"auth", "feishu", "app_id"}},
		{secrets.FeishuAppSecret, []string{"auth", "feishu", "app_secret"}},
		{secrets.FeishuState, []string{"auth", "feishu", "state_secret"}},
		{secrets.OIDCClient, []string{"auth", "oidc", "client_secret"}},
		{secrets.KratosAdmin, []string{"auth", "kratos", "admin_token"}},
		{secrets.HydraAdmin, []string{"auth", "hydra", "admin_token"}},
		{secrets.OpenFGAToken, []string{"authz", "openfga", "token"}},
	} {
		if value := strings.TrimSpace(secret.value); value != "" {
			setRawPathValue(raw, value, secret.path...)
		}
	}
}

var requiredDatabaseRoles = []string{"core", "audit", "events", "jobs", "runtime", "observability"}

func requireExplicitDatabaseConfig(cfg *Config, hasDatabaseConfig bool, hasSplitDatabaseConfig bool) error {
	if !hasDatabaseConfig {
		return fmt.Errorf("database configuration is required; set database.default_url or database.urls in YAML")
	}
	if hasSplitDatabaseConfig {
		if err := requireCompleteDatabaseURLs(cfg.DatabaseURLs); err != nil {
			return err
		}
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		cfg.DatabaseURL = strings.TrimSpace(cfg.DatabaseURLs["core"])
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return fmt.Errorf("database configuration is required; set database.default_url or database.urls.core in YAML")
	}
	if len(cfg.DatabaseURLs) == 0 {
		cfg.DatabaseURLs = roleDatabaseURLs(cfg.DatabaseURL)
	}
	return nil
}

func requireCompleteDatabaseURLs(databaseURLs map[string]string) error {
	for _, role := range requiredDatabaseRoles {
		if strings.TrimSpace(databaseURLs[role]) == "" {
			return fmt.Errorf("database.urls.%s is required", role)
		}
	}
	return nil
}

func applyDatabasePassword(cfg *Config, password string) error {
	envName := strings.TrimSpace(cfg.DatabasePasswordEnv)
	if envName == "" {
		return nil
	}
	if envName != "ORRERY_DATABASE_PASSWORD" {
		return fmt.Errorf("database.password_env must be ORRERY_DATABASE_PASSWORD")
	}
	if password == "" {
		return fmt.Errorf("database password is required through %s", envName)
	}
	var err error
	cfg.DatabaseURL, err = databaseURLWithPassword(cfg.DatabaseURL, password)
	if err != nil {
		return fmt.Errorf("apply database password to database.default_url: %w", err)
	}
	for role, databaseURL := range cfg.DatabaseURLs {
		cfg.DatabaseURLs[role], err = databaseURLWithPassword(databaseURL, password)
		if err != nil {
			return fmt.Errorf("apply database password to database.urls.%s: %w", role, err)
		}
	}
	return nil
}

func databaseURLWithPassword(rawURL, password string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return rawURL, nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid database URL")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", fmt.Errorf("password injection requires a PostgreSQL URL")
	}
	if parsed.User == nil || strings.TrimSpace(parsed.User.Username()) == "" {
		return "", fmt.Errorf("database URL username is required")
	}
	if _, present := parsed.User.Password(); present {
		return "", fmt.Errorf("database URL must not contain an inline password when database.password_env is set")
	}
	parsed.User = url.UserPassword(parsed.User.Username(), password)
	return parsed.String(), nil
}

func validateProfileConfig(cfg Config) error {
	profile := strings.ToLower(strings.TrimSpace(cfg.Profile))
	if profile == "" {
		return nil
	}
	if cfg.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1 for profile configuration")
	}
	if profile != "development" && profile != "production" {
		return fmt.Errorf("profile must be development or production")
	}
	environment := strings.ToLower(strings.TrimSpace(cfg.Environment))
	if environment != profile {
		return fmt.Errorf("profile %q does not match environment %q", profile, cfg.Environment)
	}
	for _, section := range []string{
		"server", "device_delivery", "paths", "database", "runtime", "integrations", "bootstrap",
		"filestore", "logging", "health", "queue", "lock", "liveness", "metrics",
		"secret", "sweeper", "tracing", "resource", "auth", "authz", "discussion",
	} {
		if _, ok := cfg.raw[section]; !ok {
			return fmt.Errorf("%s section is required for profile %s", section, profile)
		}
	}
	for _, path := range requiredProfilePaths {
		if !rawPathExists(cfg.raw, strings.Split(path, ".")...) {
			return fmt.Errorf("%s is required for profile %s", path, profile)
		}
	}
	if strings.TrimSpace(cfg.Server.Addr) == "" {
		return fmt.Errorf("server.addr is required")
	}
	if err := validateDeviceDeliverySection(cfg); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.DatabasePasswordEnv) == "" {
		for role, databaseURL := range cfg.DatabaseURLs {
			scheme, ok := databaseURLScheme(databaseURL)
			if !ok {
				return fmt.Errorf("database.urls.%s is invalid", role)
			}
			if scheme == "postgres" || scheme == "postgresql" {
				return fmt.Errorf("database.password_env is required for PostgreSQL")
			}
		}
	} else if cfg.DatabasePasswordEnv != "ORRERY_DATABASE_PASSWORD" {
		return fmt.Errorf("database.password_env must be ORRERY_DATABASE_PASSWORD")
	}
	if err := requireCompleteDatabaseURLs(cfg.DatabaseURLs); err != nil {
		return err
	}
	for role := range cfg.DatabaseURLs {
		if !isRequiredDatabaseRole(role) {
			return fmt.Errorf("database.urls.%s is not a supported database role", role)
		}
	}
	if err := validateProfileDatabaseURL(profile, "database.default_url", cfg.DatabaseURL, cfg.DatabasePasswordEnv, cfg.AllowEmbeddedProductionDatabase); err != nil {
		return err
	}
	for role, databaseURL := range cfg.DatabaseURLs {
		if err := validateProfileDatabaseURL(profile, "database.urls."+role, databaseURL, cfg.DatabasePasswordEnv, cfg.AllowEmbeddedProductionDatabase); err != nil {
			return err
		}
	}
	if strings.TrimSpace(cfg.Runtime.ExecEnvRoot) == "" {
		return fmt.Errorf("runtime.execenv.root is required")
	}
	if strings.TrimSpace(cfg.Runtime.MCP.URL) == "" || strings.TrimSpace(cfg.Runtime.MCP.ServerName) == "" || strings.TrimSpace(cfg.Runtime.MCP.Transport) == "" {
		return fmt.Errorf("runtime.mcp.url, server_name, and transport are required")
	}
	if err := validatePeopleIntegration(cfg.Integrations.People); err != nil {
		return err
	}
	return nil
}

func isRequiredDatabaseRole(role string) bool {
	for _, required := range requiredDatabaseRoles {
		if role == required {
			return true
		}
	}
	return false
}

func validateProfileDatabaseURL(profile, path, rawURL, passwordEnv string, allowEmbeddedProduction bool) error {
	trimmed := strings.TrimSpace(rawURL)
	scheme, ok := databaseURLScheme(trimmed)
	if !ok {
		return fmt.Errorf("%s is invalid", path)
	}
	switch scheme {
	case "memory", "file", "sqlite", "postgres", "postgresql":
	default:
		return fmt.Errorf("%s uses unsupported scheme %q", path, scheme)
	}
	isPostgres := scheme == "postgres" || scheme == "postgresql"
	if profile == "production" && !isPostgres && !allowEmbeddedProduction {
		return fmt.Errorf("%s must use PostgreSQL in production", path)
	}
	if !isPostgres {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || parsed.User == nil || strings.TrimSpace(parsed.User.Username()) == "" {
		return fmt.Errorf("%s is invalid", path)
	}
	if _, present := parsed.User.Password(); present && strings.TrimSpace(passwordEnv) == "" {
		return fmt.Errorf("%s must not contain an inline password", path)
	}
	return nil
}

func databaseURLScheme(rawURL string) (string, bool) {
	scheme, payload, ok := strings.Cut(strings.TrimSpace(rawURL), "://")
	if !ok || strings.TrimSpace(scheme) == "" || strings.TrimSpace(payload) == "" {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(scheme)), true
}

var requiredProfilePaths = []string{
	"schema_version", "profile", "environment",
	"server.addr", "server.trusted_proxy_cidrs", "server.trusted_viewer_headers_enabled",
	"device_delivery.public_origin", "device_delivery.default_tenant_id", "device_delivery.max_artifact_bytes",
	"paths.base_dir",
	"database.password_env", "database.default_url", "database.urls.core", "database.urls.audit",
	"database.urls.events", "database.urls.jobs", "database.urls.runtime", "database.urls.observability",
	"runtime.execenv.root", "runtime.managed.enabled", "runtime.mcp.url", "runtime.mcp.server_name", "runtime.mcp.transport",
	"integrations.legal.mcp_url",
	"bootstrap.pageindex.api_url", "bootstrap.pageindex.api_key_ref", "bootstrap.gateway.legacy_config",
	"filestore.backend", "filestore.root", "filestore.base_url",
	"logging.level", "logging.console", "logging.file",
	"health.readiness_cache_ttl",
	"queue.claim_ttl", "queue.max_attempts", "queue.sweep_batch_size",
	"lock.default_ttl", "liveness.heartbeat_ttl",
	"metrics.addr", "metrics.buffer_size", "metrics.warn_interval",
	"secret.root", "sweeper.timeout", "sweeper.batch_size", "tracing.buffer_size",
	"resource.mcp_registry_url",
	"auth.require_https", "auth.local.enabled", "auth.local.superadmin_mfa_enabled",
	"auth.identity.mode", "auth.identity.auto_link_enabled",
	"auth.kratos.public_url", "auth.kratos.callback_url", "auth.kratos.display_name", "auth.kratos.session_cookie_names",
	"auth.kratos.admin_url", "auth.kratos.schema_id", "auth.kratos.feishu_provider_id",
	"auth.feishu.app_id", "auth.feishu.callback_url", "auth.feishu.binding_callback_url",
	"auth.feishu.allowed_tenant_key", "auth.feishu.auto_provision_enabled", "auth.feishu.directory_sync_enabled",
	"auth.oidc.introspection_url", "auth.oidc.client_id", "auth.oidc.issuer", "auth.oidc.audience", "auth.oidc.provider",
	"auth.hydra.admin_url", "auth.hydra.remember_for", "auth.hydra.client_sync_enabled",
	"authz.mode", "authz.role_scope.mode",
	"authz.openfga.api_url", "authz.openfga.store_id", "authz.openfga.authorization_model_id",
	"authz.openfga.timeout", "authz.openfga.ignore_duplicate_writes", "authz.openfga.enforce_ready",
	"authz.openfga.enforce_object_types", "authz.openfga.fail_open_dev", "authz.relation_backfill.mode",
	"discussion.agent_id", "discussion.agent.runtime_kind", "discussion.agent.runtime_config_ref",
	"discussion.agent.runtime_config", "discussion.agent.system_prompt", "discussion.agent.model",
	"discussion.agent.working_directory", "discussion.agent.timeout",
}

func rawPathExists(raw map[string]any, path ...string) bool {
	if len(path) == 0 {
		return false
	}
	var current any = raw
	for _, segment := range path {
		mapping, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = mapping[segment]
		if !ok {
			return false
		}
	}
	return true
}

// processSecretValues is the complete environment boundary for application
// configuration. Non-sensitive runtime values are intentionally
// absent: their only authority is the selected YAML file.
func processSecretValues() SecretValues {
	return SecretValues{
		DatabasePassword: os.Getenv("ORRERY_DATABASE_PASSWORD"),
		LocalMFATOTP:     os.Getenv("ORRERY_AUTH_LOCAL_MFA_TOTP_SECRET"),
		FeishuAppID:      os.Getenv("ORRERY_FEISHU_APP_ID"),
		FeishuAppSecret:  os.Getenv("ORRERY_FEISHU_APP_SECRET"),
		FeishuState:      os.Getenv("ORRERY_FEISHU_STATE_SECRET"),
		OIDCClient:       os.Getenv("ORRERY_OIDC_CLIENT_SECRET"),
		KratosAdmin:      os.Getenv("ORRERY_KRATOS_ADMIN_TOKEN"),
		HydraAdmin:       os.Getenv("ORRERY_HYDRA_ADMIN_TOKEN"),
		OpenFGAToken:     os.Getenv("ORRERY_AUTHZ_OPENFGA_TOKEN"),
	}
}

func roleDatabaseURLs(rawURL string) map[string]string {
	result := map[string]string{}
	for _, role := range requiredDatabaseRoles {
		result[role] = rawURL
	}
	return result
}

func setRawValue(raw *map[string]any, section string, key string, value any) {
	setRawPathValue(raw, value, section, key)
}

func setRawPathValue(raw *map[string]any, value any, path ...string) {
	if len(path) == 0 {
		return
	}
	if *raw == nil {
		*raw = map[string]any{}
	}
	node := *raw
	for _, part := range path[:len(path)-1] {
		child, ok := node[part].(map[string]any)
		if !ok {
			child = map[string]any{}
			node[part] = child
		}
		node = child
	}
	node[path[len(path)-1]] = value
}

func rawFromConfig(cfg Config, existing map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range existing {
		out[key] = value
	}
	databaseURLs := map[string]any{}
	for name, value := range cfg.DatabaseURLs {
		databaseURLs[name] = value
	}
	if cfg.SchemaVersion != 0 {
		out["schema_version"] = cfg.SchemaVersion
	}
	if cfg.Profile != "" {
		out["profile"] = cfg.Profile
	}
	out["environment"] = cfg.Environment
	out["server"] = map[string]any{
		"addr":                           cfg.Server.Addr,
		"trusted_proxy_cidrs":            append([]string(nil), cfg.Server.TrustedProxyCIDRs...),
		"trusted_viewer_headers_enabled": cfg.Server.TrustedViewerHeadersEnabled,
	}
	out["device_delivery"] = map[string]any{
		"public_origin":      cfg.DeviceDelivery.PublicOrigin,
		"default_tenant_id":  cfg.DeviceDelivery.DefaultTenantID,
		"max_artifact_bytes": cfg.DeviceDelivery.MaxArtifactBytes,
	}
	if cfg.BaseDir != "" {
		out["paths"] = map[string]any{
			"base_dir": cfg.BaseDir,
		}
	}
	out["database"] = map[string]any{
		"default_url":                  cfg.DatabaseURL,
		"urls":                         databaseURLs,
		"password_env":                 cfg.DatabasePasswordEnv,
		"allow_embedded_in_production": cfg.AllowEmbeddedProductionDatabase,
	}
	out["runtime"] = map[string]any{
		"execenv": map[string]any{"root": cfg.Runtime.ExecEnvRoot},
		"managed": map[string]any{"enabled": cfg.Runtime.ManagedEnabled},
		"mcp": map[string]any{
			"url":         cfg.Runtime.MCP.URL,
			"server_name": cfg.Runtime.MCP.ServerName,
			"transport":   cfg.Runtime.MCP.Transport,
		},
	}
	out["development"] = map[string]any{
		"allow_owner_self_approval": cfg.Development.AllowOwnerSelfApproval,
		"workspace_root":            cfg.Development.WorkspaceRoot,
		"workspace_lease_ttl":       cfg.Development.WorkspaceLeaseTTL.String(),
		"workspace_retention":       cfg.Development.WorkspaceRetention.String(),
		"git_committer_name":        cfg.Development.GitCommitterName,
		"git_committer_email":       cfg.Development.GitCommitterEmail,
		"git_signing_key_ref":       cfg.Development.GitSigningKeyRef,
		"git_signing_format":        cfg.Development.GitSigningFormat,
		"require_signed_commits":    cfg.Development.RequireSignedCommits,
		"max_changed_file_bytes":    cfg.Development.MaxChangedFileBytes,
	}
	out["integrations"] = map[string]any{
		"legal": map[string]any{"mcp_url": cfg.Integrations.LegalMCPURL},
		"people": map[string]any{
			"enabled":             cfg.Integrations.People.Enabled,
			"base_url":            cfg.Integrations.People.BaseURL,
			"token_ref":           cfg.Integrations.People.TokenRef,
			"people_tenant_id":    cfg.Integrations.People.PeopleTenantID,
			"orrery_tenant_id":    cfg.Integrations.People.OrreryTenantID,
			"reconcile_interval":  cfg.Integrations.People.ReconcileInterval.String(),
			"planner_cron":        cfg.Integrations.People.PlannerCron,
			"planner_timezone":    cfg.Integrations.People.PlannerTimezone,
			"agent_bindings":      cloneStringMap(cfg.Integrations.People.AgentBindings),
			"human_role_mappings": cloneStringMap(cfg.Integrations.People.HumanRoleMappings),
		},
	}
	out["bootstrap"] = map[string]any{
		"image_generation": map[string]any{
			"enabled":     cfg.Bootstrap.ImageGeneration.Enabled,
			"base_url":    cfg.Bootstrap.ImageGeneration.BaseURL,
			"model":       cfg.Bootstrap.ImageGeneration.Model,
			"api_key_ref": cfg.Bootstrap.ImageGeneration.APIKeyRef,
		},
		"pageindex": map[string]any{
			"enabled":     cfg.Bootstrap.PageIndexEnabled,
			"api_url":     cfg.Bootstrap.PageIndexAPIURL,
			"api_key_ref": cfg.Bootstrap.PageIndexAPIKeyRef,
		},
		"gateway": map[string]any{"legacy_config": cfg.Bootstrap.GatewayConfigPath},
	}
	out["filestore"] = map[string]any{
		"backend":  cfg.FileStore.Backend,
		"root":     cfg.FileStore.Root,
		"base_url": cfg.FileStore.BaseURL,
		"s3": map[string]any{
			"bucket":       cfg.FileStore.S3.Bucket,
			"region":       cfg.FileStore.S3.Region,
			"endpoint_url": cfg.FileStore.S3.EndpointURL,
			"cdn_domain":   cfg.FileStore.S3.CDNDomain,
		},
	}
	out["logging"] = map[string]any{
		"level":   cfg.Logging.Level,
		"console": cfg.Logging.Console,
		"file":    cfg.Logging.File,
	}
	out["health"] = map[string]any{
		"readiness_cache_ttl": cfg.ReadinessCacheTTL.String(),
	}
	out["queue"] = map[string]any{
		"claim_ttl":          cfg.Queue.ClaimTTL.String(),
		"max_attempts":       cfg.Queue.MaxAttempts,
		"sweep_batch_size":   cfg.Queue.SweepBatchSize,
		"worker_concurrency": cfg.Queue.WorkerConcurrency,
	}
	out["lock"] = map[string]any{
		"default_ttl": cfg.Lock.DefaultTTL.String(),
	}
	out["liveness"] = map[string]any{
		"heartbeat_ttl": cfg.Liveness.HeartbeatTTL.String(),
	}
	out["metrics"] = map[string]any{
		"addr":          cfg.Metrics.Addr,
		"buffer_size":   cfg.Metrics.BufferSize,
		"warn_interval": cfg.Metrics.WarnInterval.String(),
	}
	out["secret"] = map[string]any{
		"root": cfg.Secret.Root,
	}
	out["sweeper"] = map[string]any{
		"timeout":    cfg.Sweeper.Timeout.String(),
		"batch_size": cfg.Sweeper.BatchSize,
	}
	out["tracing"] = map[string]any{
		"buffer_size": cfg.Tracing.BufferSize,
	}
	return out
}

func validatePeopleIntegration(cfg PeopleIntegrationConfig) error {
	if !cfg.Enabled {
		return nil
	}
	required := []struct {
		path  string
		value string
	}{
		{path: "integrations.people.base_url", value: cfg.BaseURL},
		{path: "integrations.people.token_ref", value: cfg.TokenRef},
		{path: "integrations.people.people_tenant_id", value: cfg.PeopleTenantID},
		{path: "integrations.people.orrery_tenant_id", value: cfg.OrreryTenantID},
		{path: "integrations.people.planner_cron", value: cfg.PlannerCron},
		{path: "integrations.people.planner_timezone", value: cfg.PlannerTimezone},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required when integrations.people.enabled is true", field.path)
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("integrations.people.base_url must be an HTTP(S) URL")
	}
	if !strings.HasPrefix(strings.TrimSpace(cfg.TokenRef), "secret://") {
		return fmt.Errorf("integrations.people.token_ref must use secret://")
	}
	if cfg.ReconcileInterval <= 0 || cfg.ReconcileInterval > 24*time.Hour {
		return fmt.Errorf("integrations.people.reconcile_interval must be positive and no greater than 24h")
	}
	if len(cfg.AgentBindings) == 0 {
		return fmt.Errorf("integrations.people.agent_bindings is required when integrations.people.enabled is true")
	}
	for subjectID, agentProfileID := range cfg.AgentBindings {
		if strings.TrimSpace(subjectID) == "" || strings.TrimSpace(agentProfileID) == "" {
			return fmt.Errorf("integrations.people.agent_bindings must not contain blank IDs")
		}
	}
	for responsibilityCode, roleID := range cfg.HumanRoleMappings {
		if strings.TrimSpace(responsibilityCode) == "" || strings.TrimSpace(roleID) == "" {
			return fmt.Errorf("integrations.people.human_role_mappings must not contain blank codes or role IDs")
		}
	}
	if _, err := time.LoadLocation(strings.TrimSpace(cfg.PlannerTimezone)); err != nil {
		return fmt.Errorf("integrations.people.planner_timezone is invalid: %w", err)
	}
	return nil
}

func resolveRelativePaths(cfg *Config) {
	base := strings.TrimSpace(cfg.BaseDir)
	if base == "" {
		return
	}
	cfg.BaseDir = filepath.Clean(base)
	cfg.DatabaseURL = resolveLocalDatabaseURL(cfg.BaseDir, cfg.DatabaseURL)
	for role, databaseURL := range cfg.DatabaseURLs {
		cfg.DatabaseURLs[role] = resolveLocalDatabaseURL(cfg.BaseDir, databaseURL)
	}
	cfg.FileStoreRoot = resolveLocalPath(cfg.BaseDir, cfg.FileStoreRoot)
	cfg.FileStore.Root = resolveLocalPath(cfg.BaseDir, cfg.FileStore.Root)
	cfg.Logging.File = resolveLocalPath(cfg.BaseDir, cfg.Logging.File)
	cfg.Secret.Root = resolveLocalPath(cfg.BaseDir, cfg.Secret.Root)
	cfg.Runtime.ExecEnvRoot = resolveLocalPath(cfg.BaseDir, cfg.Runtime.ExecEnvRoot)
	cfg.Development.WorkspaceRoot = resolveLocalPath(cfg.BaseDir, cfg.Development.WorkspaceRoot)
	cfg.Bootstrap.GatewayConfigPath = resolveLocalPath(cfg.BaseDir, cfg.Bootstrap.GatewayConfigPath)
}

func resolveLocalDatabaseURL(base, rawURL string) string {
	switch {
	case strings.HasPrefix(rawURL, "file://"):
		return "file://" + resolveLocalPath(base, strings.TrimPrefix(rawURL, "file://"))
	case strings.HasPrefix(rawURL, "sqlite://"):
		return "sqlite://" + resolveLocalPath(base, strings.TrimPrefix(rawURL, "sqlite://"))
	default:
		return rawURL
	}
}

func resolveLocalPath(base, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

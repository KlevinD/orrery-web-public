package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type yamlConfig struct {
	SchemaVersion int    `yaml:"schema_version"`
	Profile       string `yaml:"profile"`
	Environment   string `yaml:"environment"`
	Server        struct {
		Addr                        string   `yaml:"addr"`
		TrustedProxyCIDRs           []string `yaml:"trusted_proxy_cidrs"`
		TrustedViewerHeadersEnabled *bool    `yaml:"trusted_viewer_headers_enabled"`
	} `yaml:"server"`
	DeviceDelivery struct {
		PublicOrigin     *string `yaml:"public_origin"`
		DefaultTenantID  *string `yaml:"default_tenant_id"`
		MaxArtifactBytes *int64  `yaml:"max_artifact_bytes"`
	} `yaml:"device_delivery"`
	Paths struct {
		BaseDir *string `yaml:"base_dir"`
	} `yaml:"paths"`
	Database struct {
		DefaultURL                string            `yaml:"default_url"`
		URLs                      map[string]string `yaml:"urls"`
		PasswordEnv               string            `yaml:"password_env"`
		AllowEmbeddedInProduction *bool             `yaml:"allow_embedded_in_production"`
	} `yaml:"database"`
	Runtime struct {
		ExecEnv struct {
			Root string `yaml:"root"`
		} `yaml:"execenv"`
		Managed struct {
			Enabled *bool `yaml:"enabled"`
		} `yaml:"managed"`
		MCP struct {
			URL        string `yaml:"url"`
			ServerName string `yaml:"server_name"`
			Transport  string `yaml:"transport"`
		} `yaml:"mcp"`
	} `yaml:"runtime"`
	Development struct {
		AllowOwnerSelfApproval *bool  `yaml:"allow_owner_self_approval"`
		WorkspaceRoot          string `yaml:"workspace_root"`
		WorkspaceLeaseTTL      string `yaml:"workspace_lease_ttl"`
		WorkspaceRetention     string `yaml:"workspace_retention"`
		GitCommitterName       string `yaml:"git_committer_name"`
		GitCommitterEmail      string `yaml:"git_committer_email"`
		GitSigningKeyRef       string `yaml:"git_signing_key_ref"`
		GitSigningFormat       string `yaml:"git_signing_format"`
		RequireSignedCommits   *bool  `yaml:"require_signed_commits"`
		MaxChangedFileBytes    *int64 `yaml:"max_changed_file_bytes"`
	} `yaml:"development"`
	Integrations struct {
		Legal struct {
			MCPURL string `yaml:"mcp_url"`
		} `yaml:"legal"`
		People struct {
			Enabled           *bool             `yaml:"enabled"`
			BaseURL           string            `yaml:"base_url"`
			TokenRef          string            `yaml:"token_ref"`
			PeopleTenantID    string            `yaml:"people_tenant_id"`
			OrreryTenantID    string            `yaml:"orrery_tenant_id"`
			ReconcileInterval string            `yaml:"reconcile_interval"`
			PlannerCron       string            `yaml:"planner_cron"`
			PlannerTimezone   string            `yaml:"planner_timezone"`
			AgentBindings     map[string]string `yaml:"agent_bindings"`
			HumanRoleMappings map[string]string `yaml:"human_role_mappings"`
		} `yaml:"people"`
	} `yaml:"integrations"`
	Bootstrap struct {
		ImageGeneration struct {
			Enabled   bool   `yaml:"enabled"`
			BaseURL   string `yaml:"base_url"`
			Model     string `yaml:"model"`
			APIKeyRef string `yaml:"api_key_ref"`
		} `yaml:"image_generation"`
		PageIndex struct {
			Enabled   *bool  `yaml:"enabled"`
			APIURL    string `yaml:"api_url"`
			APIKeyRef string `yaml:"api_key_ref"`
		} `yaml:"pageindex"`
		Gateway struct {
			LegacyConfig string `yaml:"legacy_config"`
		} `yaml:"gateway"`
	} `yaml:"bootstrap"`
	FileStore struct {
		Backend string `yaml:"backend"`
		Root    string `yaml:"root"`
		BaseURL string `yaml:"base_url"`
		S3      struct {
			Bucket      string `yaml:"bucket"`
			Region      string `yaml:"region"`
			EndpointURL string `yaml:"endpoint_url"`
			CDNDomain   string `yaml:"cdn_domain"`
		} `yaml:"s3"`
	} `yaml:"filestore"`
	Logging struct {
		Level   string `yaml:"level"`
		Console *bool  `yaml:"console"`
		File    string `yaml:"file"`
	} `yaml:"logging"`
	Health struct {
		ReadinessCacheTTL string `yaml:"readiness_cache_ttl"`
	} `yaml:"health"`
	Queue struct {
		ClaimTTL          string `yaml:"claim_ttl"`
		MaxAttempts       *int   `yaml:"max_attempts"`
		SweepBatchSize    *int   `yaml:"sweep_batch_size"`
		WorkerConcurrency *int   `yaml:"worker_concurrency"`
	} `yaml:"queue"`
	Lock struct {
		DefaultTTL string `yaml:"default_ttl"`
	} `yaml:"lock"`
	Liveness struct {
		HeartbeatTTL string `yaml:"heartbeat_ttl"`
	} `yaml:"liveness"`
	Metrics struct {
		Addr         string `yaml:"addr"`
		BufferSize   *int   `yaml:"buffer_size"`
		WarnInterval string `yaml:"warn_interval"`
	} `yaml:"metrics"`
	Secret struct {
		Root string `yaml:"root"`
	} `yaml:"secret"`
	Sweeper struct {
		Timeout   string `yaml:"timeout"`
		BatchSize *int   `yaml:"batch_size"`
	} `yaml:"sweeper"`
	Tracing struct {
		BufferSize *int `yaml:"buffer_size"`
	} `yaml:"tracing"`
	Resource struct {
		MCPRegistryURL string `yaml:"mcp_registry_url"`
	} `yaml:"resource"`
	Auth        yamlAuthConfig                  `yaml:"auth"`
	Authz       yamlAuthzConfig                 `yaml:"authz"`
	Discussion  yamlDiscussionConfig            `yaml:"discussion"`
	Knowledge   yamlKnowledgeConfig             `yaml:"knowledge"`
	Ontology    yamlOntologyConfig              `yaml:"ontology"`
	Mission     map[string]any                  `yaml:"mission"`
	Agents      map[string]yamlAgentRuntimeSpec `yaml:"agents"`
	Maintenance struct {
		RepairRuntimeConfigRef string `yaml:"repair_runtime_config_ref"`
		RepairTimeout          string `yaml:"repair_timeout"`
	} `yaml:"maintenance"`
	AgentDefaults yamlAgentRuntimeSpec `yaml:"agent_defaults"`
	Extensions    map[string]any       `yaml:",inline"`
}

type yamlAuthConfig struct {
	RequireHTTPS *bool `yaml:"require_https"`
	Local        struct {
		Enabled              *bool  `yaml:"enabled"`
		SuperadminMFAEnabled *bool  `yaml:"superadmin_mfa_enabled"`
		MFATOTPSecret        string `yaml:"mfa_totp_secret"`
	} `yaml:"local"`
	Identity struct {
		Mode            string `yaml:"mode"`
		AutoLinkEnabled *bool  `yaml:"auto_link_enabled"`
	} `yaml:"identity"`
	Kratos struct {
		PublicURL          string `yaml:"public_url"`
		CallbackURL        string `yaml:"callback_url"`
		DisplayName        string `yaml:"display_name"`
		SessionCookieNames string `yaml:"session_cookie_names"`
		AdminURL           string `yaml:"admin_url"`
		AdminToken         string `yaml:"admin_token"`
		SchemaID           string `yaml:"schema_id"`
		FeishuProviderID   string `yaml:"feishu_provider_id"`
	} `yaml:"kratos"`
	Feishu struct {
		AppID              string `yaml:"app_id"`
		AppSecret          string `yaml:"app_secret"`
		CallbackURL        string `yaml:"callback_url"`
		BindingCallbackURL string `yaml:"binding_callback_url"`
		StateSecret        string `yaml:"state_secret"`
		AllowedTenantKey   string `yaml:"allowed_tenant_key"`
		AutoProvision      *bool  `yaml:"auto_provision_enabled"`
		DirectorySync      *bool  `yaml:"directory_sync_enabled"`
	} `yaml:"feishu"`
	OIDC struct {
		IntrospectionURL string `yaml:"introspection_url"`
		ClientID         string `yaml:"client_id"`
		ClientSecret     string `yaml:"client_secret"`
		Issuer           string `yaml:"issuer"`
		Audience         string `yaml:"audience"`
		Provider         string `yaml:"provider"`
	} `yaml:"oidc"`
	Hydra struct {
		AdminURL          string `yaml:"admin_url"`
		AdminToken        string `yaml:"admin_token"`
		RememberFor       string `yaml:"remember_for"`
		ClientSyncEnabled *bool  `yaml:"client_sync_enabled"`
	} `yaml:"hydra"`
}

type yamlAuthzConfig struct {
	Mode      string `yaml:"mode"`
	RoleScope struct {
		Mode string `yaml:"mode"`
	} `yaml:"role_scope"`
	OpenFGA struct {
		APIURL                string `yaml:"api_url"`
		StoreID               string `yaml:"store_id"`
		AuthorizationModelID  string `yaml:"authorization_model_id"`
		Token                 string `yaml:"token"`
		Timeout               string `yaml:"timeout"`
		IgnoreDuplicateWrites *bool  `yaml:"ignore_duplicate_writes"`
		EnforceReady          *bool  `yaml:"enforce_ready"`
		EnforceObjectTypes    string `yaml:"enforce_object_types"`
		FailOpenDev           *bool  `yaml:"fail_open_dev"`
	} `yaml:"openfga"`
	RelationBackfill struct {
		Mode string `yaml:"mode"`
	} `yaml:"relation_backfill"`
}

type yamlDiscussionConfig struct {
	AgentID string               `yaml:"agent_id"`
	Agent   yamlAgentRuntimeSpec `yaml:"agent"`
}

type yamlAgentRuntimeSpec struct {
	RuntimeKind      string `yaml:"runtime_kind"`
	RuntimeConfigRef string `yaml:"runtime_config_ref"`
	RuntimeConfig    any    `yaml:"runtime_config"`
	SystemPrompt     string `yaml:"system_prompt"`
	Model            string `yaml:"model"`
	WorkingDirectory string `yaml:"working_directory"`
	Timeout          string `yaml:"timeout"`
}

type yamlKnowledgeConfig struct {
	Root               string `yaml:"root"`
	VectorBackend      string `yaml:"vector_backend"`
	DefaultChunkPolicy struct {
		MaxRunes      *int  `yaml:"max_runes"`
		OverlapRunes  *int  `yaml:"overlap_runes"`
		MinRunes      *int  `yaml:"min_runes"`
		PreserveHeads *bool `yaml:"preserve_heads"`
	} `yaml:"default_chunk_policy"`
	Embedding struct {
		DefaultProfileRef string `yaml:"default_profile_ref"`
		BatchSize         *int   `yaml:"batch_size"`
		RequestTimeout    string `yaml:"request_timeout"`
	} `yaml:"embedding"`
	Ingest struct {
		MaxDocumentBytes      *int     `yaml:"max_document_bytes"`
		SupportedContentTypes []string `yaml:"supported_content_types"`
	} `yaml:"ingest"`
}

type yamlOntologyConfig struct {
	MetricRecordingSweeper struct {
		Lookback string `yaml:"lookback"`
		Limit    *int   `yaml:"limit"`
	} `yaml:"metric_recording_sweeper"`
	PolicyAlertAuditSweeper struct {
		Lookback     string `yaml:"lookback"`
		TrendLimit   *int   `yaml:"trend_limit"`
		ProfileLimit *int   `yaml:"profile_limit"`
		MinFailures  *int   `yaml:"min_failures"`
	} `yaml:"policy_alert_audit_sweeper"`
	AlertNotificationSweeper struct {
		Limit          *int   `yaml:"limit"`
		RepeatWindow   string `yaml:"repeat_window"`
		RecoveryWindow string `yaml:"recovery_window"`
		Log            struct {
			Enabled *bool  `yaml:"enabled"`
			Channel string `yaml:"channel"`
		} `yaml:"log"`
		Webhook struct {
			Enabled               *bool             `yaml:"enabled"`
			Endpoint              string            `yaml:"endpoint"`
			Channel               string            `yaml:"channel"`
			Headers               map[string]string `yaml:"headers"`
			AllowPrivateEndpoints *bool             `yaml:"allow_private_endpoints"`
		} `yaml:"webhook"`
	} `yaml:"alert_notification_sweeper"`
	AlertNotificationRetrySweeper struct {
		Enabled     *bool  `yaml:"enabled"`
		Limit       *int   `yaml:"limit"`
		MaxAttempts *int   `yaml:"max_attempts"`
		Backoff     string `yaml:"backoff"`
	} `yaml:"alert_notification_retry_sweeper"`
	ArtifactHealthSweeper struct {
		Limit *int `yaml:"limit"`
		S3    struct {
			Enabled             *bool    `yaml:"enabled"`
			Region              string   `yaml:"region"`
			EndpointURL         string   `yaml:"endpoint_url"`
			ExpectedBucketOwner string   `yaml:"expected_bucket_owner"`
			AllowedPrefixes     []string `yaml:"allowed_prefixes"`
		} `yaml:"s3"`
	} `yaml:"artifact_health_sweeper"`
}

func loadYAMLFile(path string) (yamlConfig, map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return yamlConfig{}, nil, fmt.Errorf("read yaml config: %w", err)
	}
	var parsed yamlConfig
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&parsed); err != nil {
		return yamlConfig{}, nil, fmt.Errorf("parse yaml config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return yamlConfig{}, nil, fmt.Errorf("parse yaml config: multiple YAML documents are not supported")
		}
		return yamlConfig{}, nil, fmt.Errorf("parse yaml config: %w", err)
	}
	var rawMap map[string]any
	if err := yaml.Unmarshal(raw, &rawMap); err != nil {
		return yamlConfig{}, nil, fmt.Errorf("parse raw yaml config: %w", err)
	}
	if rawMap == nil {
		rawMap = map[string]any{}
	}
	configDir := filepath.Dir(path)
	if parsed.Paths.BaseDir == nil {
		baseDir := configDir
		parsed.Paths.BaseDir = &baseDir
	} else if strings.TrimSpace(*parsed.Paths.BaseDir) == "" {
		blank := ""
		parsed.Paths.BaseDir = &blank
	} else if !filepath.IsAbs(*parsed.Paths.BaseDir) {
		baseDir := filepath.Join(configDir, *parsed.Paths.BaseDir)
		parsed.Paths.BaseDir = &baseDir
	}
	return parsed, rawMap, nil
}

func applyYAML(cfg *Config, parsed yamlConfig, raw map[string]any) error {
	if parsed.Profile != "" && len(parsed.Extensions) > 0 {
		unknown := make([]string, 0, len(parsed.Extensions))
		for key := range parsed.Extensions {
			unknown = append(unknown, key)
		}
		slices.Sort(unknown)
		return fmt.Errorf("unknown top-level configuration section(s): %s", strings.Join(unknown, ", "))
	}
	if parsed.Profile != "" {
		if err := rejectYAMLSecrets(parsed); err != nil {
			return err
		}
	}
	if parsed.SchemaVersion != 0 {
		cfg.SchemaVersion = parsed.SchemaVersion
	}
	if parsed.Profile != "" {
		cfg.Profile = strings.TrimSpace(parsed.Profile)
	}
	if parsed.Environment != "" {
		cfg.Environment = parsed.Environment
	}
	if parsed.Server.Addr != "" {
		cfg.Server.Addr = strings.TrimSpace(parsed.Server.Addr)
	}
	if parsed.Server.TrustedProxyCIDRs != nil {
		cfg.Server.TrustedProxyCIDRs = append([]string(nil), parsed.Server.TrustedProxyCIDRs...)
	}
	if parsed.Server.TrustedViewerHeadersEnabled != nil {
		cfg.Server.TrustedViewerHeadersEnabled = *parsed.Server.TrustedViewerHeadersEnabled
	}
	if parsed.DeviceDelivery.PublicOrigin != nil {
		cfg.DeviceDelivery.PublicOrigin = strings.TrimSpace(*parsed.DeviceDelivery.PublicOrigin)
	}
	if parsed.DeviceDelivery.DefaultTenantID != nil {
		cfg.DeviceDelivery.DefaultTenantID = strings.TrimSpace(*parsed.DeviceDelivery.DefaultTenantID)
	}
	if parsed.DeviceDelivery.MaxArtifactBytes != nil {
		cfg.DeviceDelivery.MaxArtifactBytes = *parsed.DeviceDelivery.MaxArtifactBytes
	}
	if parsed.Paths.BaseDir != nil {
		cfg.BaseDir = *parsed.Paths.BaseDir
	}
	if parsed.Database.DefaultURL != "" {
		cfg.DatabaseURL = parsed.Database.DefaultURL
		cfg.DatabaseURLs = roleDatabaseURLs(parsed.Database.DefaultURL)
	}
	if len(parsed.Database.URLs) > 0 {
		cfg.DatabaseURLs = map[string]string{}
	}
	for name, value := range parsed.Database.URLs {
		cfg.DatabaseURLs[name] = value
	}
	if parsed.Database.PasswordEnv != "" {
		cfg.DatabasePasswordEnv = strings.TrimSpace(parsed.Database.PasswordEnv)
	}
	if parsed.Database.AllowEmbeddedInProduction != nil {
		cfg.AllowEmbeddedProductionDatabase = *parsed.Database.AllowEmbeddedInProduction
	}
	if parsed.Runtime.ExecEnv.Root != "" {
		cfg.Runtime.ExecEnvRoot = strings.TrimSpace(parsed.Runtime.ExecEnv.Root)
	}
	if parsed.Runtime.Managed.Enabled != nil {
		cfg.Runtime.ManagedEnabled = *parsed.Runtime.Managed.Enabled
	}
	if parsed.Runtime.MCP.URL != "" {
		cfg.Runtime.MCP.URL = strings.TrimSpace(parsed.Runtime.MCP.URL)
	}
	if parsed.Runtime.MCP.ServerName != "" {
		cfg.Runtime.MCP.ServerName = strings.TrimSpace(parsed.Runtime.MCP.ServerName)
	}
	if parsed.Runtime.MCP.Transport != "" {
		cfg.Runtime.MCP.Transport = strings.TrimSpace(parsed.Runtime.MCP.Transport)
	}
	if parsed.Development.AllowOwnerSelfApproval != nil {
		cfg.Development.AllowOwnerSelfApproval = *parsed.Development.AllowOwnerSelfApproval
	}
	if parsed.Development.WorkspaceRoot != "" {
		cfg.Development.WorkspaceRoot = strings.TrimSpace(parsed.Development.WorkspaceRoot)
	}
	if parsed.Development.WorkspaceLeaseTTL != "" {
		value, err := time.ParseDuration(parsed.Development.WorkspaceLeaseTTL)
		if err != nil || value <= 0 {
			return fmt.Errorf("development.workspace_lease_ttl must be a positive duration")
		}
		cfg.Development.WorkspaceLeaseTTL = value
	}
	if parsed.Development.WorkspaceRetention != "" {
		value, err := time.ParseDuration(parsed.Development.WorkspaceRetention)
		if err != nil || value <= 0 {
			return fmt.Errorf("development.workspace_retention must be a positive duration")
		}
		cfg.Development.WorkspaceRetention = value
	}
	if parsed.Development.GitCommitterName != "" {
		cfg.Development.GitCommitterName = strings.TrimSpace(parsed.Development.GitCommitterName)
	}
	if parsed.Development.GitCommitterEmail != "" {
		cfg.Development.GitCommitterEmail = strings.TrimSpace(parsed.Development.GitCommitterEmail)
	}
	if parsed.Development.GitSigningKeyRef != "" {
		cfg.Development.GitSigningKeyRef = strings.TrimSpace(parsed.Development.GitSigningKeyRef)
	}
	if parsed.Development.GitSigningFormat != "" {
		cfg.Development.GitSigningFormat = strings.TrimSpace(parsed.Development.GitSigningFormat)
	}
	if parsed.Development.RequireSignedCommits != nil {
		cfg.Development.RequireSignedCommits = *parsed.Development.RequireSignedCommits
	}
	if parsed.Development.MaxChangedFileBytes != nil {
		cfg.Development.MaxChangedFileBytes = *parsed.Development.MaxChangedFileBytes
	}
	if parsed.Integrations.Legal.MCPURL != "" {
		cfg.Integrations.LegalMCPURL = strings.TrimSpace(parsed.Integrations.Legal.MCPURL)
	}
	if parsed.Integrations.People.Enabled != nil {
		cfg.Integrations.People.Enabled = *parsed.Integrations.People.Enabled
	}
	if parsed.Integrations.People.BaseURL != "" {
		cfg.Integrations.People.BaseURL = strings.TrimRight(strings.TrimSpace(parsed.Integrations.People.BaseURL), "/")
	}
	if parsed.Integrations.People.TokenRef != "" {
		cfg.Integrations.People.TokenRef = strings.TrimSpace(parsed.Integrations.People.TokenRef)
	}
	if parsed.Integrations.People.PeopleTenantID != "" {
		cfg.Integrations.People.PeopleTenantID = strings.TrimSpace(parsed.Integrations.People.PeopleTenantID)
	}
	if parsed.Integrations.People.OrreryTenantID != "" {
		cfg.Integrations.People.OrreryTenantID = strings.TrimSpace(parsed.Integrations.People.OrreryTenantID)
	}
	if parsed.Integrations.People.ReconcileInterval != "" {
		value, err := time.ParseDuration(parsed.Integrations.People.ReconcileInterval)
		if err != nil || value <= 0 {
			return fmt.Errorf("integrations.people.reconcile_interval must be a positive duration")
		}
		cfg.Integrations.People.ReconcileInterval = value
	}
	if parsed.Integrations.People.PlannerCron != "" {
		cfg.Integrations.People.PlannerCron = strings.TrimSpace(parsed.Integrations.People.PlannerCron)
	}
	if parsed.Integrations.People.PlannerTimezone != "" {
		cfg.Integrations.People.PlannerTimezone = strings.TrimSpace(parsed.Integrations.People.PlannerTimezone)
	}
	if parsed.Integrations.People.AgentBindings != nil {
		cfg.Integrations.People.AgentBindings = make(map[string]string, len(parsed.Integrations.People.AgentBindings))
		for subjectID, agentProfileID := range parsed.Integrations.People.AgentBindings {
			cfg.Integrations.People.AgentBindings[strings.TrimSpace(subjectID)] = strings.TrimSpace(agentProfileID)
		}
	}
	if parsed.Integrations.People.HumanRoleMappings != nil {
		cfg.Integrations.People.HumanRoleMappings = make(map[string]string, len(parsed.Integrations.People.HumanRoleMappings))
		for responsibilityCode, roleID := range parsed.Integrations.People.HumanRoleMappings {
			cfg.Integrations.People.HumanRoleMappings[strings.TrimSpace(responsibilityCode)] = strings.TrimSpace(roleID)
		}
	}
	if parsed.Bootstrap.PageIndex.Enabled != nil {
		cfg.Bootstrap.PageIndexEnabled = *parsed.Bootstrap.PageIndex.Enabled
	}
	cfg.Bootstrap.ImageGeneration = ImageGenerationBootstrapConfig{
		Enabled:   parsed.Bootstrap.ImageGeneration.Enabled,
		BaseURL:   strings.TrimSpace(parsed.Bootstrap.ImageGeneration.BaseURL),
		Model:     strings.TrimSpace(parsed.Bootstrap.ImageGeneration.Model),
		APIKeyRef: strings.TrimSpace(parsed.Bootstrap.ImageGeneration.APIKeyRef),
	}
	if parsed.Bootstrap.PageIndex.APIURL != "" {
		cfg.Bootstrap.PageIndexAPIURL = strings.TrimSpace(parsed.Bootstrap.PageIndex.APIURL)
	}
	if parsed.Bootstrap.PageIndex.APIKeyRef != "" {
		cfg.Bootstrap.PageIndexAPIKeyRef = strings.TrimSpace(parsed.Bootstrap.PageIndex.APIKeyRef)
	}
	if parsed.Bootstrap.Gateway.LegacyConfig != "" {
		cfg.Bootstrap.GatewayConfigPath = strings.TrimSpace(parsed.Bootstrap.Gateway.LegacyConfig)
	}
	if parsed.FileStore.Root != "" {
		cfg.FileStoreRoot = parsed.FileStore.Root
		cfg.FileStore.Root = parsed.FileStore.Root
	}
	if parsed.FileStore.Backend != "" {
		cfg.FileStore.Backend = parsed.FileStore.Backend
	}
	if parsed.FileStore.BaseURL != "" {
		cfg.FileStore.BaseURL = parsed.FileStore.BaseURL
	}
	if parsed.FileStore.S3.Bucket != "" {
		cfg.FileStore.S3.Bucket = parsed.FileStore.S3.Bucket
	}
	if parsed.FileStore.S3.Region != "" {
		cfg.FileStore.S3.Region = parsed.FileStore.S3.Region
	}
	if parsed.FileStore.S3.EndpointURL != "" {
		cfg.FileStore.S3.EndpointURL = parsed.FileStore.S3.EndpointURL
	}
	if parsed.FileStore.S3.CDNDomain != "" {
		cfg.FileStore.S3.CDNDomain = parsed.FileStore.S3.CDNDomain
	}
	if parsed.Logging.Level != "" {
		cfg.Logging.Level = parsed.Logging.Level
	}
	if parsed.Logging.Console != nil {
		cfg.Logging.Console = *parsed.Logging.Console
	}
	if parsed.Logging.File != "" {
		cfg.Logging.File = parsed.Logging.File
	}
	if parsed.Health.ReadinessCacheTTL != "" {
		duration, err := time.ParseDuration(parsed.Health.ReadinessCacheTTL)
		if err != nil {
			return fmt.Errorf("parse health.readiness_cache_ttl: %w", err)
		}
		cfg.ReadinessCacheTTL = duration
	}
	if parsed.Queue.ClaimTTL != "" {
		duration, err := time.ParseDuration(parsed.Queue.ClaimTTL)
		if err != nil {
			return fmt.Errorf("parse queue.claim_ttl: %w", err)
		}
		cfg.Queue.ClaimTTL = duration
	}
	if parsed.Queue.MaxAttempts != nil {
		cfg.Queue.MaxAttempts = *parsed.Queue.MaxAttempts
	}
	if parsed.Queue.SweepBatchSize != nil {
		cfg.Queue.SweepBatchSize = *parsed.Queue.SweepBatchSize
	}
	if parsed.Queue.WorkerConcurrency != nil {
		cfg.Queue.WorkerConcurrency = *parsed.Queue.WorkerConcurrency
	}
	if parsed.Lock.DefaultTTL != "" {
		duration, err := time.ParseDuration(parsed.Lock.DefaultTTL)
		if err != nil {
			return fmt.Errorf("parse lock.default_ttl: %w", err)
		}
		cfg.Lock.DefaultTTL = duration
	}
	if parsed.Liveness.HeartbeatTTL != "" {
		duration, err := time.ParseDuration(parsed.Liveness.HeartbeatTTL)
		if err != nil {
			return fmt.Errorf("parse liveness.heartbeat_ttl: %w", err)
		}
		cfg.Liveness.HeartbeatTTL = duration
	}
	if parsed.Metrics.BufferSize != nil {
		cfg.Metrics.BufferSize = *parsed.Metrics.BufferSize
	}
	if parsed.Metrics.Addr != "" {
		cfg.Metrics.Addr = parsed.Metrics.Addr
	}
	if parsed.Metrics.WarnInterval != "" {
		duration, err := time.ParseDuration(parsed.Metrics.WarnInterval)
		if err != nil {
			return fmt.Errorf("parse metrics.warn_interval: %w", err)
		}
		cfg.Metrics.WarnInterval = duration
	}
	if parsed.Secret.Root != "" {
		cfg.Secret.Root = parsed.Secret.Root
	}
	if parsed.Sweeper.Timeout != "" {
		duration, err := time.ParseDuration(parsed.Sweeper.Timeout)
		if err != nil {
			return fmt.Errorf("parse sweeper.timeout: %w", err)
		}
		cfg.Sweeper.Timeout = duration
	}
	if parsed.Sweeper.BatchSize != nil {
		cfg.Sweeper.BatchSize = *parsed.Sweeper.BatchSize
	}
	if parsed.Tracing.BufferSize != nil {
		cfg.Tracing.BufferSize = *parsed.Tracing.BufferSize
	}
	if strings.TrimSpace(parsed.Resource.MCPRegistryURL) != "" {
		setRawValue(&raw, "resource", "mcp_registry_url", strings.TrimSpace(parsed.Resource.MCPRegistryURL))
	}
	if parsed.Maintenance.RepairTimeout != "" {
		timeout, err := time.ParseDuration(parsed.Maintenance.RepairTimeout)
		if err != nil || timeout <= 0 || timeout > 90*time.Minute {
			return fmt.Errorf("maintenance.repair_timeout must be a duration greater than zero and at most 90m")
		}
	}
	cfg.raw = raw
	return nil
}

func rejectYAMLSecrets(parsed yamlConfig) error {
	secretFields := []struct {
		path  string
		value string
	}{
		{path: "auth.local.mfa_totp_secret", value: parsed.Auth.Local.MFATOTPSecret},
		{path: "auth.feishu.app_secret", value: parsed.Auth.Feishu.AppSecret},
		{path: "auth.feishu.state_secret", value: parsed.Auth.Feishu.StateSecret},
		{path: "auth.oidc.client_secret", value: parsed.Auth.OIDC.ClientSecret},
		{path: "auth.hydra.admin_token", value: parsed.Auth.Hydra.AdminToken},
		{path: "authz.openfga.token", value: parsed.Authz.OpenFGA.Token},
	}
	for _, field := range secretFields {
		if strings.TrimSpace(field.value) != "" {
			return fmt.Errorf("%s must be supplied through env or a secret provider, not YAML", field.path)
		}
	}
	return nil
}

func LoadYAML(path string) (Config, error) {
	return loadYAML(path, processSecretValues())
}

// LoadYAMLWithSecrets supports callers that obtain secrets from a provider
// other than the process environment while preserving the same YAML schema.
func LoadYAMLWithSecrets(path string, secrets SecretValues) (Config, error) {
	return loadYAML(path, secrets)
}

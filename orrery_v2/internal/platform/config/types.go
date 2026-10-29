package config

import "time"

type Config struct {
	SchemaVersion                   int
	Profile                         string
	Environment                     string
	BaseDir                         string
	DatabaseURL                     string
	DatabaseURLs                    map[string]string
	DatabasePasswordEnv             string
	AllowEmbeddedProductionDatabase bool
	Server                          ServerConfig
	DeviceDelivery                  DeviceDeliveryConfig
	Runtime                         RuntimeConfig
	Development                     DevelopmentConfig
	Integrations                    IntegrationsConfig
	Bootstrap                       BootstrapConfig
	FileStoreRoot                   string
	FileStore                       FileStoreConfig
	ReadinessCacheTTL               time.Duration
	Logging                         LoggingConfig
	Queue                           QueueConfig
	Lock                            LockConfig
	Liveness                        LivenessConfig
	Metrics                         MetricsConfig
	Secret                          SecretConfig
	Sweeper                         SweeperConfig
	Tracing                         TracingConfig
	raw                             map[string]any
}

type DevelopmentConfig struct {
	AllowOwnerSelfApproval bool
	WorkspaceRoot          string
	WorkspaceLeaseTTL      time.Duration
	WorkspaceRetention     time.Duration
	GitCommitterName       string
	GitCommitterEmail      string
	GitSigningKeyRef       string
	GitSigningFormat       string
	RequireSignedCommits   bool
	MaxChangedFileBytes    int64
}

type ServerConfig struct {
	Addr                        string
	TrustedProxyCIDRs           []string
	TrustedViewerHeadersEnabled bool
}

type DeviceDeliveryConfig struct {
	PublicOrigin     string
	DefaultTenantID  string
	MaxArtifactBytes int64
}

type RuntimeConfig struct {
	ExecEnvRoot    string
	ManagedEnabled bool
	MCP            RuntimeMCPConfig
}

type RuntimeMCPConfig struct {
	URL        string
	ServerName string
	Transport  string
}

type IntegrationsConfig struct {
	LegalMCPURL string
	People      PeopleIntegrationConfig
}

type PeopleIntegrationConfig struct {
	Enabled           bool
	BaseURL           string
	TokenRef          string
	PeopleTenantID    string
	OrreryTenantID    string
	ReconcileInterval time.Duration
	PlannerCron       string
	PlannerTimezone   string
	AgentBindings     map[string]string
	HumanRoleMappings map[string]string
}

type BootstrapConfig struct {
	ImageGeneration    ImageGenerationBootstrapConfig
	PageIndexEnabled   bool
	PageIndexAPIURL    string
	PageIndexAPIKeyRef string
	GatewayConfigPath  string
}

type ImageGenerationBootstrapConfig struct {
	Enabled   bool
	BaseURL   string
	Model     string
	APIKeyRef string
}

type LoggingConfig struct {
	Level   string
	Console bool
	File    string
}

type FileStoreConfig struct {
	Backend string
	Root    string
	BaseURL string
	S3      S3Config
}

type S3Config struct {
	Bucket      string
	Region      string
	EndpointURL string
	CDNDomain   string
}

type QueueConfig struct {
	ClaimTTL          time.Duration
	MaxAttempts       int
	SweepBatchSize    int
	WorkerConcurrency int
}

type LockConfig struct {
	DefaultTTL time.Duration
}

type LivenessConfig struct {
	HeartbeatTTL time.Duration
}

type MetricsConfig struct {
	Addr         string
	BufferSize   int
	WarnInterval time.Duration
}

type SecretConfig struct {
	Root string
}

type SweeperConfig struct {
	Timeout   time.Duration
	BatchSize int
}

type TracingConfig struct {
	BufferSize int
}

package config

type Config struct {
	Gateway  GatewayConfig  `json:"gateway" yaml:"gateway"`
	Adapters AdaptersConfig `json:"adapters" yaml:"adapters"`
}

type GatewayConfig struct {
	Port                      int    `json:"port" yaml:"port"`
	BindAddress               string `json:"bind_address" yaml:"bindAddress"`
	IdleSessionTimeoutMinutes int    `json:"idle_session_timeout_minutes" yaml:"idleSessionTimeoutMinutes"`
	StaticAssetsPath          string `json:"static_assets_path" yaml:"staticAssetsPath"`
	MaxPerSessionMCPInstances int    `json:"max_per_session_mcp_instances" yaml:"maxPerSessionMcpInstances"`
}

type FeishuAdapterConfig struct {
	AccountID           string                         `json:"-" yaml:"-"`
	Enabled             bool                           `json:"enabled" yaml:"enabled"`
	AppID               string                         `json:"app_id" yaml:"appId"`
	AppSecret           string                         `json:"app_secret" yaml:"appSecret"`
	EncryptKey          string                         `json:"encrypt_key" yaml:"encryptKey"`
	VerifyToken         string                         `json:"verify_token" yaml:"verifyToken"`
	DefaultSessionLabel string                         `json:"default_session_label" yaml:"defaultSessionLabel"`
	ConnectionMode      string                         `json:"connection_mode" yaml:"connectionMode"`
	DomainName          string                         `json:"domain_name" yaml:"domainName"`
	Accounts            map[string]FeishuAccountConfig `json:"accounts,omitempty" yaml:"accounts,omitempty"`
}

type FeishuAccountConfig struct {
	Enabled             bool   `json:"enabled" yaml:"enabled"`
	AppID               string `json:"app_id" yaml:"appId"`
	AppSecret           string `json:"app_secret" yaml:"appSecret"`
	EncryptKey          string `json:"encrypt_key" yaml:"encryptKey"`
	VerifyToken         string `json:"verify_token" yaml:"verifyToken"`
	DefaultSessionLabel string `json:"default_session_label" yaml:"defaultSessionLabel"`
	ConnectionMode      string `json:"connection_mode" yaml:"connectionMode"`
	DomainName          string `json:"domain_name" yaml:"domainName"`
}

type PlatformAdapterConfig struct {
	Enabled    bool           `json:"enabled" yaml:"enabled"`
	Token      string         `json:"token" yaml:"token"`
	APIKey     string         `json:"api_key" yaml:"apiKey"`
	WebhookURL string         `json:"webhook_url" yaml:"webhookUrl"`
	Extra      map[string]any `json:"extra" yaml:"extra"`
}

type WeixinAdapterConfig struct {
	Enabled          bool           `json:"enabled" yaml:"enabled"`
	Extra            map[string]any `json:"extra" yaml:"extra"`
	LegacyToken      string         `json:"-" yaml:"token,omitempty"`
	LegacyAPIKey     string         `json:"-" yaml:"apiKey,omitempty"`
	LegacyWebhookURL string         `json:"-" yaml:"webhookUrl,omitempty"`
}

type AdaptersConfig struct {
	Feishu   FeishuAdapterConfig   `json:"feishu" yaml:"feishu"`
	Weixin   WeixinAdapterConfig   `json:"weixin" yaml:"weixin"`
	DingTalk PlatformAdapterConfig `json:"dingtalk" yaml:"dingtalk"`
	Email    PlatformAdapterConfig `json:"email" yaml:"email"`
}

type Record struct {
	Path    string `json:"path"`
	RawYAML string `json:"raw_yaml"`
	Config  Config `json:"config"`
}

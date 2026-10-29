package feishu

import (
	"strings"

	gwconfig "orrery_v2/internal/gateway/config"
)

const (
	ModeStream   = "stream"
	ModeWebhook  = "webhook"
	ModeOutbound = "outbound"
)

type Options struct {
	AccountAlias         string
	Enabled              bool
	AppID                string
	AppSecret            string
	EncryptKey           string
	VerifyToken          string
	DefaultSessionLabel  string
	ConnectionMode       string
	DomainName           string
	MeetingEventsEnabled bool
}

func (o Options) AccountID() string {
	if value := strings.TrimSpace(o.AccountAlias); value != "" {
		return value
	}
	if value := strings.TrimSpace(o.AppID); value != "" {
		return "feishu:" + value
	}
	return ""
}

func OptionsFromAccountConfig(accountID string, config gwconfig.FeishuAccountConfig) Options {
	return Options{
		AccountAlias:        strings.TrimSpace(accountID),
		Enabled:             config.Enabled,
		AppID:               config.AppID,
		AppSecret:           config.AppSecret,
		EncryptKey:          config.EncryptKey,
		VerifyToken:         config.VerifyToken,
		DefaultSessionLabel: firstNonEmpty(config.DefaultSessionLabel, "general"),
		ConnectionMode:      firstNonEmpty(config.ConnectionMode, ModeStream),
		DomainName:          firstNonEmpty(config.DomainName, "feishu"),
	}
}

func OptionsFromConfig(config gwconfig.FeishuAdapterConfig) Options {
	opts := Options{
		AccountAlias:        config.AccountID,
		Enabled:             config.Enabled,
		AppID:               config.AppID,
		AppSecret:           config.AppSecret,
		EncryptKey:          config.EncryptKey,
		VerifyToken:         config.VerifyToken,
		DefaultSessionLabel: firstNonEmpty(config.DefaultSessionLabel, "general"),
		ConnectionMode:      firstNonEmpty(config.ConnectionMode, ModeStream),
		DomainName:          firstNonEmpty(config.DomainName, "feishu"),
	}
	return opts
}

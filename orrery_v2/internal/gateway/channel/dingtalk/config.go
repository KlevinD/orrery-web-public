package dingtalk

import (
	"strings"

	gwconfig "orrery_v2/internal/gateway/config"
)

type Options struct {
	ClientID     string
	ClientSecret string
	RobotCode    string
	AgentID      string
	APIBaseURL   string
}

func OptionsFromConfig(config gwconfig.PlatformAdapterConfig) Options {
	opts := Options{ClientID: strings.TrimSpace(config.APIKey), ClientSecret: strings.TrimSpace(config.Token), APIBaseURL: "https://api.dingtalk.com"}
	if config.Extra != nil {
		if text, ok := config.Extra["clientId"].(string); ok && opts.ClientID == "" {
			opts.ClientID = text
		}
		if text, ok := config.Extra["clientSecret"].(string); ok && opts.ClientSecret == "" {
			opts.ClientSecret = text
		}
		if text, ok := config.Extra["robotCode"].(string); ok {
			opts.RobotCode = text
		}
		if text, ok := config.Extra["agentId"].(string); ok {
			opts.AgentID = text
		}
		if text, ok := config.Extra["apiBaseUrl"].(string); ok && strings.TrimSpace(text) != "" {
			opts.APIBaseURL = text
		}
	}
	opts.ClientID = strings.TrimSpace(opts.ClientID)
	opts.ClientSecret = strings.TrimSpace(opts.ClientSecret)
	opts.RobotCode = firstNonBlank(strings.TrimSpace(opts.RobotCode), opts.ClientID)
	opts.AgentID = strings.TrimSpace(opts.AgentID)
	opts.APIBaseURL = strings.TrimRight(strings.TrimSpace(opts.APIBaseURL), "/")
	return opts
}

func (o Options) AccountID() string {
	if value := strings.TrimSpace(o.ClientID); value != "" {
		return "dingtalk:" + value
	}
	return ""
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

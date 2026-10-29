package email

import (
	"strconv"
	"strings"

	gwconfig "orrery_v2/internal/gateway/config"
)

type Options struct {
	Address  string
	Username string
	Password string
	IMAPHost string
	SMTPHost string
	IMAPPort int
	SMTPPort int
	TLSMode  string
}

func OptionsFromConfig(config gwconfig.PlatformAdapterConfig) Options {
	opts := Options{
		Username: strings.TrimSpace(config.APIKey),
		Password: config.Token,
		IMAPPort: 993,
		SMTPPort: 587,
		TLSMode:  "starttls",
	}
	if config.Extra == nil {
		return opts
	}
	opts.Address = strings.TrimSpace(stringExtra(config.Extra, "from", stringExtra(config.Extra, "address", "")))
	if opts.Username == "" {
		opts.Username = strings.TrimSpace(stringExtra(config.Extra, "username", ""))
	}
	if opts.Password == "" {
		opts.Password = stringExtra(config.Extra, "password", "")
	}
	opts.IMAPHost = stringExtra(config.Extra, "imapHost", "")
	opts.SMTPHost = stringExtra(config.Extra, "smtpHost", "")
	opts.IMAPPort = intExtra(config.Extra, "imapPort", opts.IMAPPort)
	opts.SMTPPort = intExtra(config.Extra, "smtpPort", opts.SMTPPort)
	opts.TLSMode = strings.ToLower(strings.TrimSpace(stringExtra(config.Extra, "smtpTLS", opts.TLSMode)))
	if _, configured := config.Extra["smtpTLS"]; !configured && opts.SMTPPort == 465 {
		opts.TLSMode = "implicit"
	}
	return opts
}

func stringExtra(extra map[string]any, key, fallback string) string {
	if text, ok := extra[key].(string); ok {
		return text
	}
	return fallback
}

func intExtra(extra map[string]any, key string, fallback int) int {
	value, ok := extra[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

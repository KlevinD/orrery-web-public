package weixin

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"orrery_v2/internal/gateway/channel"
	gwconfig "orrery_v2/internal/gateway/config"
)

const (
	defaultIlinkLoginBaseURL     = "https://ilinkai.weixin.qq.com"
	defaultIlinkBaseURL          = "https://ilinkai.weixin.qq.com/ilink/bot"
	defaultIlinkCDNBaseURL       = "https://novac2c.cdn.weixin.qq.com/c2c"
	defaultIlinkGetUpdatesPath   = "getupdates"
	defaultIlinkSendMessagePath  = "sendmessage"
	defaultIlinkChannelVersion   = "2.4.6"
	defaultIlinkAppID            = "bot"
	defaultIlinkAppClientVersion = "132102"
	defaultIlinkBotType          = "3"
	defaultPollIntervalSeconds   = 2
	defaultRequestTimeoutSeconds = 60
	defaultSeenMessageLimit      = 5000
	defaultInboundMaxAttempts    = 3
	defaultDeadLetterLimit       = 500
	maxConfiguredAccounts        = 50
)

type Options struct {
	BotToken              string
	AccountID             string
	AccountUserID         string
	IlinkLoginBaseURL     string
	IlinkBaseURL          string
	IlinkCDNBaseURL       string
	GetUpdatesPath        string
	SendMessagePath       string
	XWechatUIN            string
	SKRouteTag            string
	IlinkChannelVersion   string
	IlinkAppID            string
	IlinkAppClientVersion string
	IlinkBotType          string
	GetUpdatesBuf         string
	StatePath             string
	ContextTokens         int
	ReplyChunkSize        int
	PollIntervalSeconds   int
	RequestTimeoutSeconds int
	SeenMessageLimit      int
	InboundMaxAttempts    int
	Accounts              []AccountOptions
	InboundEvents         channel.InboundEventStore
}

type AccountOptions struct {
	TenantID        string
	UserID          string
	BotID           string
	BotToken        string
	IlinkBaseURL    string
	IlinkCDNBaseURL string
	StatePath       string
	XWechatUIN      string
}

func OptionsFromConfig(config gwconfig.WeixinAdapterConfig) Options {
	opts := Options{
		IlinkLoginBaseURL:     defaultIlinkLoginBaseURL,
		IlinkBaseURL:          defaultIlinkBaseURL,
		IlinkCDNBaseURL:       defaultIlinkCDNBaseURL,
		GetUpdatesPath:        defaultIlinkGetUpdatesPath,
		SendMessagePath:       defaultIlinkSendMessagePath,
		IlinkChannelVersion:   defaultIlinkChannelVersion,
		IlinkAppID:            defaultIlinkAppID,
		IlinkAppClientVersion: defaultIlinkAppClientVersion,
		IlinkBotType:          defaultIlinkBotType,
		ContextTokens:         8192,
		ReplyChunkSize:        1800,
		PollIntervalSeconds:   defaultPollIntervalSeconds,
		RequestTimeoutSeconds: defaultRequestTimeoutSeconds,
		SeenMessageLimit:      defaultSeenMessageLimit,
		InboundMaxAttempts:    defaultInboundMaxAttempts,
	}
	if config.Extra == nil {
		return opts.WithDefaults()
	}
	opts.IlinkLoginBaseURL = stringExtra(config.Extra, "ilinkLoginBaseUrl", opts.IlinkLoginBaseURL)
	opts.IlinkLoginBaseURL = stringExtra(config.Extra, "loginBaseUrl", opts.IlinkLoginBaseURL)
	opts.IlinkBaseURL = stringExtra(config.Extra, "ilinkBaseUrl", opts.IlinkBaseURL)
	opts.IlinkBaseURL = stringExtra(config.Extra, "baseUrl", opts.IlinkBaseURL)
	opts.IlinkCDNBaseURL = stringExtra(config.Extra, "ilinkCdnBaseUrl", opts.IlinkCDNBaseURL)
	opts.IlinkCDNBaseURL = stringExtra(config.Extra, "cdnBaseUrl", opts.IlinkCDNBaseURL)
	opts.GetUpdatesPath = stringExtra(config.Extra, "getUpdatesPath", opts.GetUpdatesPath)
	opts.SendMessagePath = stringExtra(config.Extra, "sendMessagePath", opts.SendMessagePath)
	opts.XWechatUIN = stringExtra(config.Extra, "xWechatUin", opts.XWechatUIN)
	opts.XWechatUIN = stringExtra(config.Extra, "xWechatUIN", opts.XWechatUIN)
	opts.SKRouteTag = stringExtra(config.Extra, "skRouteTag", opts.SKRouteTag)
	opts.IlinkChannelVersion = stringExtra(config.Extra, "channelVersion", opts.IlinkChannelVersion)
	opts.IlinkChannelVersion = stringExtra(config.Extra, "ilinkChannelVersion", opts.IlinkChannelVersion)
	opts.IlinkAppID = stringExtra(config.Extra, "ilinkAppId", opts.IlinkAppID)
	opts.IlinkAppClientVersion = stringExtra(config.Extra, "ilinkAppClientVersion", opts.IlinkAppClientVersion)
	opts.IlinkBotType = stringExtra(config.Extra, "botType", opts.IlinkBotType)
	opts.IlinkBotType = stringExtra(config.Extra, "ilinkBotType", opts.IlinkBotType)
	opts.GetUpdatesBuf = stringExtra(config.Extra, "getUpdatesBuf", opts.GetUpdatesBuf)
	opts.StatePath = stringExtra(config.Extra, "statePath", opts.StatePath)
	opts.ContextTokens = intExtra(config.Extra, "contextTokens", opts.ContextTokens)
	opts.ReplyChunkSize = intExtra(config.Extra, "replyChunkSize", opts.ReplyChunkSize)
	opts.PollIntervalSeconds = intExtra(config.Extra, "pollIntervalSeconds", opts.PollIntervalSeconds)
	opts.RequestTimeoutSeconds = intExtra(config.Extra, "requestTimeoutSeconds", opts.RequestTimeoutSeconds)
	opts.SeenMessageLimit = intExtra(config.Extra, "seenMessageLimit", opts.SeenMessageLimit)
	opts.InboundMaxAttempts = intExtra(config.Extra, "inboundMaxAttempts", opts.InboundMaxAttempts)
	opts.Accounts = accountOptionsExtra(config.Extra, opts)
	return opts.WithDefaults()
}

func (o Options) WithDefaults() Options {
	if strings.TrimSpace(o.IlinkBaseURL) == "" {
		o.IlinkBaseURL = defaultIlinkBaseURL
	}
	if strings.TrimSpace(o.IlinkLoginBaseURL) == "" {
		o.IlinkLoginBaseURL = defaultIlinkLoginBaseURL
	}
	if strings.TrimSpace(o.IlinkCDNBaseURL) == "" {
		o.IlinkCDNBaseURL = defaultIlinkCDNBaseURL
	}
	if strings.TrimSpace(o.GetUpdatesPath) == "" {
		o.GetUpdatesPath = defaultIlinkGetUpdatesPath
	}
	if strings.TrimSpace(o.SendMessagePath) == "" {
		o.SendMessagePath = defaultIlinkSendMessagePath
	}
	if strings.TrimSpace(o.IlinkChannelVersion) == "" {
		o.IlinkChannelVersion = defaultIlinkChannelVersion
	}
	if strings.TrimSpace(o.IlinkAppID) == "" {
		o.IlinkAppID = defaultIlinkAppID
	}
	if strings.TrimSpace(o.IlinkAppClientVersion) == "" {
		o.IlinkAppClientVersion = defaultIlinkAppClientVersion
	}
	if strings.TrimSpace(o.IlinkBotType) == "" {
		o.IlinkBotType = defaultIlinkBotType
	}
	if o.ContextTokens <= 0 {
		o.ContextTokens = 8192
	}
	if o.ReplyChunkSize <= 0 {
		o.ReplyChunkSize = 1800
	}
	if o.PollIntervalSeconds <= 0 {
		o.PollIntervalSeconds = defaultPollIntervalSeconds
	} else if o.PollIntervalSeconds > 300 {
		o.PollIntervalSeconds = 300
	}
	if o.RequestTimeoutSeconds <= 0 {
		o.RequestTimeoutSeconds = defaultRequestTimeoutSeconds
	} else if o.RequestTimeoutSeconds > 120 {
		o.RequestTimeoutSeconds = 120
	}
	if o.SeenMessageLimit <= 0 {
		o.SeenMessageLimit = defaultSeenMessageLimit
	} else if o.SeenMessageLimit > 100000 {
		o.SeenMessageLimit = 100000
	}
	if o.InboundMaxAttempts <= 0 {
		o.InboundMaxAttempts = defaultInboundMaxAttempts
	} else if o.InboundMaxAttempts > 20 {
		o.InboundMaxAttempts = 20
	}
	return o
}

func accountOptionsExtra(extra map[string]any, base Options) []AccountOptions {
	if extra == nil {
		return nil
	}
	value, ok := extra["weixinAccounts"]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make([]AccountOptions, 0, len(typed))
		for key, raw := range typed {
			account := accountOptionFromRaw(raw, base)
			if tenantID, userID, scoped := gwconfig.ParseWeixinBindingKey(key); scoped {
				if account.TenantID == "" {
					account.TenantID = tenantID
				}
				if account.UserID == "" {
					account.UserID = userID
				}
			}
			if account.UserID == "" {
				account.UserID = strings.TrimSpace(key)
			}
			if account.BotToken == "" {
				continue
			}
			out = append(out, account)
		}
		sortAccountOptions(out)
		return out
	case []any:
		out := make([]AccountOptions, 0, len(typed))
		for _, raw := range typed {
			account := accountOptionFromRaw(raw, base)
			if account.BotToken == "" {
				continue
			}
			out = append(out, account)
		}
		sortAccountOptions(out)
		return out
	default:
		return nil
	}
}

// ValidateConfiguredAccounts distinguishes a legitimate not-yet-provisioned
// channel from malformed account data that must not be reported as connected.
func ValidateConfiguredAccounts(config gwconfig.WeixinAdapterConfig) (int, error) {
	if config.Extra == nil {
		return 0, nil
	}
	raw, exists := config.Extra["weixinAccounts"]
	if !exists || raw == nil {
		return 0, nil
	}
	base := OptionsFromConfig(gwconfig.WeixinAdapterConfig{Extra: withoutWeixinAccounts(config.Extra)})
	var accounts []AccountOptions
	switch typed := raw.(type) {
	case map[string]any:
		accounts = make([]AccountOptions, 0, len(typed))
		for key, value := range typed {
			account := accountOptionFromRaw(value, base)
			tenantID, userID, ok := gwconfig.ParseWeixinBindingKey(key)
			if !ok {
				return 0, fmt.Errorf("invalid Weixin account binding key %q", key)
			}
			if account.TenantID != "" && gwconfig.NormalizeTenantID(account.TenantID) != tenantID {
				return 0, fmt.Errorf("Weixin account %q tenantId does not match its binding key", key)
			}
			if account.UserID != "" && strings.TrimSpace(account.UserID) != userID {
				return 0, fmt.Errorf("Weixin account %q userId does not match its binding key", key)
			}
			account.TenantID = tenantID
			account.UserID = userID
			accounts = append(accounts, account)
		}
	case []any:
		return 0, fmt.Errorf("weixinAccounts list format cannot provide tenant-safe inbound routes; use a scoped account map")
	default:
		return 0, fmt.Errorf("weixinAccounts must be an account map or list")
	}
	seen := make(map[string]struct{}, len(accounts))
	if len(accounts) > maxConfiguredAccounts {
		return 0, fmt.Errorf("weixinAccounts exceeds the production limit of %d connected accounts", maxConfiguredAccounts)
	}
	for _, account := range accounts {
		if strings.TrimSpace(account.BotToken) == "" || strings.TrimSpace(account.BotID) == "" || strings.TrimSpace(account.UserID) == "" {
			return 0, fmt.Errorf("each Weixin account requires userId, botId, and token")
		}
		accountID := strings.TrimSpace(account.BotID)
		if _, duplicate := seen[accountID]; duplicate {
			return 0, fmt.Errorf("duplicate Weixin botId %q", accountID)
		}
		seen[accountID] = struct{}{}
	}
	return len(accounts), nil
}

func withoutWeixinAccounts(extra map[string]any) map[string]any {
	copyExtra := make(map[string]any, len(extra))
	for key, value := range extra {
		if key != "weixinAccounts" {
			copyExtra[key] = value
		}
	}
	return copyExtra
}

func sortAccountOptions(accounts []AccountOptions) {
	sort.SliceStable(accounts, func(i, j int) bool {
		left := strings.TrimSpace(accounts[i].TenantID) + "\x00" + strings.TrimSpace(accounts[i].UserID) + "\x00" + strings.TrimSpace(accounts[i].BotID)
		right := strings.TrimSpace(accounts[j].TenantID) + "\x00" + strings.TrimSpace(accounts[j].UserID) + "\x00" + strings.TrimSpace(accounts[j].BotID)
		return left < right
	})
}

func accountOptionFromRaw(raw any, base Options) AccountOptions {
	account := AccountOptions{IlinkBaseURL: base.IlinkBaseURL, IlinkCDNBaseURL: base.IlinkCDNBaseURL}
	fields, ok := raw.(map[string]any)
	if !ok {
		return account
	}
	account.TenantID = stringExtra(fields, "tenantId", account.TenantID)
	account.TenantID = stringExtra(fields, "tenant_id", account.TenantID)
	account.UserID = stringExtra(fields, "userId", account.UserID)
	account.UserID = stringExtra(fields, "user_id", account.UserID)
	account.UserID = stringExtra(fields, "weixinUserId", account.UserID)
	account.UserID = stringExtra(fields, "ilinkUserId", account.UserID)
	account.BotID = stringExtra(fields, "botId", account.BotID)
	account.BotID = stringExtra(fields, "bot_id", account.BotID)
	account.BotID = stringExtra(fields, "accountId", account.BotID)
	account.BotID = stringExtra(fields, "ilinkBotId", account.BotID)
	account.BotToken = stringExtra(fields, "token", account.BotToken)
	account.IlinkBaseURL = stringExtra(fields, "ilinkBaseUrl", account.IlinkBaseURL)
	account.IlinkBaseURL = stringExtra(fields, "baseUrl", account.IlinkBaseURL)
	account.IlinkCDNBaseURL = stringExtra(fields, "ilinkCdnBaseUrl", account.IlinkCDNBaseURL)
	account.IlinkCDNBaseURL = stringExtra(fields, "cdnBaseUrl", account.IlinkCDNBaseURL)
	account.StatePath = stringExtra(fields, "statePath", account.StatePath)
	account.XWechatUIN = stringExtra(fields, "xWechatUin", account.XWechatUIN)
	return account
}

func stringExtra(extra map[string]any, key, fallback string) string {
	value, ok := extra[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return typed
		}
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
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

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

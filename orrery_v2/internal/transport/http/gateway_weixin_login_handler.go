package httptransport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/gateway/channel/weixin"
	gwconfig "orrery_v2/internal/gateway/config"
)

const (
	gatewayWeixinLoginTTL             = 5 * time.Minute
	maxGatewayWeixinLoginSessions     = 128
	maxGatewayWeixinLoginRequestBytes = 64 << 10
)

type gatewayWeixinLoginStartResponseDTO struct {
	SessionKey string `json:"session_key"`
	QRCodeURL  string `json:"qrcode_url"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	ExpiresAt  string `json:"expires_at"`
}

type gatewayWeixinLoginStartRequest struct {
	AgentID          string
	OwnerID          string
	TargetKind       string
	HumanID          string
	HumanProfileID   string
	HumanDisplayName string
	TenantID         string
}

type gatewayWeixinLoginStatusResponseDTO struct {
	SessionKey       string `json:"session_key"`
	Status           string `json:"status"`
	Message          string `json:"message"`
	QRCodeURL        string `json:"qrcode_url,omitempty"`
	Connected        bool   `json:"connected"`
	AlreadyConnected bool   `json:"already_connected,omitempty"`
	AccountID        string `json:"account_id,omitempty"`
	BaseURL          string `json:"base_url,omitempty"`
	UserID           string `json:"user_id,omitempty"`
	BoundUserID      string `json:"bound_user_id,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	HumanID          string `json:"human_id,omitempty"`
	BoundHumanID     string `json:"bound_human_id,omitempty"`
	TenantID         string `json:"tenant_id,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
}

type gatewayWeixinVerifyCodeRequest struct {
	VerifyCode string `json:"verify_code"`
}

type gatewayWeixinLoginSession struct {
	SessionKey        string
	QRCode            string
	QRCodeURL         string
	StartedAt         time.Time
	CurrentLoginURL   string
	PendingCode       string
	BindingAgentID    string
	BindingOwnerID    string
	BindingTargetKind string
	BindingHumanID    string
	BindingTenantID   string
	Options           weixin.Options
}

type gatewayWeixinLoginClient interface {
	FetchQRCode(context.Context, weixin.IlinkLoginOptions, []string) (weixin.IlinkQRCode, error)
	PollQRCodeStatus(context.Context, weixin.IlinkLoginOptions, string, string) (weixin.IlinkQRCodeStatus, error)
}

type gatewayWeixinLoginPrincipal struct {
	TenantID string
	HumanID  string
}

type gatewayWeixinLoginManager struct {
	mu                    sync.Mutex
	sessions              map[string]*gatewayWeixinLoginSession
	pendingStarts         map[gatewayWeixinLoginPrincipal]struct{}
	client                gatewayWeixinLoginClient
	allowInsecureLoopback bool
}

func newGatewayWeixinLoginManager(allowInsecureLoopback ...bool) *gatewayWeixinLoginManager {
	manager := &gatewayWeixinLoginManager{
		sessions:      map[string]*gatewayWeixinLoginSession{},
		pendingStarts: map[gatewayWeixinLoginPrincipal]struct{}{},
		client:        weixin.NewIlinkLoginClient(nil),
	}
	if len(allowInsecureLoopback) > 0 {
		manager.allowInsecureLoopback = allowInsecureLoopback[0]
	}
	return manager
}

func (r *Router) submitProfileWeixinVerifyCode(w http.ResponseWriter, req *http.Request, sessionKey string) {
	if req.Body != nil {
		req.Body = http.MaxBytesReader(w, req.Body, maxGatewayWeixinLoginRequestBytes)
	}
	var body gatewayWeixinVerifyCodeRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	if err := rejectTrailingGatewayJSON(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	if err := r.gatewayWeixinLogins.setVerifyCode(sessionKey, body.VerifyCode); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "weixin_login_invalid_session", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, gatewayWeixinLoginStatusResponseDTO{
		SessionKey: sessionKey,
		Status:     "verify_code_submitted",
		Message:    "验证码已提交，请继续在手机微信中确认。",
	})
}

func (r *Router) saveGatewayWeixinLogin(req *http.Request, status gatewayWeixinLoginStatusResponseDTO) (gwconfig.Record, error) {
	store := r.gatewayStore()
	bindingAgentID := r.gatewayWeixinLogins.sessionBindingAgentID(status.SessionKey)
	bindingOwnerID := r.gatewayWeixinLogins.sessionBindingOwnerID(status.SessionKey)
	bindingTargetKind := r.gatewayWeixinLogins.sessionBindingTargetKind(status.SessionKey)
	bindingHumanID := r.gatewayWeixinLogins.sessionBindingHumanID(status.SessionKey)
	bindingTenantID := r.gatewayWeixinLogins.sessionBindingTenantID(status.SessionKey)
	if bindingHumanID == "" {
		return gwconfig.Record{}, fmt.Errorf("Weixin login must belong to the signed-in user")
	}
	if bindingTenantID == "" {
		return gwconfig.Record{}, fmt.Errorf("personal Weixin login session is missing tenant scope")
	}
	if strings.TrimSpace(status.AccountID) == "" {
		return gwconfig.Record{}, fmt.Errorf("weixin login confirmed without account id")
	}
	token := r.gatewayWeixinLogins.sessionToken(status.SessionKey)
	if token == "" {
		return gwconfig.Record{}, fmt.Errorf("weixin login confirmed without bot token")
	}
	return store.UpdateAndApply(func(cfg *gwconfig.Config) error {
		adapter := cfg.Adapters.Weixin
		if !adapter.Enabled {
			return fmt.Errorf("Weixin Gateway was disabled while the personal binding was in progress")
		}
		if adapter.Extra == nil {
			adapter.Extra = map[string]any{}
		}
		boundUserID := gatewayWeixinFirstNonBlank(status.UserID, status.AccountID)
		if boundUserID == "" {
			return fmt.Errorf("weixin login confirmed without user id")
		}
		if bindingTargetKind == profileWeixinTargetPersonalAssistant {
			upsertGatewayWeixinPersonalAssistantBinding(adapter.Extra, bindingTenantID, boundUserID, bindingOwnerID)
		} else if bindingAgentID != "" {
			removeGatewayWeixinPersonalAssistantBinding(adapter.Extra, bindingTenantID, boundUserID)
			upsertGatewayWeixinAgentBinding(adapter.Extra, bindingTenantID, boundUserID, bindingAgentID)
			upsertGatewayWeixinActiveAgentBinding(adapter.Extra, bindingTenantID, boundUserID, bindingAgentID)
		}
		replaceGatewayWeixinHumanBinding(adapter.Extra, bindingTenantID, boundUserID, bindingHumanID)
		upsertGatewayWeixinAccount(adapter.Extra, gatewayWeixinAccountBinding{
			TenantID:     bindingTenantID,
			UserID:       boundUserID,
			BotID:        status.AccountID,
			Token:        token,
			IlinkBaseURL: weixin.NormalizeIlinkBotBaseURL(status.BaseURL),
		})
		cfg.Adapters.Weixin = adapter
		return nil
	}, func(cfg gwconfig.Config) error {
		return r.reloadGatewayBindings(req, cfg)
	})
}

func (r *Router) rebindKnownGatewayWeixinAccount(req *http.Request, status gatewayWeixinLoginStatusResponseDTO) (gwconfig.Record, gatewayWeixinLoginStatusResponseDTO, error) {
	bindingAgentID := r.gatewayWeixinLogins.sessionBindingAgentID(status.SessionKey)
	bindingOwnerID := r.gatewayWeixinLogins.sessionBindingOwnerID(status.SessionKey)
	bindingTargetKind := r.gatewayWeixinLogins.sessionBindingTargetKind(status.SessionKey)
	bindingHumanID := r.gatewayWeixinLogins.sessionBindingHumanID(status.SessionKey)
	bindingTenantID := r.gatewayWeixinLogins.sessionBindingTenantID(status.SessionKey)
	if bindingHumanID == "" || bindingTenantID == "" {
		return gwconfig.Record{}, gatewayWeixinLoginStatusResponseDTO{}, fmt.Errorf("Weixin login must belong to the signed-in user and tenant")
	}
	store := r.gatewayStore()
	var account gatewayWeixinKnownAccount
	var userID string
	saved, err := store.UpdateAndApply(func(cfg *gwconfig.Config) error {
		adapter := cfg.Adapters.Weixin
		if !adapter.Enabled {
			return fmt.Errorf("Weixin Gateway was disabled while the personal binding was in progress")
		}
		if adapter.Extra == nil {
			adapter.Extra = map[string]any{}
		}
		var ok bool
		account, ok = gatewayWeixinKnownAccountForStatus(gatewayWeixinKnownAccounts(adapter.Extra, bindingTenantID), status)
		if !ok {
			return fmt.Errorf("Weixin did not return an account identifier that exactly matches a stored account")
		}
		userID = gatewayWeixinFirstNonBlank(status.UserID, account.UserID, status.AccountID)
		if userID == "" {
			return fmt.Errorf("known weixin account is missing user id")
		}
		if bindingTargetKind == profileWeixinTargetPersonalAssistant {
			upsertGatewayWeixinPersonalAssistantBinding(adapter.Extra, bindingTenantID, userID, bindingOwnerID)
		} else if bindingAgentID != "" {
			removeGatewayWeixinPersonalAssistantBinding(adapter.Extra, bindingTenantID, userID)
			upsertGatewayWeixinAgentBinding(adapter.Extra, bindingTenantID, userID, bindingAgentID)
			upsertGatewayWeixinActiveAgentBinding(adapter.Extra, bindingTenantID, userID, bindingAgentID)
		}
		replaceGatewayWeixinHumanBinding(adapter.Extra, bindingTenantID, userID, bindingHumanID)
		if account.Token != "" {
			upsertGatewayWeixinAccount(adapter.Extra, gatewayWeixinAccountBinding{
				TenantID:     bindingTenantID,
				UserID:       userID,
				BotID:        gatewayWeixinFirstNonBlank(status.AccountID, account.BotID),
				Token:        account.Token,
				IlinkBaseURL: gatewayWeixinFirstNonBlank(status.BaseURL, account.IlinkBaseURL, platformExtraString(adapter, "ilinkBaseUrl")),
			})
		}
		cfg.Adapters.Weixin = adapter
		return nil
	}, func(cfg gwconfig.Config) error {
		return r.reloadGatewayBindings(req, cfg)
	})
	if err != nil {
		return gwconfig.Record{}, gatewayWeixinLoginStatusResponseDTO{}, err
	}
	return saved, gatewayWeixinLoginStatusResponseDTO{
		SessionKey:   status.SessionKey,
		Status:       "connected",
		Connected:    true,
		AccountID:    gatewayWeixinFirstNonBlank(status.AccountID, account.BotID),
		BaseURL:      gatewayWeixinFirstNonBlank(status.BaseURL, account.IlinkBaseURL),
		UserID:       gatewayWeixinFirstNonBlank(status.UserID, account.UserID),
		BoundUserID:  userID,
		AgentID:      bindingAgentID,
		HumanID:      bindingHumanID,
		BoundHumanID: bindingHumanID,
		TenantID:     bindingTenantID,
	}, nil
}

func (m *gatewayWeixinLoginManager) start(ctx context.Context, config gwconfig.WeixinAdapterConfig) (gatewayWeixinLoginStartResponseDTO, error) {
	if m == nil {
		return gatewayWeixinLoginStartResponseDTO{}, fmt.Errorf("weixin login manager is not configured")
	}
	m.purgeExpired()
	opts := weixin.OptionsFromConfig(config)
	bindingAgentID := strings.TrimSpace(configExtraString(config.Extra, "_bindingAgentID"))
	bindingOwnerID := strings.TrimSpace(configExtraString(config.Extra, "_bindingOwnerID"))
	bindingTargetKind := strings.TrimSpace(configExtraString(config.Extra, "_bindingTargetKind"))
	bindingHumanID := strings.TrimSpace(configExtraString(config.Extra, "_bindingHumanID"))
	bindingTenantID := strings.TrimSpace(configExtraString(config.Extra, "_bindingTenantID"))
	if bindingHumanID == "" || bindingTenantID == "" {
		return gatewayWeixinLoginStartResponseDTO{}, fmt.Errorf("Weixin login must belong to the signed-in user and tenant")
	}
	if bindingTargetKind == profileWeixinTargetPersonalAssistant && bindingOwnerID == "" {
		return gatewayWeixinLoginStartResponseDTO{}, fmt.Errorf("personal assistant Weixin login is missing account owner scope")
	}
	principal, err := m.reserveSessionCapacity(bindingTenantID, bindingHumanID)
	if err != nil {
		return gatewayWeixinLoginStartResponseDTO{}, err
	}
	defer m.releaseSessionCapacity(principal)
	loginOptions := loginOptionsFromWeixinOptions(opts)
	loginOptions.AllowInsecureLoopback = m.allowInsecureLoopback
	qr, err := m.client.FetchQRCode(ctx, loginOptions, nil)
	if err != nil {
		return gatewayWeixinLoginStartResponseDTO{}, err
	}
	now := time.Now().UTC()
	sessionKey, err := newGatewayWeixinLoginSessionKey()
	if err != nil {
		return gatewayWeixinLoginStartResponseDTO{}, err
	}
	session := &gatewayWeixinLoginSession{
		SessionKey:        sessionKey,
		QRCode:            qr.Code,
		QRCodeURL:         qr.URL,
		StartedAt:         now,
		CurrentLoginURL:   opts.IlinkLoginBaseURL,
		BindingAgentID:    bindingAgentID,
		BindingOwnerID:    bindingOwnerID,
		BindingTargetKind: bindingTargetKind,
		BindingHumanID:    bindingHumanID,
		BindingTenantID:   bindingTenantID,
		Options:           opts,
	}
	if err := m.registerSession(session); err != nil {
		return gatewayWeixinLoginStartResponseDTO{}, err
	}
	return gatewayWeixinLoginStartResponseDTO{
		SessionKey: sessionKey,
		QRCodeURL:  qr.URL,
		Status:     "waiting",
		Message:    "请用手机微信扫描二维码，并在手机上确认登录。",
		ExpiresAt:  now.Add(gatewayWeixinLoginTTL).Format(time.RFC3339),
	}, nil
}

func (m *gatewayWeixinLoginManager) poll(ctx context.Context, sessionKey string) (gatewayWeixinLoginStatusResponseDTO, error) {
	session, err := m.snapshot(sessionKey)
	if err != nil {
		return gatewayWeixinLoginStatusResponseDTO{}, err
	}
	loginOptions := loginOptionsFromSession(session)
	loginOptions.AllowInsecureLoopback = m.allowInsecureLoopback
	status, err := m.client.PollQRCodeStatus(ctx, loginOptions, session.QRCode, session.PendingCode)
	if err != nil {
		return gatewayWeixinLoginStatusResponseDTO{}, err
	}
	return m.applyStatus(session, status)
}

func (m *gatewayWeixinLoginManager) setVerifyCode(sessionKey string, verifyCode string) error {
	verifyCode = strings.TrimSpace(verifyCode)
	if !validGatewayWeixinVerifyCode(verifyCode) {
		return fmt.Errorf("verify_code must contain 4 to 8 digits")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[strings.TrimSpace(sessionKey)]
	if session == nil {
		return fmt.Errorf("login session not found")
	}
	if time.Since(session.StartedAt) > gatewayWeixinLoginTTL {
		delete(m.sessions, session.SessionKey)
		return fmt.Errorf("login session expired")
	}
	session.PendingCode = verifyCode
	return nil
}

func (m *gatewayWeixinLoginManager) sessionToken(sessionKey string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[strings.TrimSpace(sessionKey)]
	if session == nil {
		return ""
	}
	return strings.TrimSpace(session.Options.BotToken)
}

func (m *gatewayWeixinLoginManager) sessionBindingAgentID(sessionKey string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[strings.TrimSpace(sessionKey)]
	if session == nil {
		return ""
	}
	return strings.TrimSpace(session.BindingAgentID)
}

func (m *gatewayWeixinLoginManager) sessionBindingOwnerID(sessionKey string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[strings.TrimSpace(sessionKey)]
	if session == nil {
		return ""
	}
	return strings.TrimSpace(session.BindingOwnerID)
}

func (m *gatewayWeixinLoginManager) sessionBindingTargetKind(sessionKey string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[strings.TrimSpace(sessionKey)]
	if session == nil {
		return ""
	}
	return strings.TrimSpace(session.BindingTargetKind)
}

func (m *gatewayWeixinLoginManager) sessionBindingHumanID(sessionKey string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[strings.TrimSpace(sessionKey)]
	if session == nil {
		return ""
	}
	return strings.TrimSpace(session.BindingHumanID)
}

func (m *gatewayWeixinLoginManager) sessionBindingTenantID(sessionKey string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[strings.TrimSpace(sessionKey)]
	if session == nil {
		return ""
	}
	return strings.TrimSpace(session.BindingTenantID)
}

func (m *gatewayWeixinLoginManager) finish(sessionKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, strings.TrimSpace(sessionKey))
}

func overlayGatewayWeixinLoginConfig(config gwconfig.WeixinAdapterConfig, body gatewayWeixinLoginStartRequest) gwconfig.WeixinAdapterConfig {
	if strings.TrimSpace(body.TargetKind) != "" {
		if config.Extra == nil {
			config.Extra = map[string]any{}
		}
		config.Extra["_bindingTargetKind"] = strings.TrimSpace(body.TargetKind)
		config.Extra["_bindingOwnerID"] = strings.TrimSpace(body.OwnerID)
	}
	if strings.TrimSpace(body.AgentID) != "" {
		if config.Extra == nil {
			config.Extra = map[string]any{}
		}
		config.Extra["_bindingAgentID"] = strings.TrimSpace(body.AgentID)
	}
	humanID := gatewayWeixinFirstNonBlank(body.HumanProfileID, body.HumanID)
	if humanID != "" {
		if config.Extra == nil {
			config.Extra = map[string]any{}
		}
		config.Extra["_bindingHumanID"] = humanID
		config.Extra["_bindingTenantID"] = strings.TrimSpace(body.TenantID)
		if strings.TrimSpace(body.HumanDisplayName) != "" {
			config.Extra["_bindingHumanDisplayName"] = strings.TrimSpace(body.HumanDisplayName)
		}
	}
	return config
}

func (m *gatewayWeixinLoginManager) snapshot(sessionKey string) (gatewayWeixinLoginSession, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return gatewayWeixinLoginSession{}, fmt.Errorf("login session is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[sessionKey]
	if session == nil {
		return gatewayWeixinLoginSession{}, fmt.Errorf("login session not found")
	}
	if time.Since(session.StartedAt) > gatewayWeixinLoginTTL {
		delete(m.sessions, sessionKey)
		return gatewayWeixinLoginSession{}, fmt.Errorf("login session expired")
	}
	return *session, nil
}

func (m *gatewayWeixinLoginManager) applyStatus(session gatewayWeixinLoginSession, status weixin.IlinkQRCodeStatus) (gatewayWeixinLoginStatusResponseDTO, error) {
	dto := gatewayWeixinLoginStatusResponseDTO{
		SessionKey: session.SessionKey,
		Status:     status.Status,
		QRCodeURL:  session.QRCodeURL,
		ExpiresAt:  session.StartedAt.Add(gatewayWeixinLoginTTL).Format(time.RFC3339),
	}
	m.mu.Lock()
	current := m.sessions[session.SessionKey]
	if current != nil {
		switch status.Status {
		case "scaned":
			current.PendingCode = ""
		case "scaned_but_redirect":
			if status.RedirectHost != "" {
				redirectURL := "https://" + strings.TrimSpace(status.RedirectHost)
				if err := weixin.ValidateIlinkBotBaseURL(redirectURL, m.allowInsecureLoopback); err != nil {
					m.mu.Unlock()
					return gatewayWeixinLoginStatusResponseDTO{}, fmt.Errorf("invalid Weixin login redirect: %w", err)
				}
				current.CurrentLoginURL = weixin.NormalizeIlinkLoginBaseURL(redirectURL)
			}
		case "confirmed":
			if err := weixin.ValidateIlinkBotBaseURL(status.BaseURL, m.allowInsecureLoopback); err != nil {
				m.mu.Unlock()
				return gatewayWeixinLoginStatusResponseDTO{}, fmt.Errorf("invalid Weixin bot endpoint: %w", err)
			}
			current.Options.BotToken = status.BotToken
		case "binded_redirect":
			if strings.TrimSpace(status.BaseURL) != "" {
				if err := weixin.ValidateIlinkBotBaseURL(status.BaseURL, m.allowInsecureLoopback); err != nil {
					m.mu.Unlock()
					return gatewayWeixinLoginStatusResponseDTO{}, fmt.Errorf("invalid Weixin bot endpoint: %w", err)
				}
			}
		case "expired", "verify_code_blocked":
			delete(m.sessions, session.SessionKey)
		}
	}
	m.mu.Unlock()
	switch status.Status {
	case "wait":
		dto.Message = "请用手机微信扫描二维码。"
	case "scaned":
		dto.Message = "已扫码，请在手机微信上确认登录。"
	case "need_verifycode":
		dto.Message = "微信要求输入手机上显示的数字验证码。"
	case "scaned_but_redirect":
		dto.Status = "scaned"
		dto.Message = "已扫码，正在切换微信登录节点。"
	case "confirmed":
		dto.AccountID = status.IlinkBotID
		dto.BaseURL = status.BaseURL
		dto.UserID = status.IlinkUserID
		dto.Message = "扫码成功，正在保存微信通道。"
	case "binded_redirect":
		dto.Status = "already_connected"
		dto.AlreadyConnected = true
		dto.AccountID = status.IlinkBotID
		dto.BaseURL = status.BaseURL
		dto.UserID = status.IlinkUserID
		dto.BoundUserID = status.IlinkUserID
		dto.AgentID = session.BindingAgentID
		dto.HumanID = session.BindingHumanID
		dto.BoundHumanID = session.BindingHumanID
		dto.Message = "此微信账号已连接过当前服务，无需重复扫码。"
	case "expired":
		dto.Message = "二维码已过期，请重新生成。"
	case "verify_code_blocked":
		dto.Message = "验证码多次输入错误，请重新生成二维码。"
	default:
		dto.Message = "正在等待微信确认。"
	}
	return dto, nil
}

func (m *gatewayWeixinLoginManager) purgeExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, session := range m.sessions {
		if time.Since(session.StartedAt) > gatewayWeixinLoginTTL {
			delete(m.sessions, key)
		}
	}
}

func loginOptionsFromSession(session gatewayWeixinLoginSession) weixin.IlinkLoginOptions {
	opts := loginOptionsFromWeixinOptions(session.Options)
	opts.LoginBaseURL = session.CurrentLoginURL
	return opts
}

func loginOptionsFromWeixinOptions(opts weixin.Options) weixin.IlinkLoginOptions {
	opts = opts.WithDefaults()
	return weixin.IlinkLoginOptions{
		LoginBaseURL:          opts.IlinkLoginBaseURL,
		BotType:               opts.IlinkBotType,
		IlinkAppID:            opts.IlinkAppID,
		IlinkAppClientVersion: opts.IlinkAppClientVersion,
		RequestTimeoutSeconds: opts.RequestTimeoutSeconds,
	}
}

func newGatewayWeixinLoginSessionKey() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate Weixin login session key: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

func (m *gatewayWeixinLoginManager) reserveSessionCapacity(bindingTenantID string, bindingHumanID string) (gatewayWeixinLoginPrincipal, error) {
	principal := gatewayWeixinLoginPrincipal{
		TenantID: strings.TrimSpace(bindingTenantID),
		HumanID:  strings.TrimSpace(bindingHumanID),
	}
	if principal.TenantID == "" || principal.HumanID == "" {
		return gatewayWeixinLoginPrincipal{}, fmt.Errorf("Weixin login must belong to the signed-in user and tenant")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingStarts == nil {
		m.pendingStarts = map[gatewayWeixinLoginPrincipal]struct{}{}
	}
	if _, exists := m.pendingStarts[principal]; exists {
		return gatewayWeixinLoginPrincipal{}, fmt.Errorf("Weixin login is already being started for this user")
	}

	occupied := len(m.sessions)
	for pending := range m.pendingStarts {
		if !m.hasSessionForPrincipalLocked(pending) {
			occupied++
		}
	}
	if !m.hasSessionForPrincipalLocked(principal) && occupied >= maxGatewayWeixinLoginSessions {
		return gatewayWeixinLoginPrincipal{}, fmt.Errorf("too many active Weixin login sessions")
	}
	m.pendingStarts[principal] = struct{}{}
	return principal, nil
}

func (m *gatewayWeixinLoginManager) releaseSessionCapacity(principal gatewayWeixinLoginPrincipal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pendingStarts, principal)
}

func (m *gatewayWeixinLoginManager) hasSessionForPrincipalLocked(principal gatewayWeixinLoginPrincipal) bool {
	for _, session := range m.sessions {
		if strings.TrimSpace(session.BindingHumanID) == principal.HumanID && strings.TrimSpace(session.BindingTenantID) == principal.TenantID {
			return true
		}
	}
	return false
}

func (m *gatewayWeixinLoginManager) registerSession(session *gatewayWeixinLoginSession) error {
	if session == nil || strings.TrimSpace(session.SessionKey) == "" {
		return fmt.Errorf("Weixin login session is invalid")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	humanID := strings.TrimSpace(session.BindingHumanID)
	tenantID := strings.TrimSpace(session.BindingTenantID)
	if humanID == "" || tenantID == "" {
		return fmt.Errorf("Weixin login must belong to the signed-in user and tenant")
	}
	for key, existing := range m.sessions {
		if strings.TrimSpace(existing.BindingHumanID) == humanID && strings.TrimSpace(existing.BindingTenantID) == tenantID {
			delete(m.sessions, key)
		}
	}
	if len(m.sessions) >= maxGatewayWeixinLoginSessions {
		return fmt.Errorf("too many active Weixin login sessions")
	}
	m.sessions[session.SessionKey] = session
	return nil
}

func validGatewayWeixinVerifyCode(value string) bool {
	if len(value) < 4 || len(value) > 8 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func platformExtraString(config gwconfig.WeixinAdapterConfig, key string) string {
	if config.Extra == nil {
		return ""
	}
	value, ok := config.Extra[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

type gatewayWeixinAccountBinding struct {
	TenantID     string
	UserID       string
	BotID        string
	Token        string
	IlinkBaseURL string
}

type gatewayWeixinKnownAccount struct {
	TenantID     string
	UserID       string
	BotID        string
	Token        string
	IlinkBaseURL string
}

func gatewayWeixinKnownAccounts(extra map[string]any, tenantID string) []gatewayWeixinKnownAccount {
	if extra == nil {
		return nil
	}
	raw, ok := extra["weixinAccounts"].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make([]gatewayWeixinKnownAccount, 0, len(raw))
	tenantID = gwconfig.NormalizeTenantID(tenantID)
	for key, value := range raw {
		account, ok := value.(map[string]any)
		if !ok {
			continue
		}
		keyTenantID, keyUserID, scoped := gwconfig.ParseWeixinBindingKey(key)
		if !scoped {
			continue
		}
		item := gatewayWeixinKnownAccount{
			TenantID:     gwconfig.NormalizeTenantID(gatewayWeixinFirstNonBlank(configExtraString(account, "tenantId"), configExtraString(account, "tenant_id"), keyTenantID)),
			UserID:       gatewayWeixinFirstNonBlank(configExtraString(account, "userId"), configExtraString(account, "user_id"), keyUserID),
			BotID:        gatewayWeixinFirstNonBlank(configExtraString(account, "botId"), configExtraString(account, "bot_id"), configExtraString(account, "accountId")),
			Token:        configExtraString(account, "token"),
			IlinkBaseURL: gatewayWeixinFirstNonBlank(configExtraString(account, "ilinkBaseUrl"), configExtraString(account, "baseUrl")),
		}
		if item.TenantID != tenantID || item.TenantID != keyTenantID || item.UserID != keyUserID {
			continue
		}
		if item.UserID == "" && item.BotID == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func gatewayWeixinKnownAccountForStatus(accounts []gatewayWeixinKnownAccount, status gatewayWeixinLoginStatusResponseDTO) (gatewayWeixinKnownAccount, bool) {
	userID := strings.TrimSpace(status.UserID)
	if userID == "" {
		userID = strings.TrimSpace(status.BoundUserID)
	}
	accountID := strings.TrimSpace(status.AccountID)
	for _, account := range accounts {
		if userID != "" && account.UserID == userID {
			return account, true
		}
		if accountID != "" && account.BotID == accountID {
			return account, true
		}
	}
	return gatewayWeixinKnownAccount{}, false
}

func gatewayWeixinRebindMessage(status gatewayWeixinLoginStatusResponseDTO) string {
	switch {
	case strings.TrimSpace(status.AgentID) != "" && strings.TrimSpace(status.BoundHumanID) != "":
		return "此微信账号已连接过当前服务，已重新绑定到所选 Agent 和真人用户。"
	case strings.TrimSpace(status.AgentID) != "":
		return "此微信账号已连接过当前服务，已重新绑定到所选 Agent。"
	case strings.TrimSpace(status.BoundHumanID) != "":
		return "此微信账号已连接过当前服务，已重新绑定到所选真人用户。"
	default:
		return "此微信账号已连接过当前服务。"
	}
}

func upsertGatewayWeixinAgentBinding(extra map[string]any, tenantID string, userID string, agentID string) {
	bindingKey := gwconfig.WeixinBindingKey(tenantID, userID)
	agentID = strings.TrimSpace(agentID)
	if bindingKey == "" || agentID == "" {
		return
	}
	bindings := map[string]any{}
	switch typed := extra["agentBindings"].(type) {
	case map[string]any:
		bindings = typed
	case map[string]string:
		for key, value := range typed {
			bindings[key] = value
		}
	}
	if gwconfig.NormalizeTenantID(tenantID) == gwconfig.DefaultTenantID {
		delete(bindings, strings.TrimSpace(userID))
	}
	bindings[bindingKey] = agentID
	extra["agentBindings"] = bindings
}

func upsertGatewayWeixinHumanBinding(extra map[string]any, tenantID string, userID string, humanID string) {
	bindingKey := gwconfig.WeixinBindingKey(tenantID, userID)
	humanID = strings.TrimSpace(humanID)
	if bindingKey == "" || humanID == "" {
		return
	}
	bindings := map[string]any{}
	switch typed := extra["weixinUserBindings"].(type) {
	case map[string]any:
		bindings = typed
	case map[string]string:
		for key, value := range typed {
			bindings[key] = value
		}
	}
	if gwconfig.NormalizeTenantID(tenantID) == gwconfig.DefaultTenantID {
		delete(bindings, strings.TrimSpace(userID))
	}
	bindings[bindingKey] = humanID
	extra["weixinUserBindings"] = bindings
}

func upsertGatewayWeixinAccount(extra map[string]any, account gatewayWeixinAccountBinding) {
	userID := strings.TrimSpace(account.UserID)
	tenantID := gwconfig.NormalizeTenantID(account.TenantID)
	bindingKey := gwconfig.WeixinBindingKey(tenantID, userID)
	if bindingKey == "" || strings.TrimSpace(account.Token) == "" {
		return
	}
	accounts := map[string]any{}
	switch typed := extra["weixinAccounts"].(type) {
	case map[string]any:
		accounts = typed
	case map[string]map[string]any:
		for key, value := range typed {
			accounts[key] = value
		}
	}
	if tenantID == gwconfig.DefaultTenantID {
		delete(accounts, userID)
	}
	accounts[bindingKey] = map[string]any{
		"tenantId":     tenantID,
		"userId":       userID,
		"botId":        strings.TrimSpace(account.BotID),
		"token":        strings.TrimSpace(account.Token),
		"ilinkBaseUrl": strings.TrimSpace(account.IlinkBaseURL),
	}
	extra["weixinAccounts"] = accounts
}

func configExtraString(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	value, ok := extra[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func gatewayWeixinFirstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

package httptransport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/gateway/channel/feishu"
	gwconfig "orrery_v2/internal/gateway/config"
)

const (
	feishuRegistrationTTL         = 10 * time.Minute
	maxFeishuRegistrationSessions = 20
)

var (
	errFeishuRegistrationNotFound = errors.New("Feishu registration session not found")
	errFeishuRegistrationBusy     = errors.New("a Feishu registration is already active for this administrator")
	errFeishuRegistrationCapacity = errors.New("too many Feishu registrations are active")
)

type feishuRegistrationDTO struct {
	RegistrationID  string `json:"registration_id"`
	Status          string `json:"status"`
	VerificationURL string `json:"verification_url,omitempty"`
	ExpiresAt       string `json:"expires_at"`
	AccountID       string `json:"account_id,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
}

type feishuRegistrationSession struct {
	ID              string
	TenantID        string
	OwnerID         string
	Status          string
	VerificationURL string
	ExpiresAt       time.Time
	AccountID       string
	ErrorCode       string
	cancel          context.CancelFunc
}

type feishuRegistrationOutcome struct {
	TenantID  string
	OwnerID   string
	AccountID string
	Status    string
	ErrorCode string
}

type startFeishuRegistrationRequest struct {
	AccountID string `json:"account_id"`
}

func (s *feishuRegistrationSession) dto() feishuRegistrationDTO {
	return feishuRegistrationDTO{
		RegistrationID:  s.ID,
		Status:          s.Status,
		VerificationURL: s.VerificationURL,
		ExpiresAt:       s.ExpiresAt.UTC().Format(time.RFC3339),
		AccountID:       s.AccountID,
		ErrorCode:       s.ErrorCode,
	}
}

type feishuRegistrationManager struct {
	mu        sync.Mutex
	client    feishu.RegistrationClient
	sessions  map[string]*feishuRegistrationSession
	now       func() time.Time
	lifecycle context.Context
}

func newFeishuRegistrationManager(client feishu.RegistrationClient, parents ...context.Context) *feishuRegistrationManager {
	if client == nil {
		client = feishu.NewSDKRegistrationClient()
	}
	lifecycle := context.Background()
	if len(parents) > 0 && parents[0] != nil {
		lifecycle = parents[0]
	}
	return &feishuRegistrationManager{client: client, sessions: map[string]*feishuRegistrationSession{}, now: time.Now, lifecycle: lifecycle}
}

func (m *feishuRegistrationManager) Start(
	_ context.Context,
	tenantID string,
	ownerID string,
	accountID string,
	onComplete func(context.Context, feishu.RegistrationResult) error,
	onFinished ...func(feishuRegistrationOutcome),
) (feishuRegistrationDTO, error) {
	if m == nil || m.client == nil {
		return feishuRegistrationDTO{}, errors.New("Feishu registration service is unavailable")
	}
	tenantID = strings.TrimSpace(tenantID)
	ownerID = strings.TrimSpace(ownerID)
	accountID = strings.TrimSpace(accountID)
	if tenantID == "" || ownerID == "" || !validFeishuAccountAlias(accountID) {
		return feishuRegistrationDTO{}, errors.New("authenticated tenant, administrator, and valid Feishu account alias are required")
	}
	if err := m.lifecycle.Err(); err != nil {
		return feishuRegistrationDTO{}, errors.New("Feishu registration service is shutting down")
	}
	now := m.now().UTC()
	m.mu.Lock()
	m.pruneLocked(now)
	active := 0
	for _, existing := range m.sessions {
		if !feishuRegistrationActive(existing.Status) {
			continue
		}
		active++
		if existing.TenantID == tenantID && existing.OwnerID == ownerID {
			m.mu.Unlock()
			return feishuRegistrationDTO{}, errFeishuRegistrationBusy
		}
	}
	if active >= maxFeishuRegistrationSessions {
		m.mu.Unlock()
		return feishuRegistrationDTO{}, errFeishuRegistrationCapacity
	}
	registrationID, err := newFeishuRegistrationID()
	if err != nil {
		m.mu.Unlock()
		return feishuRegistrationDTO{}, err
	}
	registrationCtx, cancel := context.WithTimeout(m.lifecycle, feishuRegistrationTTL)
	session := &feishuRegistrationSession{
		ID: registrationID, TenantID: tenantID, OwnerID: ownerID, Status: "starting",
		ExpiresAt: now.Add(feishuRegistrationTTL), AccountID: accountID, cancel: cancel,
	}
	m.sessions[session.ID] = session
	started := session.dto()
	m.mu.Unlock()

	var finished func(feishuRegistrationOutcome)
	if len(onFinished) > 0 {
		finished = onFinished[0]
	}
	go m.run(registrationCtx, session.ID, onComplete, finished)
	return started, nil
}

func (m *feishuRegistrationManager) run(
	ctx context.Context,
	registrationID string,
	onComplete func(context.Context, feishu.RegistrationResult) error,
	onFinished func(feishuRegistrationOutcome),
) {
	result, err := m.client.Register(ctx, func(qr feishu.RegistrationQRCode) {
		m.mu.Lock()
		defer m.mu.Unlock()
		session := m.sessions[registrationID]
		if session == nil || !feishuRegistrationActive(session.Status) {
			return
		}
		verificationURL := strings.TrimSpace(qr.URL)
		if verificationURL == "" {
			return
		}
		session.VerificationURL = verificationURL
		if qr.ExpireIn > 0 {
			expiresAt := m.now().UTC().Add(time.Duration(qr.ExpireIn) * time.Second)
			if expiresAt.Before(session.ExpiresAt) {
				session.ExpiresAt = expiresAt
			}
		}
		session.Status = "pending"
	}, func(status feishu.RegistrationStatus) {
		m.mu.Lock()
		defer m.mu.Unlock()
		session := m.sessions[registrationID]
		if session != nil && feishuRegistrationActive(session.Status) && strings.TrimSpace(status.Status) != "" {
			session.Status = "pending"
		}
	})
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		if onComplete == nil {
			err = errors.New("Feishu registration credential apply service is unavailable")
		} else {
			applyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = onComplete(applyCtx, result)
			cancel()
		}
	}
	m.mu.Lock()
	session := m.sessions[registrationID]
	if session == nil {
		m.mu.Unlock()
		return
	}
	if err == nil {
		session.Status = "connected"
		session.ErrorCode = ""
	} else if errors.Is(ctx.Err(), context.Canceled) {
		if session.Status != "cancelled" {
			session.Status = "expired"
		}
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		session.Status = "expired"
	} else {
		session.Status = "failed"
		session.ErrorCode = feishuRegistrationErrorCode(err)
	}
	session.cancel = nil
	outcome := feishuRegistrationOutcome{
		TenantID: session.TenantID, OwnerID: session.OwnerID, AccountID: session.AccountID,
		Status: session.Status, ErrorCode: session.ErrorCode,
	}
	m.mu.Unlock()
	if onFinished != nil {
		onFinished(outcome)
	}
}

func (m *feishuRegistrationManager) Get(tenantID, ownerID, registrationID string) (feishuRegistrationDTO, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[strings.TrimSpace(registrationID)]
	if session == nil || session.TenantID != strings.TrimSpace(tenantID) || session.OwnerID != strings.TrimSpace(ownerID) {
		return feishuRegistrationDTO{}, errFeishuRegistrationNotFound
	}
	if feishuRegistrationActive(session.Status) && !m.now().UTC().Before(session.ExpiresAt) {
		if session.cancel != nil {
			session.cancel()
		}
		session.Status = "expired"
		session.cancel = nil
	}
	return session.dto(), nil
}

func (m *feishuRegistrationManager) Cancel(tenantID, ownerID, registrationID string) (feishuRegistrationDTO, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[strings.TrimSpace(registrationID)]
	if session == nil || session.TenantID != strings.TrimSpace(tenantID) || session.OwnerID != strings.TrimSpace(ownerID) {
		return feishuRegistrationDTO{}, errFeishuRegistrationNotFound
	}
	if feishuRegistrationActive(session.Status) {
		session.Status = "cancelled"
		if session.cancel != nil {
			session.cancel()
			session.cancel = nil
		}
	}
	return session.dto(), nil
}

func (m *feishuRegistrationManager) pruneLocked(now time.Time) {
	for id, session := range m.sessions {
		if feishuRegistrationActive(session.Status) && !now.Before(session.ExpiresAt) {
			if session.cancel != nil {
				session.cancel()
			}
			delete(m.sessions, id)
			continue
		}
		if !feishuRegistrationActive(session.Status) && now.Sub(session.ExpiresAt) > feishuRegistrationTTL {
			delete(m.sessions, id)
		}
	}
}

func feishuRegistrationActive(status string) bool {
	return status == "starting" || status == "pending"
}

func feishuRegistrationErrorCode(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "access_denied") || strings.Contains(message, "access denied"):
		return "access_denied"
	case strings.Contains(message, "expired") || errors.Is(err, context.DeadlineExceeded):
		return "expired"
	case gwconfig.IsApplyError(err):
		return "gateway_reload_failed"
	default:
		return "registration_failed"
	}
}

func newFeishuRegistrationID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("failed to generate Feishu registration id")
	}
	return "freg_" + hex.EncodeToString(raw), nil
}

func (r *Router) handleStartFeishuRegistration(w http.ResponseWriter, req *http.Request) {
	tenantID, ownerID, ok := r.gatewayRegistrationActor(w, req)
	if !ok {
		return
	}
	var body startFeishuRegistrationRequest
	if !decodeFeishuRegistrationStart(w, req, &body) {
		return
	}
	if !validFeishuAccountAlias(body.AccountID) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "feishu_account_alias_invalid", Message: "The Feishu account alias is invalid.", CorrelationID: correlationID(req)})
		return
	}
	body.AccountID = strings.TrimSpace(body.AccountID)
	registration, err := r.gatewayFeishuRegistrations.Start(req.Context(), tenantID, ownerID, body.AccountID, func(ctx context.Context, result feishu.RegistrationResult) error {
		return r.completeFeishuRegistration(ctx, body.AccountID, result)
	}, func(outcome feishuRegistrationOutcome) {
		r.recordFeishuRegistrationAudit(req, outcome)
	})
	if err != nil {
		status := http.StatusBadRequest
		code := "feishu_registration_failed"
		if errors.Is(err, errFeishuRegistrationBusy) || errors.Is(err, errFeishuRegistrationCapacity) {
			status = http.StatusTooManyRequests
			code = "feishu_registration_busy"
		}
		writeJSON(w, status, ErrorResponse{Code: code, Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusAccepted, registration)
}

func (r *Router) handleGetFeishuRegistration(w http.ResponseWriter, req *http.Request, registrationID string) {
	tenantID, ownerID, ok := r.gatewayRegistrationActor(w, req)
	if !ok {
		return
	}
	registration, err := r.gatewayFeishuRegistrations.Get(tenantID, ownerID, registrationID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "feishu_registration_not_found", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, registration)
}

func (r *Router) handleCancelFeishuRegistration(w http.ResponseWriter, req *http.Request, registrationID string) {
	tenantID, ownerID, ok := r.gatewayRegistrationActor(w, req)
	if !ok {
		return
	}
	registration, err := r.gatewayFeishuRegistrations.Cancel(tenantID, ownerID, registrationID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "feishu_registration_not_found", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, registration)
}

func (r *Router) completeFeishuRegistration(ctx context.Context, accountID string, result feishu.RegistrationResult) error {
	accountID = strings.TrimSpace(accountID)
	if !validFeishuAccountAlias(accountID) {
		return errors.New("Feishu account alias is invalid")
	}
	if strings.TrimSpace(result.AppID) == "" || strings.TrimSpace(result.AppSecret) == "" {
		return errors.New("Feishu registration returned incomplete credentials")
	}
	_, err := r.gatewayStore().UpdateAndApply(func(cfg *gwconfig.Config) error {
		cfg.Adapters.Feishu.Enabled = true
		if cfg.Adapters.Feishu.Accounts == nil {
			cfg.Adapters.Feishu.Accounts = map[string]gwconfig.FeishuAccountConfig{}
		}
		account := cfg.Adapters.Feishu.Accounts[accountID]
		account.Enabled = true
		account.AppID = strings.TrimSpace(result.AppID)
		account.AppSecret = strings.TrimSpace(result.AppSecret)
		account.ConnectionMode = feishu.ModeStream
		account.DomainName = feishuDomainForTenantBrand(result.TenantBrand)
		if strings.TrimSpace(account.DefaultSessionLabel) == "" {
			account.DefaultSessionLabel = "general"
		}
		cfg.Adapters.Feishu.Accounts[accountID] = account
		return nil
	}, func(cfg gwconfig.Config) error {
		if r.gatewaySettingsChanged == nil {
			return errors.New("Gateway runtime reload service is unavailable")
		}
		return r.gatewaySettingsChanged(ctx, cfg)
	})
	return err
}

func decodeFeishuRegistrationStart(w http.ResponseWriter, req *http.Request, target *startFeishuRegistrationRequest) bool {
	if req.Body != nil {
		req.Body = http.MaxBytesReader(w, req.Body, 4096)
	}
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: "The request body is invalid.", CorrelationID: correlationID(req)})
		return false
	}
	if err := rejectTrailingGatewayJSON(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: "The request body is invalid.", CorrelationID: correlationID(req)})
		return false
	}
	if strings.TrimSpace(target.AccountID) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "feishu_account_alias_required", Message: "A Feishu account alias is required.", CorrelationID: correlationID(req)})
		return false
	}
	return true
}

func validFeishuAccountAlias(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index, char := range value {
		asciiLetter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
		digit := char >= '0' && char <= '9'
		if asciiLetter || digit || index > 0 && (char == '-' || char == '_' || char == '.') {
			continue
		}
		return false
	}
	return true
}

func (r *Router) recordFeishuRegistrationAudit(req *http.Request, outcome feishuRegistrationOutcome) {
	if r == nil || r.auditRecorder == nil || req == nil {
		return
	}
	result := "failed"
	severity := "warning"
	if outcome.Status == "connected" {
		result = "succeeded"
		severity = "info"
	}
	metadata := map[string]string{
		"tenant_id":  auditCleanValue(outcome.TenantID, 256),
		"account_id": auditCleanValue(outcome.AccountID, 128),
		"actor_id":   auditCleanValue(outcome.OwnerID, 256),
		"status":     auditCleanValue(outcome.Status, 64),
	}
	if outcome.ErrorCode != "" {
		metadata["error_code"] = auditCleanValue(outcome.ErrorCode, 128)
	}
	_ = r.auditRecorder.RecordAuditEvent(context.WithoutCancel(req.Context()), operations.AuditEventDTO{
		ActorID:       firstNonEmpty(outcome.OwnerID, "unknown"),
		ViewerID:      outcome.OwnerID,
		EntityType:    "gateway_feishu_registration",
		EntityID:      "feishu:" + auditCleanValue(outcome.AccountID, 128),
		Action:        "gateway.feishu_registration",
		Result:        result,
		Severity:      severity,
		CorrelationID: correlationID(req),
		Summary:       "Feishu registration " + auditCleanValue(outcome.Status, 64) + " for account " + auditCleanValue(outcome.AccountID, 128),
		Metadata:      metadata,
		OccurredAt:    time.Now().UTC(),
	})
}

func feishuDomainForTenantBrand(tenantBrand string) string {
	switch strings.ToLower(strings.TrimSpace(tenantBrand)) {
	case "lark", "larksuite":
		return "lark"
	default:
		return "feishu"
	}
}

func (r *Router) gatewayRegistrationActor(w http.ResponseWriter, req *http.Request) (string, string, bool) {
	principal, ok := identity.PrincipalFromContext(req.Context())
	if !ok || !principal.Authenticated() {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: "authentication_required", Message: "Authentication is required.", CorrelationID: correlationID(req)})
		return "", "", false
	}
	principal = identity.NormalizePrincipal(principal)
	tenantID := strings.TrimSpace(principal.TenantID)
	if r != nil && r.agentProfiles != nil && principal.AgentProfileID != "" {
		profile, err := r.agentProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(principal.AgentProfileID))
		if err != nil {
			status := http.StatusServiceUnavailable
			code := "feishu_registration_actor_unavailable"
			message := "Administrator tenant validation is temporarily unavailable."
			if errors.Is(err, agentcore.ErrNotFound) {
				status = http.StatusForbidden
				code = "feishu_registration_actor_profile_not_found"
				message = "The authenticated administrator profile was not found."
			}
			writeJSON(w, status, ErrorResponse{Code: code, Message: message, CorrelationID: correlationID(req)})
			return "", "", false
		}
		profileTenantID := strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID))
		if tenantID != "" && profileTenantID != "" && tenantID != profileTenantID {
			writeJSON(w, http.StatusForbidden, ErrorResponse{Code: "feishu_registration_tenant_mismatch", Message: "Authenticated tenant does not match the administrator organization.", CorrelationID: correlationID(req)})
			return "", "", false
		}
		if tenantID == "" {
			tenantID = profileTenantID
		}
	}
	ownerID := firstNonEmpty(principal.UserID, principal.Username, principal.Subject)
	if tenantID == "" || ownerID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "feishu_registration_actor_invalid", Message: "Authenticated tenant and administrator are required.", CorrelationID: correlationID(req)})
		return "", "", false
	}
	return tenantID, ownerID, true
}

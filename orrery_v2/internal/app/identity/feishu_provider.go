package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultFeishuAuthorizeURL = "https://accounts.feishu.cn/open-apis/authen/v1/authorize"
	defaultFeishuTokenURL     = "https://open.feishu.cn/open-apis/authen/v2/oauth/token"
	defaultFeishuUserInfoURL  = "https://open.feishu.cn/open-apis/authen/v1/user_info"
	feishuOAuthStateTTL       = 10 * time.Minute
	maxFeishuResponseBytes    = 1 << 20
)

type FeishuProviderConfig struct {
	AppID              string
	AppSecret          string
	CallbackURL        string
	BindingCallbackURL string
	StateSecret        string
	AllowedTenantKey   string
	AuthorizeURL       string
	TokenURL           string
	UserInfoURL        string
	HTTPClient         *http.Client
}

type FeishuIdentityProvider struct {
	appID              string
	appSecret          string
	callbackURL        *url.URL
	bindingCallbackURL *url.URL
	stateKey           []byte
	allowedTenantKey   string
	authorizeURL       *url.URL
	tokenURL           *url.URL
	userInfoURL        *url.URL
	httpClient         *http.Client
	now                func() time.Time
}

type feishuOAuthState struct {
	Version        int    `json:"v"`
	Mode           string `json:"mode"`
	Nonce          string `json:"nonce"`
	ReturnTo       string `json:"return_to,omitempty"`
	AgentProfileID string `json:"agent_profile_id,omitempty"`
	ExpiresAt      int64  `json:"expires_at"`
}

func NewFeishuIdentityProvider(cfg FeishuProviderConfig) (*FeishuIdentityProvider, error) {
	appID := strings.TrimSpace(cfg.AppID)
	appSecret := strings.TrimSpace(cfg.AppSecret)
	stateSecret := strings.TrimSpace(cfg.StateSecret)
	if appID == "" || appSecret == "" || stateSecret == "" {
		return nil, fmt.Errorf("%w: feishu app_id, app_secret and state_secret are required", ErrInvalidRequest)
	}
	callbackURL, err := parseBaseURL(cfg.CallbackURL)
	if err != nil {
		return nil, fmt.Errorf("%w: feishu callback_url is invalid: %v", ErrInvalidRequest, err)
	}
	bindingCallbackURL, err := parseBaseURL(cfg.BindingCallbackURL)
	if err != nil {
		return nil, fmt.Errorf("%w: feishu binding_callback_url is invalid: %v", ErrInvalidRequest, err)
	}
	authorizeURL, err := parseBaseURL(firstNonEmpty(cfg.AuthorizeURL, defaultFeishuAuthorizeURL))
	if err != nil {
		return nil, fmt.Errorf("%w: feishu authorize_url is invalid: %v", ErrInvalidRequest, err)
	}
	tokenURL, err := parseBaseURL(firstNonEmpty(cfg.TokenURL, defaultFeishuTokenURL))
	if err != nil {
		return nil, fmt.Errorf("%w: feishu token_url is invalid: %v", ErrInvalidRequest, err)
	}
	userInfoURL, err := parseBaseURL(firstNonEmpty(cfg.UserInfoURL, defaultFeishuUserInfoURL))
	if err != nil {
		return nil, fmt.Errorf("%w: feishu user_info_url is invalid: %v", ErrInvalidRequest, err)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	stateKey := sha256.Sum256([]byte("orrery:feishu:oauth-state:v1\n" + stateSecret))
	return &FeishuIdentityProvider{
		appID:              appID,
		appSecret:          appSecret,
		callbackURL:        callbackURL,
		bindingCallbackURL: bindingCallbackURL,
		stateKey:           stateKey[:],
		allowedTenantKey:   strings.TrimSpace(cfg.AllowedTenantKey),
		authorizeURL:       authorizeURL,
		tokenURL:           tokenURL,
		userInfoURL:        userInfoURL,
		httpClient:         client,
		now:                func() time.Time { return time.Now().UTC() },
	}, nil
}

func (p *FeishuIdentityProvider) Name() string { return ProviderFeishu }

func (p *FeishuIdentityProvider) DisplayName() string { return "飞书" }

func (p *FeishuIdentityProvider) BeginBrowserLogin(ctx context.Context, req BeginLoginRequest) (LoginRedirect, error) {
	return p.begin(ctx, "login", req.ReturnTo, "")
}

func (p *FeishuIdentityProvider) BeginBrowserBinding(ctx context.Context, req BeginBindingRequest) (LoginRedirect, error) {
	profileID := strings.TrimSpace(req.AgentProfileID)
	if profileID == "" {
		return LoginRedirect{}, fmt.Errorf("%w: agent profile is required", ErrInvalidRequest)
	}
	return p.begin(ctx, "binding", req.ReturnTo, profileID)
}

func (p *FeishuIdentityProvider) begin(ctx context.Context, mode, returnTo, agentProfileID string) (LoginRedirect, error) {
	if err := ctx.Err(); err != nil {
		return LoginRedirect{}, err
	}
	if p == nil || p.authorizeURL == nil || p.callbackURL == nil || p.bindingCallbackURL == nil || len(p.stateKey) == 0 {
		return LoginRedirect{}, fmt.Errorf("%w: feishu provider is not configured", ErrServiceDependency)
	}
	if !safeRelativeReturnTo(returnTo) {
		returnTo = "/"
	}
	nonceBytes := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, nonceBytes); err != nil {
		return LoginRedirect{}, fmt.Errorf("%w: generate feishu oauth state", ErrServiceUnavailable)
	}
	expiresAt := p.currentTime().Add(feishuOAuthStateTTL)
	state, err := p.signState(feishuOAuthState{
		Version:        1,
		Mode:           mode,
		Nonce:          base64.RawURLEncoding.EncodeToString(nonceBytes),
		ReturnTo:       returnTo,
		AgentProfileID: strings.TrimSpace(agentProfileID),
		ExpiresAt:      expiresAt.Unix(),
	})
	if err != nil {
		return LoginRedirect{}, err
	}
	redirectURI := p.callbackURL
	if mode == "binding" {
		redirectURI = p.bindingCallbackURL
	}
	authorize := *p.authorizeURL
	query := authorize.Query()
	query.Set("app_id", p.appID)
	query.Set("redirect_uri", redirectURI.String())
	query.Set("state", state)
	authorize.RawQuery = query.Encode()
	return LoginRedirect{URL: authorize.String(), FlowState: state, ExpiresAt: expiresAt}, nil
}

func (p *FeishuIdentityProvider) CompleteBrowserLogin(ctx context.Context, req CompleteLoginRequest) (ExternalIdentity, error) {
	return p.complete(ctx, req, "login", p.callbackURL)
}

func (p *FeishuIdentityProvider) CompleteBrowserBinding(ctx context.Context, req CompleteLoginRequest) (ExternalIdentity, error) {
	return p.complete(ctx, req, "binding", p.bindingCallbackURL)
}

func (p *FeishuIdentityProvider) complete(ctx context.Context, req CompleteLoginRequest, mode string, redirectURI *url.URL) (ExternalIdentity, error) {
	if err := ctx.Err(); err != nil {
		return ExternalIdentity{}, err
	}
	if p == nil || redirectURI == nil || p.tokenURL == nil || p.userInfoURL == nil || p.httpClient == nil {
		return ExternalIdentity{}, fmt.Errorf("%w: feishu provider is not configured", ErrServiceDependency)
	}
	code := strings.TrimSpace(req.Code)
	stateToken := strings.TrimSpace(req.State)
	if code == "" || stateToken == "" {
		return ExternalIdentity{}, ErrUnauthorized
	}
	state, err := p.parseState(stateToken)
	if err != nil {
		return ExternalIdentity{}, err
	}
	if state.Mode != mode || (mode == "binding" && strings.TrimSpace(state.AgentProfileID) == "") {
		return ExternalIdentity{}, ErrUnauthorized
	}
	if !flowCookieMatches(req.Cookie, BrowserFlowCookieName(ProviderFeishu), stateToken) {
		return ExternalIdentity{}, ErrFlowCookieInvalid
	}
	accessToken, err := p.exchangeCode(ctx, code, redirectURI.String())
	if err != nil {
		return ExternalIdentity{}, err
	}
	external, err := p.fetchUserInfo(ctx, accessToken)
	if err != nil {
		return ExternalIdentity{}, err
	}
	if mode == "binding" {
		if external.Traits == nil {
			external.Traits = map[string]string{}
		}
		external.Traits["binding_agent_profile_id"] = state.AgentProfileID
	}
	return NormalizeExternalIdentity(external), nil
}

func (p *FeishuIdentityProvider) CallbackReturnTo(stateToken string) string {
	state, err := p.parseState(stateToken)
	if err != nil || !safeRelativeReturnTo(state.ReturnTo) {
		return "/"
	}
	return state.ReturnTo
}

func (p *FeishuIdentityProvider) BindingReturnTo(stateToken string) string {
	state, err := p.parseState(stateToken)
	if err != nil || state.Mode != "binding" || !safeRelativeReturnTo(state.ReturnTo) {
		return "/profile"
	}
	return state.ReturnTo
}

func (p *FeishuIdentityProvider) exchangeCode(ctx context.Context, code, redirectURI string) (string, error) {
	payload := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     p.appID,
		"client_secret": p.appSecret,
		"code":          code,
		"redirect_uri":  redirectURI,
	}
	var response struct {
		Code        int    `json:"code"`
		Message     string `json:"msg"`
		AccessToken string `json:"access_token"`
		Data        struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := p.doJSON(ctx, http.MethodPost, p.tokenURL, "", payload, &response); err != nil {
		return "", err
	}
	if response.Code != 0 {
		return "", ErrUnauthorized
	}
	accessToken := firstNonEmpty(response.Data.AccessToken, response.AccessToken)
	if accessToken == "" {
		return "", fmt.Errorf("%w: feishu token response is incomplete", ErrServiceUnavailable)
	}
	return accessToken, nil
}

func (p *FeishuIdentityProvider) fetchUserInfo(ctx context.Context, accessToken string) (ExternalIdentity, error) {
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"msg"`
		Data    struct {
			Name            string `json:"name"`
			EnglishName     string `json:"en_name"`
			OpenID          string `json:"open_id"`
			UnionID         string `json:"union_id"`
			UserID          string `json:"user_id"`
			Email           string `json:"email"`
			EnterpriseEmail string `json:"enterprise_email"`
			TenantKey       string `json:"tenant_key"`
		} `json:"data"`
	}
	if err := p.doJSON(ctx, http.MethodGet, p.userInfoURL, accessToken, nil, &response); err != nil {
		return ExternalIdentity{}, err
	}
	if response.Code != 0 {
		return ExternalIdentity{}, ErrUnauthorized
	}
	tenantKey := strings.TrimSpace(response.Data.TenantKey)
	if p.allowedTenantKey != "" && !strings.EqualFold(p.allowedTenantKey, tenantKey) {
		return ExternalIdentity{}, ErrUnauthorized
	}
	stableID := firstNonEmpty(response.Data.UnionID, response.Data.OpenID)
	if stableID == "" {
		return ExternalIdentity{}, fmt.Errorf("%w: feishu user identity is incomplete", ErrServiceUnavailable)
	}
	subject := stableID
	if tenantKey != "" {
		subject = tenantKey + ":" + stableID
	}
	email := firstNonEmpty(response.Data.EnterpriseEmail, response.Data.Email)
	username := firstNonEmpty(email, response.Data.UserID, response.Data.OpenID, stableID)
	return ExternalIdentity{
		Provider:    ProviderFeishu,
		Subject:     subject,
		Username:    username,
		Email:       email,
		DisplayName: firstNonEmpty(response.Data.Name, response.Data.EnglishName, username),
		Traits: map[string]string{
			"tenant_key": tenantKey,
			"open_id":    strings.TrimSpace(response.Data.OpenID),
			"union_id":   strings.TrimSpace(response.Data.UnionID),
			"user_id":    strings.TrimSpace(response.Data.UserID),
		},
	}, nil
}

func (p *FeishuIdentityProvider) doJSON(ctx context.Context, method string, endpoint *url.URL, bearer string, body any, output any) error {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		requestBody = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), requestBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(bearer) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: feishu identity request failed", ErrServiceUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: feishu identity service returned status %d", ErrServiceUnavailable, resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxFeishuResponseBytes))
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("%w: decode feishu identity response", ErrServiceUnavailable)
	}
	return nil
}

func (p *FeishuIdentityProvider) signState(state feishuOAuthState) (string, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, p.stateKey)
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature, nil
}

func (p *FeishuIdentityProvider) parseState(token string) (feishuOAuthState, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if p == nil || len(p.stateKey) == 0 || len(parts) != 2 {
		return feishuOAuthState{}, ErrUnauthorized
	}
	mac := hmac.New(sha256.New, p.stateKey)
	_, _ = mac.Write([]byte(parts[0]))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(got, want) {
		return feishuOAuthState{}, ErrUnauthorized
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return feishuOAuthState{}, ErrUnauthorized
	}
	var state feishuOAuthState
	if err := json.Unmarshal(payload, &state); err != nil || state.Version != 1 || strings.TrimSpace(state.Nonce) == "" {
		return feishuOAuthState{}, ErrUnauthorized
	}
	if state.ExpiresAt <= 0 || !p.currentTime().Before(time.Unix(state.ExpiresAt, 0).UTC()) {
		return feishuOAuthState{}, ErrFlowExpired
	}
	return state, nil
}

func (p *FeishuIdentityProvider) currentTime() time.Time {
	if p != nil && p.now != nil {
		return p.now().UTC()
	}
	return time.Now().UTC()
}

func BrowserFlowCookieName(provider string) string {
	provider = normalizeLower(provider)
	if provider == "" {
		provider = "identity"
	}
	return "orrery_identity_" + provider + "_flow"
}

func flowCookieMatches(rawCookie, name, expected string) bool {
	if strings.TrimSpace(rawCookie) == "" || strings.TrimSpace(expected) == "" {
		return false
	}
	request := &http.Request{Header: http.Header{"Cookie": []string{rawCookie}}}
	cookie, err := request.Cookie(name)
	if err != nil || len(cookie.Value) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(expected)) == 1
}

var _ BrowserIdentityBindingProvider = (*FeishuIdentityProvider)(nil)

package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type KratosProviderConfig struct {
	PublicURL          string
	CallbackURL        string
	DisplayName        string
	SessionCookieNames []string
	HTTPClient         *http.Client
}

type KratosIdentityProvider struct {
	publicURL          *url.URL
	callbackURL        *url.URL
	displayName        string
	httpClient         *http.Client
	sessionCookieNames map[string]struct{}
}

func NewKratosIdentityProvider(cfg KratosProviderConfig) (*KratosIdentityProvider, error) {
	publicURL, err := parseBaseURL(cfg.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("%w: kratos public_url is invalid: %v", ErrInvalidRequest, err)
	}
	callbackURL, err := parseBaseURL(cfg.CallbackURL)
	if err != nil {
		return nil, fmt.Errorf("%w: kratos callback_url is invalid: %v", ErrInvalidRequest, err)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	displayName := strings.TrimSpace(cfg.DisplayName)
	if displayName == "" {
		displayName = "Ory Kratos"
	}
	cookieNames := map[string]struct{}{}
	for _, name := range cfg.SessionCookieNames {
		if name = strings.TrimSpace(name); name != "" {
			cookieNames[name] = struct{}{}
		}
	}
	if len(cookieNames) == 0 {
		cookieNames["ory_kratos_session"] = struct{}{}
	}
	return &KratosIdentityProvider{
		publicURL:          publicURL,
		callbackURL:        callbackURL,
		displayName:        displayName,
		httpClient:         client,
		sessionCookieNames: cookieNames,
	}, nil
}

func (p *KratosIdentityProvider) Name() string {
	return ProviderKratos
}

func (p *KratosIdentityProvider) DisplayName() string {
	if p == nil || strings.TrimSpace(p.displayName) == "" {
		return "Ory Kratos"
	}
	return p.displayName
}

func (p *KratosIdentityProvider) BeginBrowserLogin(ctx context.Context, req BeginLoginRequest) (LoginRedirect, error) {
	if err := ctx.Err(); err != nil {
		return LoginRedirect{}, err
	}
	if p == nil || p.publicURL == nil || p.callbackURL == nil {
		return LoginRedirect{}, fmt.Errorf("%w: kratos provider is not configured", ErrInvalidRequest)
	}
	loginURL := p.resolvePublicPath("/self-service/login/browser")
	query := loginURL.Query()
	query.Set("return_to", p.callbackWithReturnTo(req.ReturnTo))
	loginURL.RawQuery = query.Encode()
	return LoginRedirect{URL: loginURL.String()}, nil
}

func (p *KratosIdentityProvider) BeginBrowserLogout(ctx context.Context, req BeginLogoutRequest) (LoginRedirect, error) {
	if err := ctx.Err(); err != nil {
		return LoginRedirect{}, err
	}
	if p == nil || p.publicURL == nil || p.callbackURL == nil {
		return LoginRedirect{}, fmt.Errorf("%w: kratos provider is not configured", ErrInvalidRequest)
	}
	returnTo := strings.TrimSpace(req.ReturnTo)
	if !safeRelativeReturnTo(returnTo) {
		returnTo = "/login"
	}
	returnURL, err := url.Parse(returnTo)
	if err != nil {
		return LoginRedirect{}, fmt.Errorf("%w: logout return_to is invalid", ErrInvalidRequest)
	}
	returnURL.Scheme = p.callbackURL.Scheme
	returnURL.Host = p.callbackURL.Host
	cookie := p.kratosCookieHeader(req.Cookie)
	if cookie == "" {
		return LoginRedirect{URL: returnURL.String()}, nil
	}

	logoutURL := p.resolvePublicPath("/self-service/logout/browser")
	query := logoutURL.Query()
	query.Set("return_to", returnURL.String())
	logoutURL.RawQuery = query.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, logoutURL.String(), nil)
	if err != nil {
		return LoginRedirect{}, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Cookie", cookie)
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return LoginRedirect{}, fmt.Errorf("kratos browser logout: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return LoginRedirect{URL: returnURL.String()}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return LoginRedirect{}, fmt.Errorf("kratos browser logout returned status %d", resp.StatusCode)
	}
	var flow struct {
		LogoutURL string `json:"logout_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&flow); err != nil {
		return LoginRedirect{}, fmt.Errorf("decode kratos browser logout: %w", err)
	}
	resolved, err := url.Parse(strings.TrimSpace(flow.LogoutURL))
	if err != nil || (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Host == "" {
		return LoginRedirect{}, fmt.Errorf("kratos browser logout returned an invalid logout_url")
	}
	return LoginRedirect{URL: resolved.String()}, nil
}

func (p *KratosIdentityProvider) CompleteBrowserLogin(ctx context.Context, req CompleteLoginRequest) (ExternalIdentity, error) {
	if err := ctx.Err(); err != nil {
		return ExternalIdentity{}, err
	}
	if p == nil || p.publicURL == nil || p.httpClient == nil {
		return ExternalIdentity{}, fmt.Errorf("%w: kratos provider is not configured", ErrInvalidRequest)
	}
	cookie := p.kratosCookieHeader(req.Cookie)
	if cookie == "" {
		return ExternalIdentity{}, ErrBrowserSessionUnavailable
	}
	whoamiURL := p.resolvePublicPath("/sessions/whoami")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, whoamiURL.String(), nil)
	if err != nil {
		return ExternalIdentity{}, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Cookie", cookie)
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("kratos whoami: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ExternalIdentity{}, ErrBrowserSessionUnavailable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ExternalIdentity{}, fmt.Errorf("kratos whoami returned status %d", resp.StatusCode)
	}
	var session kratosSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return ExternalIdentity{}, fmt.Errorf("decode kratos whoami: %w", err)
	}
	if !session.Active || strings.TrimSpace(session.Identity.ID) == "" {
		return ExternalIdentity{}, ErrBrowserSessionUnavailable
	}
	if state := strings.ToLower(strings.TrimSpace(session.Identity.State)); state != "" && state != "active" {
		return ExternalIdentity{}, ErrBrowserSessionUnavailable
	}
	traits := map[string]string{}
	if agentProfileID := traitString(session.Identity.Traits, "agent_profile_id", "agentProfileId"); agentProfileID != "" {
		traits["agent_profile_id"] = agentProfileID
	}
	if personID := traitString(session.Identity.Traits, "person_id"); personID != "" {
		traits["person_id"] = personID
	}
	if personRevision, ok := traitPositiveInt64(session.Identity.Traits, "person_revision"); ok {
		traits["person_revision"] = strconv.FormatInt(personRevision, 10)
	}
	external := ExternalIdentity{
		Provider:    ProviderKratos,
		Subject:     session.Identity.ID,
		Username:    traitString(session.Identity.Traits, "username", "preferred_username", "email"),
		Email:       traitString(session.Identity.Traits, "email"),
		DisplayName: firstNonEmpty(traitString(session.Identity.Traits, "display_name", "name"), traitString(session.Identity.Traits, "username", "email")),
		Traits:      traits,
		ExpiresAt:   session.ExpiresAtValue(),
	}
	return NormalizeExternalIdentity(external), nil
}

func (p *KratosIdentityProvider) kratosCookieHeader(raw string) string {
	if p == nil || len(p.sessionCookieNames) == 0 || strings.TrimSpace(raw) == "" {
		return ""
	}
	request := &http.Request{Header: http.Header{"Cookie": []string{raw}}}
	selected := make([]string, 0, len(p.sessionCookieNames))
	for _, cookie := range request.Cookies() {
		if _, ok := p.sessionCookieNames[cookie.Name]; !ok {
			continue
		}
		selected = append(selected, cookie.String())
	}
	return strings.Join(selected, "; ")
}

func (p *KratosIdentityProvider) kratosLoginFlowCookieHeader(raw string) string {
	if p == nil || strings.TrimSpace(raw) == "" {
		return ""
	}
	request := &http.Request{Header: http.Header{"Cookie": []string{raw}}}
	selected := make([]string, 0, len(p.sessionCookieNames)+1)
	for _, cookie := range request.Cookies() {
		_, sessionCookie := p.sessionCookieNames[cookie.Name]
		if !sessionCookie && !strings.HasPrefix(cookie.Name, "csrf_token_") {
			continue
		}
		selected = append(selected, cookie.String())
	}
	return strings.Join(selected, "; ")
}

func (p *KratosIdentityProvider) resolvePublicPath(path string) *url.URL {
	resolved := *p.publicURL
	basePath := strings.TrimRight(resolved.Path, "/")
	resolved.Path = basePath + "/" + strings.TrimLeft(path, "/")
	return &resolved
}

func (p *KratosIdentityProvider) callbackWithReturnTo(returnTo string) string {
	callback := *p.callbackURL
	if safeRelativeReturnTo(returnTo) {
		query := callback.Query()
		query.Set("return_to", strings.TrimSpace(returnTo))
		callback.RawQuery = query.Encode()
	}
	return callback.String()
}

func parseBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("url must use http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("url host is required")
	}
	return parsed, nil
}

func safeRelativeReturnTo(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.Contains(value, "\\")
}

func traitString(traits map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := traits[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				return trimmed
			}
		case fmt.Stringer:
			if trimmed := strings.TrimSpace(typed.String()); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func traitPositiveInt64(traits map[string]any, key string) (int64, bool) {
	value, ok := traits[key]
	if !ok || value == nil {
		return 0, false
	}
	var parsed int64
	switch typed := value.(type) {
	case float64:
		if typed != math.Trunc(typed) || typed > math.MaxInt64 {
			return 0, false
		}
		parsed = int64(typed)
	case string:
		var err error
		parsed, err = strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return parsed, parsed > 0
}

type kratosSession struct {
	Active    bool           `json:"active"`
	ExpiresAt *time.Time     `json:"expires_at"`
	Identity  kratosIdentity `json:"identity"`
}

func (s kratosSession) ExpiresAtValue() time.Time {
	if s.ExpiresAt == nil {
		return time.Time{}
	}
	return s.ExpiresAt.UTC()
}

type kratosIdentity struct {
	ID     string         `json:"id"`
	State  string         `json:"state"`
	Traits map[string]any `json:"traits"`
}

var _ BrowserIdentityProvider = (*KratosIdentityProvider)(nil)

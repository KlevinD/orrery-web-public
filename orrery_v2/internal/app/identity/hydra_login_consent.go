package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OAuth2Redirect struct {
	RedirectTo string `json:"redirect_to"`
}

type OAuth2ConsentSummary struct {
	Challenge         string
	Subject           string
	Client            OIDCClient
	RequestedScopes   []string
	GrantScopes       []string
	RequestedAudience []string
	GrantAudience     []string
}

type HydraAdminConfig struct {
	AdminURL    string
	AdminToken  string
	HTTPClient  *http.Client
	RememberFor time.Duration
}

type HydraAdminClient struct {
	adminURL    *url.URL
	adminToken  string
	httpClient  *http.Client
	rememberFor time.Duration
}

type HydraAdminStatusError struct {
	StatusCode int
}

func (e HydraAdminStatusError) Error() string {
	return fmt.Sprintf("hydra admin returned status %d", e.StatusCode)
}

type OIDCLoginConsentService struct {
	hydra   *HydraAdminClient
	clients *OIDCClientRegistry
}

func NewHydraAdminClient(cfg HydraAdminConfig) (*HydraAdminClient, error) {
	adminURL, err := parseBaseURL(cfg.AdminURL)
	if err != nil {
		return nil, fmt.Errorf("%w: hydra admin_url is invalid: %v", ErrInvalidRequest, err)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	rememberFor := cfg.RememberFor
	if rememberFor <= 0 {
		rememberFor = 24 * time.Hour
	}
	return &HydraAdminClient{
		adminURL:    adminURL,
		adminToken:  strings.TrimSpace(cfg.AdminToken),
		httpClient:  client,
		rememberFor: rememberFor,
	}, nil
}

func NewOIDCLoginConsentService(hydra *HydraAdminClient, clients *OIDCClientRegistry) *OIDCLoginConsentService {
	if clients == nil {
		clients = NewDefaultOIDCClientRegistry()
	}
	return &OIDCLoginConsentService{hydra: hydra, clients: clients}
}

func (s *OIDCLoginConsentService) AcceptLogin(ctx context.Context, challenge string, principal Principal) (OAuth2Redirect, error) {
	if s == nil || s.hydra == nil {
		return OAuth2Redirect{}, fmt.Errorf("%w: oidc login consent service is not configured", ErrInvalidRequest)
	}
	principal = NormalizePrincipal(principal)
	if !principal.Authenticated() {
		return OAuth2Redirect{}, ErrUnauthorized
	}
	login, err := s.hydra.GetLoginRequest(ctx, challenge)
	if err != nil {
		return OAuth2Redirect{}, err
	}
	client, ok := s.clients.Client(login.Client.ClientID)
	if !ok {
		return OAuth2Redirect{}, fmt.Errorf("%w: oauth2 client is not registered", ErrInvalidRequest)
	}
	if err := validatePublicClientPKCE(client, login.RequestURL); err != nil {
		return OAuth2Redirect{}, err
	}
	subject := oauth2SubjectFromPrincipal(principal)
	if subject == "" {
		return OAuth2Redirect{}, ErrUnauthorized
	}
	if login.Skip {
		skipSubject := strings.TrimSpace(login.Subject)
		if skipSubject != "" && skipSubject != subject {
			return OAuth2Redirect{}, ErrUnauthorized
		}
	}
	return s.hydra.AcceptLoginRequest(ctx, challenge, HydraAcceptLoginRequest{
		Subject:     subject,
		Remember:    true,
		RememberFor: int(s.hydra.rememberFor.Seconds()),
		Context:     oauth2SessionClaims(principal),
	})
}

func (s *OIDCLoginConsentService) AcceptConsent(ctx context.Context, challenge string, principal Principal) (OAuth2Redirect, error) {
	return s.acceptConsent(ctx, challenge, principal, false)
}

func (s *OIDCLoginConsentService) ApproveConsent(ctx context.Context, challenge string, principal Principal) (OAuth2Redirect, error) {
	return s.acceptConsent(ctx, challenge, principal, true)
}

func (s *OIDCLoginConsentService) PreviewConsent(ctx context.Context, challenge string, principal Principal) (OAuth2ConsentSummary, error) {
	if s == nil || s.hydra == nil || s.clients == nil {
		return OAuth2ConsentSummary{}, fmt.Errorf("%w: oidc login consent service is not configured", ErrInvalidRequest)
	}
	principal = NormalizePrincipal(principal)
	if !principal.Authenticated() {
		return OAuth2ConsentSummary{}, ErrUnauthorized
	}
	consent, err := s.hydra.GetConsentRequest(ctx, challenge)
	if err != nil {
		return OAuth2ConsentSummary{}, err
	}
	if err := validateConsentSubject(consent, principal); err != nil {
		return OAuth2ConsentSummary{}, err
	}
	grant, err := s.consentGrant(consent, principal, true)
	if err != nil {
		return OAuth2ConsentSummary{}, err
	}
	return OAuth2ConsentSummary{
		Challenge:         firstNonEmpty(consent.Challenge, challenge),
		Subject:           consent.Subject,
		Client:            grant.Client,
		RequestedScopes:   cleanList(consent.RequestedScope, true),
		GrantScopes:       grant.Scopes,
		RequestedAudience: cleanList(consent.RequestedAccessTokenAudience, false),
		GrantAudience:     grant.Audience,
	}, nil
}

func (s *OIDCLoginConsentService) RejectConsent(ctx context.Context, challenge string, principal Principal) (OAuth2Redirect, error) {
	if s == nil || s.hydra == nil || s.clients == nil {
		return OAuth2Redirect{}, fmt.Errorf("%w: oidc login consent service is not configured", ErrInvalidRequest)
	}
	principal = NormalizePrincipal(principal)
	if !principal.Authenticated() {
		return OAuth2Redirect{}, ErrUnauthorized
	}
	consent, err := s.hydra.GetConsentRequest(ctx, challenge)
	if err != nil {
		return OAuth2Redirect{}, err
	}
	if err := validateConsentSubject(consent, principal); err != nil {
		return OAuth2Redirect{}, err
	}
	if _, ok := s.clients.Client(consent.Client.ClientID); !ok {
		return OAuth2Redirect{}, fmt.Errorf("%w: oauth2 client is not registered", ErrInvalidRequest)
	}
	return s.hydra.RejectConsentRequest(ctx, challenge, HydraRejectConsentRequest{
		Error:            "access_denied",
		ErrorDescription: "The resource owner denied the consent request.",
	})
}

func (s *OIDCLoginConsentService) acceptConsent(ctx context.Context, challenge string, principal Principal, approved bool) (OAuth2Redirect, error) {
	if s == nil || s.hydra == nil || s.clients == nil {
		return OAuth2Redirect{}, fmt.Errorf("%w: oidc login consent service is not configured", ErrInvalidRequest)
	}
	principal = NormalizePrincipal(principal)
	if !principal.Authenticated() {
		return OAuth2Redirect{}, ErrUnauthorized
	}
	consent, err := s.hydra.GetConsentRequest(ctx, challenge)
	if err != nil {
		return OAuth2Redirect{}, err
	}
	if err := validateConsentSubject(consent, principal); err != nil {
		return OAuth2Redirect{}, err
	}
	grant, err := s.consentGrant(consent, principal, approved)
	if err != nil {
		return OAuth2Redirect{}, err
	}
	session := HydraConsentSession{
		AccessToken: oauth2SessionClaims(principal),
		IDToken:     oauth2IDTokenClaims(principal, grant.Scopes),
	}
	return s.hydra.AcceptConsentRequest(ctx, challenge, HydraAcceptConsentRequest{
		GrantScope:               grant.Scopes,
		GrantAccessTokenAudience: grant.Audience,
		Remember:                 true,
		RememberFor:              int(s.hydra.rememberFor.Seconds()),
		Session:                  session,
	})
}

func (s *OIDCLoginConsentService) consentGrant(consent HydraConsentRequest, principal Principal, approved bool) (ConsentGrant, error) {
	return s.clients.Grant(ConsentGrantRequest{
		ClientID:          consent.Client.ClientID,
		RequestedScopes:   consent.RequestedScope,
		RequestedAudience: consent.RequestedAccessTokenAudience,
		Principal:         principal,
		Skip:              consent.Skip,
		Approved:          approved,
	})
}

func validateConsentSubject(consent HydraConsentRequest, principal Principal) error {
	subject := strings.TrimSpace(consent.Subject)
	expected := strings.TrimSpace(oauth2SubjectFromPrincipal(principal))
	if subject == "" || expected == "" {
		return ErrUnauthorized
	}
	if subject != expected {
		return ErrUnauthorized
	}
	return nil
}

type HydraLoginRequest struct {
	Challenge  string            `json:"challenge"`
	Subject    string            `json:"subject"`
	Skip       bool              `json:"skip"`
	Client     HydraClientRef    `json:"client"`
	RequestURL string            `json:"request_url"`
	Context    map[string]any    `json:"context"`
	OIDC       map[string]any    `json:"oidc_context"`
	Ext        map[string]string `json:"-"`
}

type HydraConsentRequest struct {
	Challenge                    string         `json:"challenge"`
	Subject                      string         `json:"subject"`
	Skip                         bool           `json:"skip"`
	Client                       HydraClientRef `json:"client"`
	RequestedScope               []string       `json:"requested_scope"`
	RequestedAccessTokenAudience []string       `json:"requested_access_token_audience"`
}

type HydraClientRef struct {
	ClientID string `json:"client_id"`
	Name     string `json:"client_name"`
}

type HydraOAuth2Client struct {
	ClientID                string         `json:"client_id,omitempty"`
	ClientName              string         `json:"client_name,omitempty"`
	ClientSecret            string         `json:"client_secret,omitempty"`
	RedirectURIs            []string       `json:"redirect_uris,omitempty"`
	GrantTypes              []string       `json:"grant_types,omitempty"`
	ResponseTypes           []string       `json:"response_types,omitempty"`
	Scope                   string         `json:"scope,omitempty"`
	Audience                []string       `json:"audience,omitempty"`
	TokenEndpointAuthMethod string         `json:"token_endpoint_auth_method,omitempty"`
	SkipConsent             bool           `json:"skip_consent,omitempty"`
	Metadata                map[string]any `json:"metadata,omitempty"`
}

type HydraAcceptLoginRequest struct {
	Subject     string         `json:"subject"`
	Remember    bool           `json:"remember"`
	RememberFor int            `json:"remember_for,omitempty"`
	Context     map[string]any `json:"context,omitempty"`
}

type HydraAcceptConsentRequest struct {
	GrantScope               []string            `json:"grant_scope"`
	GrantAccessTokenAudience []string            `json:"grant_access_token_audience,omitempty"`
	Remember                 bool                `json:"remember"`
	RememberFor              int                 `json:"remember_for,omitempty"`
	Session                  HydraConsentSession `json:"session,omitempty"`
}

type HydraRejectConsentRequest struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type HydraConsentSession struct {
	AccessToken map[string]any `json:"access_token,omitempty"`
	IDToken     map[string]any `json:"id_token,omitempty"`
}

func (c *HydraAdminClient) GetLoginRequest(ctx context.Context, challenge string) (HydraLoginRequest, error) {
	var out HydraLoginRequest
	err := c.do(ctx, http.MethodGet, "/admin/oauth2/auth/requests/login", map[string]string{"login_challenge": challenge}, nil, &out)
	return out, err
}

func (c *HydraAdminClient) AcceptLoginRequest(ctx context.Context, challenge string, body HydraAcceptLoginRequest) (OAuth2Redirect, error) {
	var out OAuth2Redirect
	err := c.do(ctx, http.MethodPut, "/admin/oauth2/auth/requests/login/accept", map[string]string{"login_challenge": challenge}, body, &out)
	return out, err
}

func (c *HydraAdminClient) GetConsentRequest(ctx context.Context, challenge string) (HydraConsentRequest, error) {
	var out HydraConsentRequest
	err := c.do(ctx, http.MethodGet, "/admin/oauth2/auth/requests/consent", map[string]string{"consent_challenge": challenge}, nil, &out)
	return out, err
}

func (c *HydraAdminClient) AcceptConsentRequest(ctx context.Context, challenge string, body HydraAcceptConsentRequest) (OAuth2Redirect, error) {
	var out OAuth2Redirect
	err := c.do(ctx, http.MethodPut, "/admin/oauth2/auth/requests/consent/accept", map[string]string{"consent_challenge": challenge}, body, &out)
	return out, err
}

func (c *HydraAdminClient) RejectConsentRequest(ctx context.Context, challenge string, body HydraRejectConsentRequest) (OAuth2Redirect, error) {
	var out OAuth2Redirect
	err := c.do(ctx, http.MethodPut, "/admin/oauth2/auth/requests/consent/reject", map[string]string{"consent_challenge": challenge}, body, &out)
	return out, err
}

func (c *HydraAdminClient) RevokeSubjectSessions(ctx context.Context, subject string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return fmt.Errorf("%w: oauth2 subject is required", ErrInvalidRequest)
	}
	loginErr := c.do(ctx, http.MethodDelete, "/admin/oauth2/auth/sessions/login", map[string]string{
		"subject": subject,
	}, nil, nil)
	consentErr := c.do(ctx, http.MethodDelete, "/admin/oauth2/auth/sessions/consent", map[string]string{
		"subject": subject,
		"all":     "true",
	}, nil, nil)
	return errors.Join(loginErr, consentErr)
}

func (c *HydraAdminClient) UpsertClient(ctx context.Context, client OIDCClient) (OIDCClient, error) {
	client = normalizeOIDCClient(client)
	if err := validateHydraOAuth2Client(client); err != nil {
		return OIDCClient{}, err
	}
	body := hydraOAuth2ClientFromOIDCClient(client)
	var existing HydraOAuth2Client
	err := c.do(ctx, http.MethodGet, "/admin/clients/"+url.PathEscape(client.ClientID), nil, nil, &existing)
	if err != nil {
		var status HydraAdminStatusError
		if !errors.As(err, &status) || status.StatusCode != http.StatusNotFound {
			return OIDCClient{}, err
		}
		var created HydraOAuth2Client
		if err := c.do(ctx, http.MethodPost, "/admin/clients", nil, body, &created); err != nil {
			return OIDCClient{}, err
		}
		return oidcClientFromHydraOAuth2Client(client, created), nil
	}
	var updated HydraOAuth2Client
	if err := c.do(ctx, http.MethodPut, "/admin/clients/"+url.PathEscape(client.ClientID), nil, body, &updated); err != nil {
		return OIDCClient{}, err
	}
	return oidcClientFromHydraOAuth2Client(client, updated), nil
}

func (c *HydraAdminClient) do(ctx context.Context, method string, path string, query map[string]string, body any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.adminURL == nil || c.httpClient == nil {
		return fmt.Errorf("%w: hydra admin client is not configured", ErrInvalidRequest)
	}
	var requestBody *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		requestBody = bytes.NewReader(encoded)
	} else {
		requestBody = bytes.NewReader(nil)
	}
	adminURL := c.resolveAdminPath(path)
	q := adminURL.Query()
	for key, value := range query {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%w: %s is required", ErrInvalidRequest, key)
		}
		q.Set(key, value)
	}
	adminURL.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, method, adminURL.String(), requestBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.adminToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.adminToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("hydra admin request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return HydraAdminStatusError{StatusCode: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode hydra admin response: %w", err)
	}
	return nil
}

func (c *HydraAdminClient) resolveAdminPath(path string) *url.URL {
	resolved := *c.adminURL
	basePath := strings.TrimRight(resolved.Path, "/")
	requestPath := "/" + strings.TrimLeft(path, "/")
	if strings.HasSuffix(basePath, "/admin") && strings.HasPrefix(requestPath, "/admin/") {
		requestPath = strings.TrimPrefix(requestPath, "/admin")
	}
	resolved.Path = basePath + requestPath
	return &resolved
}

func validateHydraOAuth2Client(client OIDCClient) error {
	if client.ClientID == "" {
		return fmt.Errorf("%w: client_id is required", ErrInvalidRequest)
	}
	if len(client.AllowedScopes) == 0 {
		return fmt.Errorf("%w: allowed_scopes is required", ErrInvalidRequest)
	}
	if client.AutoConsent && !client.FirstParty {
		return fmt.Errorf("%w: auto_consent is restricted to first-party clients", ErrInvalidRequest)
	}
	grantTypes := defaultHydraGrantTypes(client)
	if stringSetHas(grantTypes, "authorization_code") && len(client.RedirectURIs) == 0 {
		return fmt.Errorf("%w: redirect_uris are required for authorization_code clients", ErrInvalidRequest)
	}
	if oauth2ClientRequiresPKCE(client) && strings.TrimSpace(client.ClientSecret) != "" {
		return fmt.Errorf("%w: public PKCE clients cannot use a client_secret", ErrInvalidRequest)
	}
	return nil
}

func oauth2ClientRequiresPKCE(client OIDCClient) bool {
	method := strings.TrimSpace(client.TokenEndpointAuthMethod)
	if method == "" {
		method = defaultHydraTokenEndpointAuthMethod(client)
	}
	return method == "none" && stringSetHas(defaultHydraGrantTypes(client), "authorization_code")
}

func validatePublicClientPKCE(client OIDCClient, requestURL string) error {
	if !oauth2ClientRequiresPKCE(client) {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(requestURL))
	if err != nil || parsed == nil {
		return fmt.Errorf("%w: public oauth2 client requires a valid PKCE authorization request", ErrInvalidRequest)
	}
	query := parsed.Query()
	if strings.TrimSpace(query.Get("code_challenge")) == "" {
		return fmt.Errorf("%w: public oauth2 client requires PKCE code_challenge", ErrInvalidRequest)
	}
	if !strings.EqualFold(strings.TrimSpace(query.Get("code_challenge_method")), "S256") {
		return fmt.Errorf("%w: public oauth2 client requires PKCE S256", ErrInvalidRequest)
	}
	return nil
}

func hydraOAuth2ClientFromOIDCClient(client OIDCClient) HydraOAuth2Client {
	grantTypes := defaultHydraGrantTypes(client)
	responseTypes := client.ResponseTypes
	if len(responseTypes) == 0 && stringSetHas(grantTypes, "authorization_code") {
		responseTypes = []string{"code"}
	}
	method := client.TokenEndpointAuthMethod
	if method == "" {
		method = defaultHydraTokenEndpointAuthMethod(client)
	}
	out := HydraOAuth2Client{
		ClientID:                client.ClientID,
		ClientName:              firstNonEmpty(client.DisplayName, client.ClientID),
		ClientSecret:            client.ClientSecret,
		RedirectURIs:            append([]string(nil), client.RedirectURIs...),
		GrantTypes:              append([]string(nil), grantTypes...),
		ResponseTypes:           append([]string(nil), responseTypes...),
		Scope:                   strings.Join(client.AllowedScopes, " "),
		Audience:                append([]string(nil), client.Audience...),
		TokenEndpointAuthMethod: method,
		SkipConsent:             client.AutoConsent && client.FirstParty,
		Metadata: map[string]any{
			"kind":         client.Kind,
			"first_party":  client.FirstParty,
			"auto_consent": client.AutoConsent,
		},
	}
	return out
}

func oidcClientFromHydraOAuth2Client(base OIDCClient, hydra HydraOAuth2Client) OIDCClient {
	out := base
	out.ClientID = firstNonEmpty(hydra.ClientID, out.ClientID)
	out.DisplayName = firstNonEmpty(hydra.ClientName, out.DisplayName)
	out.RedirectURIs = cleanList(firstNonEmptyList(hydra.RedirectURIs, out.RedirectURIs), false)
	out.GrantTypes = cleanList(firstNonEmptyList(hydra.GrantTypes, out.GrantTypes), true)
	out.ResponseTypes = cleanList(firstNonEmptyList(hydra.ResponseTypes, out.ResponseTypes), true)
	out.TokenEndpointAuthMethod = firstNonEmpty(hydra.TokenEndpointAuthMethod, out.TokenEndpointAuthMethod)
	out.Audience = cleanList(firstNonEmptyList(hydra.Audience, out.Audience), false)
	if hydra.Scope != "" {
		out.AllowedScopes = cleanList(strings.Fields(hydra.Scope), true)
	}
	out.ClientSecret = strings.TrimSpace(hydra.ClientSecret)
	return normalizeOIDCClient(out)
}

func defaultHydraGrantTypes(client OIDCClient) []string {
	if len(client.GrantTypes) > 0 {
		return append([]string(nil), client.GrantTypes...)
	}
	return []string{"authorization_code", "refresh_token"}
}

func defaultHydraTokenEndpointAuthMethod(client OIDCClient) string {
	switch client.Kind {
	case "cli", "desktop", "agent_runtime":
		return "none"
	default:
		return "client_secret_basic"
	}
}

func firstNonEmptyList(primary []string, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

func stringSetHas(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func oauth2SubjectFromPrincipal(principal Principal) string {
	principal = NormalizePrincipal(principal)
	return firstNonEmpty(principal.AgentProfileID, principal.UserID, principal.Username, principal.Subject)
}

func oauth2SessionClaims(principal Principal) map[string]any {
	principal = NormalizePrincipal(principal)
	claims := map[string]any{
		"subject_provider": principal.SubjectProvider,
		"subject":          principal.Subject,
		"username":         firstNonEmpty(principal.Username, principal.UserID, principal.Subject),
		"agent_profile_id": principal.AgentProfileID,
		"viewer_kind":      principal.ViewerKind,
	}
	if principal.DisplayName != "" {
		claims["display_name"] = principal.DisplayName
	}
	if principal.TenantID != "" {
		claims["tenant_id"] = principal.TenantID
	}
	return claims
}

func oauth2IDTokenClaims(principal Principal, scopes []string) map[string]any {
	claims := oauth2SessionClaims(principal)
	scopeSet := stringSet(cleanList(scopes, true))
	if _, ok := scopeSet["profile"]; ok {
		claims["name"] = firstNonEmpty(principal.DisplayName, principal.Username, principal.UserID, principal.Subject)
		claims["preferred_username"] = firstNonEmpty(principal.Username, principal.UserID, principal.Subject)
	}
	return claims
}

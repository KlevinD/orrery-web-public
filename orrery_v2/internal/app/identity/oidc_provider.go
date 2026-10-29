package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OIDCTokenProviderConfig struct {
	ProviderName     string
	IntrospectionURL string
	ClientID         string
	ClientSecret     string
	Issuer           string
	Audience         string
	HTTPClient       *http.Client
}

type OIDCTokenProvider struct {
	providerName     string
	introspectionURL *url.URL
	clientID         string
	clientSecret     string
	issuer           string
	audience         string
	httpClient       *http.Client
}

func NewOIDCTokenProvider(cfg OIDCTokenProviderConfig) (*OIDCTokenProvider, error) {
	introspectionURL, err := parseBaseURL(cfg.IntrospectionURL)
	if err != nil {
		return nil, fmt.Errorf("%w: oidc introspection_url is invalid: %v", ErrInvalidRequest, err)
	}
	issuer := strings.TrimSpace(cfg.Issuer)
	if issuer == "" {
		return nil, fmt.Errorf("%w: oidc issuer is required", ErrInvalidRequest)
	}
	audience := strings.TrimSpace(cfg.Audience)
	if audience == "" {
		return nil, fmt.Errorf("%w: oidc audience is required", ErrInvalidRequest)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	providerName := normalizeLower(cfg.ProviderName)
	if providerName == "" {
		providerName = ProviderHydra
	}
	return &OIDCTokenProvider{
		providerName:     providerName,
		introspectionURL: introspectionURL,
		clientID:         strings.TrimSpace(cfg.ClientID),
		clientSecret:     strings.TrimSpace(cfg.ClientSecret),
		issuer:           issuer,
		audience:         audience,
		httpClient:       client,
	}, nil
}

func (p *OIDCTokenProvider) Name() string {
	if p == nil || strings.TrimSpace(p.providerName) == "" {
		return ProviderOIDC
	}
	return p.providerName
}

func (p *OIDCTokenProvider) VerifyAPIToken(ctx context.Context, token string) (ExternalIdentity, error) {
	if err := ctx.Err(); err != nil {
		return ExternalIdentity{}, err
	}
	if p == nil || p.introspectionURL == nil || p.httpClient == nil {
		return ExternalIdentity{}, fmt.Errorf("%w: oidc token provider is not configured", ErrInvalidRequest)
	}
	token = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(token), "Bearer "))
	if token == "" {
		return ExternalIdentity{}, ErrUnauthorized
	}
	form := url.Values{}
	form.Set("token", token)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.introspectionURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return ExternalIdentity{}, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if p.clientID != "" || p.clientSecret != "" {
		httpReq.SetBasicAuth(p.clientID, p.clientSecret)
	}
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("oidc introspection: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ExternalIdentity{}, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ExternalIdentity{}, fmt.Errorf("oidc introspection returned status %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	var claims map[string]any
	if err := decoder.Decode(&claims); err != nil {
		return ExternalIdentity{}, fmt.Errorf("decode oidc introspection: %w", err)
	}
	if !claimBool(claims["active"]) {
		return ExternalIdentity{}, ErrUnauthorized
	}
	if !p.audienceAllowed(claims) {
		return ExternalIdentity{}, ErrUnauthorized
	}
	if !p.issuerAllowed(claims) {
		return ExternalIdentity{}, ErrUnauthorized
	}
	subject := traitString(claims, "sub", "subject")
	if subject == "" {
		return ExternalIdentity{}, ErrUnauthorized
	}
	expiresAt := claimUnixTime(claims["exp"])
	if !expiresAt.IsZero() && time.Now().UTC().After(expiresAt) {
		return ExternalIdentity{}, ErrUnauthorized
	}
	traits := map[string]string{}
	// Only the introspection envelope identifies the client that received the
	// token. Consent profile claims must never be able to impersonate it.
	if clientID := traitString(claims, "client_id"); clientID != "" {
		traits["oidc_client_id"] = clientID
	}
	// Hydra returns consent session claims under ext, not at the introspection root.
	profileClaims := claims
	if ext, ok := claims["ext"].(map[string]any); ok && p.Name() == ProviderHydra {
		profileClaims = ext
		if source := traitString(ext, "subject_provider"); source != "" {
			traits["oauth2_subject_provider"] = source
			traits["oauth2_subject"] = traitString(ext, "subject")
		}
	}
	if agentProfileID := traitString(profileClaims, "agent_profile_id", "agentProfileId"); agentProfileID != "" {
		traits["agent_profile_id"] = agentProfileID
	}
	rawScopes := claimStringList(claims["scope"], true, true)
	if len(rawScopes) > 0 {
		traits["oidc_scope"] = strings.Join(rawScopes, " ")
	}
	external := ExternalIdentity{
		Provider:    p.Name(),
		Subject:     subject,
		Username:    traitString(profileClaims, "username", "preferred_username", "email"),
		Email:       traitString(profileClaims, "email"),
		DisplayName: firstNonEmpty(traitString(profileClaims, "display_name", "name"), traitString(profileClaims, "username", "preferred_username", "email")),
		Traits:      traits,
		Groups:      claimStringList(profileClaims["groups"], false, false),
		Scopes:      oidcCoarseScopes(rawScopes),
		ExpiresAt:   expiresAt,
	}
	return NormalizeExternalIdentity(external), nil
}

func (p *OIDCTokenProvider) audienceAllowed(claims map[string]any) bool {
	if p == nil || p.audience == "" {
		return false
	}
	for _, aud := range claimStringList(firstClaim(claims, "aud", "audience"), false, false) {
		if aud == p.audience {
			return true
		}
	}
	return false
}

func (p *OIDCTokenProvider) issuerAllowed(claims map[string]any) bool {
	if p == nil || p.issuer == "" {
		return false
	}
	issuer := traitString(claims, "iss", "issuer")
	return issuer == p.issuer
}

func firstClaim(claims map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := claims[key]; ok {
			return value
		}
	}
	return nil
}

func claimBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func claimUnixTime(value any) time.Time {
	var unix int64
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return time.Time{}
		}
		unix = parsed
	case float64:
		unix = int64(typed)
	case int64:
		unix = typed
	case int:
		unix = int64(typed)
	case string:
		if parsed, err := json.Number(strings.TrimSpace(typed)).Int64(); err == nil {
			unix = parsed
		}
	}
	if unix <= 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0).UTC()
}

func claimStringList(value any, splitFields bool, lower bool) []string {
	out := []string{}
	appendValue := func(raw string) {
		if splitFields {
			for _, item := range strings.Fields(raw) {
				out = append(out, item)
			}
			return
		}
		out = append(out, raw)
	}
	switch typed := value.(type) {
	case string:
		appendValue(typed)
	case []string:
		for _, item := range typed {
			appendValue(item)
		}
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				appendValue(text)
			}
		}
	}
	return cleanList(out, lower)
}

func oidcCoarseScopes(scopes []string) []string {
	out := []string{}
	for _, scope := range scopes {
		if coarse := oidcScopeToCoarseScope(scope); coarse != "" {
			out = append(out, coarse)
		}
	}
	return cleanList(out, true)
}

var _ TokenIdentityProvider = (*OIDCTokenProvider)(nil)

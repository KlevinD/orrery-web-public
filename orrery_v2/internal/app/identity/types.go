package identity

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	ProviderLocal  = "local"
	ProviderKratos = "kratos"
	ProviderHydra  = "hydra"
	ProviderOIDC   = "oidc"
	ProviderFeishu = "feishu"

	ViewerKindUser   = "user"
	ViewerKindAgent  = "agent"
	ViewerKindSystem = "system"

	SubjectStatusActive      = "active"
	SubjectStatusDisabled    = "disabled"
	SubjectStatusPendingLink = "pending_link"
)

var ErrInvalidRequest = errors.New("identity request is invalid")
var ErrUnauthorized = errors.New("identity is unauthorized")
var ErrBrowserSessionUnavailable = errors.New("identity browser session is unavailable")
var ErrServiceDependency = errors.New("identity service dependency is not configured")
var ErrFlowCookieInvalid = errors.New("identity login flow cookie is invalid")
var ErrFlowNotFound = errors.New("identity login flow was not found")
var ErrFlowExpired = errors.New("identity login flow expired")
var ErrServiceUnavailable = errors.New("identity service is unavailable")
var ErrConflict = errors.New("identity binding conflicts with an existing account")

type ExternalIdentity struct {
	Provider    string
	Subject     string
	Username    string
	Email       string
	DisplayName string
	Traits      map[string]string
	Groups      []string
	Scopes      []string
	ExpiresAt   time.Time
}

type IdentitySubject struct {
	ID             string
	Provider       string
	Subject        string
	UserID         string
	Username       string
	Email          string
	DisplayName    string
	AgentProfileID string
	Status         string
	LastLoginAt    time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type SubjectQuery struct {
	Provider       string
	Subject        string
	AgentProfileID string
	Status         string
	Limit          int
	Offset         int
}

type SubjectPage struct {
	Subjects []IdentitySubject
	Limit    int
	Offset   int
	Total    int
	Returned int
	HasMore  bool
}

type Principal struct {
	SubjectProvider           string
	Subject                   string
	UserID                    string
	Username                  string
	Email                     string
	DisplayName               string
	AgentProfileID            string
	ViewerKind                string
	TenantID                  string
	CoarseScopes              []string
	CredentialScopes          []string
	CredentialScopeRestricted bool
	ExpiresAt                 time.Time
}

type ObjectRef struct {
	Type     string
	ID       string
	TenantID string
}

type BeginLoginRequest struct {
	ReturnTo string
}

type BeginLogoutRequest struct {
	ReturnTo string
	Cookie   string
}

type LoginRedirect struct {
	URL       string
	FlowState string
	ExpiresAt time.Time
}

type CompleteLoginRequest struct {
	RequestURL string
	Code       string
	State      string
	Cookie     string
}

type ProviderInfo struct {
	Name        string
	DisplayName string
	Kind        string
	Enabled     bool
	Primary     bool
}

type LinkSubjectRequest struct {
	Provider       string
	Subject        string
	AgentProfileID string
	Username       string
	Email          string
	DisplayName    string
	Status         string
}

type BrowserIdentityProvider interface {
	Name() string
	BeginBrowserLogin(ctx context.Context, req BeginLoginRequest) (LoginRedirect, error)
	CompleteBrowserLogin(ctx context.Context, req CompleteLoginRequest) (ExternalIdentity, error)
}

type BrowserIdentityLogoutProvider interface {
	BeginBrowserLogout(ctx context.Context, req BeginLogoutRequest) (LoginRedirect, error)
}

type BeginBindingRequest struct {
	AgentProfileID string
	ReturnTo       string
}

type BrowserIdentityBindingProvider interface {
	BrowserIdentityProvider
	BeginBrowserBinding(ctx context.Context, req BeginBindingRequest) (LoginRedirect, error)
	CompleteBrowserBinding(ctx context.Context, req CompleteLoginRequest) (ExternalIdentity, error)
}

type TokenIdentityProvider interface {
	Name() string
	VerifyAPIToken(ctx context.Context, token string) (ExternalIdentity, error)
}

type SubjectMapper interface {
	ResolveOrLink(ctx context.Context, external ExternalIdentity) (IdentitySubject, error)
}

func NormalizeExternalIdentity(in ExternalIdentity) ExternalIdentity {
	in.Provider = normalizeLower(in.Provider)
	in.Subject = strings.TrimSpace(in.Subject)
	in.Username = normalizeUsername(in.Username)
	in.Email = strings.TrimSpace(in.Email)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.Traits != nil {
		traits := make(map[string]string, len(in.Traits))
		for key, value := range in.Traits {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			traits[key] = strings.TrimSpace(value)
		}
		in.Traits = traits
	}
	in.Groups = cleanList(in.Groups, false)
	in.Scopes = cleanList(in.Scopes, true)
	if !in.ExpiresAt.IsZero() {
		in.ExpiresAt = in.ExpiresAt.UTC()
	}
	return in
}

func NormalizeIdentitySubject(in IdentitySubject) IdentitySubject {
	in.ID = strings.TrimSpace(in.ID)
	in.Provider = normalizeLower(in.Provider)
	in.Subject = strings.TrimSpace(in.Subject)
	in.UserID = strings.TrimSpace(in.UserID)
	in.Username = normalizeUsername(in.Username)
	in.Email = strings.TrimSpace(in.Email)
	in.Email = strings.TrimSpace(in.Email)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.AgentProfileID = strings.TrimSpace(in.AgentProfileID)
	in.Status = normalizeStatus(in.Status)
	if !in.LastLoginAt.IsZero() {
		in.LastLoginAt = in.LastLoginAt.UTC()
	}
	if !in.CreatedAt.IsZero() {
		in.CreatedAt = in.CreatedAt.UTC()
	}
	if !in.UpdatedAt.IsZero() {
		in.UpdatedAt = in.UpdatedAt.UTC()
	}
	return in
}

func NormalizePrincipal(in Principal) Principal {
	in.SubjectProvider = normalizeLower(in.SubjectProvider)
	in.Subject = strings.TrimSpace(in.Subject)
	in.UserID = strings.TrimSpace(in.UserID)
	in.Username = normalizeUsername(in.Username)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.AgentProfileID = strings.TrimSpace(in.AgentProfileID)
	in.ViewerKind = normalizeLower(firstNonEmpty(in.ViewerKind, ViewerKindUser))
	in.TenantID = strings.TrimSpace(in.TenantID)
	in.CoarseScopes = cleanList(in.CoarseScopes, true)
	in.CredentialScopes = cleanList(in.CredentialScopes, true)
	if !in.ExpiresAt.IsZero() {
		in.ExpiresAt = in.ExpiresAt.UTC()
	}
	return in
}

func PrincipalFromSubject(subject IdentitySubject, scopes []string, expiresAt time.Time) Principal {
	subject = NormalizeIdentitySubject(subject)
	return NormalizePrincipal(Principal{
		SubjectProvider: subject.Provider,
		Subject:         subject.Subject,
		UserID:          firstNonEmpty(subject.UserID, subject.Username),
		Username:        subject.Username,
		Email:           subject.Email,
		DisplayName:     subject.DisplayName,
		AgentProfileID:  subject.AgentProfileID,
		ViewerKind:      ViewerKindUser,
		CoarseScopes:    scopes,
		ExpiresAt:       expiresAt,
	})
}

func (p Principal) Authenticated() bool {
	p = NormalizePrincipal(p)
	return p.SubjectProvider != "" && p.Subject != "" && firstNonEmpty(p.Username, p.UserID, p.Subject) != "" && p.ViewerKind != ""
}

func (p Principal) PermissionScopeHeader() string {
	return strings.Join(NormalizePrincipal(p).CoarseScopes, ",")
}

func normalizeStatus(value string) string {
	switch normalizeLower(value) {
	case "", SubjectStatusActive:
		return SubjectStatusActive
	case SubjectStatusDisabled:
		return SubjectStatusDisabled
	case SubjectStatusPendingLink:
		return SubjectStatusPendingLink
	default:
		return SubjectStatusPendingLink
	}
}

func normalizeLower(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func cleanList(values []string, lower bool) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.ToLower(value)
		}
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

package userauth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"orrery_v2/internal/app/identity"
)

type BrowserLoginService struct {
	sessions     *Service
	mapper       identity.SubjectMapper
	providers    map[string]identity.BrowserIdentityProvider
	provisioners map[string]FederatedIdentityProvisioner
	peopleAccess PersonAccessValidator
	passwordRule InitialPasswordRequirementReader
	localEnabled bool
}

type FederatedIdentityProvisioner interface {
	ProvisionIdentity(ctx context.Context, external identity.ExternalIdentity) (identity.IdentitySubject, error)
}

type PersonAccessValidator interface {
	ValidatePersonAccess(ctx context.Context, personID string, revision int64) error
}

type InitialPasswordRequirementReader interface {
	InitialPasswordChangeRequired(ctx context.Context, subject string) (bool, error)
}

func NewBrowserLoginService(sessions *Service, mapper identity.SubjectMapper, localEnabled bool, providers ...identity.BrowserIdentityProvider) *BrowserLoginService {
	service := &BrowserLoginService{
		sessions:     sessions,
		mapper:       mapper,
		providers:    map[string]identity.BrowserIdentityProvider{},
		provisioners: map[string]FederatedIdentityProvisioner{},
		localEnabled: localEnabled,
	}
	for _, provider := range providers {
		service.RegisterProvider(provider)
	}
	return service
}

func (s *BrowserLoginService) WithPersonAccessValidator(validator PersonAccessValidator) *BrowserLoginService {
	if s != nil {
		s.peopleAccess = validator
	}
	return s
}

func (s *BrowserLoginService) WithInitialPasswordRequirementReader(reader InitialPasswordRequirementReader) *BrowserLoginService {
	if s != nil {
		s.passwordRule = reader
	}
	return s
}

func (s *BrowserLoginService) RegisterProvisioner(providerName string, provisioner FederatedIdentityProvisioner) {
	if s == nil || provisioner == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(providerName))
	if name == "" {
		return
	}
	if s.provisioners == nil {
		s.provisioners = map[string]FederatedIdentityProvisioner{}
	}
	s.provisioners[name] = provisioner
}

func (s *BrowserLoginService) RegisterProvider(provider identity.BrowserIdentityProvider) {
	if s == nil || provider == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(provider.Name()))
	if name == "" {
		return
	}
	if s.providers == nil {
		s.providers = map[string]identity.BrowserIdentityProvider{}
	}
	s.providers[name] = provider
}

func (s *BrowserLoginService) LocalLoginEnabled(ctx context.Context) bool {
	_ = ctx
	return s != nil && s.localEnabled
}

func (s *BrowserLoginService) Providers(ctx context.Context) []identity.ProviderInfo {
	_ = ctx
	var out []identity.ProviderInfo
	if s == nil {
		return nil
	}
	if s.localEnabled {
		out = append(out, identity.ProviderInfo{Name: identity.ProviderLocal, DisplayName: "Local", Kind: "local", Enabled: true, Primary: len(s.providers) == 0})
	}
	names := make([]string, 0, len(s.providers))
	for name := range s.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		provider := s.providers[name]
		out = append(out, identity.ProviderInfo{
			Name:        name,
			DisplayName: providerDisplayName(provider),
			Kind:        "browser",
			Enabled:     true,
			Primary:     name == identity.ProviderKratos,
		})
	}
	return out
}

func (s *BrowserLoginService) BeginBrowserLogin(ctx context.Context, providerName string, req identity.BeginLoginRequest) (identity.LoginRedirect, error) {
	provider, err := s.provider(providerName)
	if err != nil {
		return identity.LoginRedirect{}, err
	}
	return provider.BeginBrowserLogin(ctx, req)
}

func (s *BrowserLoginService) BeginBrowserLogout(ctx context.Context, providerName string, req identity.BeginLogoutRequest) (identity.LoginRedirect, error) {
	provider, err := s.provider(providerName)
	if err != nil {
		return identity.LoginRedirect{}, err
	}
	logoutProvider, ok := provider.(identity.BrowserIdentityLogoutProvider)
	if !ok {
		return identity.LoginRedirect{}, fmt.Errorf("%w: identity provider does not support browser logout", identity.ErrInvalidRequest)
	}
	return logoutProvider.BeginBrowserLogout(ctx, req)
}

func (s *BrowserLoginService) GetBrowserLoginFlow(ctx context.Context, providerName string, req identity.LoginFlowRequest) (identity.BrowserLoginFlow, error) {
	provider, err := s.provider(providerName)
	if err != nil {
		return identity.BrowserLoginFlow{}, err
	}
	flowProvider, ok := provider.(identity.BrowserLoginFlowProvider)
	if !ok {
		return identity.BrowserLoginFlow{}, fmt.Errorf("%w: identity provider does not expose login flows", identity.ErrInvalidRequest)
	}
	return flowProvider.GetBrowserLoginFlow(ctx, req)
}

func (s *BrowserLoginService) CompleteBrowserLogin(ctx context.Context, providerName string, req identity.CompleteLoginRequest) (SessionDTO, error) {
	if s == nil || s.sessions == nil || s.mapper == nil {
		return SessionDTO{}, ErrServiceDependency
	}
	provider, err := s.provider(providerName)
	if err != nil {
		return SessionDTO{}, err
	}
	external, err := provider.CompleteBrowserLogin(ctx, req)
	if err != nil {
		if errors.Is(err, identity.ErrUnauthorized) {
			return SessionDTO{}, ErrUnauthorized
		}
		return SessionDTO{}, err
	}
	if s.peopleAccess != nil && external.Provider == identity.ProviderKratos &&
		!identity.IsProtectedBreakGlassIdentity(external.Provider, external.Username, external.Traits["agent_profile_id"]) {
		personID := strings.TrimSpace(external.Traits["person_id"])
		revision, parseErr := strconv.ParseInt(strings.TrimSpace(external.Traits["person_revision"]), 10, 64)
		if personID == "" || parseErr != nil || revision <= 0 {
			return SessionDTO{}, ErrUnauthorized
		}
		if err := s.peopleAccess.ValidatePersonAccess(ctx, personID, revision); err != nil {
			if errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, ErrUnauthorized) {
				return SessionDTO{}, ErrUnauthorized
			}
			return SessionDTO{}, err
		}
	}
	mustChangePassword := false
	if s.passwordRule != nil && external.Provider == identity.ProviderKratos {
		mustChangePassword, err = s.passwordRule.InitialPasswordChangeRequired(ctx, external.Subject)
		if err != nil {
			return SessionDTO{}, err
		}
	}
	subject, err := s.mapper.ResolveOrLink(ctx, external)
	if err != nil {
		if errors.Is(err, identity.ErrSubjectNotFound) {
			if provisioner := s.provisioners[strings.ToLower(strings.TrimSpace(external.Provider))]; provisioner != nil {
				subject, err = provisioner.ProvisionIdentity(ctx, external)
			}
		}
	}
	if err != nil {
		if errors.Is(err, identity.ErrSubjectNotFound) {
			return SessionDTO{}, ErrUnauthorized
		}
		return SessionDTO{}, err
	}
	if mustChangePassword {
		return s.sessions.CreatePasswordChangeSessionForIdentitySubjectUntil(ctx, subject, external.ExpiresAt)
	}
	return s.sessions.CreateSessionForIdentitySubjectUntil(ctx, subject, external.ExpiresAt)
}

func (s *BrowserLoginService) CallbackReturnTo(providerName, state string) string {
	provider, err := s.provider(providerName)
	if err != nil {
		return "/"
	}
	if redirector, ok := provider.(interface{ CallbackReturnTo(string) string }); ok {
		return redirector.CallbackReturnTo(state)
	}
	return "/"
}

func (s *BrowserLoginService) provider(providerName string) (identity.BrowserIdentityProvider, error) {
	if s == nil {
		return nil, ErrServiceDependency
	}
	name := strings.ToLower(strings.TrimSpace(providerName))
	if name == "" || name == identity.ProviderLocal {
		return nil, fmt.Errorf("%w: external provider is required", ErrInvalidRequest)
	}
	provider, ok := s.providers[name]
	if !ok {
		return nil, fmt.Errorf("%w: identity provider is not configured", ErrInvalidRequest)
	}
	return provider, nil
}

func providerDisplayName(provider identity.BrowserIdentityProvider) string {
	if provider == nil {
		return ""
	}
	if named, ok := provider.(interface{ DisplayName() string }); ok {
		if displayName := strings.TrimSpace(named.DisplayName()); displayName != "" {
			return displayName
		}
	}
	return provider.Name()
}

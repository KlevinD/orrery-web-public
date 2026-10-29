package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
)

func TestBrowserLoginServiceCreatesAppSessionForExternalIdentitySubject(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	externalExpiresAt := now.Add(2 * time.Hour)
	accounts := NewMemoryAccountStore()
	if err := accounts.SaveAccount(ctx, Account{
		UserID:          "human-operator",
		Username:        "operator",
		DisplayName:     "Operator",
		AgentProfileID:  "human-operator",
		Enabled:         true,
		PermissionScope: []string{"operations.read"},
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	provider := fakeBrowserIdentityProvider{
		name: identity.ProviderKratos,
		external: identity.ExternalIdentity{
			Provider:    identity.ProviderKratos,
			Subject:     "kratos-id-1",
			Username:    "external-operator",
			Email:       "operator@example.com",
			DisplayName: "Operator One",
			Traits:      map[string]string{"agent_profile_id": "human-operator"},
			ExpiresAt:   externalExpiresAt,
		},
	}
	auth := NewService(accounts)
	auth.now = func() time.Time { return now }
	subjects := identity.NewMemorySubjectStore()
	auth.WithIdentitySubjectStore(subjects)
	login := NewBrowserLoginService(auth, identity.NewSubjectMapper(subjects).WithAutoLink(true), false, provider)

	session, err := login.CompleteBrowserLogin(ctx, identity.ProviderKratos, identity.CompleteLoginRequest{Cookie: "ory=abc"})
	if err != nil {
		t.Fatalf("CompleteBrowserLogin: %v", err)
	}
	if session.User.Username != "operator" || session.User.AgentProfileID != "human-operator" {
		t.Fatalf("session user = %+v", session.User)
	}
	if session.SubjectProvider != identity.ProviderKratos || session.Subject != "kratos-id-1" || session.AccessToken == "" {
		t.Fatalf("session identity = %+v", session)
	}
	if !session.ExpiresAt.Equal(externalExpiresAt) {
		t.Fatalf("session expires_at = %s, want external identity expiry %s", session.ExpiresAt, externalExpiresAt)
	}

	principal, err := auth.AuthenticatePrincipal(ctx, session.AccessToken)
	if err != nil {
		t.Fatalf("AuthenticatePrincipal: %v", err)
	}
	if principal.SubjectProvider != identity.ProviderKratos || principal.Subject != "kratos-id-1" || principal.Username != "operator" {
		t.Fatalf("principal = %+v", principal)
	}
	if principal.AgentProfileID != "human-operator" || !containsScope(principal.CoarseScopes, "operations.read") {
		t.Fatalf("principal profile/scopes = %+v", principal)
	}
	subject, err := subjects.GetSubject(ctx, identity.ProviderKratos, "kratos-id-1")
	if err != nil {
		t.Fatalf("GetSubject: %v", err)
	}
	subject.Status = identity.SubjectStatusDisabled
	if err := subjects.SaveSubject(ctx, subject); err != nil {
		t.Fatalf("SaveSubject disabled: %v", err)
	}
	if _, err := auth.AuthenticatePrincipal(ctx, session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticatePrincipal after subject disable error = %v, want ErrUnauthorized", err)
	}
	if login.LocalLoginEnabled(ctx) {
		t.Fatalf("local login should be disabled")
	}
}

func TestBrowserLoginServiceRejectsSessionAfterIdentitySubjectRelink(t *testing.T) {
	ctx := context.Background()
	accounts := NewMemoryAccountStore()
	for _, account := range []Account{
		{UserID: "old-account", Username: "old-owner", AgentProfileID: "human-old", Enabled: true},
		{UserID: "new-account", Username: "new-owner", AgentProfileID: "human-new", Enabled: true},
	} {
		if err := accounts.SaveAccount(ctx, account); err != nil {
			t.Fatalf("SaveAccount: %v", err)
		}
	}
	provider := fakeBrowserIdentityProvider{
		name: identity.ProviderKratos,
		external: identity.ExternalIdentity{
			Provider: identity.ProviderKratos,
			Subject:  "kratos-relinked",
			Traits:   map[string]string{"agent_profile_id": "human-old"},
		},
	}
	subjects := identity.NewMemorySubjectStore()
	auth := NewService(accounts).WithIdentitySubjectStore(subjects)
	login := NewBrowserLoginService(auth, identity.NewSubjectMapper(subjects).WithAutoLink(true), false, provider)
	session, err := login.CompleteBrowserLogin(ctx, identity.ProviderKratos, identity.CompleteLoginRequest{Cookie: "ory=abc"})
	if err != nil {
		t.Fatalf("CompleteBrowserLogin: %v", err)
	}
	subject, err := subjects.GetSubject(ctx, identity.ProviderKratos, "kratos-relinked")
	if err != nil {
		t.Fatalf("GetSubject: %v", err)
	}
	subject.AgentProfileID = "human-new"
	subject.Username = "new-owner"
	if err := subjects.SaveSubject(ctx, subject); err != nil {
		t.Fatalf("SaveSubject relinked: %v", err)
	}
	if _, err := auth.AuthenticatePrincipal(ctx, session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticatePrincipal after relink error = %v, want ErrUnauthorized", err)
	}
}

func TestBrowserLoginServiceValidatesPeopleAccessBeforeCreatingSession(t *testing.T) {
	ctx := context.Background()
	accounts := NewMemoryAccountStore()
	if err := accounts.SaveAccount(ctx, Account{
		UserID: "human-quzheng", Username: "quzheng", AgentProfileID: "human-quzheng", Enabled: true,
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	subjects := identity.NewMemorySubjectStore()
	provider := fakeBrowserIdentityProvider{
		name: identity.ProviderKratos,
		external: identity.ExternalIdentity{
			Provider: identity.ProviderKratos,
			Subject:  "kratos-quzheng",
			Username: "quzheng",
			Traits:   map[string]string{"person_id": "human-quzheng", "person_revision": "7"},
		},
	}
	validator := &fakePersonAccessValidator{}
	login := NewBrowserLoginService(
		NewService(accounts).WithIdentitySubjectStore(subjects),
		identity.NewSubjectMapper(subjects),
		false,
		provider,
	).WithPersonAccessValidator(validator)

	session, err := login.CompleteBrowserLogin(ctx, identity.ProviderKratos, identity.CompleteLoginRequest{Cookie: "ory=abc"})
	if err != nil {
		t.Fatalf("CompleteBrowserLogin: %v", err)
	}
	if validator.personID != "human-quzheng" || validator.revision != 7 {
		t.Fatalf("validator call = person:%q revision:%d", validator.personID, validator.revision)
	}
	if session.User.AgentProfileID != "human-quzheng" {
		t.Fatalf("session = %+v", session)
	}
}

func TestBrowserLoginServiceAllowsProtectedBreakGlassIdentityWithoutPeopleRecord(t *testing.T) {
	ctx := context.Background()
	accounts := NewMemoryAccountStore()
	if err := accounts.SaveAccount(ctx, Account{
		UserID: SuperadminAgentProfileID, Username: SuperadminUsername, AgentProfileID: SuperadminAgentProfileID, Enabled: true,
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	subjects := identity.NewMemorySubjectStore()
	if err := subjects.SaveSubject(ctx, identity.IdentitySubject{
		Provider:       identity.ProviderKratos,
		Subject:        "kratos-superadmin",
		UserID:         "human-superadmin",
		Username:       "superadmin",
		AgentProfileID: "human-superadmin",
		Status:         identity.SubjectStatusActive,
	}); err != nil {
		t.Fatalf("SaveSubject: %v", err)
	}
	provider := fakeBrowserIdentityProvider{
		name: identity.ProviderKratos,
		external: identity.ExternalIdentity{
			Provider: identity.ProviderKratos,
			Subject:  "kratos-superadmin",
			Username: "superadmin",
			Traits:   map[string]string{"agent_profile_id": "human-superadmin"},
		},
	}
	login := NewBrowserLoginService(
		NewService(accounts).WithIdentitySubjectStore(subjects),
		identity.NewSubjectMapper(subjects),
		false,
		provider,
	).WithPersonAccessValidator(&fakePersonAccessValidator{err: errors.New("must not validate People")})

	session, err := login.CompleteBrowserLogin(ctx, identity.ProviderKratos, identity.CompleteLoginRequest{Cookie: "ory=abc"})
	if err != nil {
		t.Fatalf("CompleteBrowserLogin: %v", err)
	}
	if session.User.AgentProfileID != SuperadminAgentProfileID {
		t.Fatalf("session = %+v", session)
	}
	migrated, err := subjects.GetSubject(ctx, identity.ProviderKratos, "kratos-superadmin")
	if err != nil || migrated.AgentProfileID != SuperadminAgentProfileID {
		t.Fatalf("legacy subject was not canonicalized: subject=%+v err=%v", migrated, err)
	}
}

func TestBrowserLoginServiceRequiresKratosInitialPasswordChange(t *testing.T) {
	ctx := context.Background()
	accounts := NewMemoryAccountStore()
	if err := accounts.SaveAccount(ctx, Account{
		UserID: "human-quzheng", Username: "quzheng", AgentProfileID: "human-quzheng", Enabled: true,
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	subjects := identity.NewMemorySubjectStore()
	provider := fakeBrowserIdentityProvider{
		name: identity.ProviderKratos,
		external: identity.ExternalIdentity{
			Provider: identity.ProviderKratos,
			Subject:  "kratos-quzheng",
			Username: "quzheng",
			Traits: map[string]string{
				"person_id": "human-quzheng", "person_revision": "7",
			},
		},
	}
	requirement := &fakeInitialPasswordRequirementReader{required: true}
	changer := &fakeExternalPasswordChanger{}
	auth := NewService(accounts).WithIdentitySubjectStore(subjects).WithExternalPasswordChanger(changer)
	login := NewBrowserLoginService(auth, identity.NewSubjectMapper(subjects), false, provider).
		WithPersonAccessValidator(&fakePersonAccessValidator{}).
		WithInitialPasswordRequirementReader(requirement)

	restricted, err := login.CompleteBrowserLogin(ctx, identity.ProviderKratos, identity.CompleteLoginRequest{Cookie: "ory=abc"})
	if err != nil {
		t.Fatalf("CompleteBrowserLogin: %v", err)
	}
	if !restricted.MustChangePassword || len(restricted.PermissionScope) != 0 {
		t.Fatalf("restricted session = %+v", restricted)
	}
	if _, err := auth.AuthenticatePrincipal(ctx, restricted.AccessToken); !errors.Is(err, ErrPasswordChangeRequired) {
		t.Fatalf("AuthenticatePrincipal error = %v, want ErrPasswordChangeRequired", err)
	}

	replacement, err := auth.ChangePassword(ctx, restricted.AccessToken, "replacement-password-1", "203.0.113.8", "test-browser")
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if replacement.MustChangePassword || requirement.subject != "kratos-quzheng" || changer.subject != "kratos-quzheng" || changer.password != "replacement-password-1" {
		t.Fatalf("replacement = %+v changer = %+v", replacement, changer)
	}
	account, err := accounts.GetAccountByUsername(ctx, "quzheng")
	if err != nil || account.PasswordSalt != "" || account.PasswordVerifier != "" {
		t.Fatalf("People account password fields changed: account=%+v err=%v", account, err)
	}
}

func TestKratosInitialPasswordChangeFailureKeepsRestrictedSession(t *testing.T) {
	ctx := context.Background()
	accounts := NewMemoryAccountStore()
	if err := accounts.SaveAccount(ctx, Account{
		UserID: "human-quzheng", Username: "quzheng", AgentProfileID: "human-quzheng", Enabled: true,
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	subjects := identity.NewMemorySubjectStore()
	changerErr := errors.New("kratos unavailable")
	changer := &fakeExternalPasswordChanger{err: changerErr}
	auth := NewService(accounts).WithIdentitySubjectStore(subjects).WithExternalPasswordChanger(changer)
	login := NewBrowserLoginService(auth, identity.NewSubjectMapper(subjects), false, fakeBrowserIdentityProvider{
		name: identity.ProviderKratos,
		external: identity.ExternalIdentity{
			Provider: identity.ProviderKratos, Subject: "kratos-quzheng", Username: "quzheng",
			Traits: map[string]string{
				"person_id": "human-quzheng", "person_revision": "7",
			},
		},
	}).WithPersonAccessValidator(&fakePersonAccessValidator{}).
		WithInitialPasswordRequirementReader(&fakeInitialPasswordRequirementReader{required: true})

	restricted, err := login.CompleteBrowserLogin(ctx, identity.ProviderKratos, identity.CompleteLoginRequest{Cookie: "ory=abc"})
	if err != nil {
		t.Fatalf("CompleteBrowserLogin: %v", err)
	}
	if _, err := auth.ChangePassword(ctx, restricted.AccessToken, "replacement-password-1", "", ""); !errors.Is(err, changerErr) {
		t.Fatalf("ChangePassword error = %v, want %v", err, changerErr)
	}
	current, err := auth.AuthenticateToken(ctx, restricted.AccessToken)
	if err != nil || !current.MustChangePassword {
		t.Fatalf("restricted session after failure = %+v err=%v", current, err)
	}
	account, err := accounts.GetAccountByUsername(ctx, "quzheng")
	if err != nil || account.PasswordSalt != "" || account.PasswordVerifier != "" {
		t.Fatalf("People account password fields changed: account=%+v err=%v", account, err)
	}
}

func TestBrowserLoginServiceFailsClosedWhenKratosPasswordRequirementIsUnavailable(t *testing.T) {
	ctx := context.Background()
	accounts := NewMemoryAccountStore()
	if err := accounts.SaveAccount(ctx, Account{
		UserID: "human-quzheng", Username: "quzheng", AgentProfileID: "human-quzheng", Enabled: true,
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	subjects := identity.NewMemorySubjectStore()
	requirementErr := errors.New("kratos admin unavailable")
	auth := NewService(accounts).WithIdentitySubjectStore(subjects)
	login := NewBrowserLoginService(auth, identity.NewSubjectMapper(subjects), false, fakeBrowserIdentityProvider{
		name: identity.ProviderKratos,
		external: identity.ExternalIdentity{
			Provider: identity.ProviderKratos, Subject: "kratos-quzheng", Username: "quzheng",
			Traits: map[string]string{"person_id": "human-quzheng", "person_revision": "7"},
		},
	}).WithPersonAccessValidator(&fakePersonAccessValidator{}).
		WithInitialPasswordRequirementReader(&fakeInitialPasswordRequirementReader{err: requirementErr})

	if _, err := login.CompleteBrowserLogin(ctx, identity.ProviderKratos, identity.CompleteLoginRequest{Cookie: "ory=abc"}); !errors.Is(err, requirementErr) {
		t.Fatalf("CompleteBrowserLogin error = %v, want %v", err, requirementErr)
	}
}

func TestBrowserLoginServiceRejectsInvalidPeopleAccess(t *testing.T) {
	for _, test := range []struct {
		name      string
		traits    map[string]string
		validator PersonAccessValidator
	}{
		{name: "missing person", traits: map[string]string{"person_revision": "7"}, validator: &fakePersonAccessValidator{}},
		{name: "missing revision", traits: map[string]string{"person_id": "human-quzheng"}, validator: &fakePersonAccessValidator{}},
		{name: "stale revision", traits: map[string]string{"person_id": "human-quzheng", "person_revision": "6"}, validator: &fakePersonAccessValidator{err: identity.ErrUnauthorized}},
		{name: "disabled person", traits: map[string]string{"person_id": "human-quzheng", "person_revision": "7"}, validator: &fakePersonAccessValidator{err: identity.ErrUnauthorized}},
	} {
		t.Run(test.name, func(t *testing.T) {
			subjects := identity.NewMemorySubjectStore()
			login := NewBrowserLoginService(
				NewService(NewMemoryAccountStore()).WithIdentitySubjectStore(subjects),
				identity.NewSubjectMapper(subjects),
				false,
				fakeBrowserIdentityProvider{name: identity.ProviderKratos, external: identity.ExternalIdentity{
					Provider: identity.ProviderKratos, Subject: "kratos-quzheng", Traits: test.traits,
				}},
			).WithPersonAccessValidator(test.validator)

			if _, err := login.CompleteBrowserLogin(context.Background(), identity.ProviderKratos, identity.CompleteLoginRequest{Cookie: "ory=abc"}); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("CompleteBrowserLogin error = %v, want ErrUnauthorized", err)
			}
		})
	}
}

func TestBrowserLoginServicePreservesUnavailableBrowserSession(t *testing.T) {
	login := NewBrowserLoginService(
		NewService(NewMemoryAccountStore()),
		identity.NewSubjectMapper(identity.NewMemorySubjectStore()),
		false,
		fakeBrowserIdentityProvider{name: identity.ProviderKratos, err: identity.ErrBrowserSessionUnavailable},
	)

	if _, err := login.CompleteBrowserLogin(context.Background(), identity.ProviderKratos, identity.CompleteLoginRequest{}); !errors.Is(err, identity.ErrBrowserSessionUnavailable) {
		t.Fatalf("CompleteBrowserLogin error = %v, want ErrBrowserSessionUnavailable", err)
	}
}

func TestBrowserLoginServiceProvisionsUnknownFederatedIdentity(t *testing.T) {
	ctx := context.Background()
	accounts := NewMemoryAccountStore()
	subjects := identity.NewMemorySubjectStore()
	auth := NewService(accounts).WithIdentitySubjectStore(subjects)
	provider := fakeBrowserIdentityProvider{
		name: identity.ProviderFeishu,
		external: identity.ExternalIdentity{
			Provider: identity.ProviderFeishu, Subject: "tenant:on_1", DisplayName: "Alice",
		},
	}
	provisioner := &fakeFederatedIdentityProvisioner{accounts: accounts, subjects: subjects}
	login := NewBrowserLoginService(auth, identity.NewSubjectMapper(subjects), false, provider)
	login.RegisterProvisioner(identity.ProviderFeishu, provisioner)

	session, err := login.CompleteBrowserLogin(ctx, identity.ProviderFeishu, identity.CompleteLoginRequest{Code: "code-1"})
	if err != nil {
		t.Fatalf("CompleteBrowserLogin: %v", err)
	}
	if !provisioner.called || session.User.AgentProfileID != "human-feishu-1" || session.SubjectProvider != identity.ProviderFeishu {
		t.Fatalf("called = %v session = %+v", provisioner.called, session)
	}
}

func TestBrowserLoginServiceListsLocalAndBrowserProviders(t *testing.T) {
	login := NewBrowserLoginService(nil, nil, true, fakeBrowserIdentityProvider{name: identity.ProviderKratos, displayName: "Company SSO"})

	providers := login.Providers(context.Background())
	if len(providers) != 2 {
		t.Fatalf("providers = %+v", providers)
	}
	if providers[0].Name != identity.ProviderLocal || providers[0].Kind != "local" || !providers[0].Enabled {
		t.Fatalf("local provider = %+v", providers[0])
	}
	if providers[1].Name != identity.ProviderKratos || providers[1].DisplayName != "Company SSO" || providers[1].Kind != "browser" {
		t.Fatalf("browser provider = %+v", providers[1])
	}
}

func TestBrowserLoginServiceLoadsBrowserProviderLoginFlow(t *testing.T) {
	provider := fakeBrowserIdentityProvider{
		name: identity.ProviderKratos,
		flow: identity.BrowserLoginFlow{
			ID:     kratosLoginFlowIDForUserAuth,
			Action: "https://kratos.example/self-service/login?flow=" + kratosLoginFlowIDForUserAuth,
			Method: "POST",
		},
	}
	login := NewBrowserLoginService(nil, nil, false, provider)

	flow, err := login.GetBrowserLoginFlow(context.Background(), identity.ProviderKratos, identity.LoginFlowRequest{ID: kratosLoginFlowIDForUserAuth})
	if err != nil {
		t.Fatalf("GetBrowserLoginFlow: %v", err)
	}
	if flow.ID != kratosLoginFlowIDForUserAuth || flow.Action != provider.flow.Action {
		t.Fatalf("flow = %+v", flow)
	}
}

func TestBrowserLoginServiceDelegatesBrowserLogout(t *testing.T) {
	provider := &fakeBrowserIdentityLogoutProvider{
		fakeBrowserIdentityProvider: fakeBrowserIdentityProvider{name: identity.ProviderKratos},
		logoutRedirect:              identity.LoginRedirect{URL: "https://idp.example/logout"},
	}
	login := NewBrowserLoginService(nil, nil, false, provider)

	redirect, err := login.BeginBrowserLogout(context.Background(), identity.ProviderKratos, identity.BeginLogoutRequest{ReturnTo: "/login"})
	if err != nil {
		t.Fatalf("BeginBrowserLogout: %v", err)
	}
	if redirect.URL != provider.logoutRedirect.URL || provider.logoutReturnTo != "/login" {
		t.Fatalf("redirect = %+v return_to = %q", redirect, provider.logoutReturnTo)
	}
}

func TestBrowserLoginServiceRejectsBrowserLogoutForUnsupportedProvider(t *testing.T) {
	login := NewBrowserLoginService(nil, nil, false, fakeBrowserIdentityProvider{name: identity.ProviderFeishu})

	_, err := login.BeginBrowserLogout(context.Background(), identity.ProviderFeishu, identity.BeginLogoutRequest{ReturnTo: "/login"})
	if !errors.Is(err, identity.ErrInvalidRequest) {
		t.Fatalf("BeginBrowserLogout error = %v, want ErrInvalidRequest", err)
	}
}

const kratosLoginFlowIDForUserAuth = "test-flow-id"

type fakeBrowserIdentityProvider struct {
	name        string
	displayName string
	external    identity.ExternalIdentity
	err         error
	flow        identity.BrowserLoginFlow
	flowErr     error
}

type fakeBrowserIdentityLogoutProvider struct {
	fakeBrowserIdentityProvider
	logoutRedirect identity.LoginRedirect
	logoutReturnTo string
}

type fakeFederatedIdentityProvisioner struct {
	accounts *MemoryAccountStore
	subjects *identity.MemorySubjectStore
	called   bool
}

type fakePersonAccessValidator struct {
	personID string
	revision int64
	err      error
}

type fakeExternalPasswordChanger struct {
	subject  string
	password string
	err      error
}

type fakeInitialPasswordRequirementReader struct {
	required bool
	subject  string
	err      error
}

func (r *fakeInitialPasswordRequirementReader) InitialPasswordChangeRequired(_ context.Context, subject string) (bool, error) {
	r.subject = subject
	return r.required, r.err
}

func (c *fakeExternalPasswordChanger) ChangeInitialPassword(_ context.Context, subject, password string) error {
	c.subject = subject
	c.password = password
	return c.err
}

func (v *fakePersonAccessValidator) ValidatePersonAccess(_ context.Context, personID string, revision int64) error {
	v.personID = personID
	v.revision = revision
	return v.err
}

func (p *fakeFederatedIdentityProvisioner) ProvisionIdentity(ctx context.Context, external identity.ExternalIdentity) (identity.IdentitySubject, error) {
	p.called = true
	if err := p.accounts.SaveAccount(ctx, Account{
		UserID: "feishu-1", Username: "feishu-1", DisplayName: external.DisplayName, AgentProfileID: "human-feishu-1", Enabled: true,
	}); err != nil {
		return identity.IdentitySubject{}, err
	}
	subject := identity.IdentitySubject{
		Provider: external.Provider, Subject: external.Subject, Username: "feishu-1", AgentProfileID: "human-feishu-1", Status: identity.SubjectStatusActive,
	}
	if err := p.subjects.SaveSubject(ctx, subject); err != nil {
		return identity.IdentitySubject{}, err
	}
	return p.subjects.GetSubject(ctx, external.Provider, external.Subject)
}

func (p fakeBrowserIdentityProvider) Name() string {
	return p.name
}

func (p fakeBrowserIdentityProvider) DisplayName() string {
	return p.displayName
}

func (p fakeBrowserIdentityProvider) BeginBrowserLogin(ctx context.Context, req identity.BeginLoginRequest) (identity.LoginRedirect, error) {
	_ = ctx
	return identity.LoginRedirect{URL: "https://idp.example/login?return_to=" + req.ReturnTo}, nil
}

func (p fakeBrowserIdentityProvider) CompleteBrowserLogin(ctx context.Context, req identity.CompleteLoginRequest) (identity.ExternalIdentity, error) {
	_ = ctx
	_ = req
	return p.external, p.err
}

func (p fakeBrowserIdentityProvider) GetBrowserLoginFlow(ctx context.Context, req identity.LoginFlowRequest) (identity.BrowserLoginFlow, error) {
	_ = ctx
	_ = req
	return p.flow, p.flowErr
}

func (p *fakeBrowserIdentityLogoutProvider) BeginBrowserLogout(ctx context.Context, req identity.BeginLogoutRequest) (identity.LoginRedirect, error) {
	_ = ctx
	p.logoutReturnTo = req.ReturnTo
	return p.logoutRedirect, nil
}

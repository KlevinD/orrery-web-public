package userauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
)

func TestTokenPrincipalServiceMapsBearerToUnifiedPrincipal(t *testing.T) {
	ctx := context.Background()
	accounts := NewMemoryAccountStore()
	if err := accounts.SaveAccount(ctx, Account{
		UserID:          "human-operator",
		Username:        "operator",
		DisplayName:     "Operator",
		AgentProfileID:  "human-operator",
		Enabled:         true,
		PermissionScope: []string{"mission.read", "operations.read", "operations.manage"},
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	subjects := identity.NewMemorySubjectStore()
	if err := subjects.SaveSubject(ctx, identity.IdentitySubject{
		Provider:       identity.ProviderHydra,
		Subject:        "hydra-sub-1",
		Username:       "operator",
		AgentProfileID: "human-operator",
		Status:         identity.SubjectStatusActive,
	}); err != nil {
		t.Fatalf("SaveSubject: %v", err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	service := NewTokenPrincipalService(
		NewService(accounts).WithIdentitySubjectStore(subjects),
		identity.NewSubjectMapper(subjects),
		fakeTokenIdentityProvider{external: identity.ExternalIdentity{
			Provider:  identity.ProviderHydra,
			Subject:   "hydra-sub-1",
			Username:  "operator",
			Scopes:    []string{"mission.read", "operations.manage", "resource.manage"},
			ExpiresAt: expiresAt,
		}},
	)

	principal, err := service.AuthenticateBearer(ctx, "opaque-token")
	if err != nil {
		t.Fatalf("AuthenticateBearer: %v", err)
	}
	if principal.SubjectProvider != identity.ProviderHydra || principal.Subject != "hydra-sub-1" || principal.Username != "operator" {
		t.Fatalf("principal = %+v", principal)
	}
	if strings.Join(principal.CoarseScopes, ",") != "mission.read,operations.manage" {
		t.Fatalf("coarse scopes = %+v", principal.CoarseScopes)
	}
	if !principal.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expires_at = %s, want %s", principal.ExpiresAt, expiresAt)
	}
}

func TestTokenPrincipalServiceRejectsDisabledSubject(t *testing.T) {
	ctx := context.Background()
	accounts := NewMemoryAccountStore()
	if err := accounts.SaveAccount(ctx, Account{
		UserID:          "human-operator",
		Username:        "operator",
		AgentProfileID:  "human-operator",
		Enabled:         true,
		PermissionScope: []string{"operations.read"},
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	subjects := identity.NewMemorySubjectStore()
	if err := subjects.SaveSubject(ctx, identity.IdentitySubject{
		Provider:       identity.ProviderHydra,
		Subject:        "hydra-sub-1",
		Username:       "operator",
		AgentProfileID: "human-operator",
		Status:         identity.SubjectStatusDisabled,
	}); err != nil {
		t.Fatalf("SaveSubject: %v", err)
	}
	service := NewTokenPrincipalService(
		NewService(accounts).WithIdentitySubjectStore(subjects),
		identity.NewSubjectMapper(subjects),
		fakeTokenIdentityProvider{external: identity.ExternalIdentity{
			Provider: identity.ProviderHydra,
			Subject:  "hydra-sub-1",
			Username: "operator",
			Scopes:   []string{"operations.read"},
		}},
	)

	if _, err := service.AuthenticateBearer(ctx, "opaque-token"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticateBearer error = %v, want ErrUnauthorized", err)
	}
}

func TestTokenPrincipalServiceRequiresTokenScopes(t *testing.T) {
	ctx := context.Background()
	accounts := NewMemoryAccountStore()
	if err := accounts.SaveAccount(ctx, Account{
		UserID:          "human-operator",
		Username:        "operator",
		AgentProfileID:  "human-operator",
		Enabled:         true,
		PermissionScope: []string{"operations.manage"},
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	subjects := identity.NewMemorySubjectStore()
	if err := subjects.SaveSubject(ctx, identity.IdentitySubject{
		Provider:       identity.ProviderHydra,
		Subject:        "hydra-sub-1",
		Username:       "operator",
		AgentProfileID: "human-operator",
		Status:         identity.SubjectStatusActive,
	}); err != nil {
		t.Fatalf("SaveSubject: %v", err)
	}
	service := NewTokenPrincipalService(
		NewService(accounts).WithIdentitySubjectStore(subjects),
		identity.NewSubjectMapper(subjects),
		fakeTokenIdentityProvider{external: identity.ExternalIdentity{
			Provider: identity.ProviderHydra,
			Subject:  "hydra-sub-1",
			Username: "operator",
		}},
	)

	principal, err := service.AuthenticateBearer(ctx, "opaque-token")
	if err != nil {
		t.Fatalf("AuthenticateBearer: %v", err)
	}
	if len(principal.CoarseScopes) != 0 {
		t.Fatalf("principal should not inherit account scopes without token scopes: %+v", principal.CoarseScopes)
	}
}

func TestTokenPrincipalServiceAllowsWebsiteAdvisorScopeWithoutTenantPermissions(t *testing.T) {
	ctx := context.Background()
	accounts := NewMemoryAccountStore()
	if err := accounts.SaveAccount(ctx, Account{
		UserID: "website-user", Username: "website-user", AgentProfileID: "human-website-user",
		Enabled: true, PermissionScope: []string{"operations.read"},
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	subjects := identity.NewMemorySubjectStore()
	if err := subjects.SaveSubject(ctx, identity.IdentitySubject{
		Provider: identity.ProviderHydra, Subject: "hydra-website-user", Username: "website-user",
		AgentProfileID: "human-website-user", Status: identity.SubjectStatusActive,
	}); err != nil {
		t.Fatalf("SaveSubject: %v", err)
	}
	service := NewTokenPrincipalService(
		NewService(accounts).WithIdentitySubjectStore(subjects), identity.NewSubjectMapper(subjects),
		fakeTokenIdentityProvider{external: identity.ExternalIdentity{
			Provider: identity.ProviderHydra, Subject: "hydra-website-user", Username: "website-user",
			Scopes: []string{"website-advisor", "operations.manage"},
		}},
	)

	principal, err := service.AuthenticateBearer(ctx, "website-token")
	if err != nil {
		t.Fatalf("AuthenticateBearer: %v", err)
	}
	if strings.Join(principal.CoarseScopes, ",") != "website-advisor" || strings.Join(principal.CredentialScopes, ",") != "website-advisor,operations.manage" {
		t.Fatalf("principal scopes = coarse:%+v credential:%+v", principal.CoarseScopes, principal.CredentialScopes)
	}
}

type fakeTokenIdentityProvider struct {
	external identity.ExternalIdentity
	err      error
}

func (p fakeTokenIdentityProvider) Name() string {
	return identity.ProviderHydra
}

func (p fakeTokenIdentityProvider) VerifyAPIToken(ctx context.Context, token string) (identity.ExternalIdentity, error) {
	if p.err != nil {
		return identity.ExternalIdentity{}, p.err
	}
	return identity.NormalizeExternalIdentity(p.external), nil
}

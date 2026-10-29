package identity

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"time"
)

func TestStoreBackedSubjectMapperLinksIdentityWithAgentProfileTrait(t *testing.T) {
	store := NewMemorySubjectStore()
	mapper := NewSubjectMapper(store).WithAutoLink(true)

	subject, err := mapper.ResolveOrLink(context.Background(), ExternalIdentity{
		Provider:    ProviderKratos,
		Subject:     "kratos-user-1",
		Username:    " Operator ",
		DisplayName: " Operator One ",
		Traits:      map[string]string{"agent_profile_id": "human-operator"},
	})
	if err != nil {
		t.Fatalf("ResolveOrLink: %v", err)
	}
	if subject.Provider != ProviderKratos || subject.Subject != "kratos-user-1" || subject.Username != "operator" || subject.AgentProfileID != "human-operator" {
		t.Fatalf("subject = %+v", subject)
	}

	loaded, err := store.GetSubjectByAgentProfileID(context.Background(), ProviderKratos, "human-operator")
	if err != nil {
		t.Fatalf("GetSubjectByAgentProfileID: %v", err)
	}
	if loaded.Subject != "kratos-user-1" {
		t.Fatalf("loaded subject = %+v", loaded)
	}
}

func TestStoreBackedSubjectMapperLinksKratosPersonIDWithoutGenericAutoLink(t *testing.T) {
	store := NewMemorySubjectStore()
	mapper := NewSubjectMapper(store)

	subject, err := mapper.ResolveOrLink(context.Background(), ExternalIdentity{
		Provider: ProviderKratos,
		Subject:  "kratos-quzheng",
		Username: "quzheng",
		Traits:   map[string]string{"person_id": "human-quzheng", "person_revision": "7"},
	})
	if err != nil {
		t.Fatalf("ResolveOrLink: %v", err)
	}
	if subject.AgentProfileID != "human-quzheng" || subject.UserID != "quzheng" {
		t.Fatalf("subject = %+v", subject)
	}
}

func TestStoreBackedSubjectMapperDoesNotTrustPersonIDFromOtherProviders(t *testing.T) {
	mapper := NewSubjectMapper(NewMemorySubjectStore())

	_, err := mapper.ResolveOrLink(context.Background(), ExternalIdentity{
		Provider: ProviderOIDC,
		Subject:  "oidc-quzheng",
		Username: "quzheng",
		Traits:   map[string]string{"person_id": "human-quzheng"},
	})
	if !errors.Is(err, ErrSubjectNotFound) {
		t.Fatalf("ResolveOrLink error = %v, want ErrSubjectNotFound", err)
	}
}

func TestStoreBackedSubjectMapperDoesNotTrustAgentProfileTraitByDefault(t *testing.T) {
	mapper := NewSubjectMapper(NewMemorySubjectStore())

	_, err := mapper.ResolveOrLink(context.Background(), ExternalIdentity{
		Provider: ProviderKratos,
		Subject:  "kratos-user-forged",
		Username: "forged",
		Traits:   map[string]string{"agent_profile_id": "human-superadmin"},
	})
	if !errors.Is(err, ErrSubjectNotFound) {
		t.Fatalf("ResolveOrLink error = %v, want ErrSubjectNotFound", err)
	}
}

func TestStoreBackedSubjectMapperLinksAdministratorProvisionedBreakGlassIdentityWhenEnabled(t *testing.T) {
	mapper := NewSubjectMapper(NewMemorySubjectStore()).WithAutoLink(true)

	subject, err := mapper.ResolveOrLink(context.Background(), ExternalIdentity{
		Provider: ProviderKratos,
		Subject:  "kratos-break-glass",
		Username: "superadmin",
		Traits:   map[string]string{"agent_profile_id": "human-superadmin"},
	})
	if err != nil {
		t.Fatalf("ResolveOrLink break-glass: %v", err)
	}
	if subject.Username != "superadmin" || subject.AgentProfileID != agentcore.SystemSuperadminPrincipalID || subject.Status != SubjectStatusActive {
		t.Fatalf("break-glass subject = %+v", subject)
	}
}

func TestStoreBackedSubjectMapperRejectsOtherAutoLinksToBreakGlassProfile(t *testing.T) {
	for _, external := range []ExternalIdentity{
		{
			Provider: ProviderKratos,
			Subject:  "kratos-forged",
			Username: "operator",
			Traits:   map[string]string{"agent_profile_id": "human-superadmin"},
		},
		{
			Provider: ProviderOIDC,
			Subject:  "oidc-superadmin",
			Username: "superadmin",
			Traits:   map[string]string{"agent_profile_id": "human-superadmin"},
		},
	} {
		mapper := NewSubjectMapper(NewMemorySubjectStore()).WithAutoLink(true)
		if _, err := mapper.ResolveOrLink(context.Background(), external); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("ResolveOrLink(%s/%s) error = %v, want ErrUnauthorized", external.Provider, external.Username, err)
		}
	}
}

func TestStoreBackedSubjectMapperDoesNotAutoLinkUnknownIdentityWithoutTrait(t *testing.T) {
	mapper := NewSubjectMapper(NewMemorySubjectStore())

	_, err := mapper.ResolveOrLink(context.Background(), ExternalIdentity{
		Provider: ProviderKratos,
		Subject:  "kratos-user-unknown",
		Username: "unknown",
	})
	if !errors.Is(err, ErrSubjectNotFound) {
		t.Fatalf("ResolveOrLink error = %v, want ErrSubjectNotFound", err)
	}
}

func TestStoreBackedSubjectMapperRejectsExistingSubjectWithoutExplicitProfileBinding(t *testing.T) {
	store := NewMemorySubjectStore()
	createdAt := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	if err := store.SaveSubject(context.Background(), IdentitySubject{
		ID:        "kratos:kratos-user-2",
		Provider:  ProviderKratos,
		Subject:   "kratos-user-2",
		UserID:    "human-user-2",
		Username:  "user-2",
		CreatedAt: createdAt,
	}); err != nil {
		t.Fatalf("SaveSubject: %v", err)
	}
	mapper := NewSubjectMapper(store)
	loginAt := time.Date(2026, 7, 2, 11, 0, 0, 0, time.UTC)
	mapper.now = func() time.Time { return loginAt }

	_, err := mapper.ResolveOrLink(context.Background(), ExternalIdentity{
		Provider:    ProviderKratos,
		Subject:     "kratos-user-2",
		Email:       "user-2@example.com",
		DisplayName: "User Two",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ResolveOrLink existing error = %v, want ErrUnauthorized", err)
	}
	stored, err := store.GetSubject(context.Background(), ProviderKratos, "kratos-user-2")
	if err != nil {
		t.Fatalf("GetSubject: %v", err)
	}
	if stored.Email != "" || stored.DisplayName != "" || !stored.LastLoginAt.IsZero() {
		t.Fatalf("unbound subject metadata was refreshed: %+v", stored)
	}
}

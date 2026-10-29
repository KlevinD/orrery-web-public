package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type StoreBackedSubjectMapper struct {
	store    SubjectStore
	now      func() time.Time
	autoLink bool
}

func (m *StoreBackedSubjectMapper) WithAutoLink(enabled bool) *StoreBackedSubjectMapper {
	if m != nil {
		m.autoLink = enabled
	}
	return m
}

func NewSubjectMapper(store SubjectStore) *StoreBackedSubjectMapper {
	return &StoreBackedSubjectMapper{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (m *StoreBackedSubjectMapper) ResolveOrLink(ctx context.Context, external ExternalIdentity) (IdentitySubject, error) {
	if m == nil || m.store == nil {
		return IdentitySubject{}, fmt.Errorf("identity subject mapper store is required")
	}
	external = NormalizeExternalIdentity(external)
	if external.Provider == "" || external.Subject == "" {
		return IdentitySubject{}, fmt.Errorf("external identity provider and subject are required")
	}
	if external.Provider == ProviderHydra && external.Traits["oauth2_subject_provider"] != "" {
		return m.resolveOAuth2Subject(ctx, external)
	}
	personID := ""
	if external.Provider == ProviderKratos {
		personID = strings.TrimSpace(external.Traits["person_id"])
	}
	personID = canonicalBreakGlassPrincipalID(external.Provider, external.Username, personID)
	existing, err := m.store.GetSubject(ctx, external.Provider, external.Subject)
	if err == nil {
		existing.AgentProfileID = canonicalBreakGlassPrincipalID(external.Provider, external.Username, existing.AgentProfileID)
		if strings.TrimSpace(existing.AgentProfileID) == "" ||
			(personID != "" && existing.AgentProfileID != personID) ||
			!canTargetProtectedBreakGlassProfile(external.Provider, external.Username, existing.AgentProfileID) {
			return IdentitySubject{}, ErrUnauthorized
		}
		existing.LastLoginAt = m.now()
		existing.Email = firstNonEmpty(external.Email, existing.Email)
		existing.DisplayName = firstNonEmpty(external.DisplayName, existing.DisplayName)
		if saveErr := m.store.SaveSubject(ctx, existing); saveErr != nil {
			return IdentitySubject{}, saveErr
		}
		return NormalizeIdentitySubject(existing), nil
	}
	if !errors.Is(err, ErrSubjectNotFound) {
		return IdentitySubject{}, err
	}
	agentProfileID := personID
	if agentProfileID == "" {
		if !m.autoLink {
			return IdentitySubject{}, ErrSubjectNotFound
		}
		agentProfileID = external.Traits["agent_profile_id"]
	}
	agentProfileID = canonicalBreakGlassPrincipalID(external.Provider, external.Username, agentProfileID)
	if agentProfileID == "" {
		return IdentitySubject{}, ErrSubjectNotFound
	}
	if !canTargetProtectedBreakGlassProfile(external.Provider, external.Username, agentProfileID) {
		return IdentitySubject{}, ErrUnauthorized
	}
	subject := IdentitySubject{
		Provider:       external.Provider,
		Subject:        external.Subject,
		UserID:         firstNonEmpty(external.Username, external.Subject),
		Username:       firstNonEmpty(external.Username, external.Subject),
		Email:          external.Email,
		DisplayName:    firstNonEmpty(external.DisplayName, external.Username, external.Subject),
		AgentProfileID: agentProfileID,
		Status:         SubjectStatusActive,
		LastLoginAt:    m.now(),
	}
	if err := m.store.SaveSubject(ctx, subject); err != nil {
		return IdentitySubject{}, err
	}
	return m.store.GetSubject(ctx, subject.Provider, subject.Subject)
}

// Delegated tokens may reuse an existing binding, never create or retarget one.
func (m *StoreBackedSubjectMapper) resolveOAuth2Subject(ctx context.Context, external ExternalIdentity) (IdentitySubject, error) {
	provider := normalizeLower(external.Traits["oauth2_subject_provider"])
	source := strings.TrimSpace(external.Traits["oauth2_subject"])
	profileID := strings.TrimSpace(external.Traits["agent_profile_id"])
	if (provider != ProviderKratos && provider != ProviderLocal && provider != ProviderFeishu) ||
		source == "" || profileID == "" || external.Subject != profileID {
		return IdentitySubject{}, ErrUnauthorized
	}
	existing, err := m.store.GetSubject(ctx, provider, source)
	if err != nil {
		return IdentitySubject{}, err
	}
	existing = NormalizeIdentitySubject(existing)
	existing.AgentProfileID = canonicalBreakGlassPrincipalID(existing.Provider, existing.Username, existing.AgentProfileID)
	if existing.Provider != provider || existing.Subject != source || existing.Status != SubjectStatusActive ||
		existing.AgentProfileID != profileID ||
		!canTargetProtectedBreakGlassProfile(existing.Provider, existing.Username, existing.AgentProfileID) {
		return IdentitySubject{}, ErrUnauthorized
	}
	return existing, nil
}

func canTargetProtectedBreakGlassProfile(provider, username, agentProfileID string) bool {
	if !isProtectedBreakGlassProfileID(agentProfileID) {
		return true
	}
	return IsProtectedBreakGlassIdentity(provider, username, agentProfileID)
}

func IsProtectedBreakGlassIdentity(provider, username, agentProfileID string) bool {
	return isProtectedBreakGlassProfileID(agentProfileID) &&
		normalizeLower(provider) == ProviderKratos &&
		normalizeUsername(username) == "superadmin"
}

func isProtectedBreakGlassProfileID(profileID string) bool {
	profileID = strings.TrimSpace(profileID)
	return strings.EqualFold(profileID, agentcore.SystemSuperadminPrincipalID) ||
		strings.EqualFold(profileID, agentcore.LegacySuperadminAgentProfileID)
}

func canonicalBreakGlassPrincipalID(provider, username, profileID string) string {
	profileID = strings.TrimSpace(profileID)
	if normalizeLower(provider) == ProviderKratos && normalizeUsername(username) == "superadmin" && isProtectedBreakGlassProfileID(profileID) {
		return agentcore.SystemSuperadminPrincipalID
	}
	return profileID
}

var _ SubjectMapper = (*StoreBackedSubjectMapper)(nil)

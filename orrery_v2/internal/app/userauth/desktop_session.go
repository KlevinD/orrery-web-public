package userauth

import (
	"context"
	"strings"

	"orrery_v2/internal/app/identity"
)

func desktopIdentitySession(external identity.ExternalIdentity) bool {
	if external.Provider != identity.ProviderHydra || external.Traits["oidc_client_id"] != "orrery-desktop" ||
		external.Traits["oauth2_subject_provider"] == "" || external.Traits["oauth2_subject"] == "" ||
		external.ExpiresAt.IsZero() {
		return false
	}
	var openid, profile bool
	for _, scope := range strings.Fields(external.Traits["oidc_scope"]) {
		switch scope {
		case "openid":
			openid = true
		case "profile":
			profile = true
		case "offline_access":
		default:
			// Legacy capability-bearing tokens retain their explicit scope ceiling.
			return false
		}
	}
	return openid && profile
}

// AuthenticateBearerSession reads an existing desktop identity, without issuing
// a Web session, setting cookies, or exposing either OAuth credential.
func (s *TokenPrincipalService) AuthenticateBearerSession(ctx context.Context, token string) (SessionDTO, error) {
	external, subject, err := s.resolveBearer(ctx, token)
	if err != nil {
		return SessionDTO{}, err
	}
	if external.Provider != identity.ProviderHydra || external.Traits["oidc_client_id"] != "orrery-desktop" ||
		external.Traits["oauth2_subject_provider"] == "" || external.Traits["oauth2_subject"] == "" {
		return SessionDTO{}, ErrUnauthorized
	}
	principal, err := s.sessions.principalForIdentitySubject(ctx, subject, external.Scopes, external.ExpiresAt, desktopIdentitySession(external))
	if err != nil {
		return SessionDTO{}, err
	}
	account, err := s.sessions.accountForIdentitySubject(ctx, subject)
	if err != nil {
		return SessionDTO{}, ErrUnauthorized
	}
	dto := sessionDTO(account, SessionRecord{
		SubjectProvider: subject.Provider, Subject: subject.Subject,
		IdentitySubjectID: subject.ID, ExpiresAt: external.ExpiresAt,
	})
	dto.PermissionScope, err = s.sessions.resolvePermissionScopes(ctx, principal, principal.CoarseScopes)
	if err != nil {
		return SessionDTO{}, err
	}
	if principal.CredentialScopeRestricted {
		dto.PermissionScope = intersectScopes(dto.PermissionScope, principal.CredentialScopes)
	}
	return dto, nil
}

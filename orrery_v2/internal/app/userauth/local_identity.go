package userauth

import (
	"context"

	"orrery_v2/internal/app/identity"
)

type LocalIdentityProvider struct {
	auth *Service
}

func NewLocalIdentityProvider(auth *Service) *LocalIdentityProvider {
	return &LocalIdentityProvider{auth: auth}
}

func (p *LocalIdentityProvider) Name() string {
	return identity.ProviderLocal
}

func (p *LocalIdentityProvider) VerifyAPIToken(ctx context.Context, token string) (identity.ExternalIdentity, error) {
	if p == nil || p.auth == nil {
		return identity.ExternalIdentity{}, ErrServiceDependency
	}
	session, err := p.auth.AuthenticateToken(ctx, token)
	if err != nil {
		return identity.ExternalIdentity{}, err
	}
	return ExternalIdentityFromSession(session), nil
}

func ExternalIdentityFromSession(session SessionDTO) identity.ExternalIdentity {
	traits := map[string]string{}
	if session.User.AgentProfileID != "" {
		traits["agent_profile_id"] = session.User.AgentProfileID
	}
	return identity.NormalizeExternalIdentity(identity.ExternalIdentity{
		Provider:    identity.ProviderLocal,
		Subject:     firstNonEmpty(session.User.UserID, session.User.Username),
		Username:    session.User.Username,
		Email:       session.User.Email,
		DisplayName: firstNonEmpty(session.User.DisplayName, session.User.Nickname, session.User.Username),
		Traits:      traits,
		Scopes:      session.PermissionScope,
		ExpiresAt:   session.ExpiresAt,
	})
}

var _ identity.TokenIdentityProvider = (*LocalIdentityProvider)(nil)

package userauth

import (
	"context"
	"errors"

	"orrery_v2/internal/app/identity"
)

type TokenPrincipalService struct {
	provider identity.TokenIdentityProvider
	mapper   identity.SubjectMapper
	sessions *Service
}

func NewTokenPrincipalService(sessions *Service, mapper identity.SubjectMapper, provider identity.TokenIdentityProvider) *TokenPrincipalService {
	return &TokenPrincipalService{
		provider: provider,
		mapper:   mapper,
		sessions: sessions,
	}
}

func (s *TokenPrincipalService) AuthenticateBearer(ctx context.Context, accessToken string) (identity.Principal, error) {
	external, subject, err := s.resolveBearer(ctx, accessToken)
	if err != nil {
		return identity.Principal{}, err
	}
	return s.sessions.principalForIdentitySubject(ctx, subject, external.Scopes, external.ExpiresAt, desktopIdentitySession(external))
}

func (s *TokenPrincipalService) resolveBearer(ctx context.Context, accessToken string) (identity.ExternalIdentity, identity.IdentitySubject, error) {
	if s == nil || s.provider == nil || s.mapper == nil || s.sessions == nil {
		return identity.ExternalIdentity{}, identity.IdentitySubject{}, ErrServiceDependency
	}
	external, err := s.provider.VerifyAPIToken(ctx, accessToken)
	if err != nil {
		if errors.Is(err, identity.ErrUnauthorized) {
			return identity.ExternalIdentity{}, identity.IdentitySubject{}, ErrUnauthorized
		}
		return identity.ExternalIdentity{}, identity.IdentitySubject{}, err
	}
	subject, err := s.mapper.ResolveOrLink(ctx, external)
	if err != nil {
		if errors.Is(err, identity.ErrSubjectNotFound) || errors.Is(err, identity.ErrUnauthorized) {
			return identity.ExternalIdentity{}, identity.IdentitySubject{}, ErrUnauthorized
		}
		return identity.ExternalIdentity{}, identity.IdentitySubject{}, err
	}
	return external, subject, nil
}

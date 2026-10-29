package authorization

import (
	"context"
	"fmt"

	"orrery_v2/internal/app/identity"
)

type AuthorizationRevisionReader interface {
	AuthorizationRevision(context.Context) (int64, error)
}

type PolicyRevisionReader interface {
	PolicyRevision(context.Context) (int64, error)
}

type authorizationRevisionSource struct {
	reader AuthorizationRevisionReader
}

func NewAuthorizationRevisionSource(reader AuthorizationRevisionReader) RevisionSource {
	return authorizationRevisionSource{reader: reader}
}

func (s authorizationRevisionSource) Revision(ctx context.Context, _ identity.Principal) (string, error) {
	if s.reader == nil {
		return "", ErrServiceDependency
	}
	revision, err := s.reader.AuthorizationRevision(ctx)
	if err != nil {
		return "", err
	}
	if revision < 0 {
		return "", fmt.Errorf("%w: authorization revision must be non-negative", ErrInvalidRequest)
	}
	return fmt.Sprintf("authorization:%d", revision), nil
}

type policyRevisionSource struct {
	reader PolicyRevisionReader
}

func NewPolicyRevisionSource(reader PolicyRevisionReader) RevisionSource {
	return policyRevisionSource{reader: reader}
}

func (s policyRevisionSource) Revision(ctx context.Context, _ identity.Principal) (string, error) {
	if s.reader == nil {
		return "", ErrServiceDependency
	}
	revision, err := s.reader.PolicyRevision(ctx)
	if err != nil {
		return "", err
	}
	if revision < 0 {
		return "", fmt.Errorf("%w: policy revision must be non-negative", ErrInvalidRequest)
	}
	return fmt.Sprintf("policy:%d", revision), nil
}

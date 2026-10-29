package authorization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
)

type AuthorizationSnapshot struct {
	PrincipalKey          string
	AuthorizationRevision string
	PolicyRevision        string
	CapturedAt            time.Time
}

func (s AuthorizationSnapshot) Cacheable() bool {
	return strings.TrimSpace(s.PrincipalKey) != "" &&
		strings.TrimSpace(s.AuthorizationRevision) != "" &&
		strings.TrimSpace(s.PolicyRevision) != ""
}

type SnapshotProvider interface {
	Capture(ctx context.Context, principal identity.Principal) (AuthorizationSnapshot, error)
}

type RevisionSource interface {
	Revision(ctx context.Context, principal identity.Principal) (string, error)
}

type SnapshotService struct {
	authorization RevisionSource
	policy        RevisionSource
	now           func() time.Time
}

func NewSnapshotService(authorization RevisionSource, policy RevisionSource) *SnapshotService {
	return &SnapshotService{authorization: authorization, policy: policy, now: time.Now}
}

func (s *SnapshotService) Capture(ctx context.Context, principal identity.Principal) (AuthorizationSnapshot, error) {
	principal = identity.NormalizePrincipal(principal)
	if !principal.Authenticated() {
		return AuthorizationSnapshot{}, fmt.Errorf("%w: authenticated principal is required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return AuthorizationSnapshot{}, err
	}
	snapshot := AuthorizationSnapshot{
		PrincipalKey: PrincipalCacheKey(principal),
		CapturedAt:   time.Now().UTC(),
	}
	if s != nil && s.now != nil {
		snapshot.CapturedAt = s.now().UTC()
	}
	var err error
	if s != nil && s.authorization != nil {
		snapshot.AuthorizationRevision, err = s.authorization.Revision(ctx, principal)
		if err != nil {
			return AuthorizationSnapshot{}, err
		}
		snapshot.AuthorizationRevision = strings.TrimSpace(snapshot.AuthorizationRevision)
	}
	if s != nil && s.policy != nil {
		snapshot.PolicyRevision, err = s.policy.Revision(ctx, principal)
		if err != nil {
			return AuthorizationSnapshot{}, err
		}
		snapshot.PolicyRevision = strings.TrimSpace(snapshot.PolicyRevision)
	}
	return snapshot, nil
}

func PrincipalCacheKey(principal identity.Principal) string {
	principal = identity.NormalizePrincipal(principal)
	if !principal.Authenticated() {
		return ""
	}
	value := strings.Join([]string{
		principal.SubjectProvider,
		principal.Subject,
		principal.ViewerKind,
		principal.UserID,
		principal.AgentProfileID,
	}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

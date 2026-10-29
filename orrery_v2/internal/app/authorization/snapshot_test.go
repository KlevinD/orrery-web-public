package authorization

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
)

type fixedRevisionSource struct {
	revision string
	err      error
}

func (s fixedRevisionSource) Revision(context.Context, identity.Principal) (string, error) {
	return s.revision, s.err
}

func TestSnapshotServiceCapturesTrustedRevisions(t *testing.T) {
	service := NewSnapshotService(fixedRevisionSource{revision: "auth-42"}, fixedRevisionSource{revision: "policy-7"})
	service.now = func() time.Time { return time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC) }
	principal := testSnapshotPrincipal()

	snapshot, err := service.Capture(context.Background(), principal)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if snapshot.PrincipalKey == "" || snapshot.AuthorizationRevision != "auth-42" || snapshot.PolicyRevision != "policy-7" || !snapshot.Cacheable() {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if !snapshot.CapturedAt.Equal(service.now()) {
		t.Fatalf("captured_at = %s", snapshot.CapturedAt)
	}
	if snapshot.PrincipalKey != PrincipalCacheKey(principal) {
		t.Fatal("principal cache key is not stable")
	}
}

func TestSnapshotServiceWithoutRevisionFailsClosedForCaching(t *testing.T) {
	snapshot, err := NewSnapshotService(nil, nil).Capture(context.Background(), testSnapshotPrincipal())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if snapshot.AuthorizationRevision != "" || snapshot.Cacheable() {
		t.Fatalf("snapshot must not be cacheable without a real revision: %#v", snapshot)
	}
}

func TestSnapshotServiceRejectsInvalidPrincipalAndDependencyFailure(t *testing.T) {
	if _, err := NewSnapshotService(nil, nil).Capture(context.Background(), identity.Principal{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Capture unauthenticated error = %v", err)
	}
	want := errors.New("revision unavailable")
	if _, err := NewSnapshotService(fixedRevisionSource{err: want}, nil).Capture(context.Background(), testSnapshotPrincipal()); !errors.Is(err, want) {
		t.Fatalf("Capture dependency error = %v", err)
	}
}

func testSnapshotPrincipal() identity.Principal {
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc",
		Subject:         "subject-1",
		UserID:          "user-1",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{"operations.read"},
	})
}

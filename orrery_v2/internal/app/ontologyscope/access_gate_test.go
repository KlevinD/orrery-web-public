package ontologyscope

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
)

type fixedBindingResolver struct {
	binding ScopeBinding
	calls   int
}

func (r *fixedBindingResolver) Resolve(context.Context, string) (ScopeBinding, error) {
	r.calls++
	return r.binding, nil
}

type recordingAccessAuthorizer struct {
	featureErr    error
	objectErr     error
	featureAction string
	objectAction  string
	object        identity.ObjectRef
	tenantID      string
}

func (a *recordingAccessAuthorizer) AuthorizeFeature(_ context.Context, _ identity.Principal, action string) error {
	a.featureAction = action
	return a.featureErr
}

func (a *recordingAccessAuthorizer) AuthorizeObject(_ context.Context, _ identity.Principal, action string, object identity.ObjectRef, tenantID string) error {
	a.objectAction = action
	a.object = object
	a.tenantID = tenantID
	return a.objectErr
}

type fixedSnapshotProvider struct {
	snapshot authorization.AuthorizationSnapshot
	err      error
	calls    int
}

func (p *fixedSnapshotProvider) Capture(context.Context, identity.Principal) (authorization.AuthorizationSnapshot, error) {
	p.calls++
	return p.snapshot, p.err
}

type changingSnapshotProvider struct {
	principalKey string
	calls        int
}

func (p *changingSnapshotProvider) Capture(context.Context, identity.Principal) (authorization.AuthorizationSnapshot, error) {
	p.calls++
	return authorization.AuthorizationSnapshot{PrincipalKey: p.principalKey, AuthorizationRevision: string(rune('0' + p.calls)), PolicyRevision: "policy-1"}, nil
}

func TestScopeAccessGateAuthorizesReadAndManageUsingBinding(t *testing.T) {
	binding := testBinding("mission:1", KindMission, "mission", "1", "org-1")
	resolver := &fixedBindingResolver{binding: binding}
	authorizer := &recordingAccessAuthorizer{}
	principal := testGatePrincipal()
	snapshot := authorization.AuthorizationSnapshot{PrincipalKey: authorization.PrincipalCacheKey(principal), AuthorizationRevision: "auth-1", CapturedAt: time.Now()}
	provider := &fixedSnapshotProvider{snapshot: snapshot}
	gate := NewScopeAccessGate(resolver, authorizer, provider)

	read, err := gate.AuthorizeRead(context.Background(), principal, binding.TenantID)
	if err != nil {
		t.Fatalf("AuthorizeRead: %v", err)
	}
	if read.Manage() || read.Binding().TenantID != binding.TenantID || read.Snapshot().AuthorizationRevision != "auth-1" {
		t.Fatalf("read context = %#v", read)
	}
	if authorizer.featureAction != FeatureReadAction || authorizer.objectAction != binding.ReadAction || authorizer.object.TenantID != "org-1" || authorizer.tenantID != "org-1" {
		t.Fatalf("read authorization = %#v", authorizer)
	}

	manage, err := gate.AuthorizeManage(context.Background(), principal, binding.TenantID)
	if err != nil {
		t.Fatalf("AuthorizeManage: %v", err)
	}
	if !manage.Manage() || authorizer.featureAction != FeatureManageAction || authorizer.objectAction != binding.ManageAction {
		t.Fatalf("manage authorization = %#v, context = %#v", authorizer, manage)
	}
}

func TestScopeAccessGateChecksFeatureBeforeResolvingTenant(t *testing.T) {
	resolver := &fixedBindingResolver{binding: testBinding("mission:secret", KindMission, "mission", "secret", "org-1")}
	authorizer := &recordingAccessAuthorizer{featureErr: ErrAccessDenied}
	provider := &fixedSnapshotProvider{snapshot: authorization.AuthorizationSnapshot{PrincipalKey: authorization.PrincipalCacheKey(testGatePrincipal())}}
	gate := NewScopeAccessGate(resolver, authorizer, provider)

	if _, err := gate.AuthorizeRead(context.Background(), testGatePrincipal(), "mission:secret"); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("AuthorizeRead error = %v", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls = %d, tenant was probed before feature authorization", resolver.calls)
	}
}

func TestScopeAccessGateFailsClosedOnObjectAndSnapshotDependencies(t *testing.T) {
	binding := testBinding("mission:1", KindMission, "mission", "1", "org-1")
	resolver := &fixedBindingResolver{binding: binding}
	authorizer := &recordingAccessAuthorizer{objectErr: ErrAccessDenied}
	provider := &fixedSnapshotProvider{snapshot: authorization.AuthorizationSnapshot{PrincipalKey: authorization.PrincipalCacheKey(testGatePrincipal())}}
	gate := NewScopeAccessGate(resolver, authorizer, provider)
	if _, err := gate.AuthorizeRead(context.Background(), testGatePrincipal(), binding.TenantID); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("object denied error = %v", err)
	}

	authorizer.objectErr = nil
	gate = NewScopeAccessGate(resolver, authorizer, &fixedSnapshotProvider{err: errors.New("revision unavailable")})
	if _, err := gate.AuthorizeRead(context.Background(), testGatePrincipal(), binding.TenantID); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("snapshot error = %v", err)
	}

	if _, err := NewScopeAccessGate(nil, nil, nil).AuthorizeRead(context.Background(), testGatePrincipal(), binding.TenantID); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("missing dependency error = %v", err)
	}

	authorizer.objectErr = errors.New("provider unavailable")
	gate = NewScopeAccessGate(resolver, authorizer, provider)
	if _, err := gate.AuthorizeRead(context.Background(), testGatePrincipal(), binding.TenantID); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("authorization provider error = %v", err)
	}
}

func TestScopeAccessGateRejectsRevisionChangeDuringAuthorization(t *testing.T) {
	binding := testBinding("mission:1", KindMission, "mission", "1", "org-1")
	provider := &changingSnapshotProvider{principalKey: authorization.PrincipalCacheKey(testGatePrincipal())}
	gate := NewScopeAccessGate(&fixedBindingResolver{binding: binding}, &recordingAccessAuthorizer{}, provider)

	if _, err := gate.AuthorizeRead(context.Background(), testGatePrincipal(), binding.TenantID); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("AuthorizeRead revision change error = %v", err)
	}
}

func testGatePrincipal() identity.Principal {
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc",
		Subject:         "subject-1",
		UserID:          "user-1",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{"operations.read", "operations.manage"},
	})
}

package ontologyscope

import (
	"context"
	"errors"
	"fmt"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
)

const FeatureReadAction = "ontology.read"
const FeatureManageAction = "ontology.manage"

type BindingResolver interface {
	Resolve(ctx context.Context, tenantID string) (ScopeBinding, error)
}

type AccessAuthorizer interface {
	AuthorizeFeature(ctx context.Context, principal identity.Principal, action string) error
	AuthorizeObject(ctx context.Context, principal identity.Principal, action string, object identity.ObjectRef, authorizationTenantID string) error
}

type ObjectAuthorizationRequest struct {
	Action                string
	Object                identity.ObjectRef
	AuthorizationTenantID string
}

type BatchAccessAuthorizer interface {
	AuthorizeObjects(ctx context.Context, principal identity.Principal, requests []ObjectAuthorizationRequest) ([]bool, error)
}

type ScopeAccessGate struct {
	resolver   BindingResolver
	authorizer AccessAuthorizer
	snapshots  authorization.SnapshotProvider
}

func NewScopeAccessGate(resolver BindingResolver, authorizer AccessAuthorizer, snapshots authorization.SnapshotProvider) *ScopeAccessGate {
	return &ScopeAccessGate{resolver: resolver, authorizer: authorizer, snapshots: snapshots}
}

type AuthorizedScopeContext struct {
	binding  ScopeBinding
	snapshot authorization.AuthorizationSnapshot
	manage   bool
}

func (c AuthorizedScopeContext) Binding() ScopeBinding {
	return c.binding
}

func (c AuthorizedScopeContext) Snapshot() authorization.AuthorizationSnapshot {
	return c.snapshot
}

func (c AuthorizedScopeContext) Manage() bool {
	return c.manage
}

func (g *ScopeAccessGate) AuthorizeRead(ctx context.Context, principal identity.Principal, tenantID string) (AuthorizedScopeContext, error) {
	return g.authorize(ctx, principal, tenantID, false)
}

func (g *ScopeAccessGate) AuthorizeManage(ctx context.Context, principal identity.Principal, tenantID string) (AuthorizedScopeContext, error) {
	return g.authorize(ctx, principal, tenantID, true)
}

func (g *ScopeAccessGate) AuthorizeFeatureRead(ctx context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	return g.authorizeFeature(ctx, principal, FeatureReadAction)
}

func (g *ScopeAccessGate) AuthorizeFeatureManage(ctx context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	return g.authorizeFeature(ctx, principal, FeatureManageAction)
}

func (g *ScopeAccessGate) authorizeFeature(ctx context.Context, principal identity.Principal, action string) (authorization.AuthorizationSnapshot, error) {
	if g == nil || g.authorizer == nil || g.snapshots == nil {
		return authorization.AuthorizationSnapshot{}, ErrDependencyUnavailable
	}
	principal = identity.NormalizePrincipal(principal)
	if !principal.Authenticated() {
		return authorization.AuthorizationSnapshot{}, fmt.Errorf("%w: authenticated principal is required", ErrAccessDenied)
	}
	before, err := g.captureSnapshot(ctx, principal)
	if err != nil {
		return authorization.AuthorizationSnapshot{}, err
	}
	if err := g.authorizer.AuthorizeFeature(ctx, principal, action); err != nil {
		if ctx.Err() != nil {
			return authorization.AuthorizationSnapshot{}, ctx.Err()
		}
		return authorization.AuthorizationSnapshot{}, scopeAuthorizationError(err, "feature authorization failed")
	}
	after, err := g.captureSnapshot(ctx, principal)
	if err != nil {
		return authorization.AuthorizationSnapshot{}, err
	}
	if revisionsChanged(before, after) {
		return authorization.AuthorizationSnapshot{}, fmt.Errorf("%w: authorization revision changed during feature check", ErrDependencyUnavailable)
	}
	return after, nil
}

func (g *ScopeAccessGate) authorizeAtSnapshot(ctx context.Context, principal identity.Principal, tenantID string, expected authorization.AuthorizationSnapshot, manage bool) (AuthorizedScopeContext, error) {
	if g == nil || g.resolver == nil || g.authorizer == nil || g.snapshots == nil {
		return AuthorizedScopeContext{}, ErrDependencyUnavailable
	}
	principal = identity.NormalizePrincipal(principal)
	if !principal.Authenticated() || expected.PrincipalKey == "" || expected.PrincipalKey != authorization.PrincipalCacheKey(principal) {
		return AuthorizedScopeContext{}, fmt.Errorf("%w: authenticated snapshot principal is required", ErrAccessDenied)
	}
	binding, err := g.resolver.Resolve(ctx, tenantID)
	if err != nil {
		return AuthorizedScopeContext{}, err
	}
	action := binding.ReadAction
	if manage {
		action = binding.ManageAction
	}
	object := binding.SourceObject
	object.TenantID = binding.AuthorizationTenantID
	if err := g.authorizer.AuthorizeObject(ctx, principal, action, object, binding.AuthorizationTenantID); err != nil {
		if ctx.Err() != nil {
			return AuthorizedScopeContext{}, ctx.Err()
		}
		return AuthorizedScopeContext{}, scopeAuthorizationError(err, "source object authorization failed")
	}
	return AuthorizedScopeContext{binding: binding, snapshot: expected, manage: manage}, nil
}

func (g *ScopeAccessGate) validateSnapshot(ctx context.Context, principal identity.Principal, expected authorization.AuthorizationSnapshot) error {
	principal = identity.NormalizePrincipal(principal)
	if !principal.Authenticated() || expected.PrincipalKey == "" || expected.PrincipalKey != authorization.PrincipalCacheKey(principal) {
		return fmt.Errorf("%w: authenticated snapshot principal is required", ErrAccessDenied)
	}
	after, err := g.captureSnapshot(ctx, principal)
	if err != nil {
		return err
	}
	if revisionsChanged(expected, after) {
		return fmt.Errorf("%w: authorization revision changed during scope check", ErrDependencyUnavailable)
	}
	return nil
}

func (g *ScopeAccessGate) authorize(ctx context.Context, principal identity.Principal, tenantID string, manage bool) (AuthorizedScopeContext, error) {
	if g == nil || g.resolver == nil || g.authorizer == nil || g.snapshots == nil {
		return AuthorizedScopeContext{}, ErrDependencyUnavailable
	}
	principal = identity.NormalizePrincipal(principal)
	if !principal.Authenticated() {
		return AuthorizedScopeContext{}, fmt.Errorf("%w: authenticated principal is required", ErrAccessDenied)
	}
	before, err := g.captureSnapshot(ctx, principal)
	if err != nil {
		return AuthorizedScopeContext{}, err
	}
	featureAction := FeatureReadAction
	if manage {
		featureAction = FeatureManageAction
	}
	if err := g.authorizer.AuthorizeFeature(ctx, principal, featureAction); err != nil {
		if ctx.Err() != nil {
			return AuthorizedScopeContext{}, ctx.Err()
		}
		return AuthorizedScopeContext{}, scopeAuthorizationError(err, "feature authorization failed")
	}
	binding, err := g.resolver.Resolve(ctx, tenantID)
	if err != nil {
		return AuthorizedScopeContext{}, err
	}
	action := binding.ReadAction
	if manage {
		action = binding.ManageAction
	}
	object := binding.SourceObject
	object.TenantID = binding.AuthorizationTenantID
	if err := g.authorizer.AuthorizeObject(ctx, principal, action, object, binding.AuthorizationTenantID); err != nil {
		if ctx.Err() != nil {
			return AuthorizedScopeContext{}, ctx.Err()
		}
		return AuthorizedScopeContext{}, scopeAuthorizationError(err, "source object authorization failed")
	}
	after, err := g.captureSnapshot(ctx, principal)
	if err != nil {
		return AuthorizedScopeContext{}, err
	}
	if revisionsChanged(before, after) {
		return AuthorizedScopeContext{}, fmt.Errorf("%w: authorization revision changed during scope check", ErrDependencyUnavailable)
	}
	return AuthorizedScopeContext{binding: binding, snapshot: after, manage: manage}, nil
}

func scopeAuthorizationError(err error, message string) error {
	if errors.Is(err, ErrAccessDenied) || errors.Is(err, authorization.ErrDenied) {
		return fmt.Errorf("%w: %s", ErrAccessDenied, message)
	}
	return fmt.Errorf("%w: %s", ErrDependencyUnavailable, message)
}

func (g *ScopeAccessGate) captureSnapshot(ctx context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	snapshot, err := g.snapshots.Capture(ctx, principal)
	if err != nil {
		if ctx.Err() != nil {
			return authorization.AuthorizationSnapshot{}, ctx.Err()
		}
		return authorization.AuthorizationSnapshot{}, fmt.Errorf("%w: capture authorization snapshot: %v", ErrDependencyUnavailable, err)
	}
	if snapshot.PrincipalKey == "" || snapshot.PrincipalKey != authorization.PrincipalCacheKey(principal) {
		return authorization.AuthorizationSnapshot{}, fmt.Errorf("%w: authorization snapshot principal mismatch", ErrDependencyUnavailable)
	}
	return snapshot, nil
}

func revisionsChanged(before authorization.AuthorizationSnapshot, after authorization.AuthorizationSnapshot) bool {
	return before.AuthorizationRevision != after.AuthorizationRevision || before.PolicyRevision != after.PolicyRevision
}

package authorization

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type authorizationRevisionReaderFunc func(context.Context) (int64, error)

func (f authorizationRevisionReaderFunc) AuthorizationRevision(ctx context.Context) (int64, error) {
	return f(ctx)
}

type policyRevisionReaderFunc func(context.Context) (int64, error)

func (f policyRevisionReaderFunc) PolicyRevision(ctx context.Context) (int64, error) {
	return f(ctx)
}

func TestAuthorizationSnapshotUsesMonotonicStoreRevisions(t *testing.T) {
	authorizationSource := NewAuthorizationRevisionSource(authorizationRevisionReaderFunc(func(context.Context) (int64, error) { return 42, nil }))
	policySource := NewPolicyRevisionSource(policyRevisionReaderFunc(func(context.Context) (int64, error) { return 7, nil }))
	snapshot, err := NewSnapshotService(authorizationSource, policySource).Capture(context.Background(), testSnapshotPrincipal())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if snapshot.AuthorizationRevision != "authorization:42" || snapshot.PolicyRevision != "policy:7" || !snapshot.Cacheable() {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestAuthorizationSnapshotRejectsInvalidOrUnavailableRevision(t *testing.T) {
	want := errors.New("revision unavailable")
	failed := NewAuthorizationRevisionSource(authorizationRevisionReaderFunc(func(context.Context) (int64, error) { return 0, want }))
	if _, err := failed.Revision(context.Background(), testSnapshotPrincipal()); !errors.Is(err, want) {
		t.Fatalf("dependency error = %v", err)
	}
	invalid := NewPolicyRevisionSource(policyRevisionReaderFunc(func(context.Context) (int64, error) { return -1, nil }))
	if _, err := invalid.Revision(context.Background(), testSnapshotPrincipal()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("negative revision error = %v", err)
	}
}

func TestRevisionInvalidationIsImmediateAndSubscriberIsolated(t *testing.T) {
	hub := NewInvalidationHub()
	var mu sync.Mutex
	cached := map[string]bool{"principal:scope": true}
	stop := hub.Subscribe(func(event InvalidationEvent) {
		if event.Kind != InvalidationAuthorization || event.Revision != 2 {
			t.Fatalf("invalidation event = %+v", event)
		}
		mu.Lock()
		delete(cached, "principal:scope")
		mu.Unlock()
	})
	hub.Subscribe(func(InvalidationEvent) { panic("subscriber failure") })
	started := time.Now()
	hub.AuthorizationRevisionAdvanced(2)
	if time.Since(started) >= 5*time.Second {
		t.Fatal("local invalidation exceeded the read-revocation target")
	}
	mu.Lock()
	_, remains := cached["principal:scope"]
	mu.Unlock()
	if remains {
		t.Fatal("cached authorization survived revision invalidation")
	}
	stop()
	hub.AuthorizationRevisionAdvanced(3)
}

package platform

import (
	"context"
	"fmt"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/lock"
)

type LockManager struct {
	store      *lock.Store
	clock      clock.Clock
	defaultTTL time.Duration
}

type LockLease struct {
	manager *LockManager
	lease   *lock.Lease
}

func NewLockManager(store *lock.Store, platformClock clock.Clock, defaultTTL time.Duration) *LockManager {
	return &LockManager{store: store, clock: platformClock, defaultTTL: defaultTTL}
}

func (m *LockManager) TryAcquire(ctx context.Context, key, owner string) (*LockLease, bool, error) {
	if m == nil || m.store == nil {
		return nil, false, fmt.Errorf("platform lock manager is not configured")
	}
	lease, ok, err := m.store.TryAcquire(ctx, key, owner, m.ttl(), m.now())
	if err != nil || !ok {
		return nil, ok, err
	}
	return &LockLease{manager: m, lease: lease}, true, nil
}

func (m *LockManager) Release(ctx context.Context, key, owner string) (bool, error) {
	if m == nil || m.store == nil {
		return false, fmt.Errorf("platform lock manager is not configured")
	}
	return m.store.Release(ctx, key, owner)
}

func (m *LockManager) Renew(ctx context.Context, key, owner string) (bool, error) {
	if m == nil || m.store == nil {
		return false, fmt.Errorf("platform lock manager is not configured")
	}
	return m.store.Renew(ctx, key, owner, m.ttl(), m.now())
}

func (m *LockManager) now() time.Time {
	if m.clock == nil {
		return time.Now().UTC()
	}
	return m.clock.Now().UTC()
}

func (m *LockManager) ttl() time.Duration {
	if m.defaultTTL > 0 {
		return m.defaultTTL
	}
	return time.Minute
}

func (l *LockLease) Release(ctx context.Context) error {
	if l == nil || l.lease == nil {
		return nil
	}
	return l.lease.Release(ctx)
}

func (l *LockLease) Renew(ctx context.Context) (bool, error) {
	if l == nil || l.manager == nil || l.lease == nil {
		return false, nil
	}
	return l.lease.Renew(ctx, l.manager.ttl(), l.manager.now())
}

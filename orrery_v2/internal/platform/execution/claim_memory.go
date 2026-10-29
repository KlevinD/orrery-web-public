package execution

import (
	"context"
	"sync"
	"time"
)

type lease struct {
	workerID  string
	expiresAt time.Time
}

type LeaseStore struct {
	mu     sync.Mutex
	leases map[string]lease
}

func NewLeaseStore() *LeaseStore {
	return &LeaseStore{leases: map[string]lease{}}
}

func (s *LeaseStore) Acquire(ctx context.Context, runID, workerID string, ttl time.Duration, now time.Time) bool {
	if ctx.Err() != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.leases[runID]
	if ok && current.expiresAt.After(now) {
		return false
	}
	s.leases[runID] = lease{workerID: workerID, expiresAt: now.Add(ttl)}
	return true
}

func (s *LeaseStore) RecoverStale(ctx context.Context, now time.Time) int {
	if ctx.Err() != nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recovered := 0
	for runID, lease := range s.leases {
		if !lease.expiresAt.After(now) {
			delete(s.leases, runID)
			recovered++
		}
	}
	return recovered
}

type NegativeCache struct {
	mu       sync.Mutex
	versions map[string]int
	empty    map[string]int
}

func NewNegativeCache() *NegativeCache {
	return &NegativeCache{versions: map[string]int{}, empty: map[string]int{}}
}

func (c *NegativeCache) CurrentVersion(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.versions[key]
}

func (c *NegativeCache) MarkEmpty(key string, observedVersion int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.empty[key] = observedVersion
}

func (c *NegativeCache) Bump(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.versions[key]++
}

func (c *NegativeCache) IsEmpty(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	emptyVersion, ok := c.empty[key]
	return ok && emptyVersion == c.versions[key]
}

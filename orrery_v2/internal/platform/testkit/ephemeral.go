package testkit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"orrery_v2/internal/platform/execution"
)

type EphemeralQueue struct {
	mu      sync.Mutex
	pending []execution.Job
	claimed map[string]execution.Job
	done    map[string]execution.Job
}

func NewEphemeralQueue() *EphemeralQueue {
	return &EphemeralQueue{claimed: map[string]execution.Job{}, done: map[string]execution.Job{}}
}

func (q *EphemeralQueue) Enqueue(ctx context.Context, job execution.Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if job.ID == "" {
		return fmt.Errorf("job id is required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, job)
	return nil
}

func (q *EphemeralQueue) Claim(ctx context.Context, workerID string) (execution.Job, bool, error) {
	if err := ctx.Err(); err != nil {
		return execution.Job{}, false, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return execution.Job{}, false, nil
	}
	job := q.pending[0]
	q.pending = q.pending[1:]
	job.WorkerID = workerID
	q.claimed[job.ID] = job
	return job, true, nil
}

func (q *EphemeralQueue) Complete(ctx context.Context, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.claimed[jobID]
	if !ok {
		return fmt.Errorf("claimed job not found: %s", jobID)
	}
	delete(q.claimed, jobID)
	q.done[jobID] = job
	return nil
}

func (q *EphemeralQueue) Fail(ctx context.Context, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.claimed[jobID]
	if !ok {
		return fmt.Errorf("claimed job not found: %s", jobID)
	}
	delete(q.claimed, jobID)
	job.WorkerID = ""
	q.pending = append([]execution.Job{job}, q.pending...)
	return nil
}

func (q *EphemeralQueue) Completed(jobID string) (execution.Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.done[jobID]
	return job, ok
}

type EphemeralQueueRunner struct {
	queue    *EphemeralQueue
	workerID string
	handler  execution.Handler
}

func NewEphemeralQueueRunner(queue *EphemeralQueue, workerID string, handler execution.Handler) *EphemeralQueueRunner {
	return &EphemeralQueueRunner{queue: queue, workerID: workerID, handler: handler}
}

func (r *EphemeralQueueRunner) RunOnce(ctx context.Context) (bool, error) {
	job, ok, err := r.queue.Claim(ctx, r.workerID)
	if err != nil || !ok {
		return false, err
	}
	if err := r.handler(ctx, job); err != nil {
		_ = r.queue.Fail(ctx, job.ID)
		return true, err
	}
	return true, r.queue.Complete(ctx, job.ID)
}

type EphemeralLivenessStore struct {
	mu        sync.Mutex
	expiresAt map[string]time.Time
}

func NewEphemeralLivenessStore() *EphemeralLivenessStore {
	return &EphemeralLivenessStore{expiresAt: map[string]time.Time{}}
}

func (m *EphemeralLivenessStore) Touch(ctx context.Context, runtimeID string, ttl time.Duration, now time.Time) {
	if ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expiresAt[runtimeID] = now.Add(ttl)
}

func (m *EphemeralLivenessStore) IsAliveBatch(ctx context.Context, runtimeIDs []string, now time.Time) map[string]bool {
	result := map[string]bool{}
	if ctx.Err() != nil {
		return result
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range runtimeIDs {
		result[id] = m.expiresAt[id].After(now)
	}
	return result
}

func (m *EphemeralLivenessStore) Forget(ctx context.Context, runtimeID string) {
	if ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.expiresAt, runtimeID)
}

type FailingLivenessStore struct{}

func (FailingLivenessStore) Touch(context.Context, string, time.Duration, time.Time) {}

func (FailingLivenessStore) IsAliveBatch(context.Context, []string, time.Time) map[string]bool {
	return map[string]bool{}
}

func (FailingLivenessStore) Forget(context.Context, string) {}

type EphemeralLock struct {
	mu      sync.Mutex
	entries map[string]ephemeralLockEntry
}

type ephemeralLockEntry struct {
	owner     string
	expiresAt time.Time
}

type EphemeralLease struct {
	store *EphemeralLock
	key   string
	owner string
}

func NewEphemeralLock() *EphemeralLock {
	return &EphemeralLock{entries: map[string]ephemeralLockEntry{}}
}

func (m *EphemeralLock) TryAcquire(ctx context.Context, key, owner string, ttl time.Duration, now time.Time) (*EphemeralLease, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.entries[key]
	if ok && current.expiresAt.After(now) {
		return nil, false
	}
	m.entries[key] = ephemeralLockEntry{owner: owner, expiresAt: now.Add(ttl)}
	return &EphemeralLease{store: m, key: key, owner: owner}, true
}

func (l *EphemeralLease) Release(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	current, ok := l.store.entries[l.key]
	if ok && current.owner == l.owner {
		delete(l.store.entries, l.key)
	}
}

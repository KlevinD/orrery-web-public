package execution

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
)

type Message struct {
	Topic         string
	Payload       []byte
	Priority      int
	MaxAttempts   int
	QueueKey      string
	NextAttemptAt time.Time
}

type JobStatus struct {
	ID            string
	Topic         string
	Payload       []byte
	Priority      int
	Status        Status
	Attempts      int
	MaxAttempts   int
	WorkerID      string
	QueueKey      string
	LeaseUntil    time.Time
	LastError     string
	FailureReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DispatchedAt  time.Time
	StartedAt     time.Time
	CompletedAt   time.Time
	NextAttemptAt time.Time
}

type QueueService struct {
	queue       *Queue
	clock       clock.Clock
	idGenerator idgen.ErrorGenerator
}

func NewQueueService(queue *Queue, platformClock clock.Clock) *QueueService {
	return NewQueueServiceWithIDGenerator(queue, platformClock, nil)
}

func NewQueueServiceWithIDGenerator(queue *Queue, platformClock clock.Clock, generator idgen.ErrorGenerator) *QueueService {
	if platformClock == nil {
		platformClock = clock.Real{}
	}
	return &QueueService{queue: queue, clock: platformClock, idGenerator: generator}
}

func (q *QueueService) Submit(ctx context.Context, message Message) (string, error) {
	if q == nil || q.queue == nil {
		return "", fmt.Errorf("platform execution queue is not configured")
	}
	topic := strings.TrimSpace(message.Topic)
	if topic == "" {
		return "", fmt.Errorf("execution topic is required")
	}
	id, err := q.newID()
	if err != nil {
		return "", err
	}
	if err := q.queue.Enqueue(ctx, Job{
		ID:            id,
		Topic:         topic,
		Payload:       append([]byte(nil), message.Payload...),
		Priority:      message.Priority,
		MaxAttempts:   message.MaxAttempts,
		QueueKey:      message.QueueKey,
		NextAttemptAt: message.NextAttemptAt,
	}, q.now()); err != nil {
		return "", err
	}
	return id, nil
}

func (q *QueueService) Enqueue(ctx context.Context, job Job) error {
	if q == nil || q.queue == nil {
		return fmt.Errorf("platform execution queue is not configured")
	}
	return q.queue.Enqueue(ctx, job, q.now())
}

func (q *QueueService) Get(ctx context.Context, jobID string) (Job, bool, error) {
	if q == nil || q.queue == nil {
		return Job{}, false, fmt.Errorf("platform execution queue is not configured")
	}
	return q.queue.Get(ctx, jobID)
}

func (q *QueueService) GetStatus(ctx context.Context, jobID string) (JobStatus, bool, error) {
	job, ok, err := q.Get(ctx, jobID)
	if err != nil || !ok {
		return JobStatus{}, ok, err
	}
	return jobStatusFromJob(job), true, nil
}

func (q *QueueService) Cancel(ctx context.Context, jobID, reason string) error {
	if q == nil || q.queue == nil {
		return fmt.Errorf("platform execution queue is not configured")
	}
	return q.queue.Cancel(ctx, jobID, reason, q.now())
}

func (q *QueueService) CancelWithResult(ctx context.Context, jobID, reason string) (CancelResult, error) {
	if q == nil || q.queue == nil {
		return CancelResult{}, fmt.Errorf("platform execution queue is not configured")
	}
	return q.queue.CancelWithResult(ctx, jobID, reason, q.now())
}

func (q *QueueService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	if q == nil || q.queue == nil {
		return nil, fmt.Errorf("platform execution queue is not configured")
	}
	return q.queue.StatusCounts(ctx)
}

func (q *QueueService) newID() (string, error) {
	if q == nil || q.idGenerator == nil {
		return "", fmt.Errorf("platform id generator is not configured")
	}
	id, err := q.idGenerator.NewIDContext()
	if err != nil {
		return "", fmt.Errorf("generate execution job id: %w", err)
	}
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("generated execution job id is empty")
	}
	return id, nil
}

func (q *QueueService) now() time.Time {
	if q == nil || q.clock == nil {
		return time.Now()
	}
	return q.clock.Now()
}

func jobStatusFromJob(job Job) JobStatus {
	return JobStatus{
		ID:            job.ID,
		Topic:         job.Topic,
		Payload:       append([]byte(nil), job.Payload...),
		Priority:      job.Priority,
		Status:        job.Status,
		Attempts:      job.Attempts,
		MaxAttempts:   job.MaxAttempts,
		WorkerID:      job.WorkerID,
		QueueKey:      job.QueueKey,
		LeaseUntil:    job.LeaseUntil,
		LastError:     job.LastError,
		FailureReason: job.FailureReason,
		CreatedAt:     job.CreatedAt,
		UpdatedAt:     job.UpdatedAt,
		DispatchedAt:  job.DispatchedAt,
		StartedAt:     job.StartedAt,
		CompletedAt:   job.CompletedAt,
		NextAttemptAt: job.NextAttemptAt,
	}
}

type ClaimsService struct {
	store      *ClaimStore
	clock      clock.Clock
	defaultTTL time.Duration
}

const minimumClaimCleanupGrace = 5 * time.Minute

func NewClaimsService(store *ClaimStore, platformClock clock.Clock, defaultTTL time.Duration) *ClaimsService {
	if platformClock == nil {
		platformClock = clock.Real{}
	}
	return &ClaimsService{store: store, clock: platformClock, defaultTTL: defaultTTL}
}

func (c *ClaimsService) Acquire(ctx context.Context, runID, workerID string) (bool, error) {
	if c == nil || c.store == nil {
		return false, fmt.Errorf("platform execution claims are not configured")
	}
	return c.store.Acquire(ctx, runID, workerID, c.ttl(), c.now())
}

func (c *ClaimsService) Renew(ctx context.Context, runID, workerID string) (bool, error) {
	if c == nil || c.store == nil {
		return false, fmt.Errorf("platform execution claims are not configured")
	}
	return c.store.Renew(ctx, runID, workerID, c.ttl(), c.now())
}

func (c *ClaimsService) Release(ctx context.Context, runID, workerID string) (bool, error) {
	if c == nil || c.store == nil {
		return false, fmt.Errorf("platform execution claims are not configured")
	}
	return c.store.Release(ctx, runID, workerID)
}

func (c *ClaimsService) RecoverStale(ctx context.Context, batchSize int) (int, error) {
	if c == nil || c.store == nil {
		return 0, fmt.Errorf("platform execution claims are not configured")
	}
	return c.store.RecoverStale(ctx, c.now().Add(-c.cleanupGrace()), batchSize)
}

func (c *ClaimsService) CurrentVersion(ctx context.Context, key string) (int, error) {
	if c == nil || c.store == nil {
		return 0, fmt.Errorf("platform execution claims are not configured")
	}
	return c.store.CurrentVersion(ctx, key)
}

func (c *ClaimsService) MarkEmpty(ctx context.Context, key string, observedVersion int) error {
	if c == nil || c.store == nil {
		return fmt.Errorf("platform execution claims are not configured")
	}
	return c.store.MarkEmpty(ctx, key, observedVersion)
}

func (c *ClaimsService) Bump(ctx context.Context, key string) error {
	if c == nil || c.store == nil {
		return fmt.Errorf("platform execution claims are not configured")
	}
	return c.store.Bump(ctx, key)
}

func (c *ClaimsService) IsEmpty(ctx context.Context, key string) (bool, error) {
	if c == nil || c.store == nil {
		return false, fmt.Errorf("platform execution claims are not configured")
	}
	return c.store.IsEmpty(ctx, key)
}

func (c *ClaimsService) now() time.Time {
	if c == nil || c.clock == nil {
		return time.Now().UTC()
	}
	return c.clock.Now().UTC()
}

func (c *ClaimsService) ttl() time.Duration {
	if c != nil && c.defaultTTL > 0 {
		return c.defaultTTL
	}
	return 30 * time.Second
}

func (c *ClaimsService) cleanupGrace() time.Duration {
	grace := 2 * c.ttl()
	if grace < minimumClaimCleanupGrace {
		return minimumClaimCleanupGrace
	}
	return grace
}

type LivenessService struct {
	store      *LivenessStore
	clock      clock.Clock
	defaultTTL time.Duration
}

func NewLivenessService(store *LivenessStore, platformClock clock.Clock, defaultTTL time.Duration) *LivenessService {
	if platformClock == nil {
		platformClock = clock.Real{}
	}
	return &LivenessService{store: store, clock: platformClock, defaultTTL: defaultTTL}
}

func (l *LivenessService) Touch(ctx context.Context, runtimeID string) error {
	if l == nil || l.store == nil {
		return fmt.Errorf("platform execution liveness is not configured")
	}
	return l.store.TouchHeartbeat(ctx, runtimeID, l.ttl(), l.now())
}

func (l *LivenessService) IsAlive(ctx context.Context, runtimeID string) (bool, error) {
	alive, err := l.IsAliveBatch(ctx, []string{runtimeID})
	if err != nil {
		return false, err
	}
	return alive[runtimeID], nil
}

func (l *LivenessService) IsAliveBatch(ctx context.Context, runtimeIDs []string) (map[string]bool, error) {
	if l == nil || l.store == nil {
		return nil, fmt.Errorf("platform execution liveness is not configured")
	}
	return l.store.IsAliveBatch(ctx, runtimeIDs, l.now()), nil
}

func (l *LivenessService) SnapshotBatch(ctx context.Context, runtimeIDs []string) (map[string]LivenessSnapshot, error) {
	if l == nil || l.store == nil {
		return nil, fmt.Errorf("platform execution liveness is not configured")
	}
	return l.store.SnapshotBatch(ctx, runtimeIDs, l.now())
}

func (l *LivenessService) Forget(ctx context.Context, runtimeID string) error {
	if l == nil || l.store == nil {
		return fmt.Errorf("platform execution liveness is not configured")
	}
	return l.store.ForgetHeartbeat(ctx, runtimeID)
}

func (l *LivenessService) SweepStale(ctx context.Context, batchSize int) (int, error) {
	if l == nil || l.store == nil {
		return 0, fmt.Errorf("platform execution liveness is not configured")
	}
	return l.store.SweepStale(ctx, l.now(), batchSize)
}

func (l *LivenessService) now() time.Time {
	if l == nil || l.clock == nil {
		return time.Now().UTC()
	}
	return l.clock.Now().UTC()
}

func (l *LivenessService) ttl() time.Duration {
	if l != nil && l.defaultTTL > 0 {
		return l.defaultTTL
	}
	return time.Minute
}

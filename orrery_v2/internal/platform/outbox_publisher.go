package platform

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/outbox"
)

type OutboxMessage struct {
	Topic          string
	Payload        []byte
	IdempotencyKey string
	Delay          time.Duration
	MaxAttempts    int
}

type OutboxStatus string

const (
	OutboxStatusPending    OutboxStatus = "pending"
	OutboxStatusRunning    OutboxStatus = "running"
	OutboxStatusDone       OutboxStatus = "done"
	OutboxStatusDeadLetter OutboxStatus = "dead_letter"
)

type OutboxJobStatus struct {
	ID             string
	Topic          string
	Payload        []byte
	IdempotencyKey string
	Status         OutboxStatus
	WorkerID       string
	Attempts       int
	MaxAttempts    int
	NextAttemptAt  time.Time
	LockedUntil    time.Time
	LastError      string
}

type OutboxPublisher struct {
	store       *outbox.Store
	clock       clock.Clock
	idGenerator idgen.ErrorGenerator
}

func NewOutboxPublisher(store *outbox.Store, platformClock clock.Clock, generator idgen.ErrorGenerator) *OutboxPublisher {
	if platformClock == nil {
		platformClock = clock.Real{}
	}
	return &OutboxPublisher{store: store, clock: platformClock, idGenerator: generator}
}

func (p *OutboxPublisher) Publish(ctx context.Context, message OutboxMessage) (string, error) {
	if p == nil || p.store == nil {
		return "", fmt.Errorf("platform outbox publisher is not configured")
	}
	topic := strings.TrimSpace(message.Topic)
	if topic == "" {
		return "", fmt.Errorf("outbox topic is required")
	}
	id, err := p.newID()
	if err != nil {
		return "", err
	}
	nextAttemptAt := p.now()
	if message.Delay > 0 {
		nextAttemptAt = nextAttemptAt.Add(message.Delay)
	}
	job := outbox.Job{
		ID:             id,
		Topic:          topic,
		Payload:        append([]byte(nil), message.Payload...),
		IdempotencyKey: message.IdempotencyKey,
		MaxAttempts:    message.MaxAttempts,
		NextAttemptAt:  nextAttemptAt,
	}
	if err := p.store.Enqueue(ctx, job); err != nil {
		return "", err
	}
	return id, nil
}

func (p *OutboxPublisher) GetStatus(ctx context.Context, jobID string) (OutboxJobStatus, bool) {
	if p == nil || p.store == nil {
		return OutboxJobStatus{}, false
	}
	job, ok := p.store.Get(ctx, jobID)
	if !ok {
		return OutboxJobStatus{}, false
	}
	return outboxJobStatusFromJob(job), true
}

func (p *OutboxPublisher) newID() (string, error) {
	if p == nil || p.idGenerator == nil {
		return "", fmt.Errorf("platform id generator is not configured")
	}
	id, err := p.idGenerator.NewIDContext()
	if err != nil {
		return "", fmt.Errorf("generate outbox job id: %w", err)
	}
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("generated outbox job id is empty")
	}
	return id, nil
}

func (p *OutboxPublisher) now() time.Time {
	if p == nil || p.clock == nil {
		return time.Now().UTC()
	}
	return p.clock.Now().UTC()
}

func outboxJobStatusFromJob(job outbox.Job) OutboxJobStatus {
	return OutboxJobStatus{
		ID:             job.ID,
		Topic:          job.Topic,
		Payload:        append([]byte(nil), job.Payload...),
		IdempotencyKey: job.IdempotencyKey,
		Status:         OutboxStatus(job.Status),
		WorkerID:       job.WorkerID,
		Attempts:       job.Attempts,
		MaxAttempts:    job.MaxAttempts,
		NextAttemptAt:  job.NextAttemptAt,
		LockedUntil:    job.LockedUntil,
		LastError:      job.LastError,
	}
}

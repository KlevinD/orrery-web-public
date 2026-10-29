package testkit

import (
	"context"
	"sync"

	memaudit "orrery_v2/internal/memory/audit"
)

type MemoryAuditRecorder struct {
	mu     sync.Mutex
	Events []memaudit.Event
}

func (r *MemoryAuditRecorder) RecordMemoryAudit(ctx context.Context, event memaudit.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Events = append(r.Events, event)
	return nil
}

func (r *MemoryAuditRecorder) Snapshot() []memaudit.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]memaudit.Event(nil), r.Events...)
}

package ontologyreasoning

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

type MemoryStore struct {
	mu   sync.RWMutex
	runs map[string]ReasoningRun
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{runs: map[string]ReasoningRun{}}
}

func (s *MemoryStore) SaveRun(ctx context.Context, run ReasoningRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.runs[run.ID]; ok && existing.TenantID != run.TenantID {
		return fmt.Errorf("%w: run id is owned by another tenant", ErrInvalidRequest)
	}
	s.runs[run.ID] = cloneReasoningRun(run)
	return nil
}

func (s *MemoryStore) GetRun(ctx context.Context, tenantID string, runID string) (ReasoningRun, error) {
	if err := ctx.Err(); err != nil {
		return ReasoningRun{}, err
	}
	if s == nil {
		return ReasoningRun{}, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[runID]
	if !ok || run.TenantID != tenantID {
		return ReasoningRun{}, ErrNotFound
	}
	return cloneReasoningRun(run), nil
}

func (s *MemoryStore) ListRuns(ctx context.Context, tenantID string) ([]ReasoningRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrInvalidRequest
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runs := make([]ReasoningRun, 0)
	for _, run := range s.runs {
		if run.TenantID == tenantID {
			runs = append(runs, cloneReasoningRun(run))
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].CreatedAt.After(runs[j].CreatedAt)
		}
		return runs[i].ID > runs[j].ID
	})
	return runs, nil
}

package memorydiary

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"orrery_v2/internal/platform/db"
)

const flushStateTable = "memory_diary_flush_state"

type FlushState struct {
	TenantID               string
	AgentID                string
	SessionID              string
	CurrentRevision        string
	LastSuccessfulRevision string
	LastSuccessfulAt       time.Time
	LastSuccessfulDate     string
	AttemptRevision        string
	AttemptTrigger         FlushTrigger
	AttemptCount           int
	Exhausted              bool
	LastFailureAt          time.Time
	LastFailure            string
}

type FlushStateStore interface {
	LoadFlushState(ctx context.Context, tenantID, agentID, sessionID string) (FlushState, bool, error)
	SaveFlushState(ctx context.Context, state FlushState) error
	MutateFlushState(ctx context.Context, tenantID, agentID, sessionID string, mutate func(*FlushState, bool) error) (FlushState, error)
}

type MemoryFlushStateStore struct {
	mu     sync.RWMutex
	states map[string]FlushState
}

func NewMemoryFlushStateStore() *MemoryFlushStateStore {
	return &MemoryFlushStateStore{states: map[string]FlushState{}}
}

func (s *MemoryFlushStateStore) LoadFlushState(ctx context.Context, tenantID, agentID, sessionID string) (FlushState, bool, error) {
	if err := ctx.Err(); err != nil {
		return FlushState{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[flushStateKey(tenantID, agentID, sessionID)]
	return state, ok, nil
}

func (s *MemoryFlushStateStore) SaveFlushState(ctx context.Context, state FlushState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = map[string]FlushState{}
	}
	s.states[flushStateKey(state.TenantID, state.AgentID, state.SessionID)] = state
	return nil
}

func (s *MemoryFlushStateStore) MutateFlushState(ctx context.Context, tenantID, agentID, sessionID string, mutate func(*FlushState, bool) error) (FlushState, error) {
	if err := ctx.Err(); err != nil {
		return FlushState{}, err
	}
	if mutate == nil {
		return FlushState{}, ErrServiceDependency
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = map[string]FlushState{}
	}
	key := flushStateKey(tenantID, agentID, sessionID)
	state, found := s.states[key]
	if !found {
		state = FlushState{TenantID: tenantID, AgentID: agentID, SessionID: sessionID}
	}
	if err := mutate(&state, found); err != nil {
		return FlushState{}, err
	}
	if err := validateFlushStateIdentity(state, tenantID, agentID, sessionID); err != nil {
		return FlushState{}, err
	}
	s.states[key] = state
	return state, nil
}

func flushStateKey(tenantID, agentID, sessionID string) string {
	return tenantID + "\x00" + agentID + "\x00" + sessionID
}

type PlatformFlushStateStore struct {
	store *db.Store
}

func NewPlatformFlushStateStore(store *db.Store) *PlatformFlushStateStore {
	return &PlatformFlushStateStore{store: store}
}

func (s *PlatformFlushStateStore) LoadFlushState(ctx context.Context, tenantID, agentID, sessionID string) (FlushState, bool, error) {
	if s == nil || s.store == nil {
		return FlushState{}, false, ErrServiceDependency
	}
	record, ok, err := s.store.GetRecord(ctx, flushStateTable, platformFlushStateKey(tenantID, agentID, sessionID))
	if err != nil || !ok {
		return FlushState{}, ok, err
	}
	payload, ok := record["payload"].(string)
	if !ok || payload == "" {
		return FlushState{}, false, fmt.Errorf("%w: flush state payload missing", ErrInvalidRequest)
	}
	var state FlushState
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		return FlushState{}, false, fmt.Errorf("decode memory diary flush state: %w", err)
	}
	if state.TenantID != tenantID || state.AgentID != agentID || state.SessionID != sessionID {
		return FlushState{}, false, ErrForbidden
	}
	return state, true, nil
}

func (s *PlatformFlushStateStore) SaveFlushState(ctx context.Context, state FlushState) error {
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	if state.TenantID == "" || state.AgentID == "" || state.SessionID == "" {
		return fmt.Errorf("%w: flush state tenant, Agent, and session are required", ErrInvalidRequest)
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode memory diary flush state: %w", err)
	}
	return s.store.PutRecord(ctx, flushStateTable, platformFlushStateKey(state.TenantID, state.AgentID, state.SessionID), db.Record{
		"tenant_id":  state.TenantID,
		"agent_id":   state.AgentID,
		"session_id": state.SessionID,
		"payload":    string(payload),
	})
}

// ReassignTenant moves durable diary flush checkpoints to the business
// tenant so a restart does not reflush already persisted agent diaries.
func (s *PlatformFlushStateStore) ReassignTenant(ctx context.Context, fromID, toID string) (int, error) {
	if s == nil || s.store == nil {
		return 0, ErrServiceDependency
	}
	if fromID == "" || toID == "" || fromID == toID {
		return 0, nil
	}
	moved := 0
	afterKey := ""
	for {
		page, err := s.store.ListRecords(ctx, flushStateTable, afterKey, db.MaxRecordPageLimit)
		if err != nil {
			return moved, err
		}
		for _, item := range page.Items {
			payload, ok := item.Record["payload"].(string)
			if !ok || payload == "" {
				return moved, fmt.Errorf("%w: flush state payload missing", ErrInvalidRequest)
			}
			var state FlushState
			if err := json.Unmarshal([]byte(payload), &state); err != nil {
				return moved, fmt.Errorf("decode memory diary flush state: %w", err)
			}
			if state.TenantID == fromID {
				state.TenantID = toID
				existing, found, err := s.LoadFlushState(ctx, toID, state.AgentID, state.SessionID)
				if err != nil {
					return moved, err
				}
				if !found || existing.LastSuccessfulAt.Before(state.LastSuccessfulAt) {
					if err := s.SaveFlushState(ctx, state); err != nil {
						return moved, err
					}
				}
				if err := s.store.DeleteRecord(ctx, flushStateTable, item.Key); err != nil {
					return moved, err
				}
				moved++
			}
			afterKey = item.Key
		}
		if !page.HasMore || len(page.Items) == 0 {
			return moved, nil
		}
	}
}

func (s *PlatformFlushStateStore) MutateFlushState(ctx context.Context, tenantID, agentID, sessionID string, mutate func(*FlushState, bool) error) (FlushState, error) {
	if s == nil || s.store == nil || mutate == nil {
		return FlushState{}, ErrServiceDependency
	}
	key := platformFlushStateKey(tenantID, agentID, sessionID)
	var result FlushState
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, flushStateTable, key); err != nil {
			return err
		}
		record, found, err := db.GetRecordTx(ctx, tx, flushStateTable, key)
		if err != nil {
			return err
		}
		state := FlushState{TenantID: tenantID, AgentID: agentID, SessionID: sessionID}
		if found {
			state, err = decodePlatformFlushState(record, tenantID, agentID, sessionID)
			if err != nil {
				return err
			}
		}
		if err := mutate(&state, found); err != nil {
			return err
		}
		if err := validateFlushStateIdentity(state, tenantID, agentID, sessionID); err != nil {
			return err
		}
		payload, err := json.Marshal(state)
		if err != nil {
			return fmt.Errorf("encode memory diary flush state: %w", err)
		}
		if err := db.PutRecordTx(ctx, tx, flushStateTable, key, db.Record{
			"tenant_id": tenantID, "agent_id": agentID, "session_id": sessionID, "payload": string(payload),
		}); err != nil {
			return err
		}
		result = state
		return nil
	})
	return result, err
}

func decodePlatformFlushState(record db.Record, tenantID, agentID, sessionID string) (FlushState, error) {
	payload, ok := record["payload"].(string)
	if !ok || payload == "" {
		return FlushState{}, fmt.Errorf("%w: flush state payload missing", ErrInvalidRequest)
	}
	var state FlushState
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		return FlushState{}, fmt.Errorf("decode memory diary flush state: %w", err)
	}
	if err := validateFlushStateIdentity(state, tenantID, agentID, sessionID); err != nil {
		return FlushState{}, err
	}
	return state, nil
}

func validateFlushStateIdentity(state FlushState, tenantID, agentID, sessionID string) error {
	if state.TenantID != tenantID || state.AgentID != agentID || state.SessionID != sessionID {
		return ErrForbidden
	}
	return nil
}

func platformFlushStateKey(tenantID, agentID, sessionID string) string {
	return stableDiaryHash(tenantID, agentID, sessionID)
}

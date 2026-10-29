package testkit

import (
	"context"
	"fmt"
	"sort"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

type FakeStore struct {
	mu         sync.RWMutex
	profiles   map[agentcore.MemoryProfileID]memcore.MemoryProfile
	candidates map[string]memcore.MemoryCandidate
	records    map[string]memcore.MemoryRecord
}

func NewFakeStore() *FakeStore {
	return &FakeStore{
		profiles:   map[agentcore.MemoryProfileID]memcore.MemoryProfile{},
		candidates: map[string]memcore.MemoryCandidate{},
		records:    map[string]memcore.MemoryRecord{},
	}
}

func (s *FakeStore) SaveProfile(ctx context.Context, profile memcore.MemoryProfile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles[profile.ID] = profile.Clone()
	return nil
}

func (s *FakeStore) GetProfile(ctx context.Context, id agentcore.MemoryProfileID) (memcore.MemoryProfile, error) {
	if err := ctx.Err(); err != nil {
		return memcore.MemoryProfile{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.profiles[id]
	if !ok {
		return memcore.MemoryProfile{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	return profile.Clone(), nil
}

func (s *FakeStore) ListProfiles(ctx context.Context) ([]memcore.MemoryProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]memcore.MemoryProfile, 0, len(s.profiles))
	for _, profile := range s.profiles {
		out = append(out, profile.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *FakeStore) SaveRecord(ctx context.Context, record memcore.MemoryRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireAcceptedCandidate(record, s.candidates); err != nil {
		return err
	}
	s.records[record.ID] = record.Normalized().Clone()
	return nil
}

func (s *FakeStore) GetRecord(ctx context.Context, id string) (memcore.MemoryRecord, error) {
	if err := ctx.Err(); err != nil {
		return memcore.MemoryRecord{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[id]
	if !ok {
		return memcore.MemoryRecord{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	return record.Clone(), nil
}

func (s *FakeStore) WithRecordWriteTx(ctx context.Context, _ string, fn func(memstore.RecordWriteTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("memory record transaction function is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(fakeRecordWriteTx{store: s})
}

type fakeRecordWriteTx struct {
	store *FakeStore
}

func (t fakeRecordWriteTx) GetCandidate(_ context.Context, id string) (memcore.MemoryCandidate, error) {
	candidate, ok := t.store.candidates[id]
	if !ok {
		return memcore.MemoryCandidate{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	return candidate.Clone(), nil
}

func (t fakeRecordWriteTx) GetRecord(_ context.Context, id string) (memcore.MemoryRecord, error) {
	record, ok := t.store.records[id]
	if !ok {
		return memcore.MemoryRecord{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	return record.Clone(), nil
}

func (t fakeRecordWriteTx) SaveCandidate(_ context.Context, candidate memcore.MemoryCandidate) error {
	candidate.ProposedLayer = memcore.NormalizeMemoryLayer(candidate.ProposedLayer)
	candidate.Sensitivity = memcore.NormalizeSensitivity(candidate.Sensitivity)
	candidate.DeIDLevel = memcore.NormalizeDeIDLevel(candidate.DeIDLevel)
	candidate.Consent = memcore.NormalizeConsentState(candidate.Consent)
	t.store.candidates[candidate.ID] = candidate.Clone()
	return nil
}

func (t fakeRecordWriteTx) SaveRecord(_ context.Context, record memcore.MemoryRecord) error {
	if err := requireAcceptedCandidate(record, t.store.candidates); err != nil {
		return err
	}
	t.store.records[record.ID] = record.Normalized().Clone()
	return nil
}

func (s *FakeStore) SaveCandidate(ctx context.Context, candidate memcore.MemoryCandidate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate.ProposedLayer = memcore.NormalizeMemoryLayer(candidate.ProposedLayer)
	candidate.Sensitivity = memcore.NormalizeSensitivity(candidate.Sensitivity)
	candidate.DeIDLevel = memcore.NormalizeDeIDLevel(candidate.DeIDLevel)
	candidate.Consent = memcore.NormalizeConsentState(candidate.Consent)
	s.candidates[candidate.ID] = cloneCandidate(candidate)
	return nil
}

func (s *FakeStore) GetCandidate(ctx context.Context, id string) (memcore.MemoryCandidate, error) {
	if err := ctx.Err(); err != nil {
		return memcore.MemoryCandidate{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	candidate, ok := s.candidates[id]
	if !ok {
		return memcore.MemoryCandidate{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	return cloneCandidate(candidate), nil
}

func (s *FakeStore) ListCandidates(ctx context.Context, query memstore.CandidateQuery) ([]memcore.MemoryCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]memcore.MemoryCandidate, 0, len(s.candidates))
	for _, candidate := range s.candidates {
		if query.MissionID != "" && candidate.MissionID != query.MissionID {
			continue
		}
		if query.Status != "" && candidate.Status != query.Status {
			continue
		}
		if query.ScopeKind != "" && candidate.ProposedScope.Kind != query.ScopeKind {
			continue
		}
		if query.ScopeID != "" && candidate.ProposedScope.ID != query.ScopeID {
			continue
		}
		candidate.ProposedLayer = memcore.NormalizeMemoryLayer(candidate.ProposedLayer)
		if query.Layer != "" && candidate.ProposedLayer != query.Layer {
			continue
		}
		if query.Topic != "" && candidate.Topic != query.Topic {
			continue
		}
		if query.ReviewRequired != nil && candidate.ReviewRequired != *query.ReviewRequired {
			continue
		}
		out = append(out, cloneCandidate(candidate))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	if query.Limit > 0 && len(out) > query.Limit {
		out = out[:query.Limit]
	}
	return out, nil
}

func (s *FakeStore) UpdateCandidateStatus(ctx context.Context, id string, status memcore.CandidateStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, ok := s.candidates[id]
	if !ok {
		return fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	candidate.Status = status
	s.candidates[id] = candidate
	return nil
}

func (s *FakeStore) UpdateRecordLifecycle(ctx context.Context, id string, status memcore.MemoryLifecycleStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	record.LifecycleStatus = memcore.NormalizeMemoryLifecycleStatus(status)
	s.records[id] = record.Normalized().Clone()
	return nil
}

func (s *FakeStore) FindRecords(ctx context.Context, query memstore.RecordQuery) ([]memcore.MemoryRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	profileSet := map[agentcore.MemoryProfileID]struct{}{}
	for _, id := range query.ProfileIDs {
		profileSet[id] = struct{}{}
	}
	var out []memcore.MemoryRecord
	for _, record := range s.records {
		record = record.Normalized()
		if len(profileSet) > 0 {
			if _, ok := profileSet[record.ProfileID]; !ok {
				continue
			}
		}
		if len(query.ScopeKinds) > 0 && !containsScopeKind(query.ScopeKinds, record.Scope.Kind) {
			continue
		}
		if len(query.ScopeIDs) > 0 && !containsString(query.ScopeIDs, record.Scope.ID) {
			continue
		}
		if len(query.Layers) > 0 && !containsMemoryLayer(query.Layers, record.Layer) {
			continue
		}
		if (len(query.Topics) > 0 || len(query.Tags) > 0) && !matchesTopicOrTag(record, query.Topics, query.Tags) {
			continue
		}
		if len(query.LifecycleStatuses) > 0 && !containsLifecycleStatus(query.LifecycleStatuses, record.LifecycleStatus) {
			continue
		}
		if !query.IncludeArchived && excludedFromMainRetrieval(record.LifecycleStatus) {
			continue
		}
		if query.MinConfidence > 0 && record.Confidence < query.MinConfidence {
			continue
		}
		out = append(out, record.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	if query.Limit > 0 && len(out) > query.Limit {
		out = out[:query.Limit]
	}
	return out, nil
}

func excludedFromMainRetrieval(status memcore.MemoryLifecycleStatus) bool {
	switch memcore.NormalizeMemoryLifecycleStatus(status) {
	case memcore.MemoryLifecycleArchived, memcore.MemoryLifecycleDeleted:
		return true
	default:
		return false
	}
}

func matchesTopicOrTag(record memcore.MemoryRecord, topics, tags []string) bool {
	if len(topics) > 0 && containsString(topics, record.Topic) {
		return true
	}
	if len(tags) > 0 {
		for _, tag := range record.Tags {
			if containsString(tags, tag) {
				return true
			}
		}
	}
	return false
}

func cloneCandidate(candidate memcore.MemoryCandidate) memcore.MemoryCandidate {
	return candidate.Clone()
}

func containsMemoryLayer(values []memcore.MemoryLayer, target memcore.MemoryLayer) bool {
	target = memcore.NormalizeMemoryLayer(target)
	for _, value := range values {
		if memcore.NormalizeMemoryLayer(value) == target {
			return true
		}
	}
	return false
}

func containsScopeKind(values []memcore.ScopeKind, target memcore.ScopeKind) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsLifecycleStatus(values []memcore.MemoryLifecycleStatus, target memcore.MemoryLifecycleStatus) bool {
	target = memcore.NormalizeMemoryLifecycleStatus(target)
	for _, value := range values {
		if memcore.NormalizeMemoryLifecycleStatus(value) == target {
			return true
		}
	}
	return false
}

func requireAcceptedCandidate(record memcore.MemoryRecord, candidates map[string]memcore.MemoryCandidate) error {
	if record.CandidateID == "" {
		return fmt.Errorf("%w: record %s has no candidate_id", memcore.ErrCandidateRequired, record.ID)
	}
	candidate, ok := candidates[record.CandidateID]
	if !ok {
		return fmt.Errorf("%w: candidate %s", memcore.ErrNotFound, record.CandidateID)
	}
	if !candidate.CanCreateRecordDirectly() {
		return fmt.Errorf("%w: candidate %s status %s", memcore.ErrCandidateRequired, candidate.ID, candidate.Status)
	}
	return nil
}

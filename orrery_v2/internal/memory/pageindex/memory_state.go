package pageindex

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

type MemoryState struct {
	mu        sync.RWMutex
	profiles  map[agentcore.MemoryProfileID]ProfileBinding
	documents map[string]DocumentBinding
}

func NewMemoryState() *MemoryState {
	return &MemoryState{
		profiles:  map[agentcore.MemoryProfileID]ProfileBinding{},
		documents: map[string]DocumentBinding{},
	}
}

func (s *MemoryState) GetProfileBinding(ctx context.Context, profileID agentcore.MemoryProfileID) (ProfileBinding, error) {
	if err := ctx.Err(); err != nil {
		return ProfileBinding{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.profiles[profileID]
	if !ok {
		return ProfileBinding{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, profileID)
	}
	return binding, nil
}

func (s *MemoryState) SaveProfileBinding(ctx context.Context, binding ProfileBinding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles[binding.ProfileID] = binding
	return nil
}

func (s *MemoryState) GetDocumentBinding(ctx context.Context, memoryID string) (DocumentBinding, error) {
	if err := ctx.Err(); err != nil {
		return DocumentBinding{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.documents[strings.TrimSpace(memoryID)]
	if !ok {
		return DocumentBinding{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, memoryID)
	}
	return binding, nil
}

func (s *MemoryState) FindDocumentBindingByProviderID(ctx context.Context, providerDocumentID string) (DocumentBinding, error) {
	if err := ctx.Err(); err != nil {
		return DocumentBinding{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, binding := range s.documents {
		if strings.TrimSpace(binding.ProviderDocumentID) == strings.TrimSpace(providerDocumentID) {
			return binding, nil
		}
	}
	return DocumentBinding{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, providerDocumentID)
}

func (s *MemoryState) SaveDocumentBinding(ctx context.Context, binding DocumentBinding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.documents[binding.MemoryID] = binding
	return nil
}

func (s *MemoryState) ListDocumentBindings(ctx context.Context, limit int) ([]DocumentBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DocumentBinding, 0, len(s.documents))
	for _, binding := range s.documents {
		out = append(out, binding)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MemoryID < out[j].MemoryID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryState) DeleteDocumentBinding(ctx context.Context, memoryID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.documents, strings.TrimSpace(memoryID))
	return nil
}

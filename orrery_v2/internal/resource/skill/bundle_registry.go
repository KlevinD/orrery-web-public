package skill

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

type BundleRegistry interface {
	Create(ctx context.Context, bundle SkillBundle) error
	Update(ctx context.Context, bundle SkillBundle) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (SkillBundle, error)
	List(ctx context.Context) ([]SkillBundle, error)
}

type MemoryBundleRegistry struct {
	mu      sync.RWMutex
	bundles map[string]SkillBundle
}

func NewMemoryBundleRegistry() *MemoryBundleRegistry {
	return &MemoryBundleRegistry{bundles: map[string]SkillBundle{}}
}

func (r *MemoryBundleRegistry) Create(ctx context.Context, bundle SkillBundle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeSkillBundle(bundle)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.bundles[normalized.ID]; ok {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, normalized.ID)
	}
	r.bundles[normalized.ID] = normalized.Clone()
	return nil
}

func (r *MemoryBundleRegistry) Update(ctx context.Context, bundle SkillBundle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeSkillBundle(bundle)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.bundles[normalized.ID]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, normalized.ID)
	}
	r.bundles[normalized.ID] = normalized.Clone()
	return nil
}

func (r *MemoryBundleRegistry) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.bundles[id]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(r.bundles, id)
	return nil
}

func (r *MemoryBundleRegistry) Get(ctx context.Context, id string) (SkillBundle, error) {
	if err := ctx.Err(); err != nil {
		return SkillBundle{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	bundle, ok := r.bundles[id]
	if !ok {
		return SkillBundle{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return bundle.Clone(), nil
}

func (r *MemoryBundleRegistry) List(ctx context.Context) ([]SkillBundle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SkillBundle, 0, len(r.bundles))
	for _, bundle := range r.bundles {
		out = append(out, bundle.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

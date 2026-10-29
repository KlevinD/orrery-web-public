package ontologyscope

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type BindingStore interface {
	GetBinding(ctx context.Context, tenantID string) (ScopeBinding, error)
	SaveBinding(ctx context.Context, binding ScopeBinding, expectedVersion int64, changedBy string) (ScopeBinding, error)
	ListBindings(ctx context.Context, query BindingQuery) ([]ScopeBinding, error)
}

type BindingSource interface {
	ResolveBinding(ctx context.Context, tenantID string) (ScopeBinding, bool, error)
}

type BindingBatchSource interface {
	ResolveBindings(ctx context.Context, tenantIDs []string) (map[string]ScopeBinding, error)
}

type BindingBatchStore interface {
	GetBindings(ctx context.Context, tenantIDs []string) (map[string]ScopeBinding, error)
}

const maxBindingResolveBatch = 4096

type Resolver struct {
	sources []BindingSource
	store   BindingStore
}

func NewResolver(store BindingStore, sources ...BindingSource) *Resolver {
	filtered := make([]BindingSource, 0, len(sources)+1)
	filtered = append(filtered, systemBindingSource{})
	for _, source := range sources {
		if source != nil {
			filtered = append(filtered, source)
		}
	}
	return &Resolver{sources: filtered, store: store}
}

func (r *Resolver) Resolve(ctx context.Context, tenantID string) (ScopeBinding, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || len(tenantID) > 256 {
		return ScopeBinding{}, fmt.Errorf("%w: canonical tenant_id is required", ErrInvalidBinding)
	}
	if r == nil || ctx == nil {
		return ScopeBinding{}, ErrDependencyUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ScopeBinding{}, err
	}
	if tenantID == "system" {
		return SystemBinding(), nil
	}
	var resolved *ScopeBinding
	for _, source := range r.sources {
		binding, found, err := source.ResolveBinding(ctx, tenantID)
		if err != nil {
			return ScopeBinding{}, err
		}
		if !found {
			continue
		}
		binding.PersistedBindingVersion = 0
		binding.PersistenceRequired = false
		binding = NormalizeBinding(binding)
		if err := ValidateBinding(binding); err != nil {
			return ScopeBinding{}, err
		}
		if binding.BindingVersion <= 0 {
			return ScopeBinding{}, fmt.Errorf("%w: resolved binding version is required", ErrInvalidBinding)
		}
		if binding.TenantID != tenantID {
			return ScopeBinding{}, fmt.Errorf("%w: resolver returned a different tenant", ErrInvalidBinding)
		}
		if resolved != nil {
			if !bindingsEquivalent(*resolved, binding) {
				return ScopeBinding{}, fmt.Errorf("%w: multiple sources disagree for tenant", ErrBindingConflict)
			}
			binding = newerEquivalentBinding(*resolved, binding)
		}
		copy := binding
		resolved = &copy
	}
	if r.store != nil {
		binding, err := r.store.GetBinding(ctx, tenantID)
		if err == nil {
			binding.PersistedBindingVersion = 0
			binding.PersistenceRequired = false
			binding = NormalizeBinding(binding)
			if validateErr := ValidateBinding(binding); validateErr != nil {
				return ScopeBinding{}, validateErr
			}
			if binding.BindingVersion <= 0 {
				return ScopeBinding{}, fmt.Errorf("%w: persisted binding version is required", ErrInvalidBinding)
			}
			persistedBindingVersion := binding.BindingVersion
			if resolved == nil && binding.Kind != KindCustom {
				return ScopeBinding{}, ErrBindingNotFound
			}
			if resolved != nil && !bindingsEquivalent(*resolved, binding) {
				return ScopeBinding{}, fmt.Errorf("%w: persisted and deterministic bindings disagree for tenant", ErrBindingConflict)
			}
			persistenceRequired := resolved == nil || binding.BindingVersion > resolved.BindingVersion
			if resolved != nil {
				binding = newerEquivalentBinding(*resolved, binding)
			}
			binding.PersistedBindingVersion = persistedBindingVersion
			binding.PersistenceRequired = persistenceRequired
			resolved = &binding
		} else if !errors.Is(err, ErrBindingNotFound) {
			return ScopeBinding{}, err
		}
	}
	if resolved == nil || resolved.State != BindingStateActive {
		return ScopeBinding{}, ErrBindingNotFound
	}
	return *resolved, nil
}

func (r *Resolver) ResolveMany(ctx context.Context, tenantIDs []string) (map[string]ScopeBinding, error) {
	if r == nil || ctx == nil || len(tenantIDs) > maxBindingResolveBatch {
		return nil, ErrDependencyUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	unique := make([]string, 0, len(tenantIDs))
	seen := make(map[string]struct{}, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		tenantID = strings.TrimSpace(tenantID)
		if tenantID == "" || len(tenantID) > 256 {
			return nil, fmt.Errorf("%w: canonical tenant_id is required", ErrInvalidBinding)
		}
		if _, exists := seen[tenantID]; exists {
			continue
		}
		seen[tenantID] = struct{}{}
		unique = append(unique, tenantID)
	}
	if len(unique) == 0 {
		return map[string]ScopeBinding{}, nil
	}

	deterministic := make(map[string]ScopeBinding, len(unique))
	for _, tenantID := range unique {
		if tenantID == "system" {
			deterministic[tenantID] = SystemBinding()
		}
	}
	for _, source := range r.sources {
		if _, system := source.(systemBindingSource); system {
			continue
		}
		resolved := make(map[string]ScopeBinding)
		if batch, ok := source.(BindingBatchSource); ok {
			var err error
			resolved, err = batch.ResolveBindings(ctx, unique)
			if err != nil {
				return nil, err
			}
		} else {
			for _, tenantID := range unique {
				if tenantID == "system" {
					continue
				}
				binding, found, err := source.ResolveBinding(ctx, tenantID)
				if err != nil {
					return nil, err
				}
				if found {
					resolved[tenantID] = binding
				}
			}
		}
		for tenantID, binding := range resolved {
			if _, requested := seen[tenantID]; !requested || tenantID == "system" {
				return nil, ErrBindingConflict
			}
			binding.PersistedBindingVersion = 0
			binding.PersistenceRequired = false
			binding = NormalizeBinding(binding)
			if err := ValidateBinding(binding); err != nil {
				return nil, err
			}
			if binding.BindingVersion <= 0 || binding.TenantID != tenantID {
				return nil, fmt.Errorf("%w: resolved binding version and tenant are required", ErrInvalidBinding)
			}
			if current, exists := deterministic[tenantID]; exists {
				if !bindingsEquivalent(current, binding) {
					return nil, fmt.Errorf("%w: multiple sources disagree for tenant", ErrBindingConflict)
				}
				binding = newerEquivalentBinding(current, binding)
			}
			deterministic[tenantID] = binding
		}
	}

	persisted := make(map[string]ScopeBinding)
	if r.store != nil {
		if batch, ok := r.store.(BindingBatchStore); ok {
			var err error
			persisted, err = batch.GetBindings(ctx, unique)
			if err != nil {
				return nil, err
			}
		} else {
			for _, tenantID := range unique {
				if tenantID == "system" {
					continue
				}
				binding, err := r.store.GetBinding(ctx, tenantID)
				if err == nil {
					persisted[tenantID] = binding
				} else if !errors.Is(err, ErrBindingNotFound) {
					return nil, err
				}
			}
		}
	}
	for tenantID, binding := range persisted {
		if _, requested := seen[tenantID]; !requested || tenantID == "system" {
			return nil, ErrBindingConflict
		}
		binding.PersistedBindingVersion = 0
		binding.PersistenceRequired = false
		binding = NormalizeBinding(binding)
		if err := ValidateBinding(binding); err != nil {
			return nil, err
		}
		if binding.BindingVersion <= 0 {
			return nil, fmt.Errorf("%w: persisted binding version is required", ErrInvalidBinding)
		}
		persistedVersion := binding.BindingVersion
		current, resolved := deterministic[tenantID]
		if !resolved && binding.Kind != KindCustom {
			continue
		}
		if resolved && !bindingsEquivalent(current, binding) {
			return nil, fmt.Errorf("%w: persisted and deterministic bindings disagree for tenant", ErrBindingConflict)
		}
		persistenceRequired := !resolved || binding.BindingVersion > current.BindingVersion
		if resolved {
			binding = newerEquivalentBinding(current, binding)
		}
		binding.PersistedBindingVersion = persistedVersion
		binding.PersistenceRequired = persistenceRequired
		deterministic[tenantID] = binding
	}

	out := make(map[string]ScopeBinding, len(deterministic))
	for tenantID, binding := range deterministic {
		if binding.State == BindingStateActive {
			out[tenantID] = binding
		}
	}
	return out, nil
}

func newerEquivalentBinding(left ScopeBinding, right ScopeBinding) ScopeBinding {
	if right.BindingVersion >= left.BindingVersion {
		return right
	}
	return left
}

type systemBindingSource struct{}

func (systemBindingSource) ResolveBinding(_ context.Context, tenantID string) (ScopeBinding, bool, error) {
	if strings.TrimSpace(tenantID) != "system" {
		return ScopeBinding{}, false, nil
	}
	return SystemBinding(), true, nil
}

type BindingSourceFunc func(ctx context.Context, tenantID string) (ScopeBinding, bool, error)

func (f BindingSourceFunc) ResolveBinding(ctx context.Context, tenantID string) (ScopeBinding, bool, error) {
	return f(ctx, tenantID)
}

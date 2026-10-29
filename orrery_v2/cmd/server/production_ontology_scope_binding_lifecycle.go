package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/platform"
)

type productionOntologySourceDeletionLifecycle struct {
	state *productionOntologyScopeState
	now   func() time.Time
}

func (l productionOntologySourceDeletionLifecycle) PrepareSourceDeletion(ctx context.Context, sourceType, sourceID string, canonicalTenantIDs []string, changedBy string) (func(context.Context) error, error) {
	return prepareOntologySourceDeletion(ctx, l.state, identity.ObjectRef{Type: sourceType, ID: sourceID}, canonicalTenantIDs, changedBy)
}

func (l productionOntologySourceDeletionLifecycle) Sweep(ctx context.Context) error {
	if l.state == nil || l.state.Store == nil || l.state.Resolver == nil {
		return ontologyscope.ErrDependencyUnavailable
	}
	now := time.Now().UTC()
	if l.now != nil {
		now = l.now().UTC()
	}
	intents, err := l.state.Store.ListRecoverableSourceDeletions(ctx, now, 100)
	if err != nil {
		return err
	}
	var result error
	for _, intent := range intents {
		if err := recoverOntologySourceDeletion(ctx, l.state, intent, intent.ChangedBy+":recovery"); err != nil {
			result = errors.Join(result, fmt.Errorf("recover ontology source deletion %s: %w", intent.IntentID, err))
		}
	}
	return result
}

func registerProductionOntologySourceDeletionRecoverySweeper(rt *platform.Runtime, state *productionOntologyScopeState) error {
	if rt == nil || state == nil {
		return fmt.Errorf("ontology source deletion recovery requires runtime and scope state")
	}
	return rt.RegisterSweeper("ontology.scope_binding_source_deletion_recovery", productionOntologySourceDeletionLifecycle{state: state})
}

func prepareOntologySourceDeletion(
	ctx context.Context,
	state *productionOntologyScopeState,
	source identity.ObjectRef,
	canonicalTenantIDs []string,
	changedBy string,
) (func(context.Context) error, error) {
	if state == nil || state.Store == nil || state.Resolver == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	source.Type = strings.ToLower(strings.TrimSpace(source.Type))
	source.ID = strings.TrimSpace(source.ID)
	changedBy = strings.TrimSpace(changedBy)
	if source.Type == "" || source.ID == "" || changedBy == "" || len(canonicalTenantIDs) == 0 {
		return nil, fmt.Errorf("%w: ontology source deletion binding is invalid", ontologyscope.ErrInvalidBinding)
	}

	type candidate struct {
		binding         ontologyscope.ScopeBinding
		expectedVersion int64
	}
	candidates := map[string]candidate{}
	canonical := make([]string, 0, len(canonicalTenantIDs))
	for _, raw := range canonicalTenantIDs {
		tenantID := strings.TrimSpace(raw)
		if tenantID == "" {
			return nil, fmt.Errorf("%w: canonical tenant id is required", ontologyscope.ErrInvalidBinding)
		}
		binding, err := state.Resolver.Resolve(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		if !ontologyBindingMatchesSource(binding, source) {
			return nil, fmt.Errorf("%w: canonical binding source does not match deletion target", ontologyscope.ErrBindingConflict)
		}
		candidates[binding.TenantID] = candidate{binding: binding, expectedVersion: binding.PersistedBindingVersion}
		canonical = append(canonical, binding.TenantID)
	}

	after := ""
	for {
		bindings, err := state.Store.ListBindings(ctx, ontologyscope.BindingQuery{
			State: ontologyscope.BindingStateActive, AfterTenantID: after, Limit: 100,
		})
		if err != nil {
			return nil, err
		}
		for _, binding := range bindings {
			if ontologyBindingMatchesSource(binding, source) {
				candidates[binding.TenantID] = candidate{binding: binding, expectedVersion: binding.BindingVersion}
			}
		}
		if len(bindings) < 100 {
			break
		}
		after = bindings[len(bindings)-1].TenantID
	}

	tenantIDs := make([]string, 0, len(candidates))
	for tenantID := range candidates {
		tenantIDs = append(tenantIDs, tenantID)
	}
	sort.Strings(tenantIDs)
	bindings := make([]ontologyscope.SourceDeletionBinding, 0, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		item := candidates[tenantID]
		bindings = append(bindings, ontologyscope.SourceDeletionBinding{Binding: item.binding, ExpectedVersion: item.expectedVersion})
	}
	intent, err := state.Store.PrepareSourceDeletion(ctx, source, canonical, bindings, changedBy)
	if err != nil {
		return nil, err
	}

	rollback := func(rollbackCtx context.Context) error {
		if rollbackCtx == nil {
			rollbackCtx = context.Background()
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(rollbackCtx), 10*time.Second)
		defer cancel()
		return recoverOntologySourceDeletion(cleanupCtx, state, intent, changedBy+":rollback")
	}
	return rollback, nil
}

func recoverOntologySourceDeletion(ctx context.Context, state *productionOntologyScopeState, intent ontologyscope.SourceDeletionIntent, changedBy string) error {
	alive, err := ontologyCanonicalSourceStillExists(ctx, state.Resolver, intent.CanonicalTenantIDs)
	if err != nil {
		return err
	}
	if alive {
		return state.Store.RestoreSourceDeletion(ctx, intent, changedBy)
	}
	return state.Store.CompleteSourceDeletion(ctx, intent.IntentID)
}

func ontologyBindingMatchesSource(binding ontologyscope.ScopeBinding, source identity.ObjectRef) bool {
	return strings.EqualFold(strings.TrimSpace(binding.SourceObject.Type), source.Type) && strings.TrimSpace(binding.SourceObject.ID) == source.ID
}

func ontologyCanonicalSourceStillExists(ctx context.Context, resolver *ontologyscope.Resolver, tenantIDs []string) (bool, error) {
	for _, tenantID := range tenantIDs {
		_, err := resolver.Resolve(ctx, tenantID)
		switch {
		case err == nil, errors.Is(err, ontologyscope.ErrBindingConflict):
			return true, nil
		case errors.Is(err, ontologyscope.ErrBindingNotFound):
			continue
		default:
			return false, err
		}
	}
	return false, nil
}

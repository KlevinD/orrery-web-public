package ontologyscope

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/identity"
)

type memoryBindingStore struct {
	bindings map[string]ScopeBinding
}

func (s *memoryBindingStore) GetBinding(_ context.Context, tenantID string) (ScopeBinding, error) {
	binding, ok := s.bindings[tenantID]
	if !ok {
		return ScopeBinding{}, ErrBindingNotFound
	}
	return binding, nil
}

func (s *memoryBindingStore) SaveBinding(_ context.Context, binding ScopeBinding, _ int64, _ string) (ScopeBinding, error) {
	if s.bindings == nil {
		s.bindings = map[string]ScopeBinding{}
	}
	s.bindings[binding.TenantID] = binding
	return binding, nil
}

func (s *memoryBindingStore) ListBindings(context.Context, BindingQuery) ([]ScopeBinding, error) {
	return nil, nil
}

func TestBindingResolverUsesExplicitSourcesAndPersistedBindings(t *testing.T) {
	mission := testBinding("mission:mission-1", KindMission, "mission", "mission-1", "org-1")
	mission.BindingVersion = 3
	custom := testBinding("knowledge:private", KindCustom, "knowledge_space", "private", "org-1")
	custom.BindingVersion = 2
	store := &memoryBindingStore{bindings: map[string]ScopeBinding{custom.TenantID: custom}}
	resolver := NewResolver(store, BindingSourceFunc(func(_ context.Context, tenantID string) (ScopeBinding, bool, error) {
		return mission, tenantID == mission.TenantID, nil
	}))

	gotMission, err := resolver.Resolve(context.Background(), mission.TenantID)
	if err != nil || gotMission.BindingVersion != 3 || gotMission.SourceObject.ID != "mission-1" || gotMission.PersistedBindingVersion != 0 || gotMission.PersistenceRequired {
		t.Fatalf("Resolve mission = %#v, %v", gotMission, err)
	}
	gotCustom, err := resolver.Resolve(context.Background(), custom.TenantID)
	if err != nil || gotCustom.Kind != KindCustom || gotCustom.BindingVersion != 2 || gotCustom.PersistedBindingVersion != 2 || !gotCustom.PersistenceRequired {
		t.Fatalf("Resolve custom = %#v, %v", gotCustom, err)
	}
}

func TestBindingResolverDoesNotGuessTenantPrefixes(t *testing.T) {
	resolver := NewResolver(&memoryBindingStore{bindings: map[string]ScopeBinding{}})
	for _, tenantID := range []string{"mission:guessed", "project:guessed", "org-without-prefix", "custom:guessed"} {
		if _, err := resolver.Resolve(context.Background(), tenantID); !errors.Is(err, ErrBindingNotFound) {
			t.Fatalf("Resolve(%q) error = %v", tenantID, err)
		}
	}
}

func TestBindingResolverRejectsDisabledAndConflictingBindings(t *testing.T) {
	disabled := testBinding("mission:disabled", KindMission, "mission", "disabled", "org-1")
	disabled.State = BindingStateDisabled
	resolver := NewResolver(&memoryBindingStore{bindings: map[string]ScopeBinding{disabled.TenantID: disabled}})
	if _, err := resolver.Resolve(context.Background(), disabled.TenantID); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("disabled Resolve error = %v", err)
	}

	one := testBinding("mission:1", KindMission, "mission", "1", "org-1")
	two := one
	two.AuthorizationTenantID = "org-2"
	resolver = NewResolver(nil,
		BindingSourceFunc(func(context.Context, string) (ScopeBinding, bool, error) { return one, true, nil }),
		BindingSourceFunc(func(context.Context, string) (ScopeBinding, bool, error) { return two, true, nil }),
	)
	if _, err := resolver.Resolve(context.Background(), one.TenantID); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("conflicting Resolve error = %v", err)
	}
}

func TestBindingResolverRejectsPersistedDeterministicConflictAndUsesNewestEquivalentVersion(t *testing.T) {
	deterministic := testBinding("project:1", KindProject, "project", "1", "org-1")
	deterministic.BindingVersion = 2
	persisted := deterministic
	persisted.BindingVersion = 4
	store := &memoryBindingStore{bindings: map[string]ScopeBinding{persisted.TenantID: persisted}}
	resolver := NewResolver(store, BindingSourceFunc(func(context.Context, string) (ScopeBinding, bool, error) {
		return deterministic, true, nil
	}))
	resolved, err := resolver.Resolve(context.Background(), deterministic.TenantID)
	if err != nil {
		t.Fatalf("Resolve equivalent binding: %v", err)
	}
	if resolved.BindingVersion != 4 || resolved.PersistedBindingVersion != 4 || !resolved.PersistenceRequired {
		t.Fatalf("resolved binding = %+v", resolved)
	}
	persisted.AuthorizationTenantID = "org-2"
	store.bindings[persisted.TenantID] = persisted
	if _, err := resolver.Resolve(context.Background(), deterministic.TenantID); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("persisted conflict error = %v", err)
	}
}

func TestBindingResolverTracksNonAuthoritativePersistedVersionForCommitFencing(t *testing.T) {
	deterministic := testBinding("project:1", KindProject, "project", "1", "org-1")
	deterministic.BindingVersion = 4
	persisted := deterministic
	persisted.BindingVersion = 2
	resolver := NewResolver(&memoryBindingStore{bindings: map[string]ScopeBinding{persisted.TenantID: persisted}}, BindingSourceFunc(func(context.Context, string) (ScopeBinding, bool, error) {
		return deterministic, true, nil
	}))

	resolved, err := resolver.Resolve(context.Background(), deterministic.TenantID)
	if err != nil {
		t.Fatalf("Resolve equivalent binding: %v", err)
	}
	if resolved.BindingVersion != 4 || resolved.PersistedBindingVersion != 2 || resolved.PersistenceRequired {
		t.Fatalf("resolved binding = %+v", resolved)
	}
}

func TestBindingResolverDoesNotResurrectDeletedKnownSourceFromPersistedBinding(t *testing.T) {
	persisted := testBinding("project:deleted", KindProject, "project", "deleted", "org-1")
	resolver := NewResolver(&memoryBindingStore{bindings: map[string]ScopeBinding{persisted.TenantID: persisted}})
	if _, err := resolver.Resolve(context.Background(), persisted.TenantID); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("deleted known source error = %v", err)
	}
	custom := persisted
	custom.TenantID = "historical-project-scope"
	custom.Kind = KindCustom
	resolver = NewResolver(&memoryBindingStore{bindings: map[string]ScopeBinding{custom.TenantID: custom}})
	if _, err := resolver.Resolve(context.Background(), custom.TenantID); err != nil {
		t.Fatalf("persisted custom scope: %v", err)
	}
}

func TestBindingResolverNilDependenciesFailClosed(t *testing.T) {
	var resolver *Resolver
	if _, err := resolver.Resolve(context.Background(), "system"); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("nil resolver error = %v", err)
	}
	if _, err := NewResolver(nil).Resolve(nil, "system"); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("nil context error = %v", err)
	}
}

func TestBindingResolverProtectsSystemBinding(t *testing.T) {
	malicious := SystemBinding()
	malicious.SourceObject = identity.ObjectRef{Type: "mission", ID: "attacker"}
	store := &memoryBindingStore{bindings: map[string]ScopeBinding{"system": malicious}}

	binding, err := NewResolver(store).Resolve(context.Background(), "system")
	if err != nil {
		t.Fatalf("Resolve system: %v", err)
	}
	if binding.SourceObject.Type != "platform" || binding.SourceObject.ID != "global" {
		t.Fatalf("system binding was overridden: %#v", binding)
	}
}

func TestBindingResolverIgnoresConflictingDomainSourceForReservedSystemScope(t *testing.T) {
	malicious := SystemBinding()
	malicious.SourceObject = identity.ObjectRef{Type: "organization", ID: "system"}
	resolver := NewResolver(nil, BindingSourceFunc(func(context.Context, string) (ScopeBinding, bool, error) {
		return malicious, true, nil
	}))
	binding, err := resolver.Resolve(context.Background(), "system")
	if err != nil {
		t.Fatalf("Resolve system: %v", err)
	}
	if binding.SourceObject.Type != "platform" || binding.SourceObject.ID != "global" {
		t.Fatalf("system binding = %+v", binding)
	}
}

func testBinding(tenantID string, kind Kind, objectType string, objectID string, authorizationTenantID string) ScopeBinding {
	readAction := objectType + ".view"
	if objectType == "organization" {
		readAction = "organization.read"
	}
	return ScopeBinding{
		TenantID:              tenantID,
		Kind:                  kind,
		SourceObject:          identity.ObjectRef{Type: objectType, ID: objectID},
		AuthorizationTenantID: authorizationTenantID,
		ReadAction:            readAction,
		ManageAction:          objectType + ".manage",
		BindingVersion:        1,
		State:                 BindingStateActive,
	}
}

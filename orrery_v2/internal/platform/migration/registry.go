package migration

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
)

type Registry struct {
	byRole map[string][]Migration
	clock  clock.Clock
}

func NewRegistry() *Registry {
	return NewRegistryWithOptions(RunnerOptions{})
}

func NewRegistryWithOptions(opts RunnerOptions) *Registry {
	return &Registry{byRole: map[string][]Migration{}, clock: migrationClock(opts.Clock)}
}

func (r *Registry) Register(role string, migration Migration) {
	if r.byRole == nil {
		r.byRole = map[string][]Migration{}
	}
	r.byRole[role] = append(r.byRole[role], migration)
}

func (r *Registry) Up(ctx context.Context, stores map[string]*db.Store) error {
	roles := r.roles()
	if err := r.validate(roles, stores); err != nil {
		return err
	}
	for _, role := range roles {
		if err := r.UpRole(ctx, role, stores[role]); err != nil {
			return fmt.Errorf("apply %s migrations: %w", role, err)
		}
	}
	return nil
}

func (r *Registry) UpRole(ctx context.Context, role string, store *db.Store) error {
	role = strings.TrimSpace(role)
	if role == "" {
		return fmt.Errorf("database role is empty")
	}
	if store == nil {
		return fmt.Errorf("database role %q is not available", role)
	}
	migrations, err := r.migrationsFor(role)
	if err != nil {
		return err
	}
	return NewRunnerWithOptions(migrations, RunnerOptions{Clock: r.clock}).Up(ctx, store)
}

func (r *Registry) LatestApplied(ctx context.Context, stores map[string]*db.Store) (bool, error) {
	roles := r.roles()
	if err := r.validate(roles, stores); err != nil {
		return false, err
	}
	for _, role := range roles {
		store := stores[role]
		migrations, err := r.migrationsFor(role)
		if err != nil {
			return false, err
		}
		ok, err := NewRunnerWithOptions(migrations, RunnerOptions{Clock: r.clock}).LatestApplied(ctx, store)
		if err != nil || !ok {
			return ok, err
		}
	}
	return true, nil
}

func (r *Registry) LatestAppliedByRole(ctx context.Context, stores map[string]*db.Store) map[string]error {
	results := map[string]error{}
	for _, role := range r.roles() {
		store := stores[role]
		if store == nil {
			results[role] = fmt.Errorf("database role %q is not available", role)
			continue
		}
		migrations, err := r.migrationsFor(role)
		if err != nil {
			results[role] = err
			continue
		}
		ok, err := NewRunnerWithOptions(migrations, RunnerOptions{Clock: r.clock}).LatestApplied(ctx, store)
		if err != nil {
			results[role] = err
			continue
		}
		if !ok {
			results[role] = fmt.Errorf("latest migration is not applied")
			continue
		}
		results[role] = nil
	}
	return results
}

func (r *Registry) Roles() []string {
	return r.roles()
}

func (r *Registry) roles() []string {
	roles := make([]string, 0, len(r.byRole))
	for role := range r.byRole {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

func (r *Registry) validate(roles []string, stores map[string]*db.Store) error {
	for _, role := range roles {
		if strings.TrimSpace(role) == "" {
			return fmt.Errorf("database role is empty")
		}
		if stores[role] == nil {
			return fmt.Errorf("database role %q is not available", role)
		}
		if _, err := r.migrationsFor(role); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) migrationsFor(role string) ([]Migration, error) {
	migrations := append([]Migration(nil), r.byRole[role]...)
	sort.SliceStable(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	seen := map[string]struct{}{}
	for _, migration := range migrations {
		version := strings.TrimSpace(migration.Version)
		if version == "" {
			return nil, fmt.Errorf("migration version is empty for database role %q", role)
		}
		if _, ok := seen[version]; ok {
			return nil, fmt.Errorf("duplicate migration version %q for database role %q", version, role)
		}
		seen[version] = struct{}{}
	}
	return migrations, nil
}

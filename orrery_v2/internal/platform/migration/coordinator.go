package migration

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
)

type Coordinator struct {
	databases  *db.Registry
	migrations *Registry
}

func OpenCoordinatorFromDatabaseURLs(ctx context.Context, urls map[string]string) (*Coordinator, error) {
	databases, err := db.OpenRegistryFromURLs(ctx, urls)
	if err != nil {
		return nil, err
	}
	return NewCoordinator(databases), nil
}

func NewCoordinator(databases *db.Registry) *Coordinator {
	return NewCoordinatorWithOptions(databases, RunnerOptions{})
}

func NewCoordinatorWithOptions(databases *db.Registry, opts RunnerOptions) *Coordinator {
	return &Coordinator{
		databases:  databases,
		migrations: PlatformMigrationRegistryWithOptions(opts),
	}
}

func (c *Coordinator) WithClock(clock clock.Clock) *Coordinator {
	if c != nil && c.migrations != nil && clock != nil {
		c.migrations.clock = clock
	}
	return c
}

func (c *Coordinator) RegisterInitializer(role string, migration Migration) error {
	if c == nil {
		return fmt.Errorf("migration coordinator is nil")
	}
	if strings.TrimSpace(role) == "" {
		return fmt.Errorf("database role is empty")
	}
	if strings.TrimSpace(migration.Version) == "" {
		return fmt.Errorf("migration version is empty for database role %q", role)
	}
	c.migrations.Register(role, migration)
	return nil
}

func (c *Coordinator) Up(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("migration coordinator is nil")
	}
	stores, err := c.storesByRole()
	if err != nil {
		return err
	}
	return c.migrations.Up(ctx, stores)
}

func (c *Coordinator) UpRole(ctx context.Context, role string) error {
	if c == nil {
		return fmt.Errorf("migration coordinator is nil")
	}
	if strings.TrimSpace(role) == "" {
		return fmt.Errorf("database role is empty")
	}
	if c.databases == nil {
		return fmt.Errorf("database registry is not configured")
	}
	if c.migrations == nil {
		return fmt.Errorf("migration registry is not configured")
	}
	store, ok := c.databases.Get(role)
	if !ok || store == nil {
		return fmt.Errorf("database role %q is not available", role)
	}
	return c.migrations.UpRole(ctx, role, store)
}

func (c *Coordinator) LatestApplied(ctx context.Context) (bool, error) {
	if c == nil {
		return false, fmt.Errorf("migration coordinator is nil")
	}
	stores, err := c.storesByRole()
	if err != nil {
		return false, err
	}
	return c.migrations.LatestApplied(ctx, stores)
}

func (c *Coordinator) LatestAppliedByRole(ctx context.Context) map[string]error {
	if c == nil {
		return map[string]error{"migration": fmt.Errorf("migration coordinator is nil")}
	}
	stores, err := c.storesByRole()
	if err != nil {
		results := map[string]error{}
		for _, role := range c.migrations.Roles() {
			results[role] = err
		}
		return results
	}
	return c.migrations.LatestAppliedByRole(ctx, stores)
}

func (c *Coordinator) Registry() *Registry {
	if c == nil {
		return nil
	}
	return c.migrations
}

func (c *Coordinator) Databases() *db.Registry {
	if c == nil {
		return nil
	}
	return c.databases
}

func (c *Coordinator) Close() error {
	if c == nil || c.databases == nil {
		return nil
	}
	return c.databases.Close()
}

func (c *Coordinator) storesByRole() (map[string]*db.Store, error) {
	if c.databases == nil {
		return nil, fmt.Errorf("database registry is not configured")
	}
	stores := map[string]*db.Store{}
	for _, role := range c.migrations.Roles() {
		store, ok := c.databases.Get(role)
		if !ok || store == nil {
			return nil, fmt.Errorf("database role %q is not available", role)
		}
		stores[role] = store
	}
	return stores, nil
}

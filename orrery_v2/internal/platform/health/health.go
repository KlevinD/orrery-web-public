package health

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

type Check struct {
	Name    string
	OK      bool
	Message string
}

type Report struct {
	OK        bool
	Checks    []Check
	CheckedAt time.Time
}

const (
	readinessStaleGrace     = 10 * time.Second
	readinessRefreshTimeout = 8 * time.Second
)

type Checker struct {
	readyMu       sync.Mutex
	refreshing    bool
	refreshDone   chan struct{}
	lastRefresh   Report
	lastRefreshAt time.Time
	store         *db.Store
	runner        *migration.Runner
	cacheTTL      time.Duration
	cached        Report
	cachedAt      time.Time
	registry      *db.Registry
	migrations    *migration.Registry
	coordinator   *migration.Coordinator
	fileStoreRoot string
	configManager *config.Manager
	customChecks  []ReadyCheck
	clock         clock.Clock
}

type ReadyCheck func(context.Context) Check

func NewChecker(store *db.Store, runner *migration.Runner, cacheTTL time.Duration) *Checker {
	return &Checker{store: store, runner: runner, cacheTTL: cacheTTL, clock: clock.Real{}}
}

func (c *Checker) WithDatabases(registry *db.Registry) *Checker {
	c.registry = registry
	return c
}

func (c *Checker) WithMigrationRegistry(registry *migration.Registry) *Checker {
	c.migrations = registry
	return c
}

func (c *Checker) WithMigrationCoordinator(coordinator *migration.Coordinator) *Checker {
	c.coordinator = coordinator
	return c
}

func (c *Checker) WithFileStoreRoot(root string) *Checker {
	c.fileStoreRoot = root
	return c
}

func (c *Checker) WithConfigManager(manager *config.Manager) *Checker {
	c.configManager = manager
	return c
}

func (c *Checker) WithClock(clock clock.Clock) *Checker {
	if clock != nil {
		c.clock = clock
	}
	return c
}

func (c *Checker) WithReadyCheck(check ReadyCheck) *Checker {
	if check != nil {
		c.customChecks = append(c.customChecks, check)
	}
	return c
}

func (c *Checker) Live(ctx context.Context) Report {
	return Report{
		OK:        ctx.Err() == nil,
		CheckedAt: c.now(),
		Checks:    []Check{{Name: "process", OK: ctx.Err() == nil}},
	}
}

func (c *Checker) Ready(ctx context.Context) Report {
	if c.configManager != nil && c.configManager.LastError() != nil {
		return c.refreshSynchronously(ctx, true)
	}

	c.readyMu.Lock()
	now := c.now()
	if c.cacheTTL > 0 && c.cached.OK && !c.cachedAt.IsZero() {
		age := now.Sub(c.cachedAt)
		if age < c.cacheTTL {
			report := c.cached
			c.readyMu.Unlock()
			return report
		}
		if age < c.cacheTTL+readinessStaleGrace {
			if !c.refreshing && c.refreshDue(now) {
				c.beginRefreshLocked()
				go c.refreshInBackground()
			}
			report := c.cached
			c.readyMu.Unlock()
			return report
		}
	}
	c.readyMu.Unlock()
	return c.refreshSynchronously(ctx, false)
}

func (c *Checker) refreshDue(now time.Time) bool {
	if c.lastRefreshAt.IsZero() {
		return true
	}
	retryAfter := c.cacheTTL
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	if retryAfter > readinessStaleGrace {
		retryAfter = readinessStaleGrace
	}
	return now.Sub(c.lastRefreshAt) >= retryAfter
}

func (c *Checker) beginRefreshLocked() {
	c.refreshing = true
	c.refreshDone = make(chan struct{})
}

func (c *Checker) refreshInBackground() {
	ctx, cancel := context.WithTimeout(context.Background(), readinessRefreshTimeout)
	defer cancel()
	c.finishRefresh(c.checkReady(ctx))
}

func (c *Checker) refreshSynchronously(ctx context.Context, requireConfigFailure bool) Report {
	ctx, cancel := context.WithTimeout(ctx, readinessRefreshTimeout)
	defer cancel()
	for {
		c.readyMu.Lock()
		if !c.refreshing {
			c.beginRefreshLocked()
			c.readyMu.Unlock()
			report := c.checkReady(ctx)
			c.finishRefresh(report)
			return report
		}
		done := c.refreshDone
		c.readyMu.Unlock()

		select {
		case <-done:
			c.readyMu.Lock()
			report := c.lastRefresh
			c.readyMu.Unlock()
			if !requireConfigFailure || hasFailedCheck(report, "config") {
				return report
			}
		case <-ctx.Done():
			return Report{
				OK:        false,
				Checks:    []Check{{Name: "readiness", OK: false, Message: ctx.Err().Error()}},
				CheckedAt: c.now(),
			}
		}
	}
}

func (c *Checker) finishRefresh(report Report) {
	c.readyMu.Lock()
	c.lastRefresh = report
	c.lastRefreshAt = c.now()
	if report.OK {
		c.cached = report
		c.cachedAt = report.CheckedAt
	}
	done := c.refreshDone
	c.refreshing = false
	c.refreshDone = nil
	close(done)
	c.readyMu.Unlock()
}

func hasFailedCheck(report Report, name string) bool {
	for _, check := range report.Checks {
		if check.Name == name && !check.OK {
			return true
		}
	}
	return false
}

func (c *Checker) checkReady(ctx context.Context) Report {
	checks := []Check{}
	dbOK := c.checkPrimaryDatabase(ctx, &checks)
	registryOK := c.checkDatabases(ctx, &checks)
	migrationsOK := c.checkMigrations(ctx, dbOK, &checks)
	filestoreOK := c.checkFileStore(&checks)
	configOK := c.checkConfig(&checks)
	restartOK := c.checkRestartRequired(&checks)
	customOK := c.checkCustom(ctx, &checks)
	return Report{OK: dbOK && registryOK && migrationsOK && filestoreOK && configOK && restartOK && customOK, Checks: checks, CheckedAt: c.now()}
}

func (c *Checker) now() time.Time {
	if c == nil || c.clock == nil {
		return time.Now().UTC()
	}
	return c.clock.Now().UTC()
}

func (c *Checker) checkPrimaryDatabase(ctx context.Context, checks *[]Check) bool {
	if c.store == nil && c.coordinator != nil && c.coordinator.Databases() != nil {
		if store, ok := c.coordinator.Databases().Get(db.RoleCore); ok {
			c.store = store
		}
	}
	if c.store == nil {
		*checks = append(*checks, Check{Name: "db", OK: false, Message: "database store is not configured"})
		return false
	}
	err := c.store.Ping(ctx)
	check := Check{Name: "db", OK: err == nil}
	if err != nil {
		check.Message = err.Error()
	}
	*checks = append(*checks, check)
	return err == nil
}

func (c *Checker) checkMigrations(ctx context.Context, dbOK bool, checks *[]Check) bool {
	if c.coordinator != nil {
		ok := true
		results := c.coordinator.LatestAppliedByRole(ctx)
		for _, role := range c.coordinator.Registry().Roles() {
			err := results[role]
			check := Check{Name: "migration." + role, OK: err == nil}
			if err != nil {
				check.Message = err.Error()
				ok = false
			}
			*checks = append(*checks, check)
		}
		return ok
	}
	if c.migrations != nil {
		if c.registry == nil {
			*checks = append(*checks, Check{Name: "migration", OK: false, Message: "database registry not configured"})
			return false
		}
		stores := map[string]*db.Store{}
		for _, name := range []string{db.RoleCore, db.RoleAudit, db.RoleEvents, db.RoleJobs, db.RoleRuntime, db.RoleObservability} {
			if store, ok := c.registry.Get(name); ok {
				stores[name] = store
			}
		}
		ok := true
		results := c.migrations.LatestAppliedByRole(ctx, stores)
		for _, role := range c.migrations.Roles() {
			err := results[role]
			check := Check{Name: "migration." + role, OK: err == nil}
			if err != nil {
				check.Message = err.Error()
				ok = false
			}
			*checks = append(*checks, check)
		}
		return ok
	}
	if c.runner == nil {
		*checks = append(*checks, Check{Name: "migration", OK: false, Message: "migration runner is not configured"})
		return false
	}
	if dbOK {
		ready, err := c.runner.LatestApplied(ctx, c.store)
		migrationsOK := err == nil && ready
		message := ""
		if err != nil {
			message = err.Error()
		}
		*checks = append(*checks, Check{Name: "migration", OK: migrationsOK, Message: message})
		return migrationsOK
	}
	*checks = append(*checks, Check{Name: "migration", OK: false, Message: "db not ready"})
	return false
}

func (c *Checker) checkDatabases(ctx context.Context, checks *[]Check) bool {
	if c.registry == nil && c.coordinator != nil {
		c.registry = c.coordinator.Databases()
	}
	if c.registry == nil {
		return true
	}
	ok := true
	for _, name := range []string{db.RoleCore, db.RoleAudit, db.RoleEvents, db.RoleJobs, db.RoleRuntime, db.RoleObservability} {
		store, exists := c.registry.Get(name)
		if !exists {
			continue
		}
		err := store.Ping(ctx)
		if err == nil {
			err = store.CheckWritable(ctx)
		}
		check := Check{Name: "db." + name, OK: err == nil}
		if err != nil {
			check.Message = err.Error()
			ok = false
		}
		*checks = append(*checks, check)
	}
	return ok
}

func (c *Checker) checkFileStore(checks *[]Check) bool {
	if c.fileStoreRoot == "" {
		return true
	}
	root := filepath.Clean(c.fileStoreRoot)
	err := os.MkdirAll(root, 0o755)
	if err == nil {
		var stat os.FileInfo
		stat, err = os.Stat(root)
		if err == nil && !stat.IsDir() {
			err = &os.PathError{Op: "stat", Path: root, Err: os.ErrInvalid}
		}
	}
	check := Check{Name: "filestore", OK: err == nil}
	if err != nil {
		check.Message = err.Error()
	}
	*checks = append(*checks, check)
	return err == nil
}

func (c *Checker) checkConfig(checks *[]Check) bool {
	if c.configManager == nil {
		return true
	}
	err := c.configManager.LastError()
	check := Check{Name: "config", OK: err == nil}
	if err != nil {
		check.Message = err.Error()
	}
	*checks = append(*checks, check)
	return err == nil
}

func (c *Checker) checkRestartRequired(checks *[]Check) bool {
	if c.configManager == nil {
		return true
	}
	err := c.configManager.LastError()
	if err == nil || !strings.Contains(err.Error(), "restart required") {
		return true
	}
	*checks = append(*checks, Check{Name: "runtime.restart_required", OK: false, Message: err.Error()})
	return false
}

func (c *Checker) checkCustom(ctx context.Context, checks *[]Check) bool {
	ok := true
	for _, checkFn := range c.customChecks {
		if checkFn == nil {
			continue
		}
		check := runReadyCheck(ctx, checkFn)
		if check.Name == "" {
			check.Name = "custom"
		}
		if !check.OK {
			ok = false
		}
		*checks = append(*checks, check)
	}
	return ok
}

func runReadyCheck(ctx context.Context, checkFn ReadyCheck) (check Check) {
	defer func() {
		if recovered := recover(); recovered != nil {
			check = Check{Name: "custom", OK: false, Message: "ready check panic"}
		}
	}()
	return checkFn(ctx)
}

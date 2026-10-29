package config

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/logging"
)

type SectionValidator func(Config) error

type Manager struct {
	mu                sync.RWMutex
	current           Config
	version           int64
	lastError         error
	subscribers       map[int]func(Config)
	changeSubscribers map[int]func(int64)
	sections          map[string]SectionValidator
	nextID            int

	lifecycleMu sync.RWMutex
	closed      bool
	watchMu     sync.Mutex
	watchStops  []func()
	watchWG     sync.WaitGroup
}

func NewManager(initial Config) *Manager {
	manager := &Manager{
		current:           initial.clone(),
		version:           1,
		subscribers:       map[int]func(Config){},
		changeSubscribers: map[int]func(int64){},
		sections:          map[string]SectionValidator{},
	}
	manager.registerPlatformSections()
	if err := manager.validate(initial); err != nil {
		manager.lastError = err
	}
	return manager
}

func (m *Manager) Current() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current.clone()
}

func (m *Manager) Lookup(path string) (any, bool) {
	return m.Current().Lookup(path)
}

func (m *Manager) String(path string) (string, bool) {
	return m.Current().String(path)
}

func (m *Manager) Duration(path string) (time.Duration, bool) {
	return m.Current().Duration(path)
}

func (m *Manager) Int(path string) (int, bool) {
	return m.Current().Int(path)
}

func (m *Manager) Bool(path string) (bool, bool) {
	return m.Current().Bool(path)
}

func (m *Manager) DecodeSection(path string, target any) error {
	return m.Current().DecodeSection(path, target)
}

func (m *Manager) Version() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.version
}

func (m *Manager) LastError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastError
}

func (m *Manager) RecordError(err error) {
	if m == nil {
		return
	}
	if err := m.beginOperation(); err != nil {
		return
	}
	defer m.endOperation()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = err
}

func (m *Manager) Subscribe(fn func(Config)) (func(), error) {
	if fn == nil {
		return nil, fmt.Errorf("config subscriber is required")
	}
	if err := m.beginOperation(); err != nil {
		return nil, err
	}
	defer m.endOperation()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := m.nextID
	m.subscribers[id] = fn
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.subscribers, id)
	}, nil
}

func (m *Manager) SubscribeChange(fn func(version int64)) (func(), error) {
	if fn == nil {
		return nil, fmt.Errorf("config change subscriber is required")
	}
	if err := m.beginOperation(); err != nil {
		return nil, err
	}
	defer m.endOperation()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := m.nextID
	m.changeSubscribers[id] = fn
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.changeSubscribers, id)
	}, nil
}

func (m *Manager) RegisterSection(name string, validator SectionValidator) error {
	if name == "" {
		return fmt.Errorf("config section name is required")
	}
	if validator == nil {
		validator = func(Config) error { return nil }
	}
	if err := m.beginOperation(); err != nil {
		return err
	}
	defer m.endOperation()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sections[name]; exists {
		return fmt.Errorf("config section %q already registered", name)
	}
	if err := runSectionValidator(name, validator, m.current); err != nil {
		m.lastError = err
		return err
	}
	m.sections[name] = validator
	return nil
}

func (m *Manager) RegisteredSections() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sections := make([]string, 0, len(m.sections))
	for name := range m.sections {
		sections = append(sections, name)
	}
	sort.Strings(sections)
	return sections
}

func (m *Manager) ReloadFromFile(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.beginOperation(); err != nil {
		return err
	}
	defer m.endOperation()
	cfg, err := LoadYAML(path)
	if err != nil {
		m.mu.Lock()
		m.lastError = err
		m.mu.Unlock()
		return err
	}
	if err := m.validateSnapshot(cfg); err != nil {
		m.mu.Lock()
		m.lastError = err
		m.mu.Unlock()
		return err
	}
	m.publish(cfg)
	return nil
}

func WatchYAML(ctx context.Context, path string, interval time.Duration, logger logging.Logger) (*Manager, error) {
	initial, err := LoadYAML(path)
	if err != nil {
		return nil, err
	}
	manager := NewManager(initial)
	if interval <= 0 {
		interval = time.Second
	}
	watchCtx, cancel := context.WithCancel(ctx)
	manager.registerWatcher(cancel)
	go func() {
		defer manager.watchWG.Done()
		manager.watch(watchCtx, path, interval, logger)
	}()
	return manager, nil
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.lifecycleMu.Lock()
	m.closed = true
	m.lifecycleMu.Unlock()

	m.watchMu.Lock()
	stops := append([]func(){}, m.watchStops...)
	m.watchStops = nil
	m.watchMu.Unlock()
	for _, stop := range stops {
		stop()
	}
	m.watchWG.Wait()
	return nil
}

func (m *Manager) registerWatcher(cancel func()) bool {
	m.lifecycleMu.RLock()
	defer m.lifecycleMu.RUnlock()
	if m.closed {
		cancel()
		return false
	}
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	m.watchStops = append(m.watchStops, cancel)
	m.watchWG.Add(1)
	return true
}

func (m *Manager) beginOperation() error {
	if m == nil {
		return fmt.Errorf("config manager is not configured")
	}
	m.lifecycleMu.RLock()
	if m.closed {
		m.lifecycleMu.RUnlock()
		return fmt.Errorf("config manager is closed")
	}
	return nil
}

func (m *Manager) endOperation() {
	m.lifecycleMu.RUnlock()
}

func (m *Manager) publish(cfg Config) {
	cfg = cfg.clone()
	m.mu.Lock()
	m.current = cfg
	m.version++
	m.lastError = nil
	version := m.version
	subscribers := make([]func(Config), 0, len(m.subscribers))
	for _, subscriber := range m.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	changeSubscribers := make([]func(int64), 0, len(m.changeSubscribers))
	for _, subscriber := range m.changeSubscribers {
		changeSubscribers = append(changeSubscribers, subscriber)
	}
	m.mu.Unlock()

	for _, subscriber := range changeSubscribers {
		m.notifyChangeSubscriber(subscriber, version)
	}
	for _, subscriber := range subscribers {
		m.notifySubscriber(subscriber, cfg.clone())
	}
}

func (m *Manager) notifyChangeSubscriber(subscriber func(int64), version int64) {
	defer func() {
		if recovered := recover(); recovered != nil {
			m.mu.Lock()
			m.lastError = fmt.Errorf("config change subscriber panic: %v", recovered)
			m.mu.Unlock()
		}
	}()
	subscriber(version)
}

func (m *Manager) notifySubscriber(subscriber func(Config), cfg Config) {
	defer func() {
		if recovered := recover(); recovered != nil {
			m.mu.Lock()
			m.lastError = fmt.Errorf("config subscriber panic: %v", recovered)
			m.mu.Unlock()
		}
	}()
	subscriber(cfg)
}

func (m *Manager) registerPlatformSections() {
	m.sections["database"] = validateDatabaseSection
	m.sections["device_delivery"] = validateDeviceDeliverySection
	m.sections["filestore"] = validateFileStoreSection
	m.sections["health"] = validateHealthSection
	m.sections["liveness"] = validateLivenessSection
	m.sections["lock"] = validateLockSection
	m.sections["logging"] = validateLoggingSection
	m.sections["metrics"] = validateMetricsSection
	m.sections["paths"] = validatePathsSection
	m.sections["queue"] = validateQueueSection
	m.sections["secret"] = validateSecretSection
	m.sections["sweeper"] = validateSweeperSection
	m.sections["tracing"] = validateTracingSection
}

func validateDeviceDeliverySection(cfg Config) error {
	if err := requireSection(cfg, "device_delivery"); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.DeviceDelivery.DefaultTenantID) == "" {
		return SectionValidationError("device_delivery", "default_tenant_id is required")
	}
	if cfg.DeviceDelivery.MaxArtifactBytes < 1 || cfg.DeviceDelivery.MaxArtifactBytes > 100*1024*1024 {
		return SectionValidationError("device_delivery", "max_artifact_bytes must be between 1 and 104857600")
	}

	rawOrigin := strings.TrimSpace(cfg.DeviceDelivery.PublicOrigin)
	production := strings.EqualFold(strings.TrimSpace(cfg.Environment), "production")
	if rawOrigin == "" {
		if production {
			return SectionValidationError("device_delivery", "public_origin is required in production")
		}
		return nil
	}
	origin, err := url.Parse(rawOrigin)
	if err != nil || origin.Hostname() == "" || origin.User != nil || origin.Path != "" || origin.RawPath != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || origin.Opaque != "" {
		return SectionValidationError("device_delivery", "public_origin must be an origin without credentials, path, query, or fragment")
	}
	if origin.Scheme == "https" {
		return nil
	}
	if !production && origin.Scheme == "http" && isLoopbackHostname(origin.Hostname()) {
		return nil
	}
	return SectionValidationError("device_delivery", "public_origin must use HTTPS; development HTTP is limited to loopback hosts")
}

func isLoopbackHostname(hostname string) bool {
	if strings.EqualFold(strings.TrimSpace(hostname), "localhost") {
		return true
	}
	address := net.ParseIP(hostname)
	return address != nil && address.IsLoopback()
}

func (m *Manager) validateSnapshot(cfg Config) error {
	m.mu.RLock()
	validators := make(map[string]SectionValidator, len(m.sections))
	for name, validator := range m.sections {
		validators[name] = validator
	}
	m.mu.RUnlock()
	return validateSections(cfg, validators)
}

func (m *Manager) validate(cfg Config) error {
	return validateSections(cfg, m.sections)
}

func validateSections(cfg Config, validators map[string]SectionValidator) error {
	for name, validator := range validators {
		if err := runSectionValidator(name, validator, cfg); err != nil {
			return fmt.Errorf("validate config section %q: %w", name, err)
		}
	}
	return nil
}

func runSectionValidator(name string, validator SectionValidator, cfg Config) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = SectionValidationError(name, fmt.Sprintf("validator panic: %v", recovered))
		}
	}()
	return validator(cfg.clone())
}

func requireSection(cfg Config, name string) error {
	if _, ok := cfg.Lookup(name); !ok {
		return SectionValidationError(name, "section is required")
	}
	return nil
}

func validateDatabaseSection(cfg Config) error {
	if err := requireSection(cfg, "database"); err != nil {
		return err
	}
	if cfg.DatabaseURL == "" {
		return SectionValidationError("database", "default_url is required")
	}
	if len(cfg.DatabaseURLs) == 0 {
		return SectionValidationError("database", "urls is required")
	}
	if !isSupportedDatabaseURL(cfg.DatabaseURL) {
		return SectionValidationError("database", "default_url must use memory://, file://, sqlite://, postgres://, or postgresql://")
	}
	for _, role := range []string{"core", "audit", "events", "jobs", "runtime", "observability"} {
		databaseURL := cfg.DatabaseURLs[role]
		if strings.TrimSpace(databaseURL) == "" {
			return SectionValidationError("database", "urls."+role+" is required")
		}
		if !isSupportedDatabaseURL(databaseURL) {
			return SectionValidationError("database", "urls."+role+" must use memory://, file://, sqlite://, postgres://, or postgresql://")
		}
	}
	return nil
}

func isSupportedDatabaseURL(rawURL string) bool {
	return strings.HasPrefix(rawURL, "memory://") ||
		strings.HasPrefix(rawURL, "file://") ||
		strings.HasPrefix(rawURL, "sqlite://") ||
		strings.HasPrefix(rawURL, "postgres://") ||
		strings.HasPrefix(rawURL, "postgresql://")
}

func validatePathsSection(cfg Config) error {
	if err := requireSection(cfg, "paths"); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.BaseDir) == "" {
		return SectionValidationError("paths", "base_dir is required")
	}
	return nil
}

func validateFileStoreSection(cfg Config) error {
	if err := requireSection(cfg, "filestore"); err != nil {
		return err
	}
	switch cfg.FileStore.Backend {
	case "local":
		if cfg.FileStore.Root == "" {
			return SectionValidationError("filestore", "root is required for local backend")
		}
	case "s3":
		if cfg.FileStore.S3.Bucket == "" {
			return SectionValidationError("filestore", "s3.bucket is required for s3 backend")
		}
		if strings.Contains(cfg.FileStore.S3.Bucket, "amazonaws.com") {
			return SectionValidationError("filestore", "s3.bucket must be a bucket name, not an S3 hostname")
		}
	default:
		return SectionValidationError("filestore", "backend must be local or s3")
	}
	return nil
}

func validateHealthSection(cfg Config) error {
	if err := requireSection(cfg, "health"); err != nil {
		return err
	}
	if cfg.ReadinessCacheTTL <= 0 {
		return SectionValidationError("health", "readiness_cache_ttl must be positive")
	}
	return nil
}

func validateLivenessSection(cfg Config) error {
	if err := requireSection(cfg, "liveness"); err != nil {
		return err
	}
	if cfg.Liveness.HeartbeatTTL <= 0 {
		return SectionValidationError("liveness", "heartbeat_ttl must be positive")
	}
	return nil
}

func validateLockSection(cfg Config) error {
	if err := requireSection(cfg, "lock"); err != nil {
		return err
	}
	if cfg.Lock.DefaultTTL <= 0 {
		return SectionValidationError("lock", "default_ttl must be positive")
	}
	return nil
}

func validateLoggingSection(cfg Config) error {
	if err := requireSection(cfg, "logging"); err != nil {
		return err
	}
	switch cfg.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return SectionValidationError("logging", "level must be debug, info, warn, or error")
	}
	return nil
}

func validateMetricsSection(cfg Config) error {
	if err := requireSection(cfg, "metrics"); err != nil {
		return err
	}
	if cfg.Metrics.BufferSize <= 0 {
		return SectionValidationError("metrics", "buffer_size must be positive")
	}
	if cfg.Metrics.WarnInterval <= 0 {
		return SectionValidationError("metrics", "warn_interval must be positive")
	}
	return nil
}

func validateQueueSection(cfg Config) error {
	if err := requireSection(cfg, "queue"); err != nil {
		return err
	}
	if cfg.Queue.ClaimTTL <= 0 {
		return SectionValidationError("queue", "claim_ttl must be positive")
	}
	if cfg.Queue.MaxAttempts <= 0 {
		return SectionValidationError("queue", "max_attempts must be positive")
	}
	if cfg.Queue.SweepBatchSize <= 0 {
		return SectionValidationError("queue", "sweep_batch_size must be positive")
	}
	if cfg.Queue.WorkerConcurrency <= 0 || cfg.Queue.WorkerConcurrency > 32 {
		return SectionValidationError("queue", "worker_concurrency must be between 1 and 32")
	}
	return nil
}

func validateSecretSection(cfg Config) error {
	if err := requireSection(cfg, "secret"); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Secret.Root) == "" {
		return SectionValidationError("secret", "root is required")
	}
	return nil
}

func validateSweeperSection(cfg Config) error {
	if err := requireSection(cfg, "sweeper"); err != nil {
		return err
	}
	if cfg.Sweeper.Timeout <= 0 {
		return SectionValidationError("sweeper", "timeout must be positive")
	}
	if cfg.Sweeper.BatchSize <= 0 {
		return SectionValidationError("sweeper", "batch_size must be positive")
	}
	return nil
}

func validateTracingSection(cfg Config) error {
	if err := requireSection(cfg, "tracing"); err != nil {
		return err
	}
	if cfg.Tracing.BufferSize <= 0 {
		return SectionValidationError("tracing", "buffer_size must be positive")
	}
	return nil
}

func (m *Manager) watch(ctx context.Context, path string, interval time.Duration, logger logging.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastMod time.Time
	var lastSize int64
	if stat, err := os.Stat(path); err == nil {
		lastMod = stat.ModTime()
		lastSize = stat.Size()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stat, err := os.Stat(path)
			if err != nil {
				m.mu.Lock()
				m.lastError = err
				m.mu.Unlock()
				continue
			}
			if stat.ModTime().Equal(lastMod) && stat.Size() == lastSize {
				continue
			}
			if err := m.ReloadFromFile(ctx, path); err != nil {
				if logger != nil {
					logger.Warn(ctx, "config reload failed",
						logging.String("operation", "config.watch"),
						logging.String("error_code", "config_reload_failed"),
						logging.String("error", err.Error()),
					)
				}
				continue
			}
			lastMod = stat.ModTime()
			lastSize = stat.Size()
		}
	}
}

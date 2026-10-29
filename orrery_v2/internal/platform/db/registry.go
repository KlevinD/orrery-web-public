package db

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

const (
	RoleCore          = "core"
	RoleAudit         = "audit"
	RoleEvents        = "events"
	RoleJobs          = "jobs"
	RoleRuntime       = "runtime"
	RoleObservability = "observability"
)

type RegistryConfig struct {
	Databases map[string]Config
}

type Registry struct {
	stores map[string]*Store
}

func RegistryConfigFromURLs(urls map[string]string) (RegistryConfig, error) {
	databases := map[string]Config{}
	for name, rawURL := range urls {
		driver, err := driverFromURL(rawURL)
		if err != nil {
			return RegistryConfig{}, fmt.Errorf("database %s: %w", name, err)
		}
		databaseName := name
		if driver == DriverMemory {
			if parsedName, err := memoryNameFromURL(rawURL); err != nil {
				return RegistryConfig{}, fmt.Errorf("database %s: %w", name, err)
			} else if parsedName != "" {
				databaseName = parsedName
			}
		}
		databases[name] = Config{Driver: driver, Name: databaseName, URL: rawURL}
	}
	return RegistryConfig{Databases: databases}, nil
}

func OpenRegistryFromURLs(ctx context.Context, urls map[string]string) (*Registry, error) {
	cfg, err := RegistryConfigFromURLs(urls)
	if err != nil {
		return nil, err
	}
	return OpenRegistry(ctx, cfg)
}

func OpenRegistry(ctx context.Context, cfg RegistryConfig) (*Registry, error) {
	if len(cfg.Databases) == 0 {
		return nil, fmt.Errorf("registry requires at least one database")
	}
	stores := map[string]*Store{}
	names := make([]string, 0, len(cfg.Databases))
	for name := range cfg.Databases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dbCfg := cfg.Databases[name]
		if dbCfg.Name == "" {
			dbCfg.Name = name
		}
		store, err := Open(ctx, dbCfg)
		if err != nil {
			for _, opened := range stores {
				_ = opened.Close()
			}
			return nil, fmt.Errorf("open %s database: %w", name, err)
		}
		stores[name] = store
	}
	return &Registry{stores: stores}, nil
}

func driverFromURL(rawURL string) (Driver, error) {
	switch {
	case strings.HasPrefix(rawURL, "memory://"):
		return DriverMemory, nil
	case strings.HasPrefix(rawURL, "file://"), strings.HasPrefix(rawURL, "sqlite://"):
		return DriverFile, nil
	case strings.HasPrefix(rawURL, "postgres://"), strings.HasPrefix(rawURL, "postgresql://"):
		return DriverPostgres, nil
	default:
		return "", fmt.Errorf("unsupported database url: %s", rawURL)
	}
}

func memoryNameFromURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse memory database url: %w", err)
	}
	return strings.Trim(strings.TrimSpace(parsed.Host+parsed.Path), "/"), nil
}

func (r *Registry) Get(name string) (*Store, bool) {
	if r == nil {
		return nil, false
	}
	store, ok := r.stores[name]
	return store, ok
}

func (r *Registry) Store(name string) (*Store, error) {
	store, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("database role %q is not available", name)
	}
	return store, nil
}

func (r *Registry) Close() error {
	if r == nil {
		return nil
	}
	var firstErr error
	for _, store := range r.stores {
		if err := store.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

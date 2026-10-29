package db

import (
	"context"
	"fmt"
	"strings"
)

type Router struct {
	registry    *Registry
	defaultRole string
}

func NewRouter(registry *Registry) *Router {
	return &Router{registry: registry, defaultRole: RoleCore}
}

func (r *Router) PutRecord(ctx context.Context, logicalTable, key string, record Record) error {
	store, table, err := r.StoreForTable(logicalTable)
	if err != nil {
		return err
	}
	return store.PutRecord(ctx, table, key, record)
}

func (r *Router) CreateRecord(ctx context.Context, logicalTable, key string, record Record) (bool, error) {
	store, table, err := r.StoreForTable(logicalTable)
	if err != nil {
		return false, err
	}
	return store.CreateRecord(ctx, table, key, record)
}

func (r *Router) CompareAndSwapRecord(ctx context.Context, logicalTable, key string, expected, replacement Record) (bool, error) {
	store, table, err := r.StoreForTable(logicalTable)
	if err != nil {
		return false, err
	}
	return store.CompareAndSwapRecord(ctx, table, key, expected, replacement)
}

func (r *Router) CompareAndDeleteRecord(ctx context.Context, logicalTable, key string, expected Record) (bool, error) {
	store, table, err := r.StoreForTable(logicalTable)
	if err != nil {
		return false, err
	}
	return store.CompareAndDeleteRecord(ctx, table, key, expected)
}

func (r *Router) GetRecord(ctx context.Context, logicalTable, key string) (Record, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	store, table, err := r.StoreForTable(logicalTable)
	if err != nil {
		return nil, false, err
	}
	return store.GetRecord(ctx, table, key)
}

func (r *Router) ListRecords(ctx context.Context, logicalTable, afterKey string, limit int) (RecordPage, error) {
	if err := ctx.Err(); err != nil {
		return RecordPage{}, err
	}
	store, table, err := r.StoreForTable(logicalTable)
	if err != nil {
		return RecordPage{}, err
	}
	return store.ListRecords(ctx, table, afterKey, limit)
}

func (r *Router) DeleteRecord(ctx context.Context, logicalTable, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store, table, err := r.StoreForTable(logicalTable)
	if err != nil {
		return err
	}
	return store.DeleteRecord(ctx, table, key)
}

func (r *Router) Get(logicalTable, key string) (Record, bool) {
	record, ok, _ := r.GetRecord(context.Background(), logicalTable, key)
	return record, ok
}

func (r *Router) StoreForTable(logicalTable string) (*Store, string, error) {
	role, table, err := r.roleAndTable(logicalTable)
	if err != nil {
		return nil, "", err
	}
	store, err := r.registry.Store(role)
	if err != nil {
		return nil, "", fmt.Errorf("%w for logical table %q", err, logicalTable)
	}
	return store, table, nil
}

func (r *Router) roleAndTable(logicalTable string) (string, string, error) {
	if r == nil || r.registry == nil {
		return "", "", fmt.Errorf("database router requires a registry")
	}
	logicalTable = strings.TrimSpace(logicalTable)
	if logicalTable == "" {
		return "", "", fmt.Errorf("logical table is required")
	}
	role := r.defaultRole
	table := logicalTable
	if before, after, ok := strings.Cut(logicalTable, "."); ok {
		role = strings.TrimSpace(before)
		table = strings.TrimSpace(after)
	}
	if role == "" {
		return "", "", fmt.Errorf("logical table %q has an empty database role", logicalTable)
	}
	if table == "" {
		return "", "", fmt.Errorf("logical table %q has an empty table name", logicalTable)
	}
	return role, table, nil
}

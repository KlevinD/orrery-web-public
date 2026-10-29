package storage

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

var specsByName = func() map[string]TableSpec {
	specs := map[string]TableSpec{}
	for _, spec := range CoreTables() {
		specs[spec.LogicalName] = spec
	}
	return specs
}()

func MustTable(logicalName string) TableSpec {
	spec, ok := specsByName[logicalName]
	if !ok {
		panic("mission storage table is not declared: " + logicalName)
	}
	return spec
}

func Put(ctx context.Context, tx uow.Tx, table TableSpec, key string, record db.Record) error {
	if err := validateAccess(ctx, tx, table, key); err != nil {
		return err
	}
	for _, field := range table.KeyFields {
		if _, ok := record[field]; !ok {
			return fmt.Errorf("mission storage table %s record missing key field %s", table.LogicalName, field)
		}
	}
	tx.Put(table.LogicalName, key, record)
	return nil
}

func Get(ctx context.Context, tx uow.Tx, table TableSpec, key string) (db.Record, bool, error) {
	if err := validateAccess(ctx, tx, table, key); err != nil {
		return nil, false, err
	}
	record, ok := tx.Get(table.LogicalName, key)
	return record, ok, nil
}

func Snapshot(ctx context.Context, tx uow.Tx, table TableSpec) ([]db.Record, error) {
	if err := validateTable(ctx, tx, table); err != nil {
		return nil, err
	}
	raw, err := uow.ListRecords(ctx, tx, table.LogicalName)
	if err != nil {
		return nil, err
	}
	out := make([]db.Record, 0, len(raw))
	for _, record := range raw {
		out = append(out, record)
	}
	return out, nil
}

func validateAccess(ctx context.Context, tx uow.Tx, table TableSpec, key string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("mission storage table %s key is required", table.LogicalName)
	}
	return validateTable(ctx, tx, table)
}

func validateTable(ctx context.Context, tx uow.Tx, table TableSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tx == nil {
		return fmt.Errorf("mission storage transaction is required")
	}
	declared, ok := specsByName[table.LogicalName]
	if !ok {
		return fmt.Errorf("mission storage table %s is not declared", table.LogicalName)
	}
	if declared.Role != table.Role {
		return fmt.Errorf("mission storage table %s role mismatch: %s != %s", table.LogicalName, table.Role, declared.Role)
	}
	return nil
}

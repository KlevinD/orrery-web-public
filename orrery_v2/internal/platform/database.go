package platform

import (
	"context"
	"database/sql"
	"fmt"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

const (
	DatabaseRoleCore          = db.RoleCore
	DatabaseRoleAudit         = db.RoleAudit
	DatabaseRoleEvents        = db.RoleEvents
	DatabaseRoleJobs          = db.RoleJobs
	DatabaseRoleRuntime       = db.RoleRuntime
	DatabaseRoleObservability = db.RoleObservability
)

type Database struct {
	store *db.Store
}

type DatabaseRow struct {
	row *sql.Row
}

type Record = db.Record

type DBRouter = db.Router

func (d Database) Exec(ctx context.Context, query string, args ...any) error {
	if d.store == nil {
		return fmt.Errorf("platform database is not configured")
	}
	_, err := d.store.Exec(ctx, query, args...)
	return err
}

func (d Database) QueryRow(ctx context.Context, query string, args ...any) DatabaseRow {
	if d.store == nil {
		return DatabaseRow{}
	}
	return DatabaseRow{row: d.store.QueryRow(ctx, query, args...)}
}

func (r DatabaseRow) Scan(dest ...any) error {
	if r.row == nil {
		return fmt.Errorf("platform database row is not configured")
	}
	return r.row.Scan(dest...)
}

func (d Database) TableExists(ctx context.Context, table string) (bool, error) {
	if d.store == nil {
		return false, fmt.Errorf("platform database is not configured")
	}
	return d.store.TableExists(ctx, table)
}

func sqlMigration(version, description string, statements []string) migration.Migration {
	return migration.Migration{
		Version:     version,
		Description: description,
		Statements:  append([]string(nil), statements...),
	}
}

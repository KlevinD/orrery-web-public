package actioncontract

import (
	"context"
	"database/sql"
	"orrery_v2/internal/platform/db"
)

type repositoryDatabase interface {
	Exec(context.Context, string, ...any) (sql.Result, error)
	Query(context.Context, string, ...any) (*sql.Rows, error)
	QueryRow(context.Context, string, ...any) *sql.Row
}
type transactionDatabase struct{ tx db.Tx }

func (d transactionDatabase) Exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return d.tx.ExecContext(ctx, q, args...)
}
func (d transactionDatabase) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return d.tx.QueryContext(ctx, q, args...)
}
func (d transactionDatabase) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return d.tx.QueryRowContext(ctx, q, args...)
}

// NewPlatformRepositoryTx keeps a domain write and its action receipt atomic.
func NewPlatformRepositoryTx(tx db.Tx) *PlatformRepository {
	if tx == nil {
		return &PlatformRepository{}
	}
	return &PlatformRepository{store: transactionDatabase{tx: tx}}
}

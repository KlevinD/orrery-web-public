package uow

import (
	"context"
	"fmt"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

type RoutedTx interface {
	PutRecord(ctx context.Context, logicalTable, key string, record db.Record) error
	GetRecord(ctx context.Context, logicalTable, key string) (db.Record, bool, error)
}

type RoutedExplicitTx interface {
	RoutedTx
	Commit(context.Context) error
	Rollback(context.Context) error
}

type RoutedUnitOfWork struct {
	router *db.Router
	logger logging.Logger
}

type routedOp struct {
	table  string
	key    string
	record db.Record
}

type routedTx struct {
	router *db.Router
	store  *db.Store
	role   string
	ops    []routedOp
	err    error
}

type routedExplicitTx struct {
	unit *RoutedUnitOfWork
	tx   *routedTx
	done bool
}

func NewRouted(router *db.Router) *RoutedUnitOfWork {
	return &RoutedUnitOfWork{router: router}
}

func (u *RoutedUnitOfWork) WithLogger(logger logging.Logger) *RoutedUnitOfWork {
	u.logger = logger
	return u
}

func (u *RoutedUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx RoutedTx) error) error {
	if u == nil || u.router == nil {
		return fmt.Errorf("routed unit of work requires a database router")
	}
	if fn == nil {
		return fmt.Errorf("transaction function is required")
	}
	tx := &routedTx{router: u.router}
	callbackErr := fn(ctx, tx)
	if callbackErr != nil {
		u.logError(ctx, "transaction_rollback", callbackErr)
		return callbackErr
	}
	if tx.err != nil {
		u.logError(ctx, "transaction_rollback", tx.err)
		return tx.err
	}
	if tx.store == nil {
		return nil
	}
	err := tx.store.WithTx(ctx, func(sqlTx db.Tx) error {
		for _, op := range tx.ops {
			if err := putSQLRecord(ctx, sqlTx, op.table, op.key, op.record); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		u.logError(ctx, "transaction_commit_failed", err)
		return fmt.Errorf("commit routed transaction: %w", err)
	}
	return nil
}

func (u *RoutedUnitOfWork) Begin(ctx context.Context) (RoutedExplicitTx, error) {
	if u == nil || u.router == nil {
		return nil, fmt.Errorf("routed unit of work requires a database router")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &routedExplicitTx{
		unit: u,
		tx:   &routedTx{router: u.router},
	}, nil
}

func (u *RoutedUnitOfWork) logError(ctx context.Context, code string, err error) {
	if u.logger == nil {
		return
	}
	u.logger.Error(ctx, "routed unit of work failed",
		logging.String("operation", "uow.routed.do"),
		logging.String("error_code", code),
		logging.String("error", err.Error()),
	)
}

func (tx *routedExplicitTx) PutRecord(ctx context.Context, logicalTable, key string, record db.Record) error {
	if err := tx.checkOpen(); err != nil {
		return err
	}
	return tx.tx.PutRecord(ctx, logicalTable, key, record)
}

func (tx *routedExplicitTx) GetRecord(ctx context.Context, logicalTable, key string) (db.Record, bool, error) {
	if err := tx.checkOpen(); err != nil {
		return nil, false, err
	}
	return tx.tx.GetRecord(ctx, logicalTable, key)
}

func (tx *routedExplicitTx) Commit(ctx context.Context) error {
	if err := tx.checkOpen(); err != nil {
		return err
	}
	tx.done = true
	if err := ctx.Err(); err != nil {
		tx.unit.logError(ctx, "transaction_commit_failed", err)
		return fmt.Errorf("commit routed transaction: %w", err)
	}
	if tx.tx.err != nil {
		tx.unit.logError(ctx, "transaction_rollback", tx.tx.err)
		return tx.tx.err
	}
	if tx.tx.store == nil {
		return nil
	}
	err := tx.tx.store.WithTx(ctx, func(sqlTx db.Tx) error {
		for _, op := range tx.tx.ops {
			if err := putSQLRecord(ctx, sqlTx, op.table, op.key, op.record); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		tx.unit.logError(ctx, "transaction_commit_failed", err)
		return fmt.Errorf("commit routed transaction: %w", err)
	}
	return nil
}

func (tx *routedExplicitTx) Rollback(ctx context.Context) error {
	if err := tx.checkOpen(); err != nil {
		return err
	}
	tx.done = true
	return ctx.Err()
}

func (tx *routedExplicitTx) checkOpen() error {
	if tx == nil || tx.tx == nil || tx.unit == nil {
		return fmt.Errorf("routed unit of work transaction is not configured")
	}
	if tx.done {
		return fmt.Errorf("routed unit of work transaction is already closed")
	}
	return nil
}

func (tx *routedTx) PutRecord(ctx context.Context, logicalTable, key string, record db.Record) error {
	if tx.err != nil {
		return tx.err
	}
	store, table, err := tx.router.StoreForTable(logicalTable)
	if err != nil {
		tx.err = err
		return err
	}
	if err := tx.bindStore(logicalTable, store); err != nil {
		tx.err = err
		return err
	}
	tx.ops = append(tx.ops, routedOp{table: table, key: key, record: cloneRecord(record)})
	return nil
}

func (tx *routedTx) GetRecord(ctx context.Context, logicalTable, key string) (db.Record, bool, error) {
	if tx.err != nil {
		return nil, false, tx.err
	}
	store, table, err := tx.router.StoreForTable(logicalTable)
	if err != nil {
		tx.err = err
		return nil, false, err
	}
	if err := tx.bindStore(logicalTable, store); err != nil {
		tx.err = err
		return nil, false, err
	}
	for i := len(tx.ops) - 1; i >= 0; i-- {
		op := tx.ops[i]
		if op.table == table && op.key == key {
			return cloneRecord(op.record), true, nil
		}
	}
	return store.GetRecord(ctx, table, key)
}

func (tx *routedTx) bindStore(logicalTable string, store *db.Store) error {
	if tx.store == nil {
		tx.store = store
		tx.role = store.Name()
		return nil
	}
	if tx.store != store {
		return fmt.Errorf("cross-database transaction is not supported for logical table %q: already using %q", logicalTable, tx.role)
	}
	return nil
}

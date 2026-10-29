package responsibility

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"orrery_v2/internal/platform/db"
)

const runtimeWakeTableName = "responsibility_runtime_wakeups"

type runtimeCycleKey struct{}

// RuntimeCycleContext prevents the coordinator's own bookkeeping from creating
// an endless stream of wakeups. External commits still publish durable events.
func RuntimeCycleContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, runtimeCycleKey{}, true)
}

type RuntimeWake struct {
	Table string `json:"table"`
	Key   string `json:"key"`
	Token string `json:"token"`
}

func (w RuntimeWake) Validate() error {
	if w.Key == "" || w.Token == "" || !runtimeWakeTable(w.Table) {
		return fmt.Errorf("%w: invalid responsibility runtime wakeup", ErrInvalidContract)
	}
	return nil
}

func runtimeWakeTable(table string) bool {
	switch table {
	case assignmentTable, activationTable, runtimeOutcomeTable, policyTable, peoplePortfolioTable, blockerTable, autoplanTable:
		return true
	}
	return false
}

func publishRuntimeWake(ctx context.Context, tx db.Tx, table, key string) error {
	if ctx.Value(runtimeCycleKey{}) == true || !runtimeWakeTable(table) {
		return nil
	}
	body, err := json.Marshal(RuntimeWake{Table: table, Key: key, Token: uuid.NewString()})
	if err != nil {
		return err
	}
	return db.PutRecordTx(ctx, tx, runtimeWakeTableName, stableHex(table, key), db.Record{"payload": string(body)})
}

// PendingRuntimeWakes coalesces changes by record. It uses the responsibility
// database, so raw SQL transactions and UnitOfWork transactions are both atomic.
func (r *Repository) PendingRuntimeWakes(ctx context.Context) ([]RuntimeWake, error) {
	page, err := r.router.ListRecords(ctx, "core."+runtimeWakeTableName, "", 1000)
	if err != nil {
		return nil, err
	}
	wakes := make([]RuntimeWake, 0, len(page.Items))
	for _, item := range page.Items {
		var wake RuntimeWake
		if err := decodeRecord(item.Record, &wake); err != nil {
			return nil, err
		}
		if err := wake.Validate(); err != nil {
			return nil, err
		}
		wakes = append(wakes, wake)
	}
	return wakes, nil
}

// AcknowledgeRuntimeWakes only removes the versions observed before a successful
// cycle. Concurrent external commits remain pending for the next cycle.
func (r *Repository) AcknowledgeRuntimeWakes(ctx context.Context, wakes []RuntimeWake) error {
	for _, wake := range wakes {
		body, err := json.Marshal(wake)
		if err != nil {
			return err
		}
		if _, err := r.router.CompareAndDeleteRecord(ctx, "core."+runtimeWakeTableName, stableHex(wake.Table, wake.Key), db.Record{"payload": string(body)}); err != nil {
			return err
		}
	}
	return nil
}

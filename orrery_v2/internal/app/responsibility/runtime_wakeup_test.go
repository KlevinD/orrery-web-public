package responsibility

import (
	"context"
	"errors"
	"os"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestRuntimeWakeCommitRollbackCoalescingAndConcurrentAcknowledgement(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter, env.UnitOfWork)
	ctx := context.Background()
	write := func(ctx context.Context, value string, abort bool) error {
		return env.DB.WithTx(ctx, func(tx db.Tx) error {
			if err := putRecordTx(ctx, tx, assignmentTable, "assignment-1", db.Record{"value": value}); err != nil {
				return err
			}
			if abort {
				return errors.New("abort")
			}
			return nil
		})
	}
	if err := write(ctx, "rollback", true); err == nil {
		t.Fatal("expected rollback")
	}
	wakes, err := repo.PendingRuntimeWakes(ctx)
	if err != nil || len(wakes) != 0 {
		t.Fatalf("rolled back wakes=%v err=%v", wakes, err)
	}
	if err := write(ctx, "v1", false); err != nil {
		t.Fatal(err)
	}
	first, err := repo.PendingRuntimeWakes(ctx)
	if err != nil || len(first) != 1 {
		t.Fatalf("first=%v err=%v", first, err)
	}
	if err := write(ctx, "v2", false); err != nil {
		t.Fatal(err)
	}
	if err := repo.AcknowledgeRuntimeWakes(ctx, first); err != nil {
		t.Fatal(err)
	}
	second, err := repo.PendingRuntimeWakes(ctx)
	if err != nil || len(second) != 1 || second[0].Token == first[0].Token {
		t.Fatalf("concurrent wake lost: %v err=%v", second, err)
	}
	if err := repo.AcknowledgeRuntimeWakes(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := write(RuntimeCycleContext(ctx), "own-bookkeeping", false); err != nil {
		t.Fatal(err)
	}
	wakes, err = repo.PendingRuntimeWakes(ctx)
	if err != nil || len(wakes) != 0 {
		t.Fatalf("self-triggered wakes=%v err=%v", wakes, err)
	}
	if err := write(ctx, "external-after-cycle", false); err != nil {
		t.Fatal(err)
	}
	wakes, err = repo.PendingRuntimeWakes(ctx)
	if err != nil || len(wakes) != 1 {
		t.Fatalf("external wake missing: %v err=%v", wakes, err)
	}
}

func TestRuntimeWakeIgnoresAttemptHeartbeatsAndAuditWrites(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter, env.UnitOfWork)
	ctx := context.Background()
	for _, table := range []string{attemptTable, auditTable} {
		if err := env.DB.WithTx(ctx, func(tx db.Tx) error { return putRecordTx(ctx, tx, table, "test", db.Record{"v": 1}) }); err != nil {
			t.Fatal(err)
		}
	}
	wakes, err := repo.PendingRuntimeWakes(ctx)
	if err != nil || len(wakes) != 0 {
		t.Fatalf("irrelevant wakes=%v err=%v", wakes, err)
	}
}

func TestPostgresRuntimeWakeConcurrentAcknowledgement(t *testing.T) {
	rawURL := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if rawURL == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{db.RoleCore: {Driver: db.DriverPostgres, URL: rawURL, Name: "runtime-wake-test"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	store, err := registry.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db.NewRouter(registry), uow.New(store))
	write := func(value string) {
		t.Helper()
		if err := store.WithTx(ctx, func(tx db.Tx) error { return putRecordTx(ctx, tx, policyTable, "postgres-wake", db.Record{"v": value}) }); err != nil {
			t.Fatal(err)
		}
	}
	pending := func() []RuntimeWake {
		t.Helper()
		all, err := repo.PendingRuntimeWakes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var result []RuntimeWake
		for _, wake := range all {
			if wake.Key == "postgres-wake" {
				result = append(result, wake)
			}
		}
		return result
	}
	write("v1")
	first := pending()
	if len(first) != 1 {
		t.Fatalf("first=%v", first)
	}
	write("v2")
	if err := repo.AcknowledgeRuntimeWakes(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := pending()
	if len(second) != 1 || second[0].Token == first[0].Token {
		t.Fatalf("new version lost: %v", second)
	}
	if err := repo.AcknowledgeRuntimeWakes(ctx, second); err != nil {
		t.Fatal(err)
	}
	if left := pending(); len(left) != 0 {
		t.Fatalf("acknowledged=%v", left)
	}
}

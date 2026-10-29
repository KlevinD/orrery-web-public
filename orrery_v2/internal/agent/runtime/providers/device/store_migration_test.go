package device

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestDeviceSchemaStatementsRenderForSQLiteAndPostgres(t *testing.T) {
	for _, dialect := range []db.Dialect{db.DialectSQLite, db.DialectPostgres} {
		statements := deviceSchemaStatements(dialect)
		if len(statements) == 0 {
			t.Fatalf("%s Device schema is empty", dialect)
		}
		joined := strings.Join(statements, "\n")
		if strings.Contains(joined, "%!") || strings.Contains(joined, "%[") {
			t.Fatalf("%s Device schema contains unresolved formatting: %s", dialect, joined)
		}
		if dialect == db.DialectPostgres && (!strings.Contains(joined, "BYTEA") || !strings.Contains(joined, "BIGINT")) {
			t.Fatalf("Postgres Device schema did not use Postgres storage types: %s", joined)
		}
		if dialect == db.DialectSQLite && (!strings.Contains(joined, "BLOB") || !strings.Contains(joined, "INTEGER")) {
			t.Fatalf("SQLite Device schema did not use SQLite storage types: %s", joined)
		}
	}
}

func TestDeviceMigrationCreatesDurableSchemaAndSupportsRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.db")
	openRegistry := func() *db.Registry {
		registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
			db.RoleRuntime: {Driver: db.DriverFile, URL: "file://" + path},
		}})
		if err != nil {
			t.Fatalf("open runtime registry: %v", err)
		}
		return registry
	}

	registry := openRegistry()
	coordinator := migration.NewCoordinator(registry)
	if err := RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register Device migration: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleRuntime); err != nil {
		t.Fatalf("apply Device migration: %v", err)
	}
	store, err := registry.Store(db.RoleRuntime)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"runtime_device_work_orders",
		"runtime_device_event_cursors",
		"runtime_device_work_order_events",
		"runtime_device_inbound_messages",
		"runtime_device_outbox_cursors",
		"runtime_device_outbox",
		"runtime_device_leases",
	} {
		exists, err := store.TableExists(ctx, table)
		if err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	var version string
	if err := store.QueryRow(ctx, `SELECT version FROM schema_migrations WHERE version = ?`, "0190_runtime_device_bridge").Scan(&version); err != nil {
		t.Fatalf("Device migration record: %v", err)
	}
	repository, err := NewSQLRepository(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	order := testWorkOrder(now)
	fingerprint, err := workOrderFingerprint(order)
	if err != nil {
		t.Fatal(err)
	}
	record := WorkOrderRecord{TenantID: "tenant-a", DeviceID: "device_test_001", SessionID: "session-a", Order: order, Fingerprint: fingerprint, State: WorkOrderOffered, CreatedAt: now, UpdatedAt: now}
	outbound := queuedOutbound{MessageID: "msg_offer_restart_001", Type: MessageWorkOrderOffer, WorkOrderID: order.WorkOrderID, SentAt: now, Payload: order}
	if _, created, err := repository.CreateOffer(ctx, record, outbound); err != nil || !created {
		t.Fatalf("create durable offer = %v, error = %v", created, err)
	}
	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := openRegistry()
	defer restarted.Close()
	restartedStore, err := restarted.Store(db.RoleRuntime)
	if err != nil {
		t.Fatal(err)
	}
	restartedRepository, err := NewSQLRepository(ctx, restartedStore)
	if err != nil {
		t.Fatal(err)
	}
	loaded, found, err := restartedRepository.GetWorkOrder(ctx, "tenant-a", "device_test_001", order.WorkOrderID)
	if err != nil || !found || loaded.Fingerprint != fingerprint {
		t.Fatalf("restarted work order = %#v, found = %v, error = %v", loaded, found, err)
	}
	pending, err := restartedRepository.ListOutbound(ctx, "tenant-a", "device_test_001")
	if err != nil || len(pending) != 1 || pending[0].MessageID != outbound.MessageID {
		t.Fatalf("restarted outbox = %#v, error = %v", pending, err)
	}
}

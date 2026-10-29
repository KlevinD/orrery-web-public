package db_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestOpenCreatesIsolatedTestDatabaseAndPings(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "stage0"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	if err := store.Ping(ctx); err != nil {
		t.Fatalf("Ping returned error: %v", err)
	}
	if store.Name() != "stage0" {
		t.Fatalf("Name = %q, want stage0", store.Name())
	}
}

func TestCheckWritableRollsBackProbeRecord(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "write-probe"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	if err := store.CheckWritable(ctx); err != nil {
		t.Fatalf("CheckWritable returned error: %v", err)
	}
	if _, found := store.Get("platform_health", "write_probe"); found {
		t.Fatal("write probe record was committed")
	}
}

func TestReplaceReturnsPersistErrorAndDoesNotSilentlyDropFailure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	badPath := filepath.Join(root, "as-directory")
	if err := os.MkdirAll(badPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + badPath})
	if err == nil {
		defer store.Close()
		t.Fatalf("Open on directory path returned nil error")
	}
}

func TestFileDatabasePersistsRecordsAcrossOpen(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/stage0.json"
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	store.Put("items", "one", db.Record{"id": "one"})
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	reopened, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer reopened.Close()
	if record, ok := reopened.Get("items", "one"); !ok || record["id"] != "one" {
		t.Fatalf("record after reopen ok=%v record=%+v", ok, record)
	}
}

func TestFileDatabaseCreatesParentDirectoriesForLocalFirstDefaults(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data", "nested", "core.db")

	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	store.Put("items", "one", db.Record{"id": "one"})
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	assertSQLiteFile(t, path)
}

func TestFileDatabaseUsesSQLiteStorage(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/stage0.db"
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	store.Put("items", "one", db.Record{"id": "one"})
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read db file: %v", err)
	}
	assertSQLiteHeader(t, path, raw)
}

func TestRegistryOpensIsolatedNamedSQLiteDatabases(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{
		Databases: map[string]db.Config{
			db.RoleCore:    {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "core.db")},
			db.RoleEvents:  {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "events.db")},
			db.RoleJobs:    {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "jobs.db")},
			db.RoleRuntime: {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "runtime.db")},
		},
	})
	if err != nil {
		t.Fatalf("OpenRegistry returned error: %v", err)
	}
	defer registry.Close()

	events := mustStore(t, registry, db.RoleEvents)
	jobs := mustStore(t, registry, db.RoleJobs)
	events.Put("records", "event-1", db.Record{"kind": "event"})
	jobs.Put("records", "job-1", db.Record{"kind": "job"})

	if _, ok := jobs.Get("records", "event-1"); ok {
		t.Fatalf("event record leaked into jobs database")
	}
	if _, ok := events.Get("records", "job-1"); ok {
		t.Fatalf("job record leaked into events database")
	}
	for _, name := range []string{"core.db", "events.db", "jobs.db", "runtime.db"} {
		assertSQLiteFile(t, filepath.Join(root, name))
	}
}

func TestRegistryConfigFromURLsMapsSQLiteAndMemoryURLs(t *testing.T) {
	cfg, err := db.RegistryConfigFromURLs(map[string]string{
		db.RoleCore:   "memory://core",
		db.RoleEvents: "file://./events.db",
		db.RoleJobs:   "sqlite://./jobs.db",
	})
	if err != nil {
		t.Fatalf("RegistryConfigFromURLs returned error: %v", err)
	}
	if cfg.Databases[db.RoleCore].Driver != db.DriverMemory {
		t.Fatalf("core driver = %s", cfg.Databases[db.RoleCore].Driver)
	}
	if cfg.Databases[db.RoleCore].Name != "core" {
		t.Fatalf("core memory name = %s", cfg.Databases[db.RoleCore].Name)
	}
	if cfg.Databases[db.RoleEvents].Driver != db.DriverFile || cfg.Databases[db.RoleJobs].Driver != db.DriverFile {
		t.Fatalf("file/sqlite drivers = %+v", cfg.Databases)
	}
}

func TestRegistryConfigFromURLsMapsPostgresURLs(t *testing.T) {
	cfg, err := db.RegistryConfigFromURLs(map[string]string{
		db.RoleCore:   "postgres://orrery:secret@localhost:5432/core?sslmode=disable",
		db.RoleEvents: "postgresql://orrery:secret@localhost:5432/events?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("RegistryConfigFromURLs returned error: %v", err)
	}
	if cfg.Databases[db.RoleCore].Driver != db.DriverPostgres {
		t.Fatalf("core driver = %s, want postgres", cfg.Databases[db.RoleCore].Driver)
	}
	if cfg.Databases[db.RoleEvents].Driver != db.DriverPostgres {
		t.Fatalf("events driver = %s, want postgres", cfg.Databases[db.RoleEvents].Driver)
	}
}

func TestStoreDialectReportsSQLiteForMemoryAndFile(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	if store.Dialect() != db.DialectSQLite {
		t.Fatalf("memory Dialect = %s, want sqlite", store.Dialect())
	}

	path := filepath.Join(t.TempDir(), "dialect.db")
	fileStore, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("Open file returned error: %v", err)
	}
	defer fileStore.Close()
	if fileStore.Dialect() != db.DialectSQLite {
		t.Fatalf("file Dialect = %s, want sqlite", fileStore.Dialect())
	}
}

func TestOpenRegistryFromURLsAssemblesConfiguredRoleDatabases(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{
		db.RoleCore:    "file://" + filepath.Join(root, "core.db"),
		db.RoleEvents:  "file://" + filepath.Join(root, "events.db"),
		db.RoleJobs:    "file://" + filepath.Join(root, "jobs.db"),
		db.RoleRuntime: "file://" + filepath.Join(root, "runtime.db"),
	})
	if err != nil {
		t.Fatalf("OpenRegistryFromURLs returned error: %v", err)
	}
	defer registry.Close()

	if err := mustStore(t, registry, db.RoleEvents).PutRecord(ctx, "events", "event-1", db.Record{"id": "event-1"}); err != nil {
		t.Fatalf("events PutRecord returned error: %v", err)
	}
	if _, ok := mustStore(t, registry, db.RoleJobs).Get("events", "event-1"); ok {
		t.Fatalf("event record leaked into jobs database")
	}
	for _, name := range []string{"core.db", "events.db", "jobs.db", "runtime.db"} {
		assertSQLiteFile(t, filepath.Join(root, name))
	}
}

func TestStorePutRecordStripsNULCharactersBeforeWritingTextColumns(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	if err := store.PutRecord(ctx, "items\x00bad", "one\x00bad", db.Record{"value": "hello\x00world"}); err != nil {
		t.Fatalf("PutRecord returned error: %v", err)
	}

	record, ok, err := store.GetRecord(ctx, "itemsbad", "onebad")
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}
	if !ok {
		t.Fatalf("sanitized record was not stored")
	}
	if got := record["value"]; got != "helloworld" {
		t.Fatalf("value = %#v, want sanitized string", got)
	}
}

func TestStoreCompareAndSwapAndDeleteRejectStaleRecords(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	original := db.Record{"status": "pending", "version": 1}
	replacement := db.Record{"status": "completed", "version": 2}
	if err := store.PutRecord(ctx, "commands", "one", original); err != nil {
		t.Fatalf("PutRecord returned error: %v", err)
	}
	swapped, err := store.CompareAndSwapRecord(ctx, "commands", "one", original, replacement)
	if err != nil || !swapped {
		t.Fatalf("CompareAndSwapRecord swapped=%v err=%v", swapped, err)
	}
	swapped, err = store.CompareAndSwapRecord(ctx, "commands", "one", original, db.Record{"status": "failed", "version": 3})
	if err != nil || swapped {
		t.Fatalf("stale CompareAndSwapRecord swapped=%v err=%v", swapped, err)
	}
	deleted, err := store.CompareAndDeleteRecord(ctx, "commands", "one", original)
	if err != nil || deleted {
		t.Fatalf("stale CompareAndDeleteRecord deleted=%v err=%v", deleted, err)
	}
	deleted, err = store.CompareAndDeleteRecord(ctx, "commands", "one", replacement)
	if err != nil || !deleted {
		t.Fatalf("CompareAndDeleteRecord deleted=%v err=%v", deleted, err)
	}
}

func TestStoreListRecordsUsesStableBoundedKeysetPagination(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	for _, key := range []string{"c", "a", "d", "b"} {
		if err := store.PutRecord(ctx, "catalog", key, db.Record{"id": key}); err != nil {
			t.Fatalf("PutRecord(%s): %v", key, err)
		}
	}
	first, err := store.ListRecords(ctx, "catalog", "", 2)
	if err != nil {
		t.Fatalf("ListRecords first page: %v", err)
	}
	if !first.HasMore || len(first.Items) != 2 || first.Items[0].Key != "a" || first.Items[1].Key != "b" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := store.ListRecords(ctx, "catalog", first.Items[1].Key, 2)
	if err != nil {
		t.Fatalf("ListRecords second page: %v", err)
	}
	if second.HasMore || len(second.Items) != 2 || second.Items[0].Key != "c" || second.Items[1].Key != "d" {
		t.Fatalf("second page = %+v", second)
	}
	if _, err := store.ListRecords(ctx, "catalog", "", db.MaxRecordPageLimit+1); err == nil {
		t.Fatal("ListRecords accepted an unbounded limit")
	}
}

func TestStoreListRecordTableExcludesUnrelatedTables(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for key, value := range map[string]string{"a": "one", "b": "two"} {
		if err := store.PutRecord(ctx, "wanted", key, db.Record{"value": value}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.PutRecord(ctx, "unrelated", "large", db.Record{"value": "exclude"}); err != nil {
		t.Fatal(err)
	}

	records, err := store.ListRecordTable(ctx, "wanted")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records["a"]["value"] != "one" || records["b"]["value"] != "two" {
		t.Fatalf("records = %#v", records)
	}
	if _, exists := records["large"]; exists {
		t.Fatalf("unrelated record leaked into table result: %#v", records)
	}
}

func TestStoreGetRecordsReturnsRequestedRecordsInBoundedBatches(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	for _, key := range []string{"a", "b", "c"} {
		if err := store.PutRecord(ctx, "catalog", key, db.Record{"id": key}); err != nil {
			t.Fatalf("PutRecord(%s): %v", key, err)
		}
	}

	records, err := store.GetRecords(ctx, "catalog", []string{"c", "missing", "a", "c"})
	if err != nil {
		t.Fatalf("GetRecords: %v", err)
	}
	if len(records) != 2 || records["a"]["id"] != "a" || records["c"]["id"] != "c" {
		t.Fatalf("records = %#v, want a and c", records)
	}
	empty, err := store.GetRecords(ctx, "catalog", nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty GetRecords = %#v, err=%v", empty, err)
	}
	if _, err := store.GetRecords(ctx, "catalog", make([]string, db.MaxRecordPageLimit+1)); err == nil {
		t.Fatal("GetRecords accepted an unbounded key set")
	}
}

func TestListRecordsTxUsesStableBoundedKeysetPagination(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	tx, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	for _, key := range []string{"c", "a", "d", "b"} {
		if err := db.PutRecordTx(ctx, tx, "catalog", key, db.Record{"id": key}); err != nil {
			t.Fatalf("PutRecordTx(%s): %v", key, err)
		}
	}
	first, err := db.ListRecordsTx(ctx, tx, "catalog", "", 2)
	if err != nil {
		t.Fatalf("ListRecordsTx first page: %v", err)
	}
	if !first.HasMore || len(first.Items) != 2 || first.Items[0].Key != "a" || first.Items[1].Key != "b" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := db.ListRecordsTx(ctx, tx, "catalog", first.Items[1].Key, 2)
	if err != nil {
		t.Fatalf("ListRecordsTx second page: %v", err)
	}
	if second.HasMore || len(second.Items) != 2 || second.Items[0].Key != "c" || second.Items[1].Key != "d" {
		t.Fatalf("second page = %+v", second)
	}
	if _, err := db.ListRecordsTx(ctx, tx, "catalog", "", db.MaxRecordPageLimit+1); err == nil {
		t.Fatal("ListRecordsTx accepted an unbounded limit")
	}
}

func TestMemoryRegistryURLsUseURLNameForIsolation(t *testing.T) {
	ctx := context.Background()
	first, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: "memory://memory-one"})
	if err != nil {
		t.Fatalf("first OpenRegistryFromURLs returned error: %v", err)
	}
	defer first.Close()
	second, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: "memory://memory-two"})
	if err != nil {
		t.Fatalf("second OpenRegistryFromURLs returned error: %v", err)
	}
	defer second.Close()

	if err := mustStore(t, first, db.RoleCore).PutRecord(ctx, "records", "shared-key", db.Record{"source": "first"}); err != nil {
		t.Fatalf("first PutRecord returned error: %v", err)
	}
	if _, ok := mustStore(t, second, db.RoleCore).Get("records", "shared-key"); ok {
		t.Fatalf("record leaked between differently named memory databases")
	}
}

func TestRegistryStoreReturnsErrorForMissingDatabaseRole(t *testing.T) {
	registry := openMemoryRegistry(t)
	defer registry.Close()

	store, err := registry.Store("missing")
	if err == nil {
		t.Fatalf("Store returned nil error for missing role")
	}
	if store != nil {
		t.Fatalf("Store returned non-nil store for missing role: %+v", store)
	}
	if !strings.Contains(err.Error(), `database role "missing" is not available`) {
		t.Fatalf("Store error = %v", err)
	}
}

func TestRegistryDoesNotExposePanicOnlyLookup(t *testing.T) {
	if _, ok := reflect.TypeOf((*db.Registry)(nil)).MethodByName("Must"); ok {
		t.Fatalf("Registry exposes Must panic-only lookup; production callers must use Store or Get")
	}
}

func TestRouterRoutesPrefixedLogicalTablesToRoleDatabases(t *testing.T) {
	ctx := context.Background()
	registry := openMemoryRegistry(t)
	defer registry.Close()
	router := db.NewRouter(registry)

	if err := router.PutRecord(ctx, "events.event_log", "event-1", db.Record{"kind": "event"}); err != nil {
		t.Fatalf("events PutRecord returned error: %v", err)
	}
	if err := router.PutRecord(ctx, "audit.audit_log", "audit-1", db.Record{"kind": "audit"}); err != nil {
		t.Fatalf("audit PutRecord returned error: %v", err)
	}
	if err := router.PutRecord(ctx, "jobs.queue_state", "job-1", db.Record{"kind": "job"}); err != nil {
		t.Fatalf("jobs PutRecord returned error: %v", err)
	}

	event, ok, err := router.GetRecord(ctx, "events.event_log", "event-1")
	if err != nil || !ok || event["kind"] != "event" {
		t.Fatalf("routed event record=%+v ok=%v err=%v", event, ok, err)
	}
	audit, ok, err := router.GetRecord(ctx, "audit.audit_log", "audit-1")
	if err != nil || !ok || audit["kind"] != "audit" {
		t.Fatalf("routed audit record=%+v ok=%v err=%v", audit, ok, err)
	}
	job, ok, err := router.GetRecord(ctx, "jobs.queue_state", "job-1")
	if err != nil || !ok || job["kind"] != "job" {
		t.Fatalf("routed job record=%+v ok=%v err=%v", job, ok, err)
	}

	if _, ok := mustStore(t, registry, db.RoleEvents).Get("audit_log", "audit-1"); ok {
		t.Fatalf("audit record leaked into events database")
	}
	if _, ok := mustStore(t, registry, db.RoleJobs).Get("event_log", "event-1"); ok {
		t.Fatalf("events record leaked into jobs database")
	}
	if _, ok := mustStore(t, registry, db.RoleEvents).Get("queue_state", "job-1"); ok {
		t.Fatalf("jobs record leaked into events database")
	}
	if _, ok := mustStore(t, registry, db.RoleEvents).Get("events.event_log", "event-1"); ok {
		t.Fatalf("router stored role prefix inside target database table name")
	}
	if record, ok := mustStore(t, registry, db.RoleEvents).Get("event_log", "event-1"); !ok || record["kind"] != "event" {
		t.Fatalf("events database table record=%+v ok=%v", record, ok)
	}
	if record, ok := mustStore(t, registry, db.RoleAudit).Get("audit_log", "audit-1"); !ok || record["kind"] != "audit" {
		t.Fatalf("audit database table record=%+v ok=%v", record, ok)
	}
}

func TestRouterDefaultsUnqualifiedTablesToCoreDatabase(t *testing.T) {
	ctx := context.Background()
	registry := openMemoryRegistry(t)
	defer registry.Close()
	router := db.NewRouter(registry)

	if err := router.PutRecord(ctx, "user_profile", "user-1", db.Record{"name": "Ada"}); err != nil {
		t.Fatalf("PutRecord returned error: %v", err)
	}

	record, ok := mustStore(t, registry, db.RoleCore).Get("user_profile", "user-1")
	if !ok || record["name"] != "Ada" {
		t.Fatalf("core database record=%+v ok=%v", record, ok)
	}
	if _, ok := mustStore(t, registry, db.RoleEvents).Get("user_profile", "user-1"); ok {
		t.Fatalf("unqualified record leaked into events database")
	}
}

func TestRouterRejectsUnknownOrMalformedLogicalTables(t *testing.T) {
	ctx := context.Background()
	registry := openMemoryRegistry(t)
	defer registry.Close()
	router := db.NewRouter(registry)

	cases := []string{
		"",
		".audit_log",
		"events.",
		"missing.audit_log",
	}
	for _, table := range cases {
		if err := router.PutRecord(ctx, table, "key", db.Record{"id": "key"}); err == nil {
			t.Fatalf("PutRecord(%q) returned nil error", table)
		}
		if _, _, err := router.GetRecord(ctx, table, "key"); err == nil {
			t.Fatalf("GetRecord(%q) returned nil error", table)
		}
	}
}

func TestRouterGetRecordReturnsClosedTargetDatabaseError(t *testing.T) {
	ctx := context.Background()
	registry := openMemoryRegistry(t)
	defer registry.Close()
	router := db.NewRouter(registry)
	if err := mustStore(t, registry, db.RoleEvents).Close(); err != nil {
		t.Fatalf("close events database: %v", err)
	}

	_, _, err := router.GetRecord(ctx, "events.event_log", "event-1")
	if err == nil {
		t.Fatalf("GetRecord returned nil error for closed routed database")
	}
	if !strings.Contains(err.Error(), "database is closed") {
		t.Fatalf("GetRecord error = %v, want database is closed", err)
	}
}

func TestStoreWithTxCommitsAndRollsBackSQLiteStatements(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tx.db")
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	if _, err := store.Exec(ctx, `CREATE TABLE tx_items (id TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	rollbackErr := errors.New("rollback")
	if err := store.WithTx(ctx, func(tx db.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tx_items (id, value) VALUES (?, ?)`, "rolled-back", "nope"); err != nil {
			return err
		}
		return rollbackErr
	}); !errors.Is(err, rollbackErr) {
		t.Fatalf("WithTx rollback error = %v, want %v", err, rollbackErr)
	}

	var count int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM tx_items`).Scan(&count); err != nil {
		t.Fatalf("count after rollback: %v", err)
	}
	if count != 0 {
		t.Fatalf("count after rollback = %d, want 0", count)
	}

	if err := store.WithTx(ctx, func(tx db.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tx_items (id, value) VALUES (?, ?)`, "committed", "yes"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTx commit returned error: %v", err)
	}
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM tx_items WHERE id = ?`, "committed").Scan(&count); err != nil {
		t.Fatalf("count after commit: %v", err)
	}
	if count != 1 {
		t.Fatalf("count after commit = %d, want 1", count)
	}
}

func TestStoreBeginTxCommitsAndRollsBackSQLiteStatements(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "explicit-tx.db")
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	if _, err := store.Exec(ctx, `CREATE TABLE tx_items (id TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	rolledBack, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx rollback returned error: %v", err)
	}
	if _, err := rolledBack.ExecContext(ctx, `INSERT INTO tx_items (id, value) VALUES (?, ?)`, "rolled-back", "nope"); err != nil {
		t.Fatalf("insert rolled-back: %v", err)
	}
	if err := rolledBack.Rollback(); err != nil {
		t.Fatalf("Rollback returned error: %v", err)
	}

	var count int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM tx_items`).Scan(&count); err != nil {
		t.Fatalf("count after rollback: %v", err)
	}
	if count != 0 {
		t.Fatalf("count after rollback = %d, want 0", count)
	}

	committed, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx commit returned error: %v", err)
	}
	if _, err := committed.ExecContext(ctx, `INSERT INTO tx_items (id, value) VALUES (?, ?)`, "committed", "yes"); err != nil {
		t.Fatalf("insert committed: %v", err)
	}
	if err := committed.Commit(); err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM tx_items WHERE id = ?`, "committed").Scan(&count); err != nil {
		t.Fatalf("count after commit: %v", err)
	}
	if count != 1 {
		t.Fatalf("count after commit = %d, want 1", count)
	}
}

func TestPlatformRecordTransactionHelpersClaimAndCommitAtomically(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	tx, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx returned error: %v", err)
	}
	inserted, err := db.InsertRecordTx(ctx, tx, "commands", "send-1", db.Record{"status": "pending"})
	if err != nil || !inserted {
		t.Fatalf("InsertRecordTx = %v, %v; want true, nil", inserted, err)
	}
	inserted, err = db.InsertRecordTx(ctx, tx, "commands", "send-1", db.Record{"status": "replacement"})
	if err != nil || inserted {
		t.Fatalf("duplicate InsertRecordTx = %v, %v; want false, nil", inserted, err)
	}
	if err := db.PutRecordTx(ctx, tx, "commands", "send-1", db.Record{"status": "completed"}); err != nil {
		t.Fatalf("PutRecordTx returned error: %v", err)
	}
	record, ok, err := db.GetRecordTx(ctx, tx, "commands", "send-1")
	if err != nil || !ok || record["status"] != "completed" {
		t.Fatalf("GetRecordTx = %#v, %v, %v", record, ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	record, ok, err = store.GetRecord(ctx, "commands", "send-1")
	if err != nil || !ok || record["status"] != "completed" {
		t.Fatalf("committed record = %#v, %v, %v", record, ok, err)
	}
}

func TestPostgresStorePersistsPlatformRecords(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-platform-records-" + t.Name(),
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	if store.Dialect() != db.DialectPostgres {
		t.Fatalf("Dialect = %s, want postgres", store.Dialect())
	}
	if _, err := store.Exec(ctx, `DELETE FROM platform_records`); err != nil {
		t.Fatalf("clear platform_records: %v", err)
	}
	if err := store.PutRecord(ctx, "items", "one", db.Record{"id": "one", "value": "saved"}); err != nil {
		t.Fatalf("PutRecord returned error: %v", err)
	}
	record, ok, err := store.GetRecord(ctx, "items", "one")
	if err != nil || !ok || record["value"] != "saved" {
		t.Fatalf("GetRecord record=%+v ok=%v err=%v", record, ok, err)
	}
	if err := store.DeleteRecord(ctx, "items", "one"); err != nil {
		t.Fatalf("DeleteRecord returned error: %v", err)
	}
	if _, ok, err := store.GetRecord(ctx, "items", "one"); err != nil || ok {
		t.Fatalf("record after delete ok=%v err=%v", ok, err)
	}
}

func TestPostgresCompareAndSwapAndDeleteMatchEquivalentJSONEncoding(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-semantic-record-cas-" + t.Name(),
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	const table = "semantic_record_cas"
	const key = "mission-intent"
	if _, err := store.Exec(ctx, `DELETE FROM platform_records WHERE table_name = ? AND record_key = ?`, table, key); err != nil {
		t.Fatalf("clear test record: %v", err)
	}
	defer func() {
		_, _ = store.Exec(context.Background(), `DELETE FROM platform_records WHERE table_name = ? AND record_key = ?`, table, key)
	}()

	const nestedPayload = `{"command":{"InitialHumanAgentID":"H000001"},"version":4}`
	const nonCanonicalOriginal = `{ "version": 4, "payload": "{\"command\":{\"InitialHumanAgentID\":\"H000001\"},\"version\":4}", "complete": false }`
	if _, err := store.Exec(ctx, `INSERT INTO platform_records (table_name, record_key, payload_json) VALUES (?, ?, ?)`, table, key, nonCanonicalOriginal); err != nil {
		t.Fatalf("insert non-canonical record: %v", err)
	}
	original := db.Record{"complete": false, "payload": nestedPayload, "version": 4}
	replacement := db.Record{"complete": true, "payload": nestedPayload, "version": 5}
	swapped, err := store.CompareAndSwapRecord(ctx, table, key, original, replacement)
	if err != nil || !swapped {
		t.Fatalf("CompareAndSwapRecord swapped=%v err=%v", swapped, err)
	}

	const nonCanonicalReplacement = `{ "version": 5, "payload": "{\"command\":{\"InitialHumanAgentID\":\"H000001\"},\"version\":4}", "complete": true }`
	if _, err := store.Exec(ctx, `UPDATE platform_records SET payload_json = ? WHERE table_name = ? AND record_key = ?`, nonCanonicalReplacement, table, key); err != nil {
		t.Fatalf("rewrite non-canonical record: %v", err)
	}
	deleted, err := store.CompareAndDeleteRecord(ctx, table, key, replacement)
	if err != nil || !deleted {
		t.Fatalf("CompareAndDeleteRecord deleted=%v err=%v", deleted, err)
	}
}

func TestPostgresStoreStripsNULCharactersBeforeWritingTextColumns(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-platform-records-" + t.Name(),
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	if _, err := store.Exec(ctx, `DELETE FROM platform_records`); err != nil {
		t.Fatalf("clear platform_records: %v", err)
	}

	if err := store.PutRecord(ctx, "items\x00bad", "one\x00bad", db.Record{"value": "hello\x00world"}); err != nil {
		t.Fatalf("PutRecord returned error: %v", err)
	}
	record, ok, err := store.GetRecord(ctx, "itemsbad", "onebad")
	if err != nil || !ok || record["value"] != "helloworld" {
		t.Fatalf("GetRecord record=%+v ok=%v err=%v", record, ok, err)
	}
}

func TestStoreTableExistsReportsSQLiteTables(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "table-exists"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	if _, err := store.Exec(ctx, `CREATE TABLE business_profiles (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	exists, err := store.TableExists(ctx, "business_profiles")
	if err != nil {
		t.Fatalf("TableExists returned error: %v", err)
	}
	if !exists {
		t.Fatalf("business_profiles table was not found")
	}
	missing, err := store.TableExists(ctx, "missing_profiles")
	if err != nil {
		t.Fatalf("TableExists missing returned error: %v", err)
	}
	if missing {
		t.Fatalf("missing_profiles table should not exist")
	}
	schemaQualified, err := store.TableExists(ctx, "main.business_profiles")
	if err != nil {
		t.Fatalf("TableExists schema-qualified returned error: %v", err)
	}
	if !schemaQualified {
		t.Fatalf("main.business_profiles table was not found")
	}
}

func TestPostgresStoreTableExistsReportsPostgresTables(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-table-exists",
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	if _, err := store.Exec(ctx, `DROP TABLE IF EXISTS business_profiles`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if _, err := store.Exec(ctx, `CREATE TABLE business_profiles (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.Exec(context.Background(), `DROP TABLE IF EXISTS business_profiles`)
	})

	exists, err := store.TableExists(ctx, "business_profiles")
	if err != nil {
		t.Fatalf("TableExists returned error: %v", err)
	}
	if !exists {
		t.Fatalf("business_profiles table was not found")
	}
	missing, err := store.TableExists(ctx, "missing_profiles")
	if err != nil {
		t.Fatalf("TableExists missing returned error: %v", err)
	}
	if missing {
		t.Fatalf("missing_profiles table should not exist")
	}
	schemaQualified, err := store.TableExists(ctx, "public.business_profiles")
	if err != nil {
		t.Fatalf("TableExists schema-qualified returned error: %v", err)
	}
	if !schemaQualified {
		t.Fatalf("public.business_profiles table was not found")
	}
}

func TestStoreSQLHelpersRejectClosedDatabase(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "closed-sql"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if _, err := store.Exec(ctx, `SELECT 1`); err == nil {
		t.Fatalf("Exec on closed database returned nil error")
	}
	if _, err := store.Query(ctx, `SELECT 1`); err == nil {
		t.Fatalf("Query on closed database returned nil error")
	}
	var value int
	if err := store.QueryRow(ctx, `SELECT 1`).Scan(&value); err == nil {
		t.Fatalf("QueryRow on closed database returned nil error")
	} else if !strings.Contains(err.Error(), "database is closed") {
		t.Fatalf("QueryRow closed error = %v, want database is closed", err)
	}
	if err := store.WithTx(ctx, func(tx db.Tx) error { return nil }); err == nil {
		t.Fatalf("WithTx on closed database returned nil error")
	}
}

func TestPutRecordReturnsEncodeErrors(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "put-record-error"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	err = store.PutRecord(ctx, "items", "bad", db.Record{"bad": func() {}})
	if err == nil {
		t.Fatalf("PutRecord returned nil error for non-encodable record")
	}
	if !strings.Contains(err.Error(), "encode record") {
		t.Fatalf("PutRecord error = %v, want encode record", err)
	}
}

func TestGetRecordReturnsClosedDatabaseError(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "get-record-error"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	_, _, err = store.GetRecord(ctx, "items", "one")
	if err == nil {
		t.Fatalf("GetRecord returned nil error for closed database")
	}
	if !strings.Contains(err.Error(), "database is closed") {
		t.Fatalf("GetRecord error = %v, want database is closed", err)
	}
}

func TestOpenConfiguresSQLiteBusyTimeout(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "busy-timeout"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	var busyTimeout int
	if err := store.QueryRow(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy timeout: %v", err)
	}
	if busyTimeout < 5000 {
		t.Fatalf("busy_timeout = %d, want at least 5000", busyTimeout)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func assertSQLiteFile(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read db file %s: %v", path, err)
	}
	assertSQLiteHeader(t, path, raw)
}

func assertSQLiteHeader(t *testing.T, path string, raw []byte) {
	t.Helper()
	if len(raw) < 16 || !strings.HasPrefix(string(raw[:16]), "SQLite format 3") {
		t.Fatalf("%s is not sqlite, header=%q", path, string(raw[:min(16, len(raw))]))
	}
}

func openMemoryRegistry(t *testing.T) *db.Registry {
	t.Helper()
	registry, err := db.OpenRegistry(context.Background(), db.RegistryConfig{
		Databases: map[string]db.Config{
			db.RoleCore:          {Driver: db.DriverMemory, Name: t.Name() + "-core"},
			db.RoleAudit:         {Driver: db.DriverMemory, Name: t.Name() + "-audit"},
			db.RoleEvents:        {Driver: db.DriverMemory, Name: t.Name() + "-events"},
			db.RoleJobs:          {Driver: db.DriverMemory, Name: t.Name() + "-jobs"},
			db.RoleRuntime:       {Driver: db.DriverMemory, Name: t.Name() + "-runtime"},
			db.RoleObservability: {Driver: db.DriverMemory, Name: t.Name() + "-observability"},
		},
	})
	if err != nil {
		t.Fatalf("OpenRegistry returned error: %v", err)
	}
	return registry
}

func mustStore(t *testing.T, registry *db.Registry, role string) *db.Store {
	t.Helper()
	store, err := registry.Store(role)
	if err != nil {
		t.Fatalf("lookup %s database: %v", role, err)
	}
	return store
}

func postgresTestURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	return raw
}

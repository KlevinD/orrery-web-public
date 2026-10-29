package agentkernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"orrery_v2/internal/platform/db"
)

func openTestJournal(t *testing.T, path string) (*SQLToolJournal, *db.Store) {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	journal, err := NewSQLToolJournal(store)
	if err != nil {
		t.Fatal(err)
	}
	return journal, store
}

func newTestJournal(t *testing.T) *SQLToolJournal {
	t.Helper()
	journal, _ := openTestJournal(t, filepath.Join(t.TempDir(), "runtime.db"))
	return journal
}

func testIntent(callID string) ToolExecution {
	fingerprint, _ := digest(map[string]any{"kb": "public", "query": "sensitive-private-question"})
	return ToolExecution{ToolExecutionKey: ToolExecutionKey{TenantID: "tenant-a", RunID: "run-a", CallID: callID},
		AgentID: "A000014", ToolID: "knowledge.search", Fingerprint: fingerprint}
}

func TestJournalPersistsIntentAndOutcomeAcrossDatabaseReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.db")
	journal, store := openTestJournal(t, path)
	completed, err := journal.Begin(ctx, testIntent("finished"))
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Finish(ctx, completed, ToolExecutionCompleted, ""); err != nil {
		t.Fatal(err)
	}
	started, err := journal.Begin(ctx, testIntent("crashed"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, reopenedStore := openTestJournal(t, path)
	for _, original := range []ToolExecution{started, completed} {
		loaded, found, err := reopened.Get(ctx, original.ToolExecutionKey)
		if err != nil || !found || loaded.ClaimID != original.ClaimID || loaded.Fingerprint != original.Fingerprint {
			t.Fatalf("durable record lost: %+v, %v, %v", loaded, found, err)
		}
		if loaded.CallID == "crashed" && loaded.State != ToolExecutionStarted || loaded.CallID == "finished" && loaded.State != ToolExecutionCompleted {
			t.Fatalf("incorrect recovered state: %+v", loaded)
		}
		if _, err := reopened.Begin(ctx, original); !errors.Is(err, ErrToolRecorded) {
			t.Fatalf("restarted operation may replay: %v", err)
		}
	}
	raw, err := reopenedStore.ListRecordTable(ctx, toolJournalTable)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(raw)
	if strings.Contains(string(payload), "sensitive-private-question") || strings.Contains(string(payload), `"kb"`) || strings.Contains(string(payload), "never-send-to-model") {
		t.Fatal("journal stored raw tool arguments or credentials")
	}
}

func TestJournalRejectsReplaysConflictsAndTerminalRewrites(t *testing.T) {
	ctx := context.Background()
	for _, outcome := range []struct {
		state  ToolExecutionState
		reason string
	}{{ToolExecutionCompleted, ""}, {ToolExecutionFailed, "reported_failure"}, {ToolExecutionUnknown, "timeout"}, {ToolExecutionNotDispatched, "cancelled_before_dispatch"}} {
		journal := newTestJournal(t)
		intent := testIntent(string(outcome.state))
		claim, err := journal.Begin(ctx, intent)
		if err != nil {
			t.Fatal(err)
		}
		forged := claim
		forged.ClaimID = "different-owner"
		if err := journal.Finish(ctx, forged, outcome.state, outcome.reason); !errors.Is(err, ErrToolConflict) {
			t.Fatalf("claim ownership bypassed: %v", err)
		}
		if err := journal.Finish(ctx, claim, outcome.state, outcome.reason); err != nil {
			t.Fatal(err)
		}
		if err := journal.Finish(ctx, claim, outcome.state, outcome.reason); err != nil {
			t.Fatalf("same finalization must be idempotent: %v", err)
		}
		if err := journal.Finish(ctx, claim, ToolExecutionUnknown, "interrupted"); !errors.Is(err, ErrToolConflict) {
			t.Fatalf("terminal outcome overwritten: %v", err)
		}
		if _, err := journal.Begin(ctx, intent); !errors.Is(err, ErrToolRecorded) {
			t.Fatalf("terminal operation may replay: %v", err)
		}
		for _, mutation := range []string{"arguments", "agent", "tool"} {
			changed := intent
			switch mutation {
			case "arguments":
				changed.Fingerprint, _ = digest("different")
			case "agent":
				changed.AgentID = "A000015"
			case "tool":
				changed.ToolID = "different.tool"
			}
			if _, err := journal.Begin(ctx, changed); !errors.Is(err, ErrToolConflict) {
				t.Fatalf("identity conflict accepted (%s): %v", mutation, err)
			}
		}
	}
}

func TestJournalClaimsAreAtomicAcrossDatabaseConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.db")
	first, _ := openTestJournal(t, path)
	second, _ := openTestJournal(t, path)
	start := make(chan struct{})
	errorsCh := make(chan error, 12)
	var workers sync.WaitGroup
	for i := 0; i < cap(errorsCh); i++ {
		workers.Add(1)
		go func(journal *SQLToolJournal) {
			defer workers.Done()
			<-start
			_, err := journal.Begin(ctx, testIntent("same-call"))
			errorsCh <- err
		}([]*SQLToolJournal{first, second}[i%2])
	}
	close(start)
	workers.Wait()
	close(errorsCh)
	claims := 0
	for err := range errorsCh {
		if err == nil {
			claims++
		} else if !errors.Is(err, ErrToolRecorded) {
			t.Fatalf("unexpected claim error: %v", err)
		}
	}
	if claims != 1 {
		t.Fatalf("concurrent dispatch would run %d times", claims)
	}
}

func TestJournalDiagnosticsRequireTenantRunAndBoundedCursor(t *testing.T) {
	ctx := context.Background()
	journal := newTestJournal(t)
	for _, id := range []string{"one", "two", "three"} {
		claim, err := journal.Begin(ctx, testIntent(id))
		if err != nil {
			t.Fatal(err)
		}
		if id != "three" {
			if err := journal.Finish(ctx, claim, ToolExecutionCompleted, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	other := testIntent("one")
	other.TenantID = "tenant-b"
	if _, err := journal.Begin(ctx, other); err != nil {
		t.Fatal(err)
	}
	otherRun := testIntent("one")
	otherRun.RunID = "run-b"
	if _, err := journal.Begin(ctx, otherRun); err != nil {
		t.Fatal(err)
	}
	seen, cursor := map[string]bool{}, ""
	for {
		page, err := journal.ListRun(ctx, "tenant-a", "run-a", cursor, 2)
		if err != nil || len(page.Items) == 0 || len(page.Items) > 2 {
			t.Fatalf("page=%+v, %v", page, err)
		}
		for _, item := range page.Items {
			if item.TenantID != "tenant-a" || item.RunID != "run-a" || seen[item.CallID] {
				t.Fatalf("scope or pagination violated: %+v", item)
			}
			seen[item.CallID] = true
		}
		if page.NextCursor == "" {
			break
		}
		if _, err := journal.ListRun(ctx, "tenant-b", "run-a", page.NextCursor, 2); !errors.Is(err, ErrInput) {
			t.Fatalf("cross-tenant cursor accepted: %v", err)
		}
		cursor = page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("missing records: %+v", seen)
	}
	if _, found, err := journal.Get(ctx, ToolExecutionKey{TenantID: "tenant-b", RunID: "run-a", CallID: "two"}); err != nil || found {
		t.Fatalf("cross-tenant record returned: found=%v, %v", found, err)
	}
	if _, err := journal.ListRun(ctx, "", "run-a", "", 10); !errors.Is(err, ErrInput) {
		t.Fatalf("unscoped query accepted: %v", err)
	}
	if _, err := journal.ListRun(ctx, "tenant-a", "run-a", "", 101); !errors.Is(err, ErrInput) {
		t.Fatalf("unbounded query accepted: %v", err)
	}
}

func TestJournalFailsClosedOnVolatileClosedOrCorruptStorage(t *testing.T) {
	ctx := context.Background()
	memory, err := db.Open(ctx, db.Config{Driver: db.DriverMemory})
	if err != nil {
		t.Fatal(err)
	}
	defer memory.Close()
	if _, err := NewSQLToolJournal(memory); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("volatile journal accepted: %v", err)
	}
	journal, store := openTestJournal(t, filepath.Join(t.TempDir(), "runtime.db"))
	intent := testIntent("corrupted")
	if err := store.PutRecord(ctx, toolJournalTable, journalKey(intent.ToolExecutionKey), db.Record{"private": "raw database secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Begin(ctx, intent); !errors.Is(err, ErrJournal) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("corrupt entry accepted or raw storage error leaked: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Begin(ctx, testIntent("closed")); !errors.Is(err, ErrJournal) {
		t.Fatalf("closed storage accepted: %v", err)
	}
}

func TestJournalBlocksChangedCallIDsInUnresolvedRunAfterReopen(t *testing.T) {
	for _, state := range []ToolExecutionState{ToolExecutionStarted, ToolExecutionUnknown} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "runtime.db")
			journal, store := openTestJournal(t, path)
			claim, err := journal.Begin(ctx, testIntent("old-id"))
			if err != nil {
				t.Fatal(err)
			}
			if state == ToolExecutionUnknown {
				if err := journal.Finish(ctx, claim, state, "timeout"); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, _ := openTestJournal(t, path)
			changed := testIntent("new-id")
			changed.ToolID = "different.tool"
			if _, err := reopened.Begin(ctx, changed); !errors.Is(err, ErrRunUnresolved) {
				t.Fatalf("new call ID bypassed unresolved %s record: %v", state, err)
			}
			if _, found, err := reopened.Get(ctx, changed.ToolExecutionKey); err != nil || found {
				t.Fatalf("rejected claim was committed: found=%v, %v", found, err)
			}
			otherTenant := changed
			otherTenant.TenantID = "tenant-b"
			if _, err := reopened.Begin(ctx, otherTenant); err != nil {
				t.Fatalf("one tenant blocked another: %v", err)
			}
			otherRun := changed
			otherRun.RunID = "run-b"
			if _, err := reopened.Begin(ctx, otherRun); err != nil {
				t.Fatalf("one run blocked another: %v", err)
			}
			if state == ToolExecutionStarted {
				if err := reopened.Finish(ctx, claim, ToolExecutionCompleted, ""); err != nil {
					t.Fatal(err)
				}
				if _, err := reopened.Begin(ctx, changed); err != nil {
					t.Fatalf("known completion did not release run: %v", err)
				}
			}
		})
	}
}

func TestJournalSerializesDifferentCallIDsAcrossDatabaseConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.db")
	first, _ := openTestJournal(t, path)
	second, _ := openTestJournal(t, path)
	start := make(chan struct{})
	errorsCh := make(chan error, 8)
	var workers sync.WaitGroup
	for i := 0; i < cap(errorsCh); i++ {
		workers.Add(1)
		go func(journal *SQLToolJournal, id string) {
			defer workers.Done()
			<-start
			_, err := journal.Begin(ctx, testIntent(id))
			errorsCh <- err
		}([]*SQLToolJournal{first, second}[i%2], fmt.Sprintf("call-%d", i))
	}
	close(start)
	workers.Wait()
	close(errorsCh)
	claims := 0
	for err := range errorsCh {
		if err == nil {
			claims++
		} else if !errors.Is(err, ErrRunUnresolved) {
			t.Fatalf("unexpected run guard error: %v", err)
		}
	}
	if claims != 1 {
		t.Fatalf("different call IDs acquired %d simultaneous claims", claims)
	}
	page, err := first.ListRun(ctx, "tenant-a", "run-a", "", 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].State != ToolExecutionStarted {
		t.Fatalf("rejected claims survived transaction rollback: %+v, %v", page, err)
	}
}

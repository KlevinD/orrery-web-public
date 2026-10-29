package agentkernel

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"orrery_v2/internal/platform/db"
)

// This opt-in contract uses unique test identities and removes only those keys.
// The DSN is supplied by the deployment verifier, never written to test output.
func TestJournalPostgresContract(t *testing.T) {
	dsn := os.Getenv("ORRERY_AGENTCORE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("PostgreSQL contract DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverPostgres, URL: dsn})
	if err != nil {
		t.Fatal("PostgreSQL contract database unavailable")
	}
	defer store.Close()
	other, err := db.Open(ctx, db.Config{Driver: db.DriverPostgres, URL: dsn})
	if err != nil {
		t.Fatal("second PostgreSQL connection unavailable")
	}
	defer other.Close()
	journals := make([]*SQLToolJournal, 2)
	journals[0], _ = NewSQLToolJournal(store)
	journals[1], _ = NewSQLToolJournal(other)
	t.Run("atomic parallel batches", func(t *testing.T) { exerciseConcurrentBatchJournal(t, journals) })
	intent := testIntent("claim-a")
	intent.TenantID = "agentcore-contract-" + uuid.NewString()
	second := intent
	second.CallID = "claim-b"
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		for _, key := range []ToolExecutionKey{intent.ToolExecutionKey, second.ToolExecutionKey} {
			if store.DeleteRecord(cleanup, toolJournalTable, journalKey(key)) != nil {
				t.Error("contract record cleanup failed")
			}
		}
	}()
	type outcome struct {
		claim ToolExecution
		err   error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index, input := range []ToolExecution{intent, second} {
		workers.Add(1)
		go func(index int, input ToolExecution) {
			defer workers.Done()
			<-start
			claim, err := journals[index].Begin(ctx, input)
			results <- outcome{claim, err}
		}(index, input)
	}
	close(start)
	workers.Wait()
	close(results)
	var winner ToolExecution
	won, blocked := 0, 0
	for result := range results {
		if result.err == nil {
			won++
			winner = result.claim
		} else if errors.Is(result.err, ErrRunUnresolved) {
			blocked++
		} else {
			t.Fatalf("unexpected concurrent claim error: %v", result.err)
		}
	}
	if won != 1 || blocked != 1 {
		t.Fatalf("claims=%d blocked=%d", won, blocked)
	}
	if journals[0].Finish(ctx, winner, ToolExecutionUnknown, "timeout") != nil {
		t.Fatal("unknown outcome was not persisted")
	}
	loser := intent
	if loser.CallID == winner.CallID {
		loser = second
	}
	if _, err := journals[1].Begin(ctx, loser); !errors.Is(err, ErrRunUnresolved) {
		t.Fatalf("unresolved run allowed a different call: %v", err)
	}
	if _, err := journals[1].Begin(ctx, winner); !errors.Is(err, ErrToolRecorded) {
		t.Fatalf("replay not blocked: %v", err)
	}
	page, err := journals[1].ListRun(ctx, winner.TenantID, winner.RunID, "", 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].State != ToolExecutionUnknown {
		t.Fatalf("readback=%+v err=%v", page, err)
	}
}

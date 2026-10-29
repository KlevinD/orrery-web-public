package agentkernel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBatchJournalRollsBackEveryMemberOnReplayOrConflict(t *testing.T) {
	ctx := context.Background()
	journal := newTestJournal(t)
	prior, err := journal.Begin(ctx, testIntent("existing"))
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Finish(ctx, prior, ToolExecutionCompleted, ""); err != nil {
		t.Fatal(err)
	}
	for _, conflict := range []bool{false, true} {
		intents := []ToolExecution{testIntent("new"), testIntent("existing")}
		expected := ErrToolRecorded
		if conflict {
			intents[1].Fingerprint, _ = digest("different arguments")
			expected = ErrToolConflict
		}
		if claims, err := journal.BeginBatch(ctx, intents); !errors.Is(err, expected) || claims != nil {
			t.Fatalf("claims=%+v err=%v", claims, err)
		}
		if _, found, err := journal.Get(ctx, intents[0].ToolExecutionKey); err != nil || found {
			t.Fatalf("partial batch persisted: found=%v err=%v", found, err)
		}
	}
}

func TestBatchJournalRejectsInvalidBatchAuthority(t *testing.T) {
	for _, mode := range []string{"empty", "duplicate", "tenant", "run", "agent", "fingerprint", "too-large"} {
		t.Run(mode, func(t *testing.T) {
			journal := newTestJournal(t)
			intents := []ToolExecution{testIntent("one"), testIntent("two")}
			switch mode {
			case "empty":
				intents = nil
			case "duplicate":
				intents[1].CallID = "one"
			case "tenant":
				intents[1].TenantID = "another-tenant"
			case "run":
				intents[1].RunID = "another-run"
			case "agent":
				intents[1].AgentID = "another-agent"
			case "fingerprint":
				intents[1].Fingerprint = "invalid"
			case "too-large":
				for len(intents) < 17 {
					intents = append(intents, testIntent(uuid.NewString()))
				}
			}
			if _, err := journal.BeginBatch(context.Background(), intents); !errors.Is(err, ErrInput) {
				t.Fatalf("invalid batch: %v", err)
			}
			page, err := journal.ListRun(context.Background(), "tenant-a", "run-a", "", 100)
			if err != nil || len(page.Items) != 0 {
				t.Fatalf("invalid batch persisted: %+v %v", page, err)
			}
		})
	}
}

func TestBatchJournalSerializesCompetingBatches(t *testing.T) {
	journal := newTestJournal(t)
	exerciseConcurrentBatchJournal(t, []*SQLToolJournal{journal, journal})
}

func exerciseConcurrentBatchJournal(t *testing.T, journals []*SQLToolJournal) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tenant := "agentcore-batch-contract-" + uuid.NewString()
	inputs := [][]ToolExecution{{testIntent("a"), testIntent("b")}, {testIntent("c"), testIntent("d")}}
	for i := range inputs {
		for j := range inputs[i] {
			inputs[i][j].TenantID = tenant
		}
	}
	probe := testIntent("probe")
	probe.TenantID = tenant
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		for _, batch := range append(inputs, []ToolExecution{probe}) {
			for _, item := range batch {
				if err := journals[0].store.DeleteRecord(cleanup, toolJournalTable, journalKey(item.ToolExecutionKey)); err != nil {
					t.Error("batch contract cleanup failed")
				}
			}
		}
	}()
	type outcome struct {
		claims []ToolExecution
		err    error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index, batch := range inputs {
		workers.Add(1)
		go func(index int, batch []ToolExecution) {
			defer workers.Done()
			<-start
			claims, err := journals[index].BeginBatch(ctx, batch)
			results <- outcome{claims, err}
		}(index, batch)
	}
	close(start)
	workers.Wait()
	close(results)
	var winner []ToolExecution
	blocked := 0
	for result := range results {
		if result.err == nil {
			if winner != nil {
				t.Fatal("two competing batches acquired the same run")
			}
			winner = result.claims
		} else if errors.Is(result.err, ErrRunUnresolved) {
			blocked++
		} else {
			t.Fatalf("batch claim error: %v", result.err)
		}
	}
	if len(winner) != 2 || blocked != 1 {
		t.Fatalf("winner=%d blocked=%d", len(winner), blocked)
	}
	page, err := journals[1].ListRun(ctx, tenant, "run-a", "", 10)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("losing batch was not rolled back: %+v %v", page, err)
	}
	if _, err := journals[1].Begin(ctx, probe); !errors.Is(err, ErrRunUnresolved) {
		t.Fatalf("ordinary claim bypassed a running batch: %v", err)
	}
	if err := journals[0].Finish(ctx, winner[0], ToolExecutionCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := journals[1].Begin(ctx, probe); !errors.Is(err, ErrRunUnresolved) {
		t.Fatalf("unfinished batch member was ignored: %v", err)
	}
	if err := journals[1].Finish(ctx, winner[1], ToolExecutionUnknown, "timeout"); err != nil {
		t.Fatal(err)
	}
	if _, err := journals[0].BeginBatch(ctx, []ToolExecution{probe}); !errors.Is(err, ErrRunUnresolved) {
		t.Fatalf("unknown batch outcome replayed: %v", err)
	}
}

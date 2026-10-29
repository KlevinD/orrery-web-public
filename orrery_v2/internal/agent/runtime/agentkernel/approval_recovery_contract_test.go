package agentkernel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"orrery_v2/internal/platform/db"
)

func TestApprovalRecoverySQLiteContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approval-contract.db")
	var stores []*SQLCheckpointStore
	for i := 0; i < 2; i++ {
		_, backing := openTestJournal(t, path)
		store, _ := NewSQLCheckpointStore(backing)
		stores = append(stores, store)
	}
	approvalRecoveryContract(t, stores)
}

func TestApprovalRecoveryPostgresContract(t *testing.T) {
	dsn := os.Getenv("ORRERY_AGENTCORE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("PostgreSQL contract DSN is not configured")
	}
	var stores []*SQLCheckpointStore
	for i := 0; i < 2; i++ {
		backing, err := db.Open(context.Background(), db.Config{Driver: db.DriverPostgres, URL: dsn})
		if err != nil {
			t.Fatal("PostgreSQL contract database unavailable")
		}
		defer backing.Close()
		store, _ := NewSQLCheckpointStore(backing)
		stores = append(stores, store)
	}
	approvalRecoveryContract(t, stores)
}

func approvalRecoveryContract(t *testing.T, stores []*SQLCheckpointStore) {
	for _, batch := range []bool{false, true} {
		name := "single"
		if batch {
			name = "batch"
		}
		t.Run(name, func(t *testing.T) { approvalProposalContract(t, stores, batch) })
	}
}

func approvalProposalContract(t *testing.T, stores []*SQLCheckpointStore, batch bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	proposal := func(t *testing.T, mutate func(*Checkpoint)) Checkpoint {
		t.Helper()
		c := testCheckpoint(t)
		c.TenantID = "agentcore-approval-contract-" + uuid.NewString()
		c.MissionID, c.TaskID, c.ModelCalls = "mission", "task", 1
		c.ModelIdentity = strings.Repeat("c", 64)
		ready, err := stores[0].Create(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			for _, item := range []struct{ table, key string }{
				{checkpointTable, checkpointKey(c.TenantID, c.AgentID, c.ID)},
				{pendingTaskTable, pendingTaskKey(c.TenantID, c.MissionID, c.TaskID)},
				{toolJournalTable, journalKey(ToolExecutionKey{TenantID: c.TenantID, RunID: c.RunID, CallID: "proposal"})},
				{toolJournalTable, journalKey(ToolExecutionKey{TenantID: c.TenantID, RunID: c.RunID, CallID: "proposal-two"})},
				{toolJournalTable, journalKey(ToolExecutionKey{TenantID: c.TenantID, RunID: c.RunID, CallID: "other-call"})},
			} {
				if err := stores[0].store.DeleteRecord(cleanup, item.table, item.key); err != nil {
					t.Error("approval contract cleanup failed")
				}
			}
		})
		ready.Phase = CheckpointToolsPending
		ready.Messages = append(ready.Messages, reply("", "tool_calls", toolCall("proposal", `{"kb":"public"}`)))
		ready.ApprovalWait = &ApprovalWait{CallID: "proposal", Fingerprint: strings.Repeat("a", 64), ExpiresAt: time.Now().Add(time.Minute)}
		if batch {
			ready.Messages[len(ready.Messages)-1].ToolCalls = append(ready.Messages[len(ready.Messages)-1].ToolCalls, toolCall("proposal-two", `{"kb":"public"}`))
			ready.ApprovalWait.Calls = []ApprovalCall{{CallID: "proposal", Fingerprint: ready.ApprovalWait.Fingerprint}, {CallID: "proposal-two", Fingerprint: strings.Repeat("b", 64)}}
			ready.ApprovalWait.CallID, ready.ApprovalWait.Fingerprint = "", ""
		}
		if mutate != nil {
			mutate(&ready)
		}
		pending, err := stores[0].Save(ctx, ready)
		if err != nil {
			t.Fatal(err)
		}
		return pending
	}
	claim := func(store *SQLCheckpointStore, c Checkpoint, owner string) (Checkpoint, error) {
		return store.Claim(ctx, c.TenantID, c.AgentID, c.ID, c.ScopeHash, owner, c.RunID)
	}
	t.Run("competing_owners_close_once", func(t *testing.T) {
		c := proposal(t, nil)
		type result struct {
			c   Checkpoint
			err error
		}
		start, results := make(chan struct{}), make(chan result, 2)
		for i := 0; i < 2; i++ {
			go func(i int) {
				<-start
				claimed, err := claim(stores[i], c, uuid.NewString())
				results <- result{claimed, err}
			}(i)
		}
		close(start)
		var claims []Checkpoint
		closed, fenced := 0, 0
		for i := 0; i < 2; i++ {
			result := <-results
			if result.err != nil {
				// SQLite may reject a concurrent read-to-write upgrade. That
				// claimant must stay fenced rather than proceed without a claim.
				if errors.Is(result.err, ErrCheckpoint) {
					fenced++
					continue
				}
				t.Fatal(result.err)
			}
			if !result.c.ApprovalWait.ExpiresAt.Equal(c.ApprovalWait.ExpiresAt) || result.c.ModelCalls != c.ModelCalls {
				t.Fatal("claim renewed approval lifetime or budget")
			}
			claims = append(claims, result.c)
		}
		for _, claimed := range claims {
			claimed.ApprovalWait = nil
			if _, err := stores[0].Save(ctx, claimed); err == nil {
				closed++
			} else if errors.Is(err, ErrCheckpoint) {
				fenced++
			} else {
				t.Fatal(err)
			}
		}
		if closed != 1 || fenced != 1 {
			t.Fatalf("closed=%d fenced=%d", closed, fenced)
		}
		if _, err := claim(stores[1], c, "after-close"); !errors.Is(err, ErrUnsafeResume) {
			t.Fatalf("closed window resumed: %v", err)
		}
	})
	t.Run("immutable_transition_and_task_scope", func(t *testing.T) {
		c := proposal(t, nil)
		for _, change := range []func(*Checkpoint){
			func(c *Checkpoint) {
				c.ApprovalWait = nil
				c.Messages[len(c.Messages)-1] = reply("", "tool_calls", toolCall("proposal", `{"kb":"private"}`))
			},
			func(c *Checkpoint) {
				p := *c.ApprovalWait
				p.ExpiresAt = p.ExpiresAt.Add(time.Minute)
				c.ApprovalWait = &p
			},
			func(c *Checkpoint) {
				c.ApprovalWait = nil
				c.Phase = CheckpointReady
				c.Messages = c.Messages[:len(c.Messages)-1]
			},
			func(c *Checkpoint) { c.ApprovalWait = nil; c.ModelCalls++ },
			func(c *Checkpoint) { c.ApprovalWait = nil; c.FirstPrompt = "a different instruction" },
		} {
			changed := c
			changed.Messages = append(changed.Messages[:0:0], c.Messages...)
			change(&changed)
			if _, err := stores[1].Save(ctx, changed); !errors.Is(err, ErrCheckpoint) {
				t.Fatalf("approval proposal changed: %v", err)
			}
		}
		for _, change := range []func(*Checkpoint){
			func(c *Checkpoint) { c.TenantID = "other" },
			func(c *Checkpoint) { c.AgentID = "other" },
			func(c *Checkpoint) { c.RunID = "other" },
			func(c *Checkpoint) { c.ScopeHash = strings.Repeat("d", 64) },
		} {
			changed := c
			change(&changed)
			if _, err := claim(stores[1], changed, "wrong-scope"); !errors.Is(err, ErrCheckpoint) {
				t.Fatalf("wrong scope claimed: %v", err)
			}
		}
		other := c
		other.ID, other.RunID, other.ApprovalWait, other.Phase = newCheckpointID(), "new-run", nil, CheckpointReady
		other.Messages = other.Messages[:len(other.Messages)-1]
		if _, err := stores[1].Create(ctx, other); !errors.Is(err, ErrUnsafeResume) {
			t.Fatalf("new run bypassed proposal: %v", err)
		}
		if err := stores[1].CheckTaskRestart(ctx, c.TenantID, c.MissionID, c.TaskID); !errors.Is(err, ErrUnsafeResume) {
			t.Fatal("generic task restart bypassed approval")
		}
	})
	t.Run("expired", func(t *testing.T) {
		c := proposal(t, func(c *Checkpoint) { c.ApprovalWait.ExpiresAt = time.Now().Add(-time.Second) })
		if _, err := claim(stores[1], c, "expired"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expired proposal claimed: %v", err)
		}
	})
	for _, state := range []ToolExecutionState{ToolExecutionStarted, ToolExecutionCompleted, ToolExecutionUnknown, ToolExecutionFailed, ToolExecutionNotDispatched} {
		ids := []string{"proposal"}
		if batch {
			ids = append(ids, "proposal-two")
		}
		for _, id := range ids {
			t.Run("recorded_"+string(state)+"_"+id, func(t *testing.T) {
				c := proposal(t, nil)
				journal, _ := NewSQLToolJournal(stores[0].store)
				intent := ToolExecution{ToolExecutionKey: ToolExecutionKey{TenantID: c.TenantID, RunID: c.RunID, CallID: id}, AgentID: c.AgentID, ToolID: "knowledge.search", Fingerprint: strings.Repeat("a", 64)}
				for _, item := range c.ApprovalWait.proposals() {
					if item.CallID == id {
						intent.Fingerprint = item.Fingerprint
					}
				}
				record, err := journal.Begin(ctx, intent)
				if err != nil {
					t.Fatal(err)
				}
				reason := map[ToolExecutionState]string{ToolExecutionUnknown: "transport_error", ToolExecutionFailed: "reported_failure", ToolExecutionNotDispatched: "blocked_before_dispatch"}[state]
				if state != ToolExecutionStarted {
					if err := journal.Finish(ctx, record, state, reason); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := claim(stores[1], c, "recorded"); !errors.Is(err, ErrUnsafeResume) {
					t.Fatalf("recorded operation replayed: %v", err)
				}
			})
		}
	}
	t.Run("other_unknown_operation", func(t *testing.T) {
		c := proposal(t, nil)
		journal, _ := NewSQLToolJournal(stores[0].store)
		_, err := journal.Begin(ctx, ToolExecution{ToolExecutionKey: ToolExecutionKey{TenantID: c.TenantID, RunID: c.RunID, CallID: "other-call"}, AgentID: c.AgentID, ToolID: "knowledge.search", Fingerprint: c.ApprovalWait.proposals()[0].Fingerprint})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := claim(stores[1], c, "other-unresolved"); !errors.Is(err, ErrUnsafeResume) {
			t.Fatalf("unresolved run resumed: %v", err)
		}
	})
}

package agentkernel

import (
	"context"
	"encoding/json"
	"errors"
	"orrery_v2/internal/agent/llm"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"orrery_v2/internal/platform/db"
)

func TestTaskCheckpointPostgresContract(t *testing.T) {
	dsn := os.Getenv("ORRERY_AGENTCORE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("PostgreSQL contract DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var databases []*db.Store
	var stores []*SQLCheckpointStore
	for i := 0; i < 2; i++ {
		database, err := db.Open(ctx, db.Config{Driver: db.DriverPostgres, URL: dsn})
		if err != nil {
			t.Fatal("PostgreSQL contract database unavailable")
		}
		defer database.Close()
		store, err := NewSQLCheckpointStore(database)
		if err != nil {
			t.Fatal(err)
		}
		databases = append(databases, database)
		stores = append(stores, store)
	}
	candidate := testCheckpoint(t)
	candidate.TenantID = "agentcore-task-contract-" + uuid.NewString()
	candidate.ModelIdentity = strings.Repeat("a", 64)
	candidate.FirstPrompt = "original goal"
	key := checkpointKey(candidate.TenantID, candidate.AgentID, candidate.ID)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if databases[0].DeleteRecord(cleanup, checkpointTable, key) != nil {
			t.Error("task contract cleanup failed")
		}
	}()
	type result struct {
		checkpoint Checkpoint
		existing   bool
		err        error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-start
			c := candidate
			c.Owner = uuid.NewString()
			opened, existing, err := stores[i].OpenTask(ctx, c)
			results <- result{opened, existing, err}
		}(i)
	}
	close(start)
	created, recovered, writes, fenced := 0, 0, 0, 0
	var winner Checkpoint
	var claims []Checkpoint
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.existing {
			recovered++
		} else {
			created++
		}
		claims = append(claims, r.checkpoint)
	}
	for _, c := range claims {
		c.ModelCalls++
		saved, err := stores[0].Save(ctx, c)
		if err == nil {
			writes++
			winner = saved
		} else if errors.Is(err, ErrCheckpoint) {
			fenced++
		} else {
			t.Fatal(err)
		}
	}
	if created != 1 || recovered != 1 || writes != 1 || fenced != 1 {
		t.Fatalf("atomic task acquisition failed: created=%d recovered=%d writes=%d fenced=%d", created, recovered, writes, fenced)
	}
	for _, mutate := range []func(*Checkpoint){
		func(c *Checkpoint) { c.ScopeHash = strings.Repeat("b", 64) },
		func(c *Checkpoint) { c.ModelIdentity = strings.Repeat("b", 64) },
		func(c *Checkpoint) { c.FirstPrompt = "changed goal" },
		func(c *Checkpoint) { c.RunID = "different-run" },
	} {
		c := candidate
		mutate(&c)
		if _, _, err := stores[1].OpenTask(ctx, c); !errors.Is(err, ErrCheckpoint) {
			t.Fatalf("changed task identity accepted: %v", err)
		}
	}
	// An expired checkpoint never receives a fresh deadline on redelivery.
	winner.Deadline = time.Now().Add(-time.Second)
	winner, err := stores[0].Save(ctx, winner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := stores[1].OpenTask(ctx, candidate); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired task resumed: %v", err)
	}
	// A pending batch takes precedence over expiry and cannot be auto-replayed.
	winner.Phase = CheckpointToolsPending
	winner.Messages = append(winner.Messages, reply("", "tool_calls", toolCall("uncertain", `{"kb":"public"}`)))
	if _, err := stores[0].Save(ctx, winner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := stores[1].OpenTask(ctx, candidate); !errors.Is(err, ErrUnsafeResume) {
		t.Fatalf("uncertain task resumed: %v", err)
	}
}

func TestCheckpointPostgresContract(t *testing.T) {
	dsn := os.Getenv("ORRERY_AGENTCORE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("PostgreSQL contract DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var databases []*db.Store
	var stores []*SQLCheckpointStore
	for i := 0; i < 2; i++ {
		store, err := db.Open(ctx, db.Config{Driver: db.DriverPostgres, URL: dsn})
		if err != nil {
			t.Fatal("PostgreSQL contract database unavailable")
		}
		defer store.Close()
		checkpoints, err := NewSQLCheckpointStore(store)
		if err != nil {
			t.Fatal(err)
		}
		databases = append(databases, store)
		stores = append(stores, checkpoints)
	}
	c := testCheckpoint(t)
	c.Messages = append(c.Messages, llm.AssistantMessage("historical evidence", nil), llm.UserMessage("latest request"))
	c.TenantID = "agentcore-checkpoint-contract-" + uuid.NewString()
	key := checkpointKey(c.TenantID, c.AgentID, c.ID)
	var archiveKeys []string
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if databases[0].DeleteRecord(cleanup, checkpointTable, key) != nil {
			t.Error("checkpoint contract cleanup failed")
		}
		for _, archiveKey := range archiveKeys {
			if databases[0].DeleteRecord(cleanup, compactionArchiveTable, archiveKey) != nil {
				t.Error("archive contract cleanup failed")
			}
		}
	}()
	created, err := stores[0].Create(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		checkpoint Checkpoint
		err        error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			claim, err := stores[i].Claim(ctx, c.TenantID, c.AgentID, c.ID, c.ScopeHash, uuid.NewString(), c.RunID)
			results <- outcome{claim, err}
		}(i)
	}
	close(start)
	workers.Wait()
	close(results)
	var claims []Checkpoint
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		claims = append(claims, result.checkpoint)
	}
	writes, blocked := 0, 0
	var winner Checkpoint
	for _, claim := range claims {
		claim.ModelCalls++
		saved, err := stores[0].Save(ctx, claim)
		if err == nil {
			writes++
			winner = saved
		} else if errors.Is(err, ErrCheckpoint) {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if writes != 1 || blocked != 1 {
		t.Fatalf("concurrent owners wrote: writes=%d blocked=%d", writes, blocked)
	}
	// Persist fragment progress across two connections and fence an interrupted
	// summarizer before committing the replacement history and archive.
	sourceHash, _ := digest(winner.Messages)
	policyHash, _ := digest("postgres-compaction-contract")
	payload, _ := json.Marshal(winner.Messages[:1])
	winner.CompactionProgress = &CompactionProgress{Version: compactionProgressVersion, SourceHash: sourceHash,
		PolicyHash: policyHash, ArchiveMessages: 1, PayloadBytes: len(payload), FragmentCap: len(payload), SummaryLimit: 128}
	winner, err = stores[0].Save(ctx, winner)
	if err != nil {
		t.Fatal(err)
	}
	winner.CompactionProgress = cloneCompactionProgress(winner.CompactionProgress)
	winner.CompactionProgress.PendingBytes = len(payload)
	winner.ModelCalls++
	winner, err = stores[0].Save(ctx, winner)
	if err != nil {
		t.Fatal(err)
	}
	winner.CompactionProgress = cloneCompactionProgress(winner.CompactionProgress)
	winner.CompactionProgress.Offset = len(payload)
	winner.CompactionProgress.PendingBytes = 0
	winner.CompactionProgress.Summary = "original goal and historical evidence retained"
	winner, err = stores[1].Save(ctx, winner)
	if err != nil {
		t.Fatal(err)
	}
	stale := winner
	winner, err = stores[0].Claim(ctx, winner.TenantID, winner.AgentID, winner.ID, winner.ScopeHash, uuid.NewString(), winner.RunID)
	if err != nil || winner.CompactionProgress == nil || winner.CompactionProgress.Offset != len(payload) || winner.CompactionProgress.Summary != stale.CompactionProgress.Summary {
		t.Fatalf("cross-connection progress recovery failed: %v", err)
	}
	if _, err := stores[1].Save(ctx, stale); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("stale summarizer retained ownership: %v", err)
	}
	archiveKey, _ := digest([]any{key, winner.Revision})
	archiveKeys = append(archiveKeys, archiveKey)
	winner.Compactions++
	winner.CompactionProgress = nil
	winner.Messages = []*llm.Message{llm.UserMessage("original goal"), llm.AssistantMessage("compacted working memory", nil)}
	winner, err = stores[1].Save(ctx, winner)
	if err != nil {
		t.Fatal(err)
	}
	archive, found, err := databases[0].GetRecord(ctx, compactionArchiveTable, archiveKey)
	if err != nil || !found {
		t.Fatalf("compaction archive missing: %v", err)
	}
	archived, err := decodeCheckpoint(archive)
	if err != nil || archived.Compactions != 0 || archived.ID != created.ID || archived.CompactionProgress == nil || archived.CompactionProgress.Offset != len(payload) {
		t.Fatalf("archive changed scope: %v", err)
	}
	winner.Phase = CheckpointToolsPending
	winner.Messages = append(winner.Messages, reply("", "tool_calls", toolCall("unfinished", `{"kb":"public"}`)))
	if _, err := stores[0].Save(ctx, winner); err != nil {
		t.Fatal(err)
	}
	if _, err := stores[1].Claim(ctx, c.TenantID, c.AgentID, c.ID, c.ScopeHash, "restarted-owner", c.RunID); !errors.Is(err, ErrUnsafeResume) {
		t.Fatalf("unsafe batch resumed: %v", err)
	}
}

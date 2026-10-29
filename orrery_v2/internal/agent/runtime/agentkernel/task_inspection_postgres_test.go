package agentkernel

import (
	"context"
	"errors"
	"orrery_v2/internal/agent/llm"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

func TestTaskInspectionPostgresContract(t *testing.T) {
	dsn := os.Getenv("ORRERY_AGENTCORE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("PostgreSQL contract DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
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
		stores = append(stores, store)
	}
	c := testCheckpoint(t)
	c.TenantID = "agentcore-review-contract-" + uuid.NewString()
	c.MissionID, c.TaskID = "mission", "task"
	c.ID = taskCheckpointID(agentcore.RuntimeRequest{TenantID: c.TenantID, AgentID: c.AgentID, RunID: c.RunID})
	other := c
	other.RunID = "other-run"
	other.ID = taskCheckpointID(agentcore.RuntimeRequest{TenantID: c.TenantID, AgentID: c.AgentID, RunID: other.RunID})
	var reviewID string
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		for _, item := range []struct{ table, key string }{
			{checkpointTable, checkpointKey(c.TenantID, c.AgentID, c.ID)},
			{checkpointTable, checkpointKey(other.TenantID, other.AgentID, other.ID)},
			{pendingTaskTable, pendingTaskKey(c.TenantID, c.MissionID, c.TaskID)},
			{taskReviewHeadTable, checkpointKey(c.TenantID, c.AgentID, c.ID)},
			{taskReviewHeadTable, checkpointKey(other.TenantID, other.AgentID, other.ID)},
			{taskReviewTable, reviewID},
		} {
			if item.key != "" && stores[0].store.DeleteRecord(cleanup, item.table, item.key) != nil {
				t.Error("review contract cleanup failed")
			}
		}
	}()
	var err error
	c, err = stores[0].Create(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	other, err = stores[1].Create(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		checkpoint Checkpoint
		err        error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for i, candidate := range []Checkpoint{c, other} {
		go func(i int, candidate Checkpoint) {
			<-start
			candidate.Phase = CheckpointToolsPending
			candidate.Messages = append(candidate.Messages, reply("", "tool_calls", toolCall("uncertain", `{"kb":"public"}`)))
			saved, err := stores[i].Save(ctx, candidate)
			results <- result{saved, err}
		}(i, candidate)
	}
	close(start)
	won, blocked := 0, 0
	var winner Checkpoint
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err == nil {
			won++
			winner = r.checkpoint
		} else if errors.Is(r.err, ErrUnsafeResume) {
			blocked++
		} else {
			t.Fatal(r.err)
		}
	}
	if won != 1 || blocked != 1 {
		t.Fatalf("two runs dispatched concurrently: won=%d blocked=%d", won, blocked)
	}
	ref := TaskRef{winner.TenantID, winner.MissionID, winner.TaskID, winner.AgentID, winner.RunID}
	view, err := stores[1].InspectTask(ctx, ref)
	if err != nil || !view.RestartBlocked || len(view.PendingTools) != 1 {
		t.Fatalf("cross-connection inspection failed: %v", err)
	}
	input := TaskReviewInput{Revision: view.Revision, Conclusion: "confirmed_not_executed", Note: "No external receipt found", EvidenceRefs: []string{"external-audit:contract"}}
	review, err := stores[0].ReviewTask(ctx, ref, "operator", "review-key", input)
	if err != nil {
		t.Fatal(err)
	}
	reviewID = review.ReviewID
	replay, err := stores[1].ReviewTask(ctx, ref, "operator", "review-key", input)
	if err != nil || !reflect.DeepEqual(review, replay) {
		t.Fatal("cross-connection review was not idempotent")
	}
	view, err = stores[1].InspectTask(ctx, ref)
	if err != nil || view.LatestReview == nil || view.LatestReview.ReviewID != reviewID || !view.RestartBlocked {
		t.Fatal("review lost or cleared pending barrier")
	}
	if err := stores[1].CheckTaskRestart(ctx, c.TenantID, c.MissionID, c.TaskID); !errors.Is(err, ErrUnsafeResume) {
		t.Fatal("review allowed unverified restart")
	}
	// Only the owning runner's complete tool-result checkpoint clears the fence.
	winner.Phase = CheckpointReady
	winner.Messages = append(winner.Messages, llm.ToolMessage("verified receipt", "uncertain"))
	if _, err := stores[0].Save(ctx, winner); err != nil {
		t.Fatal(err)
	}
	if err := stores[1].CheckTaskRestart(ctx, c.TenantID, c.MissionID, c.TaskID); err != nil {
		t.Fatal(err)
	}
}

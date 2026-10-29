package agentkernel

import (
	"context"
	"encoding/json"
	"errors"
	"orrery_v2/internal/agent/llm"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestTaskInspectionAndReviewKeepUncertainExecutionFenced(t *testing.T) {
	ctx := context.Background()
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("pending-call", `{"kb":"public"}`))}), nil
	}}
	r, input := openRunner(t, m, func(c *Config, q *agentcore.RuntimeRequest) {
		automaticTaskConfig(t, c, q)
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			return runtimecore.RuntimeToolCallResult{}, errors.New("uncertain external operation")
		})
	})
	if _, err := r.Run(ctx, input, discard); err == nil {
		t.Fatal("tool failure not exercised")
	}
	store := r.config.Checkpoints.(*SQLCheckpointStore)
	ref := TaskRef{r.request.TenantID, r.request.MissionID, r.request.TaskID, r.request.AgentID, r.request.RunID}
	status, err := store.InspectTask(ctx, ref)
	if err != nil || !status.Available || !status.RestartBlocked || status.Phase != "tools_pending" || status.ModelCalls != 1 || len(status.PendingTools) != 1 || status.PendingTools[0].CallID != "pending-call" {
		t.Fatalf("incorrect pending status: %+v %v", status, err)
	}
	data, _ := json.Marshal(status)
	for _, secret := range []string{"\"arguments\"", r.checkpoint.ScopeHash, r.checkpoint.Owner, r.checkpoint.FirstPrompt} {
		if secret != "" && strings.Contains(string(data), secret) {
			t.Fatal("inspection exposed execution content")
		}
	}
	before, _, _ := store.store.GetRecord(ctx, checkpointTable, checkpointKey(ref.TenantID, ref.AgentID, r.checkpoint.ID))
	review := TaskReviewInput{Revision: status.Revision, Conclusion: "confirmed_executed", Note: "Checked the external operation receipt", EvidenceRefs: []string{"receipt:external-operation-42"}}
	for name, mutate := range map[string]func(*TaskReviewInput){
		"blank":               func(in *TaskReviewInput) { in.Note = "  " },
		"byte-limit":          func(in *TaskReviewInput) { in.Note = strings.Repeat("汉", 667) },
		"nul-note":            func(in *TaskReviewInput) { in.Note = "invalid\x00note" },
		"no-evidence":         func(in *TaskReviewInput) { in.EvidenceRefs = nil },
		"too-many-references": func(in *TaskReviewInput) { in.EvidenceRefs = []string{"a", "b", "c", "d", "e", "f"} },
		"nul-reference":       func(in *TaskReviewInput) { in.EvidenceRefs = []string{"invalid\x00reference"} },
		"conclusion":          func(in *TaskReviewInput) { in.Conclusion = "unlock" },
		"revision-overflow":   func(in *TaskReviewInput) { in.Revision = "18446744073709551616" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := review
			mutate(&invalid)
			if _, err := store.ReviewTask(ctx, ref, "operator-1", "invalid-input", invalid); !errors.Is(err, ErrInput) {
				t.Fatalf("invalid review accepted: %v", err)
			}
		})
	}
	saved, err := store.ReviewTask(ctx, ref, "operator-1", "review-1", review)
	if err != nil || saved.ActorID != "operator-1" || saved.RecordedAt.IsZero() {
		t.Fatalf("review was not recorded: %+v %v", saved, err)
	}
	if replay, err := store.ReviewTask(ctx, ref, "operator-1", "review-1", review); err != nil || !reflect.DeepEqual(replay, saved) {
		t.Fatalf("review redelivery not idempotent: %v", err)
	}
	changed := review
	changed.Note = "changed body"
	if _, err := store.ReviewTask(ctx, ref, "operator-1", "review-1", changed); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("idempotency collision accepted: %v", err)
	}
	changed = review
	changed.Revision = "0"
	if _, err := store.ReviewTask(ctx, ref, "operator-1", "stale-review", changed); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("stale revision accepted: %v", err)
	}
	status, err = store.InspectTask(ctx, ref)
	if err != nil || !status.RestartBlocked || status.LatestReview == nil || status.LatestReview.ReviewID != saved.ReviewID || status.Revision != review.Revision {
		t.Fatalf("review unlocked or changed the checkpoint: %+v %v", status, err)
	}
	after, _, _ := store.store.GetRecord(ctx, checkpointTable, checkpointKey(ref.TenantID, ref.AgentID, r.checkpoint.ID))
	if !reflect.DeepEqual(after, before) {
		t.Fatal("operator review changed execution history")
	}
	if err := store.CheckTaskRestart(ctx, ref.TenantID, ref.MissionID, ref.TaskID); !errors.Is(err, ErrUnsafeResume) {
		t.Fatalf("operator review cleared restart barrier: %v", err)
	}
	for _, change := range []func(*agentcore.RuntimeRequest){
		func(q *agentcore.RuntimeRequest) { q.RunID = "different-run" },
		func(q *agentcore.RuntimeRequest) { q.RunID, q.AgentID = "different-agent-run", "A000099" },
	} {
		q := r.request
		change(&q)
		if _, err := reopenTaskRunner(t, r, q, m); !errors.Is(err, ErrUnsafeResume) {
			t.Fatalf("new run bypassed uncertain operation: %v", err)
		}
	}
	if m.calls.Load() != 1 {
		t.Fatal("blocked recovery performed inference")
	}
	for _, change := range []func(*TaskRef){
		func(q *TaskRef) { q.TenantID = "another-tenant" },
		func(q *TaskRef) { q.MissionID = "another-mission" },
		func(q *TaskRef) { q.TaskID = "another-task" },
	} {
		q := ref
		change(&q)
		if _, err := store.ReviewTask(ctx, q, "operator-1", "cross-scope", review); !errors.Is(err, ErrCheckpoint) {
			t.Fatalf("review crossed task scope: %v", err)
		}
		view, err := store.InspectTask(ctx, q)
		if err == nil && view.Available {
			t.Fatal("inspection crossed task scope")
		}
		if err := store.CheckTaskRestart(ctx, q.TenantID, q.MissionID, q.TaskID); err != nil {
			t.Fatalf("pending barrier crossed task scope: %v", err)
		}
	}
}

func TestTaskBarrierClearsOnlyAfterWholeToolBatchIsSaved(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "task.db")
	_, database := openTestJournal(t, path)
	store, _ := NewSQLCheckpointStore(database)
	c := testCheckpoint(t)
	c.MissionID, c.TaskID = "mission-1", "task-1"
	c.ID = taskCheckpointID(agentcore.RuntimeRequest{TenantID: c.TenantID, AgentID: c.AgentID, RunID: c.RunID})
	c, err := store.Create(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	other := c
	other.ID, other.RunID, other.Owner = "another-checkpoint", "another-run", "other-owner"
	other, err = store.Create(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	c.Phase = CheckpointToolsPending
	c.Messages = append(c.Messages, reply("", "tool_calls", toolCall("call-a", `{"kb":"public"}`)))
	c, err = store.Save(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, other); !errors.Is(err, ErrUnsafeResume) {
		t.Fatalf("existing competing run bypassed barrier: %v", err)
	}
	stale := c
	stale.Revision--
	stale.Phase = CheckpointReady
	stale.Messages = append(stale.Messages, llm.ToolMessage("receipt", "call-a"))
	if _, err := store.Save(ctx, stale); !errors.Is(err, ErrCheckpoint) {
		t.Fatal("stale owner cleared barrier")
	}
	if err := store.CheckTaskRestart(ctx, c.TenantID, c.MissionID, c.TaskID); !errors.Is(err, ErrUnsafeResume) {
		t.Fatal("failed save cleared barrier")
	}
	c.Phase = CheckpointReady
	c.Messages = append(c.Messages, llm.ToolMessage("receipt", "call-a"))
	if _, err := store.Save(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckTaskRestart(ctx, c.TenantID, c.MissionID, c.TaskID); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	_, database = openTestJournal(t, path)
	store, _ = NewSQLCheckpointStore(database)
	status, err := store.InspectTask(ctx, TaskRef{c.TenantID, c.MissionID, c.TaskID, c.AgentID, c.RunID})
	if err != nil || status.RestartBlocked || status.Phase != "ready" {
		t.Fatalf("completed tool batch lost after reopening: %+v %v", status, err)
	}
}

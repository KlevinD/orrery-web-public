package agentkernel

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

const pendingTaskTable = "runtime_agent_kernel_pending_tasks"
const taskReviewTable = "runtime_agent_kernel_task_reviews"
const taskReviewHeadTable = "runtime_agent_kernel_task_review_heads"

type TaskRef struct {
	TenantID  string
	MissionID string
	TaskID    string
	AgentID   agentcore.AgentID
	RunID     agentcore.AgentRunID
}

func (ref TaskRef) valid() bool {
	return strings.TrimSpace(ref.TenantID) != "" && strings.TrimSpace(ref.MissionID) != "" && strings.TrimSpace(ref.TaskID) != "" && !ref.AgentID.IsZero() && !ref.RunID.IsZero()
}

type PendingToolView struct {
	CallID string `json:"call_id"`
	ToolID string `json:"tool_id"`
	State  string `json:"state"`
}

// TaskInspection deliberately excludes prompts, tool arguments/results, working
// memory, model credentials, owners and authorization fingerprints.
type TaskInspection struct {
	Available             bool              `json:"available"`
	Revision              string            `json:"revision"`
	Phase                 string            `json:"phase"`
	ModelCalls            int               `json:"model_calls"`
	ToolCalls             int               `json:"tool_calls"`
	Compactions           int               `json:"compactions"`
	Summarizing           bool              `json:"summarizing"`
	SummaryProcessedBytes int               `json:"summary_processed_bytes"`
	SummaryTotalBytes     int               `json:"summary_total_bytes"`
	Deadline              time.Time         `json:"deadline,omitempty"`
	UpdatedAt             time.Time         `json:"updated_at,omitempty"`
	RestartBlocked        bool              `json:"restart_blocked"`
	PendingTools          []PendingToolView `json:"pending_tools"`
	LatestReview          *TaskReview       `json:"latest_review,omitempty"`
}

type TaskReviewInput struct {
	Revision     string   `json:"revision"`
	Conclusion   string   `json:"conclusion"`
	Note         string   `json:"note"`
	EvidenceRefs []string `json:"evidence_refs"`
}

type TaskReview struct {
	TaskReviewInput
	ReviewID   string    `json:"review_id"`
	ActorID    string    `json:"actor_id"`
	RecordedAt time.Time `json:"recorded_at"`
}

func pendingTaskKey(tenant, mission, task string) string {
	key, _ := digest([]string{tenant, mission, task})
	return key
}

// Every new run for the same task is fenced at the tool-intent transaction as
// well as at the application entry point. Changing RunID or Agent cannot bypass
// an earlier uncertain external operation.
func maintainPendingTask(ctx context.Context, tx db.Tx, old, next Checkpoint) error {
	if next.MissionID == "" || next.TaskID == "" {
		return nil
	}
	key := pendingTaskKey(next.TenantID, next.MissionID, next.TaskID)
	if db.LockRecordKeyTx(ctx, tx, pendingTaskTable, key) != nil {
		return ErrCheckpoint
	}
	record, found, err := db.GetRecordTx(ctx, tx, pendingTaskTable, key)
	if err != nil {
		return ErrCheckpoint
	}
	if found && (record["checkpoint_id"] != next.ID || record["agent_id"] != next.AgentID.String()) {
		return ErrUnsafeResume
	}
	if next.Phase == CheckpointToolsPending {
		return db.PutRecordTx(ctx, tx, pendingTaskTable, key, db.Record{"checkpoint_id": next.ID, "agent_id": next.AgentID.String(), "run_id": next.RunID.String()})
	}
	if found && old.Phase == CheckpointToolsPending {
		return db.DeleteRecordTx(ctx, tx, pendingTaskTable, key)
	}
	return nil
}

func (s *SQLCheckpointStore) CheckTaskRestart(ctx context.Context, tenant, mission, task string) error {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(mission) == "" || strings.TrimSpace(task) == "" {
		return ErrInput
	}
	_, found, err := s.store.GetRecord(ctx, pendingTaskTable, pendingTaskKey(tenant, mission, task))
	if err != nil {
		return ErrCheckpoint
	}
	if found {
		return ErrUnsafeResume
	}
	return nil
}

func (s *SQLCheckpointStore) InspectTask(ctx context.Context, ref TaskRef) (TaskInspection, error) {
	if !ref.valid() {
		return TaskInspection{}, ErrInput
	}
	id := taskCheckpointID(agentcore.RuntimeRequest{TenantID: ref.TenantID, AgentID: ref.AgentID, RunID: ref.RunID})
	key := checkpointKey(ref.TenantID, ref.AgentID, id)
	out := TaskInspection{Phase: "not_started", Revision: "0", PendingTools: []PendingToolView{}}
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		// A row lock keeps the snapshot and latest review consistent with a writer.
		if db.LockRecordKeyTx(ctx, tx, checkpointTable, key) != nil {
			return ErrCheckpoint
		}
		raw, found, err := db.GetRecordTx(ctx, tx, checkpointTable, key)
		if err != nil {
			return ErrCheckpoint
		}
		_, blocked, err := db.GetRecordTx(ctx, tx, pendingTaskTable, pendingTaskKey(ref.TenantID, ref.MissionID, ref.TaskID))
		if err != nil {
			return ErrCheckpoint
		}
		out.RestartBlocked = blocked
		if !found {
			return nil
		}
		c, err := decodeCheckpoint(raw)
		if err != nil || c.TenantID != ref.TenantID || c.AgentID != ref.AgentID || c.RunID != ref.RunID || c.MissionID != ref.MissionID || c.TaskID != ref.TaskID {
			return ErrCheckpoint
		}
		out.Available, out.Phase, out.Revision = true, string(c.Phase), strconv.FormatUint(c.Revision, 10)
		out.ModelCalls, out.ToolCalls, out.Compactions = c.ModelCalls, c.ToolCalls, c.Compactions
		out.Deadline, out.UpdatedAt = c.Deadline, c.UpdatedAt
		if p := c.CompactionProgress; p != nil {
			out.Summarizing = true
			out.SummaryProcessedBytes = p.Offset
			out.SummaryTotalBytes = p.PayloadBytes
		}
		if c.Phase == CheckpointToolsPending {
			if len(c.Messages) == 0 || c.Messages[len(c.Messages)-1] == nil {
				return ErrCheckpoint
			}
			out.RestartBlocked = true
			for _, call := range c.Messages[len(c.Messages)-1].ToolCalls {
				view := PendingToolView{CallID: call.ID, ToolID: call.Function.Name, State: "not_recorded"}
				raw, found, err := db.GetRecordTx(ctx, tx, toolJournalTable, journalKey(ToolExecutionKey{TenantID: c.TenantID, RunID: c.RunID, CallID: call.ID}))
				if err != nil {
					return ErrCheckpoint
				}
				if found {
					execution, err := decodeExecution(raw)
					if err != nil || execution.AgentID != ref.AgentID {
						return ErrCheckpoint
					}
					view.ToolID = string(execution.ToolID)
					view.State = string(execution.State)
				}
				out.PendingTools = append(out.PendingTools, view)
			}
		}
		head, found, err := db.GetRecordTx(ctx, tx, taskReviewHeadTable, key)
		if err != nil {
			return ErrCheckpoint
		}
		if found {
			data, _ := json.Marshal(head)
			var review TaskReview
			if json.Unmarshal(data, &review) != nil {
				return ErrCheckpoint
			}
			out.LatestReview = &review
		}
		return nil
	})
	if err != nil {
		return TaskInspection{}, checkpointError(ctx, err)
	}
	return out, nil
}

// ReviewTask records an operator's evidence; it never edits execution receipts,
// supplies fabricated tool results, clears a pending barrier or schedules work.
func (s *SQLCheckpointStore) ReviewTask(ctx context.Context, ref TaskRef, actor, idempotency string, input TaskReviewInput) (TaskReview, error) {
	if !ref.valid() {
		return TaskReview{}, ErrInput
	}
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(idempotency) == "" || len(idempotency) > 200 || len(actor) > 200 || !utf8.ValidString(actor) || strings.ContainsRune(actor, 0) || !utf8.ValidString(input.Note) || strings.ContainsRune(input.Note, 0) || len(strings.TrimSpace(input.Note)) == 0 || len(input.Note) > 2000 || len(input.EvidenceRefs) == 0 || len(input.EvidenceRefs) > 5 {
		return TaskReview{}, ErrInput
	}
	if _, err := strconv.ParseUint(input.Revision, 10, 64); err != nil {
		return TaskReview{}, ErrInput
	}
	switch input.Conclusion {
	case "confirmed_executed", "confirmed_not_executed", "uncertain":
	default:
		return TaskReview{}, ErrInput
	}
	for _, ref := range input.EvidenceRefs {
		if strings.TrimSpace(ref) == "" || len(ref) > 512 || !utf8.ValidString(ref) || strings.ContainsRune(ref, 0) {
			return TaskReview{}, ErrInput
		}
	}
	id := taskCheckpointID(agentcore.RuntimeRequest{TenantID: ref.TenantID, AgentID: ref.AgentID, RunID: ref.RunID})
	key := checkpointKey(ref.TenantID, ref.AgentID, id)
	reviewKey, _ := digest([]string{key, actor, idempotency})
	var result TaskReview
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		if db.LockRecordKeyTx(ctx, tx, checkpointTable, key) != nil {
			return ErrCheckpoint
		}
		raw, found, err := db.GetRecordTx(ctx, tx, checkpointTable, key)
		if err != nil || !found {
			return ErrCheckpoint
		}
		c, err := decodeCheckpoint(raw)
		if err != nil || c.MissionID != ref.MissionID || c.TaskID != ref.TaskID || c.TenantID != ref.TenantID || c.AgentID != ref.AgentID || c.RunID != ref.RunID {
			return ErrCheckpoint
		}
		prior, found, err := db.GetRecordTx(ctx, tx, taskReviewTable, reviewKey)
		if err != nil {
			return ErrCheckpoint
		}
		if found {
			data, _ := json.Marshal(prior)
			if json.Unmarshal(data, &result) != nil || result.ActorID != actor || !reflect.DeepEqual(result.TaskReviewInput, input) {
				return ErrCheckpoint
			}
			return nil
		}
		if c.Phase != CheckpointToolsPending || strconv.FormatUint(c.Revision, 10) != input.Revision {
			return ErrCheckpoint
		}
		result = TaskReview{TaskReviewInput: input, ReviewID: reviewKey, ActorID: actor, RecordedAt: time.Now().UTC()}
		data, _ := json.Marshal(result)
		var record db.Record
		if json.Unmarshal(data, &record) != nil {
			return ErrCheckpoint
		}
		created, err := db.InsertRecordTx(ctx, tx, taskReviewTable, reviewKey, record)
		if err != nil || !created {
			return ErrCheckpoint
		}
		return db.PutRecordTx(ctx, tx, taskReviewHeadTable, key, record)
	})
	if err != nil {
		return TaskReview{}, checkpointError(ctx, err)
	}
	return result, nil
}

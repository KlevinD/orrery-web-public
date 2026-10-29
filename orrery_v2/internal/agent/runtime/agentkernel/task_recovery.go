package agentkernel

import (
	"context"
	"errors"
	"orrery_v2/internal/agent/llm"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/platform/db"
)

// Automatic recovery is a host policy for a single durable task/run, separate
// from explicit conversational resume, which may legitimately start a new turn.
func automaticTask(req agentcore.RuntimeRequest) bool {
	return req.TaskID != "" && req.RuntimeOptions.ProviderSpecific["agentcore_task_mode"] == "long" && req.RuntimeOptions.ProviderSpecific["agentcore_task_recovery"] == "automatic"
}

func taskCheckpointID(req agentcore.RuntimeRequest) string {
	hash, _ := digest([]string{req.TenantID, req.AgentID.String(), req.RunID.String()})
	return "agentcore-task-" + hash
}

type taskCheckpointStore interface {
	OpenTask(context.Context, Checkpoint) (Checkpoint, bool, error)
}

// OpenTask creates or claims a run's deterministic checkpoint in one transaction.
// Scope/model changes and uncertain tool batches never fall back to a fresh run.
func (s *SQLCheckpointStore) OpenTask(ctx context.Context, candidate Checkpoint) (opened Checkpoint, resumed bool, err error) {
	key := checkpointKey(candidate.TenantID, candidate.AgentID, candidate.ID)
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		if db.LockRecordKeyTx(ctx, tx, checkpointTable, key) != nil {
			return ErrCheckpoint
		}
		raw, found, e := db.GetRecordTx(ctx, tx, checkpointTable, key)
		if e != nil {
			return ErrCheckpoint
		}
		c := candidate
		c.Revision = 1
		if found {
			c, e = decodeCheckpoint(raw)
			if e != nil || c.ID != candidate.ID || c.TenantID != candidate.TenantID || c.AgentID != candidate.AgentID || c.RunID != candidate.RunID || c.ScopeHash != candidate.ScopeHash || c.ModelIdentity != candidate.ModelIdentity || c.MissionID != candidate.MissionID || c.TaskID != candidate.TaskID {
				return ErrCheckpoint
			}
			if !sameTaskPrompt(c, candidate) {
				return ErrTaskInputChanged
			}
			if c.Phase == CheckpointToolsPending {
				if err := claimApprovalWait(ctx, tx, c); err != nil {
					return err
				}
			}
			if c.Phase == CheckpointReady && !c.Deadline.IsZero() && !time.Now().Before(c.Deadline) {
				return context.DeadlineExceeded
			}
			c.Owner, c.Revision = candidate.Owner, c.Revision+1
		}
		c.UpdatedAt = time.Now().UTC()
		if c.Phase == CheckpointReady {
			if err := maintainPendingTask(ctx, tx, c, c); err != nil {
				return err
			}
		}
		record, e := checkpointRecord(c)
		if e != nil {
			return e
		}
		if db.PutRecordTx(ctx, tx, checkpointTable, key, record) != nil {
			return ErrCheckpoint
		}
		opened, resumed = c, found
		return nil
	})
	if err != nil {
		return Checkpoint{}, false, checkpointError(ctx, err)
	}
	return opened, resumed, nil
}

func (r *Runner) modelCallLimit() int {
	limit := r.config.MaxModelCalls
	if r.TurnTimeout() > 0 {
		limit = r.config.LongTaskMaxModelCalls
	}
	if requested := r.request.RuntimeOptions.MaxTurns; requested > 0 {
		limit = min(limit, requested)
	}
	return limit
}

func (r *Runner) toolCallLimit() int {
	if r.TurnTimeout() > 0 {
		return r.config.LongTaskMaxToolCalls
	}
	return r.config.MaxToolCalls
}

// Only a failed model attempt at a durable safe boundary is automatically
// recoverable. Policy, protocol, budget, cancellation and tool failures stop.
func (r *Runner) taskCanRecover(err error, caller context.Context) bool {
	return automaticTask(r.request) && caller.Err() == nil &&
		(errors.Is(err, ErrModel) || errors.Is(err, context.DeadlineExceeded)) &&
		r.checkpoint != nil && r.checkpoint.Phase == CheckpointReady &&
		!r.checkpoint.Deadline.IsZero() && time.Now().Before(r.checkpoint.Deadline) &&
		r.checkpoint.ModelCalls < r.modelCallLimit()
}

func (r *Runner) CanResumeTask() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.canResumeTask
}

func (r *Runner) replayTaskResult(ctx context.Context, input agentcore.SendCommand, emit kernelcompat.Emit) (agentcore.TurnResultEvent, error) {
	text, err := inputText(input)
	if err != nil || strings.TrimSpace(text) != r.firstPrompt || !sameInputImages(input.Content, r.request.InputMedia) {
		return agentcore.TurnResultEvent{}, ErrInput
	}
	if _, err := inputParts(input.Content); err != nil {
		return agentcore.TurnResultEvent{}, err
	}
	if err := ctx.Err(); err != nil {
		return agentcore.TurnResultEvent{}, err
	}
	if len(r.messages) == 0 {
		return agentcore.TurnResultEvent{}, ErrCheckpoint
	}
	last := r.messages[len(r.messages)-1]
	if last.Role != llm.Assistant || len(last.ToolCalls) != 0 || last.ResponseMeta == nil {
		return agentcore.TurnResultEvent{}, ErrCheckpoint
	}
	stop := agentcore.StopReasonEndTurn
	if last.ResponseMeta.FinishReason == "content_filter" {
		stop = agentcore.StopReasonRefused
	} else if last.ResponseMeta.FinishReason != "stop" || strings.TrimSpace(last.Content) == "" {
		return agentcore.TurnResultEvent{}, ErrCheckpoint
	}
	if last.Content != "" {
		if err := emit(agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "kernel-recovered-result", Text: last.Content}}); err != nil {
			return agentcore.TurnResultEvent{}, err
		}
	}
	// No new inference occurred; never emit a second billable usage delta.
	return agentcore.TurnResultEvent{ResultText: last.Content, Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: stop}}}, nil
}

package agentkernel

import (
	"context"
	"errors"
	"orrery_v2/internal/agent/llm"
	"time"

	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/platform/db"
)

// ApprovalWait is an unexecuted proposal, never a persisted authorization grant.
// Its arguments already live in Messages; this marker adds no second copy.
type ApprovalWait struct {
	CallID      string         `json:"call_id,omitempty"`
	Fingerprint string         `json:"fingerprint,omitempty"`
	Calls       []ApprovalCall `json:"calls,omitempty"`
	ExpiresAt   time.Time      `json:"expires_at"`
}

type ApprovalCall struct {
	CallID      string `json:"call_id"`
	Fingerprint string `json:"fingerprint"`
}

func (w *ApprovalWait) proposals() []ApprovalCall {
	if len(w.Calls) > 0 {
		return w.Calls
	}
	// Preserve the original single-call format, including older checkpoints.
	return []ApprovalCall{{CallID: w.CallID, Fingerprint: w.Fingerprint}}
}

func validateApprovalWait(c Checkpoint) error {
	wait := c.ApprovalWait
	if wait == nil {
		return nil
	}
	if c.Phase != CheckpointToolsPending || c.CompactionProgress != nil || len(c.Messages) < 2 || c.ModelCalls < 1 || wait.ExpiresAt.IsZero() || (!c.Deadline.IsZero() && wait.ExpiresAt.After(c.Deadline)) {
		return ErrCheckpoint
	}
	if len(wait.Calls) > 0 && (len(wait.Calls) < 2 || wait.CallID != "" || wait.Fingerprint != "") {
		return ErrCheckpoint
	}
	proposals := wait.proposals()
	last := c.Messages[len(c.Messages)-1]
	if last == nil || last.Role != llm.Assistant || len(last.ToolCalls) != len(proposals) || last.ResponseMeta == nil || last.ResponseMeta.FinishReason != "tool_calls" {
		return ErrCheckpoint
	}
	seen := map[string]bool{}
	for i, proposal := range proposals {
		if proposal.CallID == "" || seen[proposal.CallID] || last.ToolCalls[i].ID != proposal.CallID || !validDigest(proposal.Fingerprint) {
			return ErrCheckpoint
		}
		seen[proposal.CallID] = true
	}
	if _, err := contextBlocks(c.Messages[:len(c.Messages)-1]); err != nil {
		return ErrCheckpoint
	}
	return nil
}

func validateApprovalTransition(old, next Checkpoint) error {
	if old.ApprovalWait != nil {
		// Closing this window is irreversible, even if nothing was dispatched.
		// Ownership/revision is checked by Save before this transition.
		a, _ := digest(old.Messages)
		b, _ := digest(next.Messages)
		if next.ApprovalWait != nil || next.Phase != CheckpointToolsPending || next.Turn != old.Turn || next.ModelCalls != old.ModelCalls || next.ToolCalls != old.ToolCalls || next.FirstPrompt != old.FirstPrompt || a != b {
			return ErrCheckpoint
		}
	} else if next.ApprovalWait != nil {
		if old.Phase != CheckpointReady || next.Turn != old.Turn || next.ModelCalls != old.ModelCalls || next.ToolCalls != old.ToolCalls || len(next.Messages) != len(old.Messages)+1 {
			return ErrCheckpoint
		}
		a, _ := digest(old.Messages)
		b, _ := digest(next.Messages[:len(next.Messages)-1])
		if a != b {
			return ErrCheckpoint
		}
	}
	return nil
}

// A single database serializes ownership, the proposal and execution intents.
// Other/custom stores keep the original conservative pending-batch fence.
func (r *Runner) canPersistApprovalWait() bool {
	checkpoints, ok := r.config.Checkpoints.(*SQLCheckpointStore)
	journal, hasJournal := r.config.ToolJournal.(*SQLToolJournal)
	return r.checkpoint != nil && ok && hasJournal && checkpoints != nil && journal != nil && checkpoints.store == journal.store
}

func claimApprovalWait(ctx context.Context, tx db.Tx, c Checkpoint) error {
	if c.ApprovalWait == nil {
		return ErrUnsafeResume
	}
	if !time.Now().Before(c.ApprovalWait.ExpiresAt) {
		return context.DeadlineExceeded
	}
	if err := db.LockRecordKeyTx(ctx, tx, toolJournalTable, journalPrefix(c.TenantID, c.RunID)); err != nil {
		return ErrCheckpoint
	}
	for _, proposal := range c.ApprovalWait.proposals() {
		_, found, err := db.GetRecordTx(ctx, tx, toolJournalTable, journalKey(ToolExecutionKey{TenantID: c.TenantID, RunID: c.RunID, CallID: proposal.CallID}))
		if err != nil {
			return ErrCheckpoint
		}
		if found {
			return ErrUnsafeResume
		}
	}
	if err := checkRunExecutionBatch(ctx, tx, ToolExecution{ToolExecutionKey: ToolExecutionKey{TenantID: c.TenantID, RunID: c.RunID}}, nil); err != nil {
		if errors.Is(err, ErrRunUnresolved) {
			return ErrUnsafeResume
		}
		return ErrCheckpoint
	}
	return maintainPendingTask(ctx, tx, c, c)
}

func (r *Runner) batchRequiresApproval(calls []preparedCall) bool {
	for _, call := range calls {
		if r.requiresToolApproval(call) {
			return true
		}
	}
	return false
}

func (r *Runner) beginApprovalWait(ctx context.Context, messages []*llm.Message, calls []preparedCall) error {
	wait := &ApprovalWait{}
	for _, call := range calls {
		intent, err := r.toolExecution(call)
		if err != nil {
			return err
		}
		wait.Calls = append(wait.Calls, ApprovalCall{CallID: intent.CallID, Fingerprint: intent.Fingerprint})
	}
	if len(wait.Calls) == 1 {
		wait.CallID, wait.Fingerprint = wait.Calls[0].CallID, wait.Calls[0].Fingerprint
		wait.Calls = nil
	}
	expires := time.Now().Add(r.config.ApprovalTimeout)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(expires) {
		expires = deadline
	}
	wait.ExpiresAt = expires
	return r.saveCheckpointWithApproval(ctx, CheckpointToolsPending, messages, wait)
}

func (r *Runner) resolveApprovalWait(ctx context.Context, messages []*llm.Message, calls []preparedCall, emit kernelcompat.Emit) ([]preparedCall, error) {
	wait := r.checkpoint.ApprovalWait
	if !r.canPersistApprovalWait() || wait == nil || !r.batchRequiresApproval(calls) || len(wait.proposals()) != len(calls) {
		return nil, ErrUnsafeResume
	}
	for i, proposal := range wait.proposals() {
		intent, err := r.toolExecution(calls[i])
		if err != nil || intent.CallID != proposal.CallID || intent.Fingerprint != proposal.Fingerprint {
			return nil, ErrUnsafeResume
		}
	}
	approvalCtx, stop := context.WithDeadline(ctx, wait.ExpiresAt)
	defer stop()
	// Admission covers every member before requesting approval or executing reads.
	// No grant is persisted; a recovering owner must ask again for every write.
	err := r.authorizeApprovalBatch(approvalCtx, calls)
	if err == nil {
		for i, call := range calls {
			if r.requiresToolApproval(call) {
				if err = r.awaitToolApproval(approvalCtx, call, emit); err != nil {
					break
				}
				calls[i].approvalGranted = true
			}
		}
	}
	if err == nil {
		err = r.authorizeApprovalBatch(approvalCtx, calls)
	}
	// Close before dispatch, denial, cancellation or a failed approval handler.
	// A crashed owner cannot dispatch after a new owner has claimed the proposal.
	closeCtx, stopClose := context.WithTimeout(context.WithoutCancel(ctx), r.config.JournalTimeout)
	defer stopClose()
	if closeErr := r.saveCheckpoint(closeCtx, CheckpointToolsPending, messages); closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	if err != nil {
		return nil, err
	}
	return calls, nil
}

func (r *Runner) authorizeApprovalBatch(ctx context.Context, calls []preparedCall) error {
	policyCtx, stop := context.WithTimeout(ctx, r.config.ToolTimeout)
	defer stop()
	for _, call := range calls {
		if err := r.authorizeTool(policyCtx, call); err != nil {
			return err
		}
	}
	return policyCtx.Err()
}

func (r *Runner) resumeApprovalWait(ctx context.Context, messages []*llm.Message, emit kernelcompat.Emit) ([]*llm.Message, error) {
	if r.modelCalls >= r.modelCallLimit() || validateApprovalWait(*r.checkpoint) != nil {
		return nil, ErrUnsafeResume
	}
	batch := messages[len(messages)-1].ToolCalls
	if len(batch) > r.toolCallLimit()-r.toolCalls {
		return nil, ErrUnsafeResume
	}
	seen := map[string]bool{}
	for _, message := range messages[:len(messages)-1] {
		for _, call := range message.ToolCalls {
			seen[call.ID] = true
		}
	}
	calls, err := r.prepareCalls(batch, seen)
	if err != nil {
		return nil, err
	}
	calls, err = r.resolveApprovalWait(ctx, messages, calls, emit)
	if err != nil {
		return nil, err
	}
	messages, err = r.executeToolBatch(ctx, calls, messages, seen, emit)
	if err != nil {
		return nil, err
	}
	if err := r.saveCheckpoint(ctx, CheckpointReady, messages); err != nil {
		return nil, err
	}
	return messages, nil
}

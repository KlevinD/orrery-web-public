package agentkernel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type ToolExecutionState string

const (
	ToolExecutionStarted       ToolExecutionState = "started"
	ToolExecutionCompleted     ToolExecutionState = "completed"
	ToolExecutionFailed        ToolExecutionState = "failed"
	ToolExecutionUnknown       ToolExecutionState = "unknown"
	ToolExecutionNotDispatched ToolExecutionState = "not_dispatched"
)

type ToolExecutionKey struct {
	TenantID string               `json:"tenant_id"`
	RunID    agentcore.AgentRunID `json:"run_id"`
	CallID   string               `json:"call_id"`
}

// ToolExecution stores identity and digests, never tool arguments, results,
// credentials or raw provider errors. Started is an unresolved execution intent:
// after a crash it cannot establish whether the external operation took place.
type ToolExecution struct {
	ToolExecutionKey
	AgentID     agentcore.AgentID  `json:"agent_id"`
	ToolID      agentcore.ToolID   `json:"tool_id"`
	Fingerprint string             `json:"fingerprint"`
	ClaimID     string             `json:"claim_id"`
	State       ToolExecutionState `json:"state"`
	Reason      string             `json:"reason,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

// ToolJournal must durably and atomically claim a tenant/run/call identity.
// Existing records in every state block replay, including completed records.
// Any started/unknown execution also blocks different call IDs in the same run.
// This check and the new claim must be serialized across application instances.
// Finish may only change a started record held by the returned claim. Hosts
// must preserve this store across Engine instances and process restarts.
type ToolJournal interface {
	Begin(context.Context, ToolExecution) (ToolExecution, error)
	Finish(context.Context, ToolExecution, ToolExecutionState, string) error
}

// BatchToolJournal claims one fully validated batch atomically. Members may
// execute together, but every previous started/unknown call still blocks it.
// A failed claim leaves no new records. Existing calls are never replayed.
type BatchToolJournal interface {
	ToolJournal
	BeginBatch(context.Context, []ToolExecution) ([]ToolExecution, error)
}

type ToolExecutionPage struct {
	Items      []ToolExecution
	NextCursor string
}

func (key ToolExecutionKey) valid() bool {
	return strings.TrimSpace(key.TenantID) != "" && strings.TrimSpace(string(key.RunID)) != "" && strings.TrimSpace(key.CallID) != ""
}

func digest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", ErrConfiguration
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (r *Runner) toolExecution(call preparedCall) (ToolExecution, error) {
	// Only the bound identity and validated arguments define this fingerprint.
	// No raw arguments or authorization/credential material enter the journal.
	fingerprint, err := digest(struct {
		AgentID       agentcore.AgentID
		PrincipalKind agentcore.ExecutionPrincipalKind
		PrincipalID   string
		ToolID        agentcore.ToolID
		Input         map[string]any
	}{r.request.AgentID, r.request.ExecutionPrincipal.Kind, r.request.ExecutionPrincipal.ID, call.tool.binding.ToolID, call.input})
	return ToolExecution{ToolExecutionKey: ToolExecutionKey{TenantID: r.request.TenantID, RunID: r.request.RunID, CallID: call.call.ID},
		AgentID: r.request.AgentID, ToolID: call.tool.binding.ToolID, Fingerprint: fingerprint}, err
}

func finalToolState(state ToolExecutionState, reason string) bool {
	switch state {
	case ToolExecutionCompleted:
		return reason == ""
	case ToolExecutionFailed:
		return reason == "reported_failure"
	case ToolExecutionNotDispatched:
		return reason == "cancelled_before_dispatch" || reason == "capacity_before_dispatch" || reason == "blocked_before_dispatch"
	case ToolExecutionUnknown:
		switch reason {
		case "interrupted", "cancelled", "timeout", "transport_error", "invalid_result", "result_budget", "outcome_unrecorded":
			return true
		}
	}
	return false
}

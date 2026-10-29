package core

import (
	"context"
	"time"
)

// RuntimeExecutionPolicy is resolved by the in-process engine, after defaults
// and host limits. It contains no input, output, credentials or tool arguments.
type RuntimeExecutionPolicy struct {
	Protocol               string   `json:"protocol"`
	MaxModelCalls          int      `json:"max_model_calls"`
	MaxToolCalls           int      `json:"max_tool_calls"`
	MaxParallelTools       int      `json:"max_parallel_tools"`
	MaxConcurrentCallbacks int      `json:"max_concurrent_callbacks"`
	MaxContextBytes        int      `json:"max_context_bytes"`
	MaxResponseBytes       int      `json:"max_response_bytes"`
	ModelTimeoutNS         int64    `json:"model_timeout_ns"`
	ToolTimeoutNS          int64    `json:"tool_timeout_ns"`
	JournalTimeoutNS       int64    `json:"journal_timeout_ns"`
	ApprovalTimeoutNS      int64    `json:"approval_timeout_ns"`
	DefaultLongTimeoutNS   int64    `json:"default_long_timeout_ns"`
	MaxTransportTimeoutNS  int64    `json:"max_transport_timeout_ns"`
	AutoCompact            bool     `json:"auto_compact"`
	CompactAtBytes         int      `json:"compact_at_bytes"`
	CompactKeepMessages    int      `json:"compact_keep_messages"`
	CompactSummaryBytes    int      `json:"compact_summary_bytes"`
	PromptCache            bool     `json:"prompt_cache"`
	Checkpoints            bool     `json:"checkpoints"`
	ToolJournal            bool     `json:"tool_journal"`
	ExplicitToolScopes     bool     `json:"explicit_tool_scopes"`
	ParallelToolIDs        []string `json:"parallel_tool_ids"`
	ApprovalToolIDs        []string `json:"approval_tool_ids"`

	BlockedTaskReviewProtocol string `json:"blocked_task_review_protocol,omitempty"`
	MaxBlockedTaskReviews     int    `json:"max_blocked_task_reviews,omitempty"`
}

// RuntimeExecutionSnapshot identifies a resolved session, before its first
// inference. Counters are the restored counters, not a terminal usage report.
type RuntimeExecutionSnapshot struct {
	DependencyProtocol string                 `json:"dependency_protocol,omitempty"`
	Engine             string                 `json:"engine"`
	TenantID           string                 `json:"tenant_id"`
	AgentID            string                 `json:"agent_id"`
	RunID              string                 `json:"run_id"`
	Policy             RuntimeExecutionPolicy `json:"policy"`
	ModelIdentity      string                 `json:"model_identity_sha256,omitempty"`
	BackendSessionID   string                 `json:"backend_session_id,omitempty"`
	AutomaticRecovery  bool                   `json:"automatic_recovery"`
	Continuing         bool                   `json:"continuing"`
	ReplayCompleted    bool                   `json:"replay_completed"`
	ModelCalls         int                    `json:"restored_model_calls"`
	ToolCalls          int                    `json:"restored_tool_calls"`
	Compactions        int                    `json:"restored_compactions"`
	Deadline           time.Time              `json:"deadline,omitempty"`
	TransportTimeoutNS int64                  `json:"transport_timeout_ns"`
}

// This optional host-only hook is carried in context, never in request JSON,
// provider metadata, prompts or tools. Unknown engines supply no attestation.
type RuntimeExecutionTraceSession interface {
	RecordExecution(context.Context, RuntimeExecutionSnapshot) error
}

type runtimeExecutionObserverKey struct{}

func WithRuntimeExecutionObserver(ctx context.Context, observer func(context.Context, RuntimeExecutionSnapshot) error) context.Context {
	return context.WithValue(ctx, runtimeExecutionObserverKey{}, observer)
}

func ObserveRuntimeExecution(ctx context.Context, snapshot RuntimeExecutionSnapshot) error {
	if observer, ok := ctx.Value(runtimeExecutionObserverKey{}).(func(context.Context, RuntimeExecutionSnapshot) error); ok && observer != nil {
		return observer(ctx, snapshot)
	}
	return nil
}

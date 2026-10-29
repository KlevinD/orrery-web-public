package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
)

type SignalSink interface {
	EmitSignal(ctx context.Context, signal Signal) error
}

type CommitOptions struct {
	IdempotencyKey       string
	TenantID             string
	ContextSourceRefs    []ContextSourceRef
	ApprovedContinuation bool
}

type ResultCommitter interface {
	SubmitAgentResult(ctx context.Context, result AgentResult, opts CommitOptions) error
}

type Clock interface {
	Now() time.Time
}

type TraceRecorder interface {
	RecordTrace(ctx context.Context, input AgentInput, response RuntimeResponse) (string, error)
}

type NoopTraceRecorder struct{}

func (NoopTraceRecorder) RecordTrace(context.Context, AgentInput, RuntimeResponse) (string, error) {
	return "", nil
}

type TraceStore interface {
	PutTrace(ctx context.Context, key string, data []byte) (string, error)
}

type StoreTraceRecorder struct {
	Store      TraceStore
	Redactions []string
	Now        func() time.Time
	IndexSink  TraceIndexSink
}

func (r StoreTraceRecorder) RecordTrace(ctx context.Context, input AgentInput, response RuntimeResponse) (string, error) {
	if r.Store == nil {
		return "", fmt.Errorf("trace store is required")
	}
	payload := tracePayload{
		RunID:                 input.RunID.String(),
		AgentID:               input.ContextPack.AgentID.String(),
		MissionID:             string(missionIDFromInput(input)),
		TaskID:                string(taskIDFromInput(input)),
		ResponsibilityContext: traceResponsibilityContext(input.ContextPack.ResponsibilityContext),
		RawOutputRef:          response.RawOutputRef,
		Messages:              response.Messages,
		ToolCalls:             response.ToolCalls,
		Artifacts:             response.Artifacts,
		ProviderMetadata:      response.ProviderMetadata,
		RecordedAt:            time.Now().UTC(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal trace: %w", err)
	}
	redacted := redactTrace(data, r.Redactions)
	return r.Store.PutTrace(ctx, traceKeyForInput(input), redacted)
}

func traceKeyForInput(input AgentInput) string {
	runID := traceKeySegment(input.RunID.String())
	if conversationID := strings.TrimSpace(input.ContextPack.ConversationID); conversationID != "" {
		return path.Join("discussion-conversations", traceKeySegment(conversationID), "runs", runID, "runtime-trace.json")
	}
	if discussionID := strings.TrimSpace(string(input.ContextPack.DiscussionGroupID)); discussionID != "" {
		return path.Join("discussion-conversations", traceKeySegment(discussionID), "runs", runID, "runtime-trace.json")
	}
	return path.Join("agent-runs", runID, "runtime-trace.json")
}

func traceKeySegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("/", "_", "\\", "_").Replace(value)
	if value == "" || value == "." || value == ".." {
		return "unknown"
	}
	return value
}

type tracePayload struct {
	RunID                 string                         `json:"run_id"`
	AgentID               string                         `json:"agent_id"`
	MissionID             string                         `json:"mission_id"`
	TaskID                string                         `json:"task_id"`
	ResponsibilityContext *responsibilityTraceProjection `json:"responsibility_context,omitempty"`
	RawOutputRef          string                         `json:"raw_output_ref,omitempty"`
	Messages              []RuntimeMessage               `json:"messages,omitempty"`
	ToolCalls             []RuntimeToolCall              `json:"tool_calls,omitempty"`
	Artifacts             []ArtifactRef                  `json:"artifacts,omitempty"`
	ProviderMetadata      map[string]string              `json:"provider_metadata,omitempty"`
	RecordedAt            time.Time                      `json:"recorded_at"`
}

// responsibilityTraceProjection is the trace-safe authority tuple used to
// verify responsibility injection. It deliberately excludes prompt material,
// organization/person details, narrative policy content, evidence, and source
// references. Scope values are authorization identifiers, not tool bindings or
// credentials.
type responsibilityTraceProjection struct {
	ContextID                 string   `json:"context_id"`
	ResponsibilityID          string   `json:"responsibility_id"`
	ResponsibilityVersion     int64    `json:"responsibility_version"`
	PolicyID                  string   `json:"policy_id"`
	PolicyVersion             int64    `json:"policy_version"`
	AgentProfileID            string   `json:"agent_profile_id"`
	MissionID                 string   `json:"mission_id"`
	TaskID                    string   `json:"task_id"`
	AllowedTools              []string `json:"allowed_tools,omitempty"`
	AllowedDataScopes         []string `json:"allowed_data_scopes,omitempty"`
	PolicyToolScopes          []string `json:"policy_tool_scopes,omitempty"`
	PolicyDataScopes          []string `json:"policy_data_scopes,omitempty"`
	TaskPermissionScopes      []string `json:"task_permission_scopes,omitempty"`
	RequireExplicitToolScopes bool     `json:"require_explicit_tool_scopes"`
}

func traceResponsibilityContext(value *ResponsibilityContext) *responsibilityTraceProjection {
	if value == nil {
		return nil
	}
	return &responsibilityTraceProjection{
		ContextID:                 strings.TrimSpace(value.ContextID),
		ResponsibilityID:          strings.TrimSpace(value.ResponsibilityID),
		ResponsibilityVersion:     value.ResponsibilityVersion,
		PolicyID:                  strings.TrimSpace(value.PolicyID),
		PolicyVersion:             value.PolicyVersion,
		AgentProfileID:            strings.TrimSpace(value.AgentProfileID),
		MissionID:                 strings.TrimSpace(value.MissionID),
		TaskID:                    strings.TrimSpace(value.TaskID),
		AllowedTools:              traceScopeValues(value.AllowedTools),
		AllowedDataScopes:         traceScopeValues(value.AllowedDataScopes),
		PolicyToolScopes:          traceScopeValues(value.PolicyToolScopes),
		PolicyDataScopes:          traceScopeValues(value.PolicyDataScopes),
		TaskPermissionScopes:      traceScopeValues(value.TaskPermissionScopes),
		RequireExplicitToolScopes: value.RequireExplicitToolScopes,
	}
}

func traceScopeValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func redactTrace(data []byte, redactions []string) []byte {
	out := string(data)
	for _, secret := range redactions {
		if strings.TrimSpace(secret) == "" {
			continue
		}
		out = strings.ReplaceAll(out, secret, "[REDACTED]")
	}
	return []byte(out)
}

type Notifier interface {
	Notify(ctx context.Context, input AgentInput, result AgentResult) error
}

type NoopNotifier struct{}

func (NoopNotifier) Notify(context.Context, AgentInput, AgentResult) error {
	return nil
}

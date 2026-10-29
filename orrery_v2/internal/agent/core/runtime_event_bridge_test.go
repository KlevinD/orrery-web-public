package core

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRuntimeBridgeEveryCanonicalEventVariantValidatesAndRoutes(t *testing.T) {
	zero := 0
	events := []SessionEvent{
		{Kind: EventTurnStarted, TurnStarted: &TurnStartedEvent{Epoch: 1}},
		{Kind: EventCancel, Cancel: &CancelEvent{}},
		{Kind: EventMessageDelta, MessageDelta: &MessageDeltaEvent{ItemID: "m", Text: "hello"}},
		{Kind: EventThoughtDelta, ThoughtDelta: &ThoughtDeltaEvent{ItemID: "t", Text: "think"}},
		{Kind: EventToolCall, ToolCall: &ToolCallEvent{ToolUseID: "tool", Name: "read", Subagent: SubagentInline, Input: json.RawMessage(`{"path":"relative.txt"}`)}},
		{Kind: EventToolResult, ToolResult: &ToolResultEvent{ToolUseID: "tool", Content: []ToolResultContent{{Kind: ToolResultText, Text: "ok"}}}},
		{Kind: EventHeartbeat, Heartbeat: &HeartbeatEvent{}},
		{Kind: EventTurnResult, TurnResult: &TurnResultEvent{Epoch: 1, Outcome: TurnOutcome{Kind: TurnOutcomeEndTurn}}},
		{Kind: EventDetached, Detached: &DetachedEvent{Exit: &ExitStatus{Code: &zero}, RedactedSummary: "connection refused"}},
		{Kind: EventAdapterSpecific, AdapterSpecific: &AdapterSpecificEvent{Tag: "unknown", Summary: "unsupported notification"}},
		{Kind: EventPlan, Plan: &PlanEvent{Entries: []PlanEntry{{Content: "work", Status: PlanPending}}}},
		{Kind: EventPermission, Permission: &PermissionEvent{RequestID: "p", Kind: PermissionKindTool}},
		{Kind: EventPermissionResolved, PermissionResolved: &PermissionResolvedEvent{RequestID: "p", Kind: PermissionKindTool}},
		{Kind: EventPromptAccepted, PromptAccepted: &PromptAcceptedEvent{ClientMsgID: "m"}},
		{Kind: EventUsageDelta, UsageDelta: &UsageDeltaEvent{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}},
		{Kind: EventProvisioning, Provisioning: &ProvisioningEvent{Phase: ProvisioningToolsReady}},
		{Kind: EventRewound, Rewound: &RewoundEvent{ToTurn: 1}},
		{Kind: EventConfigChanged, ConfigChanged: &ConfigChangedEvent{Model: "model"}},
		{Kind: EventCatalogUpdated, CatalogUpdated: &CatalogUpdatedEvent{Models: []ModelInfo{{ID: "m", Name: "M"}}}},
		{Kind: EventSubagentUpdate, SubagentUpdate: &SubagentUpdateEvent{Ref: "s", Status: SubagentRunning}},
		{Kind: EventSubagentDetail, SubagentDetail: &SubagentDetailEvent{Ref: "s", Model: "m"}},
		{Kind: EventNotice, Notice: &NoticeEvent{Level: NoticeWarning, Message: "warning"}},
		{Kind: EventToolOutputDelta, ToolOutputDelta: &ToolOutputDeltaEvent{ItemID: "tool", Text: "out"}},
		{Kind: EventTurnDiffUpdated, TurnDiffUpdated: &TurnDiffUpdatedEvent{Diff: "diff --git a/a b/a"}},
		{Kind: EventItemStarted, ItemStarted: &ItemStartedEvent{ItemID: "m", Kind: ItemText}},
		{Kind: EventItemCompleted, ItemCompleted: &ItemCompletedEvent{ItemID: "m"}},
		{Kind: EventMessageFinalized, MessageFinalized: &MessageFinalizedEvent{Message: FinalizedMessage{ItemID: "m", Kind: ItemText, Content: "hello", Sequence: 1}}},
		{Kind: EventSnapshot, Snapshot: &SnapshotEvent{StateRepresentation: "idle", TurnGeneration: 1}},
		{Kind: EventLagged, Lagged: &LaggedEvent{Skipped: 1}},
		{Kind: EventBackendSuspended, BackendSuspended: &BackendSuspendedEvent{}},
		{Kind: EventCheckpointList, CheckpointList: &CheckpointListEvent{Entries: []CheckpointEntry{{ID: "c"}}}},
		{Kind: EventSessionInfo, SessionInfo: &SessionInfoEvent{ContextUsage: &ContextUsage{Used: 1, Max: 2}}},
		{Kind: EventBackendBound, BackendBound: &BackendBoundEvent{BackendSessionID: "backend"}},
	}
	for _, event := range events {
		if err := event.Validate(); err != nil {
			t.Fatalf("event %q rejected: %v", event.Kind, err)
		}
		copy := event.Clone()
		if err := copy.Validate(); err != nil || copy.Kind != event.Kind {
			t.Fatalf("event %q clone invalid: %#v, %v", event.Kind, copy, err)
		}
		if event.Class() == "" || event.PersistTier() == "" {
			t.Fatalf("event %q routing incomplete", event.Kind)
		}
	}
}

func TestRuntimeBridgeEventUnionAndSecurityFailClosed(t *testing.T) {
	tests := []SessionEvent{
		{},
		{Kind: "future", Heartbeat: &HeartbeatEvent{}},
		{Kind: EventHeartbeat, Heartbeat: &HeartbeatEvent{}, Cancel: &CancelEvent{}},
		{Kind: EventCancel, Heartbeat: &HeartbeatEvent{}},
		{Kind: EventTurnStarted, TurnStarted: &TurnStartedEvent{}},
		{Kind: EventToolCall, ToolCall: &ToolCallEvent{ToolUseID: "t", Name: "exec", Input: json.RawMessage(`{"env":{"TOKEN":"x"}}`)}},
		{Kind: EventToolCall, ToolCall: &ToolCallEvent{ToolUseID: "t", Name: "http", Input: json.RawMessage(`{"accessToken":"secret"}`)}},
		{Kind: EventToolCall, ToolCall: &ToolCallEvent{ToolUseID: "t", Name: "http", Input: json.RawMessage(`{"headers":{"Authorization":"secret"}}`)}},
		{Kind: EventToolCall, ToolCall: &ToolCallEvent{ToolUseID: "t", Name: "read", Input: json.RawMessage(`{"file_path":"C:\\\\secret.txt"}`)}},
		{Kind: EventToolResult, ToolResult: &ToolResultEvent{ToolUseID: "t", Content: []ToolResultContent{{Kind: ToolResultFilePath, Path: `C:\\secret.txt`}}}},
		{Kind: EventDetached, Detached: &DetachedEvent{RedactedSummary: "line one\nsecret"}},
		{Kind: EventAdapterSpecific, AdapterSpecific: &AdapterSpecificEvent{Tag: "raw", Summary: "line one\nraw"}},
	}
	for i, event := range tests {
		if err := event.Validate(); !IsRuntimeBridgeError(err, RuntimeBridgeInvalidPayload) {
			t.Fatalf("case %d error = %v", i, err)
		}
	}
}

func TestRuntimeBridgeEnvelopeRejectsUnknownSourceAndProjection(t *testing.T) {
	event := SessionEvent{Kind: EventHeartbeat, Heartbeat: &HeartbeatEvent{}}
	envelope := SessionEnvelope{SessionID: "s", Timestamp: time.Now().UTC(), Source: EventSourceProvider, Projection: EventProjectionCanonical, Event: event}
	if err := envelope.Validate(); err != nil {
		t.Fatal(err)
	}
	envelope.Source = "native-wire"
	if err := envelope.Validate(); err == nil {
		t.Fatal("unknown source must fail closed")
	}
}

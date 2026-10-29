package session

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

type Snapshot struct {
	SessionID      agentcore.SessionID               `json:"session_id"`
	State          State                             `json:"state"`
	CanSend        bool                              `json:"can_send"`
	HasActivity    bool                              `json:"has_activity"`
	CanQueue       bool                              `json:"can_queue"`
	CanCancel      bool                              `json:"can_cancel"`
	TurnGeneration uint64                            `json:"turn_generation"`
	Sequence       uint64                            `json:"sequence"`
	Capabilities   agentcore.Capabilities            `json:"capabilities"`
	Pending        []agentcore.PendingPermissionView `json:"pending_permissions,omitempty"`
	WorkflowRoster []WorkflowAgentState              `json:"workflow_roster,omitempty"`
	LastReason     *TransitionReason                 `json:"last_reason,omitempty"`
}

type TransitionReasonKind string

const (
	TransitionStarted             TransitionReasonKind = "started"
	TransitionCompleted           TransitionReasonKind = "completed"
	TransitionErrored             TransitionReasonKind = "errored"
	TransitionCancelled           TransitionReasonKind = "cancelled"
	TransitionPermissionRequested TransitionReasonKind = "permission_requested"
	TransitionPermissionResolved  TransitionReasonKind = "permission_resolved"
)

type TransitionReason struct {
	Kind           TransitionReasonKind     `json:"kind"`
	Outcome        *agentcore.TurnOutcome   `json:"outcome,omitempty"`
	Error          *ErrorReason             `json:"error,omitempty"`
	CancelReason   agentcore.CancelReason   `json:"cancel_reason,omitempty"`
	PermissionKind agentcore.PermissionKind `json:"permission_kind,omitempty"`
}

func NewSnapshot(sessionID agentcore.SessionID, state State, generation, sequence uint64, capabilities agentcore.Capabilities, pending []agentcore.PendingPermissionView, workflow []WorkflowAgentState, lastReason *TransitionReason) (Snapshot, error) {
	snapshot := Snapshot{
		SessionID:      sessionID,
		State:          state.Clone(),
		CanSend:        CanSend(state),
		HasActivity:    HasForegroundActivity(state) || BackgroundActive(workflow),
		CanQueue:       CanQueue(state, capabilities.AcceptsProactiveInput),
		CanCancel:      CanCancel(state),
		TurnGeneration: generation,
		Sequence:       sequence,
		Capabilities:   capabilities.Clone(),
		Pending:        clonePending(pending),
		WorkflowRoster: cloneWorkflowRoster(workflow),
		LastReason:     cloneTransitionReason(lastReason),
	}
	return snapshot, snapshot.Validate()
}

func (s Snapshot) Validate() error {
	if strings.TrimSpace(string(s.SessionID)) == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "snapshot session_id is required", false)
	}
	if err := s.State.Validate(); err != nil {
		return err
	}
	if err := s.Capabilities.Validate(); err != nil {
		return err
	}
	if s.CanSend != CanSend(s.State) || s.CanQueue != CanQueue(s.State, s.Capabilities.AcceptsProactiveInput) || s.CanCancel != CanCancel(s.State) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "snapshot derived state is inconsistent", false)
	}
	if s.HasActivity != (HasForegroundActivity(s.State) || BackgroundActive(s.WorkflowRoster)) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "snapshot activity projection is inconsistent", false)
	}
	seenPending := make(map[string]struct{}, len(s.Pending))
	for _, pending := range s.Pending {
		if strings.TrimSpace(pending.RequestID) == "" {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "pending permission request_id is required", false)
		}
		if _, exists := seenPending[pending.RequestID]; exists {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "duplicate pending permission request_id", false)
		}
		seenPending[pending.RequestID] = struct{}{}
		if len(pending.Questions) != 0 {
			permission := agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: pending.RequestID, Kind: agentcore.PermissionKindTool, ToolName: "AskUserQuestion", Questions: pending.Questions}}
			if err := permission.Validate(); err != nil {
				return err
			}
		}
	}
	seenWorkflow := make(map[string]struct{}, len(s.WorkflowRoster))
	for _, workflow := range s.WorkflowRoster {
		if strings.TrimSpace(workflow.Ref) == "" {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "workflow ref is required", false)
		}
		if _, exists := seenWorkflow[workflow.Ref]; exists {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "duplicate workflow ref", false)
		}
		seenWorkflow[workflow.Ref] = struct{}{}
		switch workflow.TaskStatus {
		case WorkflowTaskRunning, WorkflowTaskCompleted, WorkflowTaskFailed, WorkflowTaskStopped:
		default:
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "invalid workflow task status", false)
		}
	}
	if err := validateTransitionReason(s.LastReason); err != nil {
		return err
	}
	return nil
}

func validateTransitionReason(reason *TransitionReason) error {
	if reason == nil {
		return nil
	}
	switch reason.Kind {
	case TransitionStarted:
		if reason.Outcome != nil || reason.Error != nil || reason.CancelReason != "" || reason.PermissionKind != "" {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "started transition cannot carry terminal details", false)
		}
	case TransitionCompleted:
		if reason.Error != nil || reason.CancelReason != "" || reason.PermissionKind != "" {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "completed transition carries invalid details", false)
		}
		if reason.Outcome != nil {
			event := agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Outcome: *reason.Outcome}}
			if err := event.Validate(); err != nil {
				return err
			}
		}
	case TransitionErrored:
		if reason.Error == nil || reason.Outcome != nil || reason.CancelReason != "" || reason.PermissionKind != "" {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "errored transition requires only an error reason", false)
		}
		if err := Errored(*reason.Error).Validate(); err != nil {
			return err
		}
	case TransitionCancelled:
		if reason.CancelReason == "" || reason.Outcome != nil || reason.Error != nil || reason.PermissionKind != "" {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "cancelled transition requires only a cancel reason", false)
		}
	case TransitionPermissionRequested:
		if reason.PermissionKind == "" || reason.Outcome != nil || reason.Error != nil || reason.CancelReason != "" {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "permission transition requires only a permission kind", false)
		}
	case TransitionPermissionResolved:
		if reason.Outcome != nil || reason.Error != nil || reason.CancelReason != "" || reason.PermissionKind != "" {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "permission-resolved transition carries invalid details", false)
		}
	default:
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "unknown transition reason", false)
	}
	return nil
}

func (s Snapshot) Clone() Snapshot {
	out := s
	out.State = s.State.Clone()
	out.Capabilities = s.Capabilities.Clone()
	out.Pending = clonePending(s.Pending)
	out.WorkflowRoster = cloneWorkflowRoster(s.WorkflowRoster)
	out.LastReason = cloneTransitionReason(s.LastReason)
	return out
}

func cloneWorkflowRoster(source []WorkflowAgentState) []WorkflowAgentState {
	out := append([]WorkflowAgentState(nil), source...)
	for i := range out {
		out[i].Tokens = cloneUint64(out[i].Tokens)
		out[i].ToolCalls = cloneUint64(out[i].ToolCalls)
	}
	return out
}

func cloneTransitionReason(source *TransitionReason) *TransitionReason {
	if source == nil {
		return nil
	}
	out := *source
	if source.Outcome != nil {
		outcome := *source.Outcome
		if source.Outcome.StopReason != nil {
			stop := *source.Outcome.StopReason
			outcome.StopReason = &stop
		}
		out.Outcome = &outcome
	}
	if source.Error != nil {
		errorReason := *source.Error
		errorReason.APIErrorStatus = cloneUint16(source.Error.APIErrorStatus)
		out.Error = &errorReason
	}
	return &out
}

func cloneUint64(source *uint64) *uint64 {
	if source == nil {
		return nil
	}
	out := *source
	return &out
}

func clonePending(source []agentcore.PendingPermissionView) []agentcore.PendingPermissionView {
	out := make([]agentcore.PendingPermissionView, len(source))
	for i, pending := range source {
		out[i] = pending
		out[i].Questions = append([]agentcore.PermissionQuestion(nil), pending.Questions...)
		for questionIndex := range out[i].Questions {
			out[i].Questions[questionIndex].Options = append([]agentcore.PermissionQuestionOption(nil), pending.Questions[questionIndex].Options...)
		}
	}
	return out
}

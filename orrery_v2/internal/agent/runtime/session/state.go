// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package session

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

// Reference: aionui-session/state.rs@v0.1.52.

type StateKind string

const (
	StateStarting StateKind = "starting"
	StateRunning  StateKind = "running"
	StateError    StateKind = "error"
	StateIdle     StateKind = "idle"
)

type State struct {
	Kind     StateKind      `json:"kind"`
	Starting *StartingState `json:"starting,omitempty"`
	Running  *RunningState  `json:"running,omitempty"`
	Error    *ErrorState    `json:"error,omitempty"`
	Idle     *IdleState     `json:"idle,omitempty"`
}

type StartingState struct{}
type IdleState struct{}

type RunningState struct {
	SinceEpoch           uint64            `json:"since_epoch"`
	SawSubstantiveOutput bool              `json:"saw_substantive_output"`
	TerminalResultSeen   bool              `json:"terminal_result_seen"`
	RequiresAction       RequiresActionSet `json:"requires_action"`
	Subagents            []SubagentState   `json:"subagents,omitempty"`
}

type RequiresActionSet struct {
	WaitingOnApproval uint32 `json:"waiting_on_approval"`
	WaitingOnAuth     uint32 `json:"waiting_on_auth"`
}

type SubagentState struct {
	Ref       string                   `json:"ref"`
	Label     string                   `json:"label,omitempty"`
	Status    agentcore.SubagentStatus `json:"status"`
	ParentRef string                   `json:"parent_ref,omitempty"`
}

type WorkflowTaskStatus string

const (
	WorkflowTaskRunning   WorkflowTaskStatus = "running"
	WorkflowTaskCompleted WorkflowTaskStatus = "completed"
	WorkflowTaskFailed    WorkflowTaskStatus = "failed"
	WorkflowTaskStopped   WorkflowTaskStatus = "stopped"
)

func (s WorkflowTaskStatus) IsTerminal() bool { return s != WorkflowTaskRunning }

type WorkflowAgentState struct {
	Ref          string                      `json:"ref"`
	TaskStatus   WorkflowTaskStatus          `json:"task_status"`
	HasLifecycle bool                        `json:"has_lifecycle"`
	Label        string                      `json:"label,omitempty"`
	LoopState    agentcore.WorkflowLoopState `json:"loop_state,omitempty"`
	Model        string                      `json:"model,omitempty"`
	Tokens       *uint64                     `json:"tokens,omitempty"`
	ToolCalls    *uint64                     `json:"tool_calls,omitempty"`
	LastToolName string                      `json:"last_tool_name,omitempty"`
}

func BackgroundActive(roster []WorkflowAgentState) bool {
	for _, agent := range roster {
		if agent.HasLifecycle && !agent.TaskStatus.IsTerminal() {
			return true
		}
	}
	return false
}

type ErrorReasonKind string

const (
	ErrorReasonCrashed   ErrorReasonKind = "crashed"
	ErrorReasonEmptyTurn ErrorReasonKind = "empty_turn"
	ErrorReasonBackend   ErrorReasonKind = "backend"
)

type ErrorReason struct {
	Kind           ErrorReasonKind `json:"kind"`
	APIErrorStatus *uint16         `json:"api_error_status,omitempty"`
	Message        string          `json:"message,omitempty"`
}

type ErrorState struct {
	Reason ErrorReason `json:"reason"`
}

func Starting() State { return State{Kind: StateStarting, Starting: &StartingState{}} }
func Idle() State     { return State{Kind: StateIdle, Idle: &IdleState{}} }
func Running(epoch uint64) State {
	return State{Kind: StateRunning, Running: &RunningState{SinceEpoch: epoch}}
}
func Errored(reason ErrorReason) State {
	return State{Kind: StateError, Error: &ErrorState{Reason: reason}}
}

func (s State) Validate() error {
	count := 0
	for _, present := range []bool{s.Starting != nil, s.Running != nil, s.Error != nil, s.Idle != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "state must contain exactly one payload", false)
	}
	matched := (s.Kind == StateStarting && s.Starting != nil) || (s.Kind == StateRunning && s.Running != nil) || (s.Kind == StateError && s.Error != nil) || (s.Kind == StateIdle && s.Idle != nil)
	if !matched {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "state kind and payload do not match", false)
	}
	if s.Running != nil {
		if s.Running.SinceEpoch == 0 {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "running state requires a positive epoch", false)
		}
		seen := make(map[string]struct{}, len(s.Running.Subagents))
		for _, subagent := range s.Running.Subagents {
			if strings.TrimSpace(subagent.Ref) == "" || !validSubagentStatus(subagent.Status) {
				return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "invalid subagent state", false)
			}
			if _, exists := seen[subagent.Ref]; exists {
				return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "duplicate subagent ref", false)
			}
			seen[subagent.Ref] = struct{}{}
		}
	}
	if s.Error != nil {
		switch s.Error.Reason.Kind {
		case ErrorReasonCrashed, ErrorReasonEmptyTurn:
			if s.Error.Reason.APIErrorStatus != nil || s.Error.Reason.Message != "" {
				return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "non-backend error cannot carry backend details", false)
			}
		case ErrorReasonBackend:
			if len(s.Error.Reason.Message) > 4096 {
				return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "backend error message exceeds limit", false)
			}
		default:
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "invalid error reason", false)
		}
	}
	return nil
}

func (s State) Clone() State {
	out := s
	if s.Starting != nil {
		out.Starting = &StartingState{}
	}
	if s.Idle != nil {
		out.Idle = &IdleState{}
	}
	if s.Error != nil {
		copy := *s.Error
		if copy.Reason.APIErrorStatus != nil {
			status := *copy.Reason.APIErrorStatus
			copy.Reason.APIErrorStatus = &status
		}
		out.Error = &copy
	}
	if s.Running != nil {
		copy := *s.Running
		copy.Subagents = append([]SubagentState(nil), s.Running.Subagents...)
		out.Running = &copy
	}
	return out
}

func CanSend(state State) bool { return state.Kind == StateIdle }

func CanQueue(state State, acceptsProactiveInput bool) bool {
	return acceptsProactiveInput && state.Kind == StateRunning && !RequiresAction(state)
}

func CanCancel(state State) bool { return state.Kind == StateStarting || state.Kind == StateRunning }

func RequiresAction(state State) bool {
	return state.Running != nil && (state.Running.RequiresAction.WaitingOnApproval > 0 || state.Running.RequiresAction.WaitingOnAuth > 0)
}

func HasForegroundActivity(state State) bool {
	switch state.Kind {
	case StateStarting:
		return true
	case StateRunning:
		if !RequiresAction(state) {
			return true
		}
		for _, subagent := range state.Running.Subagents {
			if subagent.Status == agentcore.SubagentPendingInit || subagent.Status == agentcore.SubagentRunning {
				return true
			}
		}
	}
	return false
}

func IsUnrecoverableResumeError(reason ErrorReason) bool {
	if reason.Kind != ErrorReasonBackend {
		return false
	}
	return strings.Contains(reason.Message, "No conversation found") ||
		strings.Contains(reason.Message, "error_during_execution") ||
		strings.Contains(reason.Message, "no rollout found for thread id") ||
		strings.Contains(reason.Message, "thread not found:")
}

func validSubagentStatus(status agentcore.SubagentStatus) bool {
	switch status {
	case agentcore.SubagentPendingInit, agentcore.SubagentRunning, agentcore.SubagentInterrupted, agentcore.SubagentCompleted, agentcore.SubagentErrored, agentcore.SubagentShutdown:
		return true
	default:
		return false
	}
}

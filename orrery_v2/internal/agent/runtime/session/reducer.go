// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package session

import agentcore "orrery_v2/internal/agent/core"

// Reference: aionui-session/reducer.rs@v0.1.52.

type Transition struct {
	From  State  `json:"from"`
	To    State  `json:"to"`
	Epoch uint64 `json:"epoch"`
}

type StepResult struct {
	State       State
	Transitions []Transition
}

type externalPhase string

const (
	phaseStarting       externalPhase = "starting"
	phaseRunning        externalPhase = "running"
	phaseRequiresAction externalPhase = "requires_action"
	phaseError          externalPhase = "error"
	phaseIdle           externalPhase = "idle"
)

func Step(previous State, event agentcore.SessionEvent) (StepResult, error) {
	if err := previous.Validate(); err != nil {
		return StepResult{}, err
	}
	if err := event.Validate(); err != nil {
		return StepResult{}, err
	}
	state := previous.Clone()

	if event.Kind == agentcore.EventTurnStarted {
		epoch := event.TurnStarted.Epoch
		if state.Running != nil && epoch <= state.Running.SinceEpoch {
			return StepResult{State: state}, nil
		}
		return settle(previous, Running(epoch), epoch), nil
	}

	if state.Kind == StateIdle || state.Kind == StateError {
		return StepResult{State: state}, nil
	}

	if event.Kind == agentcore.EventCancel {
		return settle(previous, Idle(), anchorEpoch(previous)), nil
	}

	if state.Kind == StateStarting {
		switch event.Kind {
		case agentcore.EventDetached:
			return settle(previous, Errored(ErrorReason{Kind: ErrorReasonCrashed}), 0), nil
		case agentcore.EventTurnResult:
			if event.TurnResult.IsError {
				return settle(previous, Errored(ErrorReason{Kind: ErrorReasonBackend, APIErrorStatus: cloneUint16(event.TurnResult.APIErrorStatus), Message: event.TurnResult.ResultText}), 0), nil
			}
		}
		return StepResult{State: state}, nil
	}

	switch event.Kind {
	case agentcore.EventMessageDelta:
		if event.MessageDelta.Text != "" {
			state.Running.SawSubstantiveOutput = true
		}
		return StepResult{State: state}, nil
	case agentcore.EventThoughtDelta, agentcore.EventToolCall, agentcore.EventHeartbeat:
		return StepResult{State: state}, nil
	case agentcore.EventToolResult:
		state.Running.SawSubstantiveOutput = true
		return StepResult{State: state}, nil
	case agentcore.EventTurnResult:
		result := event.TurnResult
		if result.Epoch != 0 && result.Epoch < state.Running.SinceEpoch {
			return StepResult{State: state}, nil
		}
		var next State
		switch {
		case result.IsError:
			next = Errored(ErrorReason{Kind: ErrorReasonBackend, APIErrorStatus: cloneUint16(result.APIErrorStatus), Message: result.ResultText})
		case !state.Running.SawSubstantiveOutput && result.ResultText == "" && !isRefusal(result.Outcome):
			next = Errored(ErrorReason{Kind: ErrorReasonEmptyTurn})
		default:
			next = Idle()
		}
		return settle(previous, next, state.Running.SinceEpoch), nil
	case agentcore.EventDetached:
		if cleanExit(event.Detached.Exit) {
			return settle(previous, Errored(ErrorReason{Kind: ErrorReasonEmptyTurn}), state.Running.SinceEpoch), nil
		}
		return settle(previous, Errored(ErrorReason{Kind: ErrorReasonCrashed}), state.Running.SinceEpoch), nil
	case agentcore.EventPermission:
		if event.Permission.Kind == agentcore.PermissionKindAuth {
			if state.Running.RequiresAction.WaitingOnAuth < ^uint32(0) {
				state.Running.RequiresAction.WaitingOnAuth++
			}
		} else {
			if state.Running.RequiresAction.WaitingOnApproval < ^uint32(0) {
				state.Running.RequiresAction.WaitingOnApproval++
			}
		}
		return settle(previous, state, state.Running.SinceEpoch), nil
	case agentcore.EventPermissionResolved:
		if event.PermissionResolved.Kind == agentcore.PermissionKindAuth {
			if state.Running.RequiresAction.WaitingOnAuth > 0 {
				state.Running.RequiresAction.WaitingOnAuth--
			}
		} else if state.Running.RequiresAction.WaitingOnApproval > 0 {
			state.Running.RequiresAction.WaitingOnApproval--
		}
		return settle(previous, state, state.Running.SinceEpoch), nil
	case agentcore.EventSubagentUpdate:
		updateSubagent(state.Running, *event.SubagentUpdate)
		return StepResult{State: state}, nil
	default:
		return StepResult{State: state}, nil
	}
}

func settle(from, to State, epoch uint64) StepResult {
	result := StepResult{State: to}
	if phase(from) != phase(to) {
		result.Transitions = []Transition{{From: from.Clone(), To: to.Clone(), Epoch: epoch}}
	}
	return result
}

func phase(state State) externalPhase {
	switch state.Kind {
	case StateStarting:
		return phaseStarting
	case StateRunning:
		if RequiresAction(state) {
			return phaseRequiresAction
		}
		return phaseRunning
	case StateError:
		return phaseError
	default:
		return phaseIdle
	}
}

func anchorEpoch(state State) uint64 {
	if state.Running != nil {
		return state.Running.SinceEpoch
	}
	return 0
}

func cleanExit(exit *agentcore.ExitStatus) bool {
	return exit != nil && exit.Code != nil && *exit.Code == 0 && exit.Signal == nil
}

func isRefusal(outcome agentcore.TurnOutcome) bool {
	return outcome.Kind == agentcore.TurnOutcomeCompleted && outcome.StopReason != nil && outcome.StopReason.Kind == agentcore.StopReasonRefused
}

func updateSubagent(running *RunningState, event agentcore.SubagentUpdateEvent) {
	next := SubagentState{Ref: event.Ref, Label: event.Label, Status: event.Status, ParentRef: event.ParentRef}
	for i := range running.Subagents {
		if running.Subagents[i].Ref != next.Ref {
			continue
		}
		if running.Subagents[i].Status.IsTerminal() && !next.Status.IsTerminal() {
			return
		}
		running.Subagents[i] = next
		return
	}
	running.Subagents = append(running.Subagents, next)
}

func cloneUint16(value *uint16) *uint16 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

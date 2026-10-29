package session

import (
	"fmt"
	"math"
	"reflect"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

// These tests exercise Orrery's release contract in docs/runtime/runtime-bridge.md.
// They replace the former upstream parity suite; historical implementation
// provenance is recorded separately and is not erased by this replacement.
func TestSessionCompletionEvidenceMatrix(t *testing.T) {
	// Independent evidence sources may arrive in any combination. Thoughts alone
	// do not count as delivered work; a provider error takes priority over output.
	for mask := 0; mask < 64; mask++ {
		t.Run(fmt.Sprintf("evidence-%02x", mask), func(t *testing.T) {
			text, tool, thinking := mask&1 != 0, mask&2 != 0, mask&4 != 0
			finalText, refusal, failed := mask&8 != 0, mask&16 != 0, mask&32 != 0
			h := stateTrace{t: t, state: Running(21)}
			if thinking {
				h.apply(agentcore.SessionEvent{Kind: agentcore.EventThoughtDelta, ThoughtDelta: &agentcore.ThoughtDeltaEvent{ItemID: "release-thought", Text: "checking"}})
			}
			if text {
				h.apply(message("license report"))
			}
			if tool {
				h.apply(agentcore.SessionEvent{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{ToolUseID: "license-scan"}})
			}
			last := result(21, failed, "", agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeEndTurn})
			if finalText {
				last.TurnResult.ResultText = "release checked"
			}
			if refusal {
				last.TurnResult.Outcome = agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonRefused}}
			}
			h.apply(last)
			if failed {
				assertStateError(t, h.state, ErrorReasonBackend)
			} else if text || tool || finalText || refusal {
				if h.state.Kind != StateIdle {
					t.Fatalf("delivered completion = %#v", h.state)
				}
			} else {
				assertStateError(t, h.state, ErrorReasonEmptyTurn)
			}
		})
	}
}

func TestSessionCancellationAndGenerationIsolation(t *testing.T) {
	h := stateTrace{t: t, state: Starting()}
	h.apply(turnStarted(40))
	h.apply(message("partial release report"))
	cancel := agentcore.SessionEvent{Kind: agentcore.EventCancel, Cancel: &agentcore.CancelEvent{}}
	transition := h.apply(cancel)
	if len(transition.Transitions) != 1 || transition.Transitions[0].Epoch != 40 || h.state.Kind != StateIdle {
		t.Fatalf("cancel did not settle the current generation: %#v", transition)
	}
	terminal := h.state.Clone()
	h.apply(result(40, true, "cancelled provider", agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}))
	h.apply(agentcore.SessionEvent{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{}})
	if !reflect.DeepEqual(h.state, terminal) {
		t.Fatal("late shutdown changed cancellation")
	}
	h.apply(turnStarted(41))
	current := h.state.Clone()
	for _, event := range []agentcore.SessionEvent{
		turnStarted(40), turnStarted(41),
		result(40, true, "previous generation failed", agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}),
	} {
		step := h.apply(event)
		if len(step.Transitions) != 0 || !reflect.DeepEqual(h.state, current) {
			t.Fatalf("stale event %s changed generation", event.Kind)
		}
	}
	h.apply(result(41, false, "complete", agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeEndTurn}))
	if h.state.Kind != StateIdle {
		t.Fatalf("next generation did not complete: %#v", h.state)
	}
}

func TestSessionActionCounterBoundaries(t *testing.T) {
	bounds := []struct{ before, issued, resolved uint32 }{
		{0, 1, 0}, {1, 2, 0}, {math.MaxUint32 - 1, math.MaxUint32, math.MaxUint32 - 2}, {math.MaxUint32, math.MaxUint32, math.MaxUint32 - 1},
	}
	for _, kind := range []agentcore.PermissionKind{agentcore.PermissionKindAuth, agentcore.PermissionKindTool} {
		for _, bound := range bounds {
			for _, other := range []uint32{0, 2} {
				for _, issue := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%d/other-%d/issue-%t", kind, bound.before, other, issue), func(t *testing.T) {
						state := Running(33)
						state.Running.RequiresAction = RequiresActionSet{WaitingOnApproval: bound.before, WaitingOnAuth: other}
						if kind == agentcore.PermissionKindAuth {
							state.Running.RequiresAction = RequiresActionSet{WaitingOnApproval: other, WaitingOnAuth: bound.before}
						}
						h := stateTrace{t: t, state: state}
						var event agentcore.SessionEvent
						want := bound.resolved
						if issue {
							event = agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: "release-action", Kind: kind}}
							want = bound.issued
						} else {
							event = agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: "release-action", Kind: kind}}
						}
						step := h.apply(event)
						counts := h.state.Running.RequiresAction
						count, unchanged := counts.WaitingOnApproval, counts.WaitingOnAuth
						if kind == agentcore.PermissionKindAuth {
							count, unchanged = counts.WaitingOnAuth, counts.WaitingOnApproval
						}
						if count != want || unchanged != other {
							t.Fatalf("counter interference/overflow: %#v", counts)
						}
						changedPhase := (bound.before > 0 || other > 0) != (want > 0 || other > 0)
						if (len(step.Transitions) == 1) != changedPhase {
							t.Fatalf("unexpected UI phase transitions: %#v", step.Transitions)
						}
					})
				}
			}
		}
	}
}

func TestSessionChildCompletionCannotBeResurrected(t *testing.T) {
	for _, terminal := range []agentcore.SubagentStatus{agentcore.SubagentCompleted, agentcore.SubagentErrored, agentcore.SubagentInterrupted, agentcore.SubagentShutdown} {
		t.Run(string(terminal), func(t *testing.T) {
			h := stateTrace{t: t, state: Running(31)}
			update := func(ref string, status agentcore.SubagentStatus, parent string) agentcore.SessionEvent {
				return agentcore.SessionEvent{Kind: agentcore.EventSubagentUpdate, SubagentUpdate: &agentcore.SubagentUpdateEvent{Ref: ref, Status: status, ParentRef: parent}}
			}
			h.apply(update("scanner", agentcore.SubagentRunning, "release"))
			h.apply(update("reviewer", agentcore.SubagentRunning, "release"))
			h.apply(update("scanner", terminal, "release"))
			finished := h.state.Clone()
			for _, active := range []agentcore.SubagentStatus{agentcore.SubagentPendingInit, agentcore.SubagentRunning} {
				h.apply(update("scanner", active, "stale-parent"))
				if !reflect.DeepEqual(h.state, finished) {
					t.Fatal("completed child was resurrected or its sibling changed")
				}
			}
			h.apply(update("scanner", agentcore.SubagentShutdown, "final-parent"))
			if h.state.Running.Subagents[0].ParentRef != "final-parent" || h.state.Running.Subagents[1].Status != agentcore.SubagentRunning {
				t.Fatalf("terminal update affected sibling: %#v", h.state)
			}
		})
	}
}

func TestSessionTerminalStatesAbsorbProviderNoise(t *testing.T) {
	events := []agentcore.SessionEvent{
		message("late output"), result(19, true, "late failure", agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}),
		{Kind: agentcore.EventCancel, Cancel: &agentcore.CancelEvent{}},
		{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: "late", Kind: agentcore.PermissionKindAuth}},
		{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{}},
		{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "future-provider", Summary: "opaque"}},
	}
	for _, state := range []State{Idle(), Errored(ErrorReason{Kind: ErrorReasonCrashed}), Errored(ErrorReason{Kind: ErrorReasonBackend, Message: "provider refused"})} {
		h := stateTrace{t: t, state: state}
		for _, event := range events {
			step := h.apply(event)
			if len(step.Transitions) != 0 || !reflect.DeepEqual(h.state, state) {
				t.Fatalf("terminal state changed after %s", event.Kind)
			}
		}
		h.apply(turnStarted(20))
		if h.state.Kind != StateRunning || h.state.Running.SinceEpoch != 20 {
			t.Fatal("new turn failed to leave terminal state")
		}
	}
}

func TestSessionExitClassificationAndErrorOwnership(t *testing.T) {
	zero, nonzero, signal := 0, 7, 15
	for _, test := range []struct {
		state  State
		exit   *agentcore.ExitStatus
		reason ErrorReasonKind
	}{
		{Starting(), &agentcore.ExitStatus{Code: &zero}, ErrorReasonCrashed},
		{Running(9), &agentcore.ExitStatus{Code: &zero}, ErrorReasonEmptyTurn},
		{Running(9), &agentcore.ExitStatus{Code: &nonzero}, ErrorReasonCrashed},
		{Running(9), &agentcore.ExitStatus{Signal: &signal}, ErrorReasonCrashed},
		{Running(9), nil, ErrorReasonCrashed},
	} {
		h := stateTrace{t: t, state: test.state}
		h.apply(agentcore.SessionEvent{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{Exit: test.exit}})
		assertStateError(t, h.state, test.reason)
	}
	status := uint16(403)
	event := result(9, true, "access denied", agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed})
	event.TurnResult.APIErrorStatus = &status
	for _, state := range []State{Starting(), Running(9)} {
		h := stateTrace{t: t, state: state}
		step := h.apply(event)
		assertStateError(t, h.state, ErrorReasonBackend)
		*step.State.Error.Reason.APIErrorStatus = 500
		if status != 403 || *step.Transitions[0].To.Error.Reason.APIErrorStatus != 403 {
			t.Fatal("error status aliases event or transition")
		}
	}
}

func TestSessionInvalidInputsFailBeforeStateMutation(t *testing.T) {
	valid := Running(18)
	valid.Running.Subagents = []SubagentState{{Ref: "release-child", Status: agentcore.SubagentRunning}}
	before := valid.Clone()
	for _, event := range []agentcore.SessionEvent{{}, {Kind: agentcore.EventTurnStarted}, turnStarted(0), {Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{}}} {
		if _, err := Step(valid, event); err == nil {
			t.Fatalf("invalid event accepted: %#v", event)
		}
		if !reflect.DeepEqual(valid, before) {
			t.Fatal("invalid event mutated state")
		}
	}
	for _, state := range []State{{}, Running(0), {Kind: StateIdle, Idle: &IdleState{}, Starting: &StartingState{}}} {
		if _, err := Step(state, message("input")); err == nil {
			t.Fatalf("invalid state accepted: %#v", state)
		}
	}
}

func TestSessionWebControlsRespectActionAndActivity(t *testing.T) {
	for _, test := range []struct {
		state                         State
		send, queue, cancel, activity bool
	}{
		{Starting(), false, false, true, true}, {Idle(), true, false, false, false},
		{Errored(ErrorReason{Kind: ErrorReasonCrashed}), false, false, false, false},
		{Running(1), false, true, true, true},
	} {
		if CanSend(test.state) != test.send || CanQueue(test.state, true) != test.queue || CanQueue(test.state, false) || CanCancel(test.state) != test.cancel || HasForegroundActivity(test.state) != test.activity {
			t.Fatalf("control mismatch: %#v", test.state)
		}
	}
	for _, kind := range []agentcore.PermissionKind{agentcore.PermissionKindTool, agentcore.PermissionKindAuth} {
		for _, status := range []agentcore.SubagentStatus{agentcore.SubagentPendingInit, agentcore.SubagentRunning, agentcore.SubagentCompleted, agentcore.SubagentErrored, agentcore.SubagentInterrupted, agentcore.SubagentShutdown} {
			state := Running(4)
			if kind == agentcore.PermissionKindTool {
				state.Running.RequiresAction.WaitingOnApproval = 1
			} else {
				state.Running.RequiresAction.WaitingOnAuth = 1
			}
			if HasForegroundActivity(state) || CanQueue(state, true) || !CanCancel(state) {
				t.Fatal("blocked session controls are inconsistent")
			}
			state.Running.Subagents = []SubagentState{{Ref: "release-child", Status: status}}
			wantActivity := status == agentcore.SubagentPendingInit || status == agentcore.SubagentRunning
			if HasForegroundActivity(state) != wantActivity {
				t.Fatalf("child %s activity mismatch", status)
			}
		}
	}
}

type stateTrace struct {
	t     *testing.T
	state State
}

func (h *stateTrace) apply(event agentcore.SessionEvent) StepResult {
	h.t.Helper()
	before, input := h.state.Clone(), event.Clone()
	step, err := Step(h.state, event)
	if err != nil {
		h.t.Fatalf("%s: %v", event.Kind, err)
	}
	if !reflect.DeepEqual(before, h.state) || !reflect.DeepEqual(input, event) {
		h.t.Fatalf("%s mutated its input", event.Kind)
	}
	if err := step.State.Validate(); err != nil {
		h.t.Fatalf("%s produced invalid state: %v", event.Kind, err)
	}
	h.state = step.State
	return step
}

func assertStateError(t *testing.T, state State, reason ErrorReasonKind) {
	t.Helper()
	if state.Kind != StateError || state.Error == nil || state.Error.Reason.Kind != reason {
		t.Fatalf("state = %#v, want error %s", state, reason)
	}
}

func turnStarted(epoch uint64) agentcore.SessionEvent {
	return agentcore.SessionEvent{Kind: agentcore.EventTurnStarted, TurnStarted: &agentcore.TurnStartedEvent{Epoch: epoch}}
}

func message(text string) agentcore.SessionEvent {
	return agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "web-release-message", Text: text}}
}

func result(epoch uint64, failed bool, text string, outcome agentcore.TurnOutcome) agentcore.SessionEvent {
	return agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: epoch, IsError: failed, ResultText: text, Outcome: outcome}}
}

// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package conformance

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/session"
)

const SessionID agentcore.SessionID = "runtime-conformance"

// Harness is implemented by provider fixture drivers. Emit may translate a
// canonical scenario into native fixture input; the suite never reaches into a
// provider's reducer or mutable state.
type Harness interface {
	agentcore.SessionBackend
	Emit(uint64, agentcore.SessionEvent)
	EndEvents()
	DispatchCount() int
}

type Factory func(*testing.T) Harness

// RunSharedSuite exercises the failure and concurrency semantics every native
// provider must preserve through the common orchestrator and conversation layer.
func RunSharedSuite(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("duplicate command receipt", func(t *testing.T) {
		harness, orchestrator := open(t, factory, session.OrchestratorOptions{})
		defer orchestrator.Close()
		command := send("duplicate", "m-1", "hello")
		first, err := orchestrator.Dispatch(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}
		second, err := orchestrator.Dispatch(context.Background(), command)
		if err != nil || !reflect.DeepEqual(first, second) || harness.DispatchCount() != 1 {
			t.Fatalf("duplicate receipt=%#v/%v first=%#v dispatches=%d", second, err, first, harness.DispatchCount())
		}
		conflict := send("duplicate", "m-1", "changed")
		if _, err := orchestrator.Dispatch(context.Background(), conflict); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeCommandConflict) {
			t.Fatalf("conflict = %v", err)
		}
	})

	t.Run("double terminal absorbed", func(t *testing.T) {
		harness, orchestrator := open(t, factory, session.OrchestratorOptions{})
		defer orchestrator.Close()
		if _, err := orchestrator.Dispatch(context.Background(), send("turn-1", "m-1", "hello")); err != nil {
			t.Fatal(err)
		}
		harness.Emit(1, message("answer"))
		harness.Emit(1, turnResult(1, false, "", agentcore.TurnOutcomeEndTurn))
		waitFor(t, func() bool { return orchestrator.Snapshot().State.Kind == session.StateIdle })
		terminal, ok := orchestrator.LatestTerminal()
		if !ok || terminal.Reason.Kind != session.TransitionCompleted {
			t.Fatalf("first terminal = %#v/%v", terminal, ok)
		}
		harness.Emit(1, turnResult(1, true, "late provider failure", agentcore.TurnOutcomeFailed))
		time.Sleep(25 * time.Millisecond)
		after, _ := orchestrator.LatestTerminal()
		if !reflect.DeepEqual(terminal, after) || orchestrator.Snapshot().State.Kind != session.StateIdle {
			t.Fatalf("double terminal escaped absorption: %#v -> %#v", terminal, after)
		}
	})

	t.Run("stale result cannot end next turn", func(t *testing.T) {
		harness, orchestrator := open(t, factory, session.OrchestratorOptions{})
		defer orchestrator.Close()
		if !harness.Capabilities().SupportsMultiTurn {
			t.Skip("provider intentionally maps one backend session to one turn")
		}
		if _, err := orchestrator.Dispatch(context.Background(), send("turn-1", "m-1", "one")); err != nil {
			t.Fatal(err)
		}
		harness.Emit(1, message("done"))
		harness.Emit(1, turnResult(1, false, "", agentcore.TurnOutcomeEndTurn))
		waitFor(t, func() bool { return orchestrator.Snapshot().State.Kind == session.StateIdle })
		if _, err := orchestrator.Dispatch(context.Background(), send("turn-2", "m-2", "two")); err != nil {
			t.Fatal(err)
		}
		harness.Emit(1, turnResult(1, true, "stale", agentcore.TurnOutcomeFailed))
		time.Sleep(25 * time.Millisecond)
		snapshot := orchestrator.Snapshot()
		if snapshot.State.Kind != session.StateRunning || snapshot.State.Running.SinceEpoch != 2 {
			t.Fatalf("stale result ended current turn: %#v", snapshot.State)
		}
	})

	t.Run("lost prompt accepted remains unconfirmed", func(t *testing.T) {
		_, orchestrator := open(t, factory, session.OrchestratorOptions{})
		defer orchestrator.Close()
		conversation, err := session.NewConversationSessionFromSnapshot(SessionID, orchestrator, "m", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conversation.Send(context.Background(), []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "hello"}}); err != nil {
			t.Fatal(err)
		}
		pending := conversation.Pending()
		if len(pending) != 1 || pending[0].Status != session.MessageSent {
			t.Fatalf("missing PromptAccepted was treated as confirmation: %#v", pending)
		}
		restored, err := session.NewConversationSessionFromSnapshot(SessionID, orchestrator, "new", pending)
		if err != nil || restored.Pending()[0].Status != session.MessageSent {
			t.Fatalf("reload lost unconfirmed state: %#v/%v", restored, err)
		}
	})

	t.Run("permission refcount cannot underflow", func(t *testing.T) {
		harness, orchestrator := open(t, factory, session.OrchestratorOptions{})
		defer orchestrator.Close()
		if _, err := orchestrator.Dispatch(context.Background(), send("turn-1", "m-1", "hello")); err != nil {
			t.Fatal(err)
		}
		harness.Emit(1, permission("p1"))
		harness.Emit(1, permission("p2"))
		harness.Emit(1, resolved("p1"))
		waitFor(t, func() bool { return orchestrator.Snapshot().State.Running.RequiresAction.WaitingOnApproval == 1 })
		harness.Emit(1, resolved("p2"))
		harness.Emit(1, resolved("unknown"))
		waitFor(t, func() bool { return orchestrator.Snapshot().Sequence >= 6 })
		if count := orchestrator.Snapshot().State.Running.RequiresAction.WaitingOnApproval; count != 0 {
			t.Fatalf("permission counter underflow/failed to settle: %d", count)
		}
	})

	t.Run("provider drain precedes EOF", func(t *testing.T) {
		harness, orchestrator := open(t, factory, session.OrchestratorOptions{})
		defer orchestrator.Close()
		subscription := orchestrator.SubscribeEvents()
		defer subscription.Close()
		harness.Emit(0, notice("first"))
		harness.Emit(0, notice("second"))
		harness.EndEvents()
		events := receive(t, subscription.Events(), 3)
		if events[0].Event.Notice.Message != "first" || events[1].Event.Notice.Message != "second" || events[2].Event.Kind != agentcore.EventDetached {
			t.Fatalf("EOF ordering = %#v", events)
		}
	})

	t.Run("lag is explicit and recoverable", func(t *testing.T) {
		harness, orchestrator := open(t, factory, session.OrchestratorOptions{BroadcastCapacity: 2})
		defer orchestrator.Close()
		subscription := orchestrator.SubscribeEvents()
		defer subscription.Close()
		for i := 0; i < 32; i++ {
			harness.Emit(0, notice("burst"))
		}
		waitFor(t, func() bool { return orchestrator.Snapshot().Sequence == 32 })
		deadline := time.After(2 * time.Second)
		for {
			select {
			case envelope := <-subscription.Events():
				if envelope.Event.Kind == agentcore.EventLagged {
					if envelope.Event.Lagged.Skipped == 0 || orchestrator.Snapshot().Sequence != 32 {
						t.Fatalf("invalid lag recovery: %#v snapshot=%#v", envelope, orchestrator.Snapshot())
					}
					return
				}
			case <-deadline:
				t.Fatal("subscriber lag was silent")
			}
		}
	})

	t.Run("race preserves unique sequence and generation", func(t *testing.T) {
		harness, orchestrator := open(t, factory, session.OrchestratorOptions{BroadcastCapacity: 256})
		defer orchestrator.Close()
		subscription := orchestrator.SubscribeEvents()
		defer subscription.Close()
		const turns = 32
		var group sync.WaitGroup
		receipts := make([]agentcore.CommandReceipt, turns)
		errorsByIndex := make([]error, turns)
		for i := 0; i < turns; i++ {
			group.Add(1)
			go func(i int) {
				defer group.Done()
				receipts[i], errorsByIndex[i] = orchestrator.Dispatch(context.Background(), send("race-"+time.Unix(0, int64(i)).Format("150405.000000000"), "race-client-"+time.Unix(0, int64(i)).Format("150405.000000000"), "x"))
			}(i)
		}
		group.Wait()
		accepted := 0
		conflicts := 0
		for i, err := range errorsByIndex {
			if err == nil {
				if !receipts[i].Accepted {
					t.Fatalf("nil-error receipt was rejected: %#v", receipts[i])
				}
				accepted++
				continue
			}
			if agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeCommandConflict) {
				conflicts++
				continue
			}
			t.Fatalf("concurrent dispatch failed unexpectedly: %v", err)
		}
		expected := turns
		if !harness.Capabilities().SupportsQueue {
			expected = 1
			if conflicts != turns-1 {
				t.Fatalf("non-queueing provider accepted/conflicted %d/%d, want 1/%d", accepted, conflicts, turns-1)
			}
		}
		if accepted != expected {
			t.Fatalf("accepted concurrent turns = %d, want %d", accepted, expected)
		}
		events := receive(t, subscription.Events(), expected)
		seenSequence := make(map[uint64]struct{}, turns)
		seenGeneration := make(map[uint64]struct{}, turns)
		for _, envelope := range events {
			if envelope.Event.Kind != agentcore.EventTurnStarted {
				continue
			}
			seenSequence[envelope.Sequence] = struct{}{}
			seenGeneration[envelope.TurnGeneration] = struct{}{}
		}
		if len(seenSequence) != expected || len(seenGeneration) != expected || harness.DispatchCount() != turns {
			t.Fatalf("race lost uniqueness: seq=%d gen=%d dispatch=%d", len(seenSequence), len(seenGeneration), harness.DispatchCount())
		}
	})
}

func open(t *testing.T, factory Factory, options session.OrchestratorOptions) (Harness, *session.Orchestrator) {
	t.Helper()
	harness := factory(t)
	orchestrator, err := session.NewOrchestrator(SessionID, harness, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	return harness, orchestrator
}

func send(commandID, clientMsgID, text string) agentcore.RuntimeCommand {
	return agentcore.RuntimeCommand{CommandID: commandID, Kind: agentcore.RuntimeCommandSend, Send: &agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: text}}, Metadata: agentcore.CommandMetadata{ClientMsgID: clientMsgID}}}
}

func message(text string) agentcore.SessionEvent {
	return agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "message", Text: text}}
}

func turnResult(epoch uint64, isError bool, text string, outcome agentcore.TurnOutcomeKind) agentcore.SessionEvent {
	return agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: epoch, IsError: isError, ResultText: text, Outcome: agentcore.TurnOutcome{Kind: outcome}}}
}

func permission(id string) agentcore.SessionEvent {
	return agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: id, Kind: agentcore.PermissionKindTool}}
}

func resolved(id string) agentcore.SessionEvent {
	return agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: id, Kind: agentcore.PermissionKindTool}}
}

func notice(text string) agentcore.SessionEvent {
	return agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeInfo, Message: text}}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func receive(t *testing.T, channel <-chan agentcore.SessionEnvelope, count int) []agentcore.SessionEnvelope {
	t.Helper()
	out := make([]agentcore.SessionEnvelope, 0, count)
	for len(out) < count {
		select {
		case envelope, ok := <-channel:
			if !ok {
				t.Fatalf("event stream closed after %d/%d events", len(out), count)
			}
			out = append(out, envelope)
		case <-time.After(3 * time.Second):
			t.Fatalf("event timeout after %d/%d", len(out), count)
		}
	}
	return out
}

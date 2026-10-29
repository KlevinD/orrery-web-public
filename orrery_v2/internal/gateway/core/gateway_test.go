package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"orrery_v2/internal/gateway/protocol"
)

type failingRunner struct{}

func (failingRunner) RunTurn(context.Context, protocol.SubmitTurnInput) (<-chan protocol.Event, error) {
	return nil, errors.New("database password=secret")
}

type nilStreamRunner struct{}

func (nilStreamRunner) RunTurn(context.Context, protocol.SubmitTurnInput) (<-chan protocol.Event, error) {
	return nil, nil
}

func TestSubmitTurnClassifiesAndRedactsRunnerFailure(t *testing.T) {
	for _, gw := range []*Gateway{NewGateway(failingRunner{}), NewGateway(nil), NewGateway(nilStreamRunner{})} {
		var events []protocol.Event
		for event := range gw.SubmitTurn(context.Background(), protocol.SubmitTurnInput{SessionKey: "s1", RunID: "run-1"}) {
			events = append(events, event)
		}
		if len(events) != 1 || events[0].Code != protocol.ErrorCodeGatewayRuntimeUnavailable ||
			strings.Contains(events[0].Message, "password") {
			t.Fatalf("events = %+v", events)
		}
	}
}

type fakeRunner struct{}

func (fakeRunner) RunTurn(ctx context.Context, input protocol.SubmitTurnInput) (<-chan protocol.Event, error) {
	ch := make(chan protocol.Event, 3)
	go func() {
		defer close(ch)
		ch <- protocol.Event{Type: protocol.EventTurnStarted, RunID: input.RunID}
		ch <- protocol.Event{Type: protocol.EventAssistantTextDelta, Text: "hello"}
		ch <- protocol.Event{Type: protocol.EventTurnCompleted, FinishReason: "completed"}
	}()
	return ch, nil
}

func TestSubmitTurnStreamsRunnerEventsAndSnapshot(t *testing.T) {
	gw := NewGateway(fakeRunner{})
	input := protocol.SubmitTurnInput{SessionKey: "s1", ChannelKey: "test", Message: "hi"}

	var seen []protocol.Event
	for event := range gw.SubmitTurn(context.Background(), input) {
		seen = append(seen, event)
	}

	if len(seen) != 3 {
		t.Fatalf("events = %d, want 3", len(seen))
	}
	snap := gw.ActiveTurnSnapshot("s1")
	if snap.Active {
		t.Fatal("snapshot should be inactive after completion")
	}
	if len(snap.Events) != 3 {
		t.Fatalf("snapshot replay events = %d, want 3", len(snap.Events))
	}
}

func TestAbortTurnCancelsActiveTurn(t *testing.T) {
	gw := NewGateway(blockingRunner{})
	events := gw.SubmitTurn(context.Background(), protocol.SubmitTurnInput{
		SessionKey: "s1",
		ChannelKey: "test",
		Message:    "hi",
		RunID:      "run-1",
	})

	if first := <-events; first.Type != protocol.EventTurnStarted {
		t.Fatalf("first event = %s", first.Type)
	}
	if err := gw.AbortTurn("s1", "run-1"); err != nil {
		t.Fatalf("AbortTurn failed: %v", err)
	}
	for range events {
	}
	if gw.ActiveTurnSnapshot("s1").Active {
		t.Fatal("turn should be inactive after abort")
	}
}

type blockingRunner struct{}

func (blockingRunner) RunTurn(ctx context.Context, input protocol.SubmitTurnInput) (<-chan protocol.Event, error) {
	ch := make(chan protocol.Event)
	go func() {
		defer close(ch)
		ch <- protocol.Event{Type: protocol.EventTurnStarted, RunID: input.RunID}
		<-ctx.Done()
		ch <- protocol.Event{Type: protocol.EventError, Message: "aborted", Recoverable: true}
	}()
	return ch, nil
}

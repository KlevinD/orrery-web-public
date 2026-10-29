package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"orrery_v2/internal/gateway/protocol"
)

const replayLimit = 200

type Gateway struct {
	runner protocol.Runner

	mu     sync.Mutex
	turns  map[string]*activeTurn
	replay map[string][]protocol.Event
}

type activeTurn struct {
	runID  string
	cancel context.CancelFunc
}

func NewGateway(runner protocol.Runner) *Gateway {
	return &Gateway{
		runner: runner,
		turns:  make(map[string]*activeTurn),
		replay: make(map[string][]protocol.Event),
	}
}

func (g *Gateway) SubmitTurn(ctx context.Context, input protocol.SubmitTurnInput) <-chan protocol.Event {
	out := make(chan protocol.Event)
	if input.RunID == "" {
		runID, err := newRunID()
		if err != nil {
			go func() {
				defer close(out)
				out <- protocol.Event{
					Type: protocol.EventError, Code: protocol.ErrorCodeGatewayRuntimeUnavailable,
					Message: "Gateway runtime is temporarily unavailable.", Recoverable: true,
				}
			}()
			return out
		}
		input.RunID = runID
	}
	runCtx, cancel := context.WithCancel(ctx)

	g.mu.Lock()
	if existing := g.turns[input.SessionKey]; existing != nil {
		existing.cancel()
	}
	g.turns[input.SessionKey] = &activeTurn{runID: input.RunID, cancel: cancel}
	g.replay[input.SessionKey] = nil
	g.mu.Unlock()

	go func() {
		defer close(out)
		defer g.finishTurn(input.SessionKey, input.RunID)

		if g.runner == nil {
			g.emit(input.SessionKey, out, protocol.Event{
				Type: protocol.EventError, RunID: input.RunID, Code: protocol.ErrorCodeGatewayRuntimeUnavailable,
				Message: "Gateway runtime is temporarily unavailable.", Recoverable: true,
			})
			return
		}
		events, err := g.runner.RunTurn(runCtx, input)
		if err != nil {
			g.emit(input.SessionKey, out, protocol.Event{
				Type: protocol.EventError, RunID: input.RunID, Code: protocol.ErrorCodeGatewayRuntimeUnavailable,
				Message: "Gateway runtime is temporarily unavailable.", Recoverable: true,
			})
			return
		}
		if events == nil {
			g.emit(input.SessionKey, out, protocol.Event{
				Type: protocol.EventError, RunID: input.RunID, Code: protocol.ErrorCodeGatewayRuntimeUnavailable,
				Message: "Gateway runtime is temporarily unavailable.", Recoverable: true,
			})
			return
		}
		for event := range events {
			if event.RunID == "" {
				event.RunID = input.RunID
			}
			g.emit(input.SessionKey, out, event)
		}
	}()

	return out
}

func (g *Gateway) AbortTurn(sessionKey, runID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	turn := g.turns[sessionKey]
	if turn == nil {
		return nil
	}
	if runID != "" && turn.runID != runID {
		return fmt.Errorf("active run mismatch")
	}
	turn.cancel()
	return nil
}

func (g *Gateway) ActiveTurnSnapshot(sessionKey string) protocol.ActiveTurnSnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	events := append([]protocol.Event(nil), g.replay[sessionKey]...)
	snapshot := protocol.ActiveTurnSnapshot{
		SessionKey: sessionKey,
		Events:     events,
	}
	if turn := g.turns[sessionKey]; turn != nil {
		snapshot.Active = true
		snapshot.RunID = turn.runID
	}
	return snapshot
}

func (g *Gateway) emit(sessionKey string, out chan<- protocol.Event, event protocol.Event) {
	g.mu.Lock()
	events := append(g.replay[sessionKey], event)
	if len(events) > replayLimit {
		events = events[len(events)-replayLimit:]
	}
	g.replay[sessionKey] = events
	g.mu.Unlock()
	out <- event
}

func (g *Gateway) finishTurn(sessionKey, runID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if turn := g.turns[sessionKey]; turn != nil && turn.runID == runID {
		delete(g.turns, sessionKey)
	}
}

func newRunID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate gateway run id: %w", err)
	}
	return "run_" + hex.EncodeToString(buf[:]), nil
}

package kernelcompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimesession "orrery_v2/internal/agent/runtime/session"
)

type turn struct {
	generation      uint64
	ctx             context.Context
	cancel          context.CancelFunc
	stopLifetime    func() bool
	stopExpiry      func() bool
	cancelRequested bool
	terminal        bool
	emitErr         error
	messageItemID   string
	tools           map[string]bool
	permissions     map[string]bool
}

type Session struct {
	id         agentcore.SessionID
	runner     Runner
	config     Config
	ctx        context.Context
	cancel     context.CancelFunc
	events     chan agentcore.SessionEnvelope
	ledger     *runtimesession.CommandLedger
	mu         sync.Mutex
	closed     bool
	poisoned   bool
	sequence   uint64
	generation uint64
	active     *turn
	closeOnce  sync.Once
	closeDone  chan struct{}
	closeErr   error
}

func newSession(id agentcore.SessionID, runner Runner, config Config) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{id: id, runner: runner, config: config, ctx: ctx, cancel: cancel,
		events: make(chan agentcore.SessionEnvelope, config.EventCapacity),
		ledger: runtimesession.NewCommandLedger(256), closeDone: make(chan struct{})}
}

func (s *Session) Events() <-chan agentcore.SessionEnvelope { return s.events }
func (s *Session) Capabilities() agentcore.Capabilities {
	return capabilitiesForEngine(s.config.Engine)
}
func (s *Session) BackendSessionID() string {
	if source, ok := s.runner.(interface{ BackendSessionID() string }); ok {
		return source.BackendSessionID()
	}
	return ""
}

func (s *Session) CanResumeTask() bool {
	if source, ok := s.runner.(interface{ CanResumeTask() bool }); ok {
		return source.CanResumeTask()
	}
	return false
}
func (s *Session) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	if err := command.Validate(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	if !s.Capabilities().Supports(command) {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "managed kernel command or content is not supported", false)
	}
	raw, err := json.Marshal(command)
	if err != nil || len(raw) > int(s.Capabilities().PayloadLimitBytes) {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "managed kernel command exceeds the payload limit", false)
	}
	return s.ledger.Execute(ctx, command, func(ctx context.Context) (agentcore.CommandReceipt, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return agentcore.CommandReceipt{}, transportClosed()
		}
		if command.Kind == agentcore.RuntimeCommandCancel {
			// A cancellation arriving after a deadline or protocol failure must
			// not reclassify that failure as a successful user cancellation.
			if s.active != nil && !s.active.terminal && s.active.ctx.Err() == nil {
				s.active.cancelRequested = true
				s.active.cancel()
			}
			return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn}, nil
		}
		if command.Kind == agentcore.RuntimeCommandAnswerPermission {
			return s.answerPermissionLocked(ctx, *command.AnswerPermission)
		}
		if s.poisoned {
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "managed kernel session requires replacement after failure", false)
		}
		if s.active != nil {
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeQueueFull, "managed kernel turn is still running", true)
		}
		s.generation++
		// A dispatch acknowledgement deadline must not cancel an admitted turn.
		// Runtime cancellation is carried by an explicit command or session close.
		turnCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.config.TurnTimeout)
		current := &turn{generation: s.generation, ctx: turnCtx, cancel: cancel, tools: make(map[string]bool), permissions: make(map[string]bool)}
		current.stopLifetime = context.AfterFunc(s.ctx, cancel)
		s.active = current
		// The canonical orchestrator creates turn_started from the admission
		// receipt. Providers must not emit that orchestrator-owned event.
		if command.Send.Metadata.ClientMsgID != "" {
			if err := s.emitLocked(current.generation, agentcore.SessionEvent{Kind: agentcore.EventPromptAccepted, PromptAccepted: &agentcore.PromptAcceptedEvent{ClientMsgID: command.Send.Metadata.ClientMsgID}}); err != nil {
				cancel()
				current.stopLifetime()
				s.active = nil
				return agentcore.CommandReceipt{}, err
			}
		}
		current.stopExpiry = context.AfterFunc(turnCtx, func() { s.expire(current) })
		input := *command.Send
		input.Content = append([]agentcore.ContentBlock(nil), input.Content...)
		for i := range input.Content {
			input.Content[i].Data = append([]byte(nil), input.Content[i].Data...)
		}
		go s.run(current, input)
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: current.generation}, nil
	})
}

func (s *Session) run(current *turn, input agentcore.SendCommand) {
	var result agentcore.TurnResultEvent
	var runErr error
	defer func() {
		if recover() != nil {
			runErr = errors.New("managed kernel runner panicked")
		}
		s.finish(current, result, runErr)
	}()
	if runErr = current.ctx.Err(); runErr != nil {
		return
	}
	// Capture on the actual dispatch context, including sessions opened earlier
	// by prewarm. Opening a session alone is not proof of a task execution.
	if source, ok := s.runner.(interface {
		RuntimeExecutionSnapshot() agentcore.RuntimeExecutionSnapshot
	}); ok {
		snapshot := source.RuntimeExecutionSnapshot()
		snapshot.Policy.MaxTransportTimeoutNS = int64(s.config.MaxTurnTimeout)
		snapshot.TransportTimeoutNS = int64(s.config.TurnTimeout)
		if runErr = agentcore.ObserveRuntimeExecution(current.ctx, snapshot); runErr != nil {
			return
		}
	}
	result, runErr = s.runner.Run(current.ctx, input, func(event agentcore.SessionEvent) error {
		return s.forward(current, event)
	})
}

func (s *Session) forward(current *turn, event agentcore.SessionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.active != current || current.terminal {
		return transportClosed()
	}
	if current.ctx.Err() != nil {
		return current.ctx.Err()
	}
	var err error
	switch event.Kind {
	case agentcore.EventMessageDelta, agentcore.EventToolCall, agentcore.EventToolResult, agentcore.EventUsageDelta:
		err = event.Validate()
	case agentcore.EventPermission, agentcore.EventPermissionResolved:
		err = s.validatePermissionEventLocked(current, event)
	default:
		err = agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "managed kernel cannot emit an adapter-owned or unsupported event", false)
	}
	if err == nil {
		raw, marshalErr := json.Marshal(event)
		if marshalErr != nil || len(raw) > int(s.Capabilities().PayloadLimitBytes) {
			err = agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "managed kernel event exceeds the payload limit", false)
		}
	}
	if err == nil && event.Kind == agentcore.EventToolCall {
		if _, exists := current.tools[event.ToolCall.ToolUseID]; exists {
			err = agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "managed kernel tool call id was reused", false)
		} else {
			current.tools[event.ToolCall.ToolUseID] = true
		}
	}
	if err == nil && event.Kind == agentcore.EventToolResult {
		if !current.tools[event.ToolResult.ToolUseID] {
			err = agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "managed kernel tool result has no pending call", false)
		} else {
			current.tools[event.ToolResult.ToolUseID] = false
		}
	}
	if err == nil {
		err = s.emitLocked(current.generation, event)
	}
	if err == nil {
		switch event.Kind {
		case agentcore.EventMessageDelta:
			current.messageItemID = event.MessageDelta.ItemID
		case agentcore.EventToolCall:
			// Text preceding a tool call is progress, not the terminal answer.
			current.messageItemID = ""
		}
	}
	if err != nil {
		current.emitErr = err
		current.cancel()
	}
	return err
}

func (s *Session) expire(current *turn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.active != current || current.terminal {
		return
	}
	result := failedResult("managed kernel deadline exceeded")
	if current.cancelRequested {
		result = cancelledResult()
	} else {
		s.poisoned = true
		if current.emitErr != nil {
			result = failedResult("managed kernel emitted an invalid event")
		}
	}
	current.terminal = true
	result.Epoch = current.generation
	_ = s.emitLocked(current.generation, agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &result})
	// Keep the runner occupied until Run actually exits, even after its terminal
	// event. A runner that ignores cancellation must never overlap another turn.
}

func (s *Session) finish(current *turn, result agentcore.TurnResultEvent, runErr error) {
	current.stopExpiry()
	defer current.cancel()
	defer current.stopLifetime()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != current {
		return
	}
	s.active = nil
	if s.closed || current.terminal {
		return
	}
	switch {
	case current.cancelRequested:
		result = cancelledResult()
	case current.emitErr != nil:
		result = failedResult("managed kernel emitted an invalid event")
		s.poisoned = true
	case errors.Is(current.ctx.Err(), context.DeadlineExceeded):
		result = failedResult("managed kernel deadline exceeded")
		s.poisoned = true
	case runErr != nil:
		result = failedResult(executionFailureMessage(runErr))
	default:
		resultEvent := agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &result}
		validation := resultEvent.Validate()
		raw, marshalErr := json.Marshal(resultEvent)
		if validation != nil || marshalErr != nil || len(raw) > int(s.Capabilities().PayloadLimitBytes) || (result.IsError && result.Outcome.Kind != agentcore.TurnOutcomeFailed) {
			result = failedResult("managed kernel returned an invalid outcome")
		}
		if result.Outcome.Kind == agentcore.TurnOutcomeCompleted || result.Outcome.Kind == agentcore.TurnOutcomeEndTurn {
			for _, pending := range current.permissions {
				if pending {
					result = failedResult("managed kernel left a permission request unfinished")
					break
				}
			}
			for _, pending := range current.tools {
				if pending {
					result = failedResult("managed kernel left a tool call unfinished")
					break
				}
			}
		}
	}
	var final *agentcore.SessionEvent
	if (result.Outcome.Kind == agentcore.TurnOutcomeCompleted || result.Outcome.Kind == agentcore.TurnOutcomeEndTurn) && strings.TrimSpace(result.ResultText) != "" {
		itemID := current.messageItemID
		if itemID == "" {
			itemID = fmt.Sprintf("kernel-final-%d", current.generation)
		}
		// The adapter finalizes only the validated runner result. Streamed
		// progress, failed execution diagnostics and cancelled turns are not
		// public finals. Reuse the answer item so projection removes its deltas.
		event := agentcore.SessionEvent{Kind: agentcore.EventMessageFinalized, MessageFinalized: &agentcore.MessageFinalizedEvent{Message: agentcore.FinalizedMessage{
			ItemID: itemID, Kind: agentcore.ItemText, Content: result.ResultText, Sequence: s.sequence + 1,
		}}}
		raw, err := json.Marshal(event)
		if event.Validate() != nil || err != nil || len(raw) > int(s.Capabilities().PayloadLimitBytes) {
			result = failedResult("managed kernel returned an invalid final message")
		} else {
			final = &event
		}
	}
	current.terminal = true
	result.Epoch = current.generation
	result.IsError = result.Outcome.Kind == agentcore.TurnOutcomeFailed
	if result.IsError {
		s.poisoned = true
	}
	if final != nil {
		if err := s.emitLocked(current.generation, *final); err != nil {
			return
		}
	}
	_ = s.emitLocked(current.generation, agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &result})
}

// emitLocked serializes sequence allocation and channel close. Backpressure is
// bounded; failure closes the stream so the host observes failure rather than
// losing a terminal event while keeping a seemingly healthy session alive.
func (s *Session) emitLocked(generation uint64, event agentcore.SessionEvent) error {
	if s.closed {
		return transportClosed()
	}
	s.sequence++
	envelope := agentcore.SessionEnvelope{SessionID: s.id, TurnGeneration: generation, Sequence: s.sequence,
		Timestamp: time.Now().UTC(), Source: agentcore.EventSourceProvider, Projection: agentcore.EventProjectionCanonical, Event: event.Clone()}
	timer := time.NewTimer(s.config.BackpressureWait)
	defer timer.Stop()
	select {
	case s.events <- envelope:
		return nil
	case <-s.ctx.Done():
	case <-timer.C:
	}
	s.closed = true
	s.cancel()
	close(s.events)
	return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeQueueFull, "managed kernel event consumer is unavailable", false)
}

func (s *Session) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.closeOnce.Do(func() {
		s.cancel()
		s.mu.Lock()
		if !s.closed {
			s.closed = true
			close(s.events)
		}
		s.mu.Unlock()
		go func() {
			s.closeErr = closeRunner(context.Background(), s.runner, s.config.CloseTimeout)
			close(s.closeDone)
		}()
	})
	select {
	case <-s.closeDone:
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func failedResult(message string) agentcore.TurnResultEvent {
	return agentcore.TurnResultEvent{IsError: true, ResultText: message, Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}}
}

func cancelledResult() agentcore.TurnResultEvent {
	return agentcore.TurnResultEvent{Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCancelled, CancelReason: agentcore.CancelReasonUserCancel}}
}

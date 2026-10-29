// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package session

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"

	agentcore "orrery_v2/internal/agent/core"
)

// Reference: aionui-session/backend/conversation_session.rs@v0.1.52.

type MessageStatus string

const (
	MessageHeld     MessageStatus = "held"
	MessageSent     MessageStatus = "sent"
	MessageAccepted MessageStatus = "accepted"
	MessageError    MessageStatus = "error"
	MessageCanceled MessageStatus = "canceled"
)

type PendingMessage struct {
	ClientMsgID    string                           `json:"client_msg_id"`
	CommandID      string                           `json:"command_id"`
	Content        []agentcore.ContentBlock         `json:"content"`
	Status         MessageStatus                    `json:"status"`
	EnqueueOrdinal uint64                           `json:"enqueue_ordinal"`
	Receipt        *agentcore.CommandReceipt        `json:"receipt,omitempty"`
	DispatchError  agentcore.RuntimeBridgeErrorCode `json:"dispatch_error,omitempty"`
}

type ConversationSnapshot struct {
	Runtime Snapshot         `json:"runtime"`
	Pending []PendingMessage `json:"pending,omitempty"`
}

type ConversationSession struct {
	sessionID    agentcore.SessionID
	orchestrator *Orchestrator
	prefix       string

	pendingMu sync.RWMutex
	pending   []PendingMessage
	next      uint64
	commands  map[string]*conversationCommandState
	// Held messages may be dispatched later by the background flush engine.
	// Retain their caller values without retaining caller cancellation.
	dispatchContexts map[string]context.Context
	flushMu          sync.Mutex
	startOnce        sync.Once
}

type conversationCommandState struct {
	fingerprint string
	done        chan struct{}
	receipt     agentcore.CommandReceipt
	errorCode   agentcore.RuntimeBridgeErrorCode
	complete    bool
}

func NewConversationSession(sessionID agentcore.SessionID, orchestrator *Orchestrator) (*ConversationSession, error) {
	prefix := "m" + strings.ReplaceAll(uuid.NewString(), "-", "")
	return NewConversationSessionFromSnapshot(sessionID, orchestrator, prefix, nil)
}

// NewConversationSessionFromSnapshot reconstructs the conversation-owned queue.
// Production callers should pass an empty prefix so a fresh per-instance namespace
// is generated; a fixed prefix exists for deterministic conformance fixtures.
func NewConversationSessionFromSnapshot(sessionID agentcore.SessionID, orchestrator *Orchestrator, prefix string, pending []PendingMessage) (*ConversationSession, error) {
	if strings.TrimSpace(string(sessionID)) == "" || orchestrator == nil || orchestrator.sessionID != sessionID {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeSessionMismatch, "conversation and orchestrator session ids must match", false)
	}
	if strings.TrimSpace(prefix) == "" {
		prefix = "m" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	conversation := &ConversationSession{
		sessionID: sessionID, orchestrator: orchestrator, prefix: prefix,
		commands: make(map[string]*conversationCommandState), dispatchContexts: make(map[string]context.Context),
	}
	if err := conversation.restorePending(pending); err != nil {
		return nil, err
	}
	return conversation, nil
}

func (s *ConversationSession) SessionID() agentcore.SessionID { return s.sessionID }

func (s *ConversationSession) Send(ctx context.Context, content []agentcore.ContentBlock) (agentcore.CommandReceipt, error) {
	ordinal := s.reserveOrdinal()
	clientMsgID := fmt.Sprintf("%s-%d", s.prefix, ordinal)
	commandID := "send-" + clientMsgID
	return s.sendWithIDs(ctx, commandID, clientMsgID, ordinal, content)
}

// SendWithIDs lets an application retry with stable distributed idempotency and
// correlation identifiers. Repeating the same IDs and payload never enqueues or
// dispatches a second prompt.
func (s *ConversationSession) SendWithIDs(ctx context.Context, commandID, clientMsgID string, content []agentcore.ContentBlock) (agentcore.CommandReceipt, error) {
	return s.sendWithIDs(ctx, commandID, clientMsgID, 0, content)
}

func (s *ConversationSession) sendWithIDs(ctx context.Context, commandID, clientMsgID string, reservedOrdinal uint64, content []agentcore.ContentBlock) (agentcore.CommandReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	command := sendRuntimeCommand(commandID, clientMsgID, content)
	if err := command.Validate(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	if strings.TrimSpace(clientMsgID) == "" || len(clientMsgID) > 256 {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "client_msg_id is required and must not exceed 256 bytes", false)
	}
	fingerprint, err := command.Fingerprint()
	if err != nil {
		return agentcore.CommandReceipt{}, err
	}

	s.flushMu.Lock()
	s.pendingMu.Lock()
	for i := range s.pending {
		existing := &s.pending[i]
		if existing.CommandID == commandID {
			state := s.commands[commandID]
			if state == nil {
				s.pendingMu.Unlock()
				s.flushMu.Unlock()
				return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "conversation command ledger is missing", false)
			}
			if state.fingerprint != fingerprint {
				s.pendingMu.Unlock()
				s.flushMu.Unlock()
				return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "command id was already used with a different prompt", false)
			}
			s.pendingMu.Unlock()
			s.flushMu.Unlock()
			select {
			case <-state.done:
				if state.errorCode != "" {
					return state.receipt, agentcore.NewRuntimeBridgeError(state.errorCode, string(state.errorCode), false)
				}
				return state.receipt, nil
			case <-ctx.Done():
				return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCancelled, "duplicate prompt wait cancelled", false)
			}
		}
		if existing.ClientMsgID == clientMsgID {
			s.pendingMu.Unlock()
			s.flushMu.Unlock()
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "client message id is already in use", false)
		}
	}
	if limit := s.orchestrator.Capabilities().PendingLimit; limit > 0 && s.outstandingLocked() >= int(limit) {
		s.pendingMu.Unlock()
		s.flushMu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeQueueFull, "conversation pending limit reached", true)
	}
	ordinal := reservedOrdinal
	if ordinal == 0 {
		s.next++
		ordinal = s.next
	} else if ordinal > s.next {
		s.next = ordinal
	}
	s.pending = append(s.pending, PendingMessage{
		ClientMsgID:    clientMsgID,
		CommandID:      commandID,
		Content:        cloneContent(content),
		Status:         MessageHeld,
		EnqueueOrdinal: ordinal,
	})
	s.commands[commandID] = &conversationCommandState{fingerprint: fingerprint, done: make(chan struct{})}
	s.dispatchContexts[commandID] = context.WithoutCancel(ctx)
	s.pendingMu.Unlock()

	result, dispatched := s.tryFlushHeadLocked(ctx, false)
	s.flushMu.Unlock()
	if dispatched {
		s.completeCommand(commandID, result.receipt, result.err)
		return result.receipt, result.err
	}
	receipt := queuedReceipt(s.orchestrator.Snapshot().TurnGeneration)
	s.completeCommand(commandID, receipt, nil)
	return receipt, nil
}

type flushResult struct {
	receipt agentcore.CommandReceipt
	err     error
}

func (s *ConversationSession) tryFlushHead(ctx context.Context, fromRisingEdge bool) (flushResult, bool) {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	return s.tryFlushHeadLocked(ctx, fromRisingEdge)
}

func (s *ConversationSession) tryFlushHeadLocked(ctx context.Context, fromRisingEdge bool) (flushResult, bool) {
	snapshot := s.orchestrator.Snapshot()
	if !snapshot.CanSend && !snapshot.CanQueue {
		return flushResult{}, false
	}
	s.pendingMu.RLock()
	if !fromRisingEdge {
		for _, message := range s.pending {
			if message.Status == MessageSent {
				s.pendingMu.RUnlock()
				return flushResult{}, false
			}
		}
	}
	head := -1
	for i := range s.pending {
		if s.pending[i].Status != MessageHeld {
			continue
		}
		if head == -1 || s.pending[i].EnqueueOrdinal < s.pending[head].EnqueueOrdinal {
			head = i
		}
	}
	if head == -1 {
		s.pendingMu.RUnlock()
		return flushResult{}, false
	}
	message := clonePendingMessage(s.pending[head])
	dispatchCtx := s.dispatchContexts[message.CommandID]
	if dispatchCtx == nil {
		dispatchCtx = ctx
	}
	s.pendingMu.RUnlock()

	receipt, err := s.orchestrator.Dispatch(dispatchCtx, sendRuntimeCommand(message.CommandID, message.ClientMsgID, message.Content))
	s.pendingMu.Lock()
	delete(s.dispatchContexts, message.CommandID)
	for i := range s.pending {
		if s.pending[i].CommandID != message.CommandID {
			continue
		}
		copyReceipt := receipt
		s.pending[i].Receipt = &copyReceipt
		if s.pending[i].Status != MessageAccepted {
			switch {
			case err != nil:
				s.pending[i].Status = MessageError
				s.pending[i].DispatchError = runtimeErrorCode(err)
			case !receipt.Accepted:
				s.pending[i].Status = MessageError
				s.pending[i].DispatchError = agentcore.RuntimeBridgeBackendRejected
			default:
				s.pending[i].Status = MessageSent
			}
		}
		break
	}
	s.pendingMu.Unlock()
	return flushResult{receipt: receipt, err: err}, true
}

func (s *ConversationSession) RunFlushEngine(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	subscription := s.orchestrator.SubscribeSnapshots()
	defer subscription.Close()
	previousOpen := false
	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-subscription.Updates():
			if !ok {
				return
			}
			open := update.Snapshot.CanSend || update.Snapshot.CanQueue
			if open && !previousOpen {
				_, _ = s.tryFlushHead(ctx, true)
			}
			previousOpen = open
			if update.Lagged > 0 {
				_ = s.orchestrator.Reconnect(ctx)
			}
		}
	}
}

// RunEventProjection keeps pending confirmation state live even when no UI
// subscriber is attached. It also requests cached-truth recovery after lag.
func (s *ConversationSession) RunEventProjection(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	subscription := s.orchestrator.SubscribeEvents()
	defer subscription.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case envelope, ok := <-subscription.Events():
			if !ok {
				return
			}
			_, _ = s.HandleEnvelope(envelope)
			if envelope.Event.Kind == agentcore.EventLagged {
				_ = s.orchestrator.Reconnect(ctx)
			}
		}
	}
}

func (s *ConversationSession) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		go s.RunEventProjection(ctx)
		go s.RunFlushEngine(ctx)
	})
}

func (s *ConversationSession) HandleEnvelope(envelope agentcore.SessionEnvelope) (PendingMessage, bool) {
	if envelope.SessionID != s.sessionID || envelope.Event.Kind != agentcore.EventPromptAccepted || envelope.Event.PromptAccepted == nil {
		return PendingMessage{}, false
	}
	id := envelope.Event.PromptAccepted.ClientMsgID
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for i := range s.pending {
		if s.pending[i].ClientMsgID != id {
			continue
		}
		if s.pending[i].Status == MessageError || s.pending[i].Status == MessageCanceled {
			return PendingMessage{}, false
		}
		s.pending[i].Status = MessageAccepted
		s.pending[i].DispatchError = ""
		return clonePendingMessage(s.pending[i]), true
	}
	return PendingMessage{}, false
}

func (s *ConversationSession) CancelHeld(clientMsgID string) bool {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for i := range s.pending {
		if s.pending[i].ClientMsgID == clientMsgID && s.pending[i].Status == MessageHeld {
			delete(s.dispatchContexts, s.pending[i].CommandID)
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return true
		}
	}
	return false
}

func (s *ConversationSession) CancelAllOutstanding() int {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	canceled := 0
	for i := range s.pending {
		if s.pending[i].Status == MessageHeld || s.pending[i].Status == MessageSent {
			s.pending[i].Status = MessageCanceled
			delete(s.dispatchContexts, s.pending[i].CommandID)
			canceled++
		}
	}
	return canceled
}

func (s *ConversationSession) Pending() []PendingMessage {
	s.pendingMu.RLock()
	defer s.pendingMu.RUnlock()
	out := make([]PendingMessage, len(s.pending))
	for i := range s.pending {
		out[i] = clonePendingMessage(s.pending[i])
	}
	return out
}

func (s *ConversationSession) Snapshot() ConversationSnapshot {
	return ConversationSnapshot{Runtime: s.orchestrator.Snapshot(), Pending: s.Pending()}
}

func (s *ConversationSession) SubscribeEvents() *EventSubscription {
	return s.orchestrator.SubscribeEvents()
}

func (s *ConversationSession) SubscribeSnapshots() *SnapshotSubscription {
	return s.orchestrator.SubscribeSnapshots()
}

func (s *ConversationSession) Reconnect(ctx context.Context) error {
	return s.orchestrator.Reconnect(ctx)
}

func (s *ConversationSession) Capabilities() agentcore.Capabilities {
	return s.orchestrator.Capabilities()
}

func (s *ConversationSession) PendingPermissionRequests() []agentcore.PendingPermissionView {
	return s.orchestrator.PendingPermissionRequests()
}

func (s *ConversationSession) LatestTerminal() (TerminalRecord, bool) {
	return s.orchestrator.LatestTerminal()
}

func (s *ConversationSession) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	return s.orchestrator.Dispatch(ctx, command)
}

func (s *ConversationSession) Cancel(ctx context.Context, target agentcore.CancelTargetKind, toolID, reason string) (agentcore.CommandReceipt, error) {
	return s.orchestrator.Dispatch(ctx, agentcore.RuntimeCommand{
		CommandID: "cancel-" + uuid.NewString(),
		Kind:      agentcore.RuntimeCommandCancel,
		Cancel:    &agentcore.CancelCommand{Target: target, ToolID: toolID, Reason: reason},
	})
}

func (s *ConversationSession) reserveOrdinal() uint64 {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	s.next++
	return s.next
}

func (s *ConversationSession) outstandingLocked() int {
	count := 0
	for _, message := range s.pending {
		if message.Status == MessageHeld || message.Status == MessageSent {
			count++
		}
	}
	return count
}

func (s *ConversationSession) restorePending(pending []PendingMessage) error {
	seenIDs := make(map[string]struct{}, len(pending))
	seenCommands := make(map[string]struct{}, len(pending))
	seenOrdinals := make(map[uint64]struct{}, len(pending))
	for _, message := range pending {
		if strings.TrimSpace(message.ClientMsgID) == "" || strings.TrimSpace(message.CommandID) == "" || message.EnqueueOrdinal == 0 {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "restored pending messages require ids and ordinal", false)
		}
		switch message.Status {
		case MessageHeld, MessageSent, MessageAccepted, MessageError, MessageCanceled:
		default:
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "restored pending message has invalid status", false)
		}
		if err := sendRuntimeCommand(message.CommandID, message.ClientMsgID, message.Content).Validate(); err != nil {
			return err
		}
		if message.Receipt != nil {
			if err := message.Receipt.Validate(); err != nil {
				return err
			}
		}
		if _, duplicate := seenIDs[message.ClientMsgID]; duplicate {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "duplicate restored client message id", false)
		}
		if _, duplicate := seenCommands[message.CommandID]; duplicate {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "duplicate restored command id", false)
		}
		if _, duplicate := seenOrdinals[message.EnqueueOrdinal]; duplicate {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "duplicate restored enqueue ordinal", false)
		}
		seenIDs[message.ClientMsgID] = struct{}{}
		seenCommands[message.CommandID] = struct{}{}
		seenOrdinals[message.EnqueueOrdinal] = struct{}{}
		if message.EnqueueOrdinal > s.next {
			s.next = message.EnqueueOrdinal
		}
		s.pending = append(s.pending, clonePendingMessage(message))
		fingerprint, _ := sendRuntimeCommand(message.CommandID, message.ClientMsgID, message.Content).Fingerprint()
		receipt := queuedReceipt(s.orchestrator.Snapshot().TurnGeneration)
		if message.Receipt != nil {
			receipt = *message.Receipt
		}
		state := &conversationCommandState{fingerprint: fingerprint, done: make(chan struct{}), receipt: receipt, errorCode: message.DispatchError, complete: true}
		close(state.done)
		s.commands[message.CommandID] = state
	}
	return nil
}

func (s *ConversationSession) completeCommand(commandID string, receipt agentcore.CommandReceipt, err error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	state := s.commands[commandID]
	if state == nil || state.complete {
		return
	}
	state.receipt = receipt
	state.errorCode = runtimeErrorCodeOrEmpty(err)
	state.complete = true
	close(state.done)
}

func queuedReceipt(generation uint64) agentcore.CommandReceipt {
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionQueued, TurnGeneration: generation}
}

func sendRuntimeCommand(commandID, clientMsgID string, content []agentcore.ContentBlock) agentcore.RuntimeCommand {
	return agentcore.RuntimeCommand{
		CommandID: commandID,
		Kind:      agentcore.RuntimeCommandSend,
		Send:      &agentcore.SendCommand{Content: cloneContent(content), Metadata: agentcore.CommandMetadata{ClientMsgID: clientMsgID}},
	}
}

func cloneContent(content []agentcore.ContentBlock) []agentcore.ContentBlock {
	out := append([]agentcore.ContentBlock(nil), content...)
	for i := range out {
		out[i].Data = append([]byte(nil), content[i].Data...)
	}
	return out
}

func clonePendingMessage(message PendingMessage) PendingMessage {
	out := message
	out.Content = cloneContent(message.Content)
	if message.Receipt != nil {
		receipt := *message.Receipt
		out.Receipt = &receipt
	}
	return out
}

func runtimeErrorCode(err error) agentcore.RuntimeBridgeErrorCode {
	for _, code := range []agentcore.RuntimeBridgeErrorCode{
		agentcore.RuntimeBridgeInvalidCommand,
		agentcore.RuntimeBridgeInvalidPayload,
		agentcore.RuntimeBridgeUnsupportedCommand,
		agentcore.RuntimeBridgeCommandConflict,
		agentcore.RuntimeBridgeSessionMismatch,
		agentcore.RuntimeBridgeGenerationMismatch,
		agentcore.RuntimeBridgeQueueFull,
		agentcore.RuntimeBridgeHandshakeTimeout,
		agentcore.RuntimeBridgeWorkspaceUnavailable,
		agentcore.RuntimeBridgeTransportClosed,
		agentcore.RuntimeBridgeProtocolError,
		agentcore.RuntimeBridgeBackendRejected,
		agentcore.RuntimeBridgeUnknownPermissionRequest,
		agentcore.RuntimeBridgeUnknownAuthRequest,
		agentcore.RuntimeBridgeCancelled,
		agentcore.RuntimeBridgeDetachedCleanNoResult,
		agentcore.RuntimeBridgeDetachedCrash,
	} {
		if agentcore.IsRuntimeBridgeError(err, code) {
			return code
		}
	}
	return agentcore.RuntimeBridgeBackendRejected
}

func runtimeErrorCodeOrEmpty(err error) agentcore.RuntimeBridgeErrorCode {
	if err == nil {
		return ""
	}
	return runtimeErrorCode(err)
}

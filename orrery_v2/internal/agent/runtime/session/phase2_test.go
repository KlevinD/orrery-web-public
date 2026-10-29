package session

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type scriptedSessionBackend struct {
	events chan agentcore.SessionEnvelope
	caps   agentcore.Capabilities

	mu              sync.Mutex
	pending         []agentcore.PendingPermissionView
	dispatches      []agentcore.RuntimeCommand
	generation      uint64
	dispatch        func(agentcore.RuntimeCommand) (agentcore.CommandReceipt, error)
	dispatchContext func(context.Context, agentcore.RuntimeCommand)
}

func newScriptedSessionBackend() *scriptedSessionBackend {
	return &scriptedSessionBackend{
		events: make(chan agentcore.SessionEnvelope, 128),
		caps: agentcore.Capabilities{
			SupportedCommands: agentcore.CommandSet{Send: true, CancelTurn: true, CancelSession: true, AnswerPermission: true},
			PromptBlocks:      agentcore.BlockSet{Text: true},
			PromptAccepted:    agentcore.PromptAcceptedNative,
			SupportsQueue:     true,
			SupportsMultiTurn: true,
		},
	}
}

func (b *scriptedSessionBackend) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	b.mu.Lock()
	b.dispatches = append(b.dispatches, command)
	custom := b.dispatch
	observeContext := b.dispatchContext
	if custom == nil && command.Kind == agentcore.RuntimeCommandSend {
		b.generation++
		generation := b.generation
		b.mu.Unlock()
		if observeContext != nil {
			observeContext(ctx, command)
		}
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: generation}, nil
	}
	b.mu.Unlock()
	if observeContext != nil {
		observeContext(ctx, command)
	}
	if custom != nil {
		return custom(command)
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn}, nil
}

func (b *scriptedSessionBackend) Events() <-chan agentcore.SessionEnvelope { return b.events }
func (b *scriptedSessionBackend) Capabilities() agentcore.Capabilities     { return b.caps.Clone() }
func (b *scriptedSessionBackend) PendingPermissionRequests() []agentcore.PendingPermissionView {
	b.mu.Lock()
	defer b.mu.Unlock()
	return clonePending(b.pending)
}
func (b *scriptedSessionBackend) emit(sessionID agentcore.SessionID, generation uint64, event agentcore.SessionEvent) {
	b.events <- agentcore.SessionEnvelope{SessionID: sessionID, TurnGeneration: generation, Event: event}
}
func (b *scriptedSessionBackend) dispatchCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.dispatches)
}
func (b *scriptedSessionBackend) dispatchedClientIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make([]string, 0, len(b.dispatches))
	for _, command := range b.dispatches {
		if command.Send != nil {
			ids = append(ids, command.Send.Metadata.ClientMsgID)
		}
	}
	return ids
}

func TestOrchestratorLedgerConcurrentIdempotencyAndConflict(t *testing.T) {
	backend := newScriptedSessionBackend()
	var entered atomic.Int32
	release := make(chan struct{})
	backend.dispatch = func(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
		if command.Kind != agentcore.RuntimeCommandSend {
			return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn}, nil
		}
		entered.Add(1)
		<-release
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: 1}, nil
	}
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{})
	defer orchestrator.Close()
	command := sendRuntimeCommand("same-command", "client-1", []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "hello"}})

	const callers = 24
	results := make(chan agentcore.CommandReceipt, callers)
	errorsSeen := make(chan error, callers)
	for range callers {
		go func() {
			receipt, err := orchestrator.Dispatch(context.Background(), command)
			results <- receipt
			errorsSeen <- err
		}()
	}
	waitFor(t, func() bool { return entered.Load() == 1 })
	close(release)
	for range callers {
		if err := <-errorsSeen; err != nil {
			t.Fatal(err)
		}
		if receipt := <-results; receipt.TurnGeneration != 1 || receipt.Admission != agentcore.AdmissionStarted {
			t.Fatalf("duplicate receipt = %#v", receipt)
		}
	}
	if backend.dispatchCount() != 1 {
		t.Fatalf("backend dispatches = %d, want 1", backend.dispatchCount())
	}
	conflict := command
	conflict.Send = &agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "different"}}, Metadata: command.Send.Metadata}
	if _, err := orchestrator.Dispatch(context.Background(), conflict); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeCommandConflict) {
		t.Fatalf("conflicting retry = %v", err)
	}
	stats := orchestrator.LedgerStats()
	if stats.Hits != callers-1 || stats.Conflicts != 1 {
		t.Fatalf("ledger stats = %#v", stats)
	}
}

func TestOrchestratorSingleLaneSequencesAndDrainBeforeEOF(t *testing.T) {
	backend := newScriptedSessionBackend()
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{BroadcastCapacity: 64})
	subscription := orchestrator.SubscribeEvents()
	defer subscription.Close()

	backend.emit("s", 0, agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeInfo, Message: "one"}})
	backend.emit("s", 0, agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeInfo, Message: "two"}})
	close(backend.events)

	envelopes := make([]agentcore.SessionEnvelope, 0, 3)
	for len(envelopes) < 3 {
		select {
		case envelope := <-subscription.Events():
			envelopes = append(envelopes, envelope)
		case <-time.After(2 * time.Second):
			t.Fatal("provider drain timed out")
		}
	}
	orchestrator.Close()
	if len(envelopes) != 3 {
		t.Fatalf("events = %#v", envelopes)
	}
	for i, envelope := range envelopes {
		if envelope.Sequence != uint64(i+1) || envelope.Timestamp.IsZero() {
			t.Fatalf("event %d metadata = %#v", i, envelope)
		}
	}
	if envelopes[0].Event.Notice.Message != "one" || envelopes[1].Event.Notice.Message != "two" || envelopes[2].Event.Kind != agentcore.EventDetached {
		t.Fatalf("EOF overtook provider drain: %#v", envelopes)
	}
}

func TestOrchestratorBoundedLagAndSnapshotRecovery(t *testing.T) {
	backend := newScriptedSessionBackend()
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{BroadcastCapacity: 2})
	defer orchestrator.Close()
	events := orchestrator.SubscribeEvents()
	defer events.Close()

	for i := 0; i < 20; i++ {
		backend.emit("s", 0, agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeInfo, Message: "event"}})
	}
	waitFor(t, func() bool { return orchestrator.Snapshot().Sequence == 20 })
	seenLag := false
	deadline := time.After(2 * time.Second)
	for !seenLag {
		select {
		case envelope := <-events.Events():
			seenLag = envelope.Event.Kind == agentcore.EventLagged && envelope.Event.Lagged.Skipped > 0
		case <-deadline:
			t.Fatal("lagging subscriber received no explicit lag signal")
		}
	}

	snapshots := orchestrator.SubscribeSnapshots()
	defer snapshots.Close()
	initial := <-snapshots.Updates()
	if initial.Snapshot.Sequence != 20 {
		t.Fatalf("cached recovery snapshot sequence = %d", initial.Snapshot.Sequence)
	}
	if err := orchestrator.Reconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case update := <-snapshots.Updates():
		if update.Snapshot.Sequence <= initial.Snapshot.Sequence {
			t.Fatalf("reconnect did not rebroadcast newer truth: %#v", update)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect snapshot timeout")
	}
}

func TestOrchestratorDoubleTerminalAndLateResultAreAbsorbed(t *testing.T) {
	backend := newScriptedSessionBackend()
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{})
	defer orchestrator.Close()
	command := sendRuntimeCommand("send-1", "m-1", []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "hello"}})
	if _, err := orchestrator.Dispatch(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	backend.emit("s", 1, message("answer"))
	backend.emit("s", 1, result(1, false, "", agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeEndTurn}))
	waitFor(t, func() bool { return orchestrator.Snapshot().State.Kind == StateIdle })
	terminal, ok := orchestrator.LatestTerminal()
	if !ok || terminal.TurnGeneration != 1 || terminal.Reason.Kind != TransitionCompleted {
		t.Fatalf("terminal = %#v/%v", terminal, ok)
	}
	backend.emit("s", 1, result(1, true, "late secret failure", agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}))
	backend.emit("s", 1, agentcore.SessionEvent{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{}})
	waitFor(t, func() bool { return orchestrator.Snapshot().Sequence >= 5 })
	after, _ := orchestrator.LatestTerminal()
	if !reflect.DeepEqual(terminal, after) || orchestrator.Snapshot().State.Kind != StateIdle {
		t.Fatalf("late terminal changed settled truth: before=%#v after=%#v state=%#v", terminal, after, orchestrator.Snapshot().State)
	}
}

func TestOrchestratorRejectsProviderOwnedTurnLoweringAndGenerationMismatch(t *testing.T) {
	backend := newScriptedSessionBackend()
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{})
	defer orchestrator.Close()
	subscription := orchestrator.SubscribeEvents()
	defer subscription.Close()
	backend.emit("s", 0, turnStarted(1))
	first := <-subscription.Events()
	if first.Event.Kind != agentcore.EventNotice || first.Event.Notice.Message != "provider protocol error" || orchestrator.Snapshot().State.Kind != StateIdle {
		t.Fatalf("provider TurnStarted escaped lowering boundary: event=%#v state=%#v", first, orchestrator.Snapshot().State)
	}
	backend.emit("s", 99, agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeInfo, Message: "ahead"}})
	second := <-subscription.Events()
	if second.Event.Kind != agentcore.EventNotice || second.Event.Notice.Message != "provider protocol error" || orchestrator.Snapshot().State.Kind != StateIdle {
		t.Fatalf("ahead generation escaped fence: event=%#v state=%#v", second, orchestrator.Snapshot().State)
	}
}

func TestConversationFIFOExactPromptAcceptanceAndHeldCancel(t *testing.T) {
	backend := newScriptedSessionBackend()
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{})
	defer orchestrator.Close()
	conversation, err := NewConversationSessionFromSnapshot("s", orchestrator, "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go conversation.RunFlushEngine(ctx)

	text := func(value string) []agentcore.ContentBlock {
		return []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: value}}
	}
	first, err := conversation.Send(context.Background(), text("first"))
	if err != nil || first.Admission != agentcore.AdmissionStarted {
		t.Fatalf("first send = %#v, %v", first, err)
	}
	second, err := conversation.Send(context.Background(), text("second"))
	if err != nil || second.Admission != agentcore.AdmissionQueued {
		t.Fatalf("second send = %#v, %v", second, err)
	}
	pending := conversation.Pending()
	if pending[0].Status != MessageSent || pending[1].Status != MessageHeld {
		t.Fatalf("pending = %#v", pending)
	}
	unknown := agentcore.SessionEnvelope{SessionID: "s", Event: agentcore.SessionEvent{Kind: agentcore.EventPromptAccepted, PromptAccepted: &agentcore.PromptAcceptedEvent{ClientMsgID: "unknown"}}}
	if _, ok := conversation.HandleEnvelope(unknown); ok {
		t.Fatal("unknown PromptAccepted changed queue")
	}
	accepted := unknown
	accepted.Event.PromptAccepted.ClientMsgID = "m-1"
	if got, ok := conversation.HandleEnvelope(accepted); !ok || got.Status != MessageAccepted {
		t.Fatalf("matching PromptAccepted = %#v/%v", got, ok)
	}
	if conversation.CancelHeld("m-1") {
		t.Fatal("dispatched message was canceled locally")
	}
	if !conversation.CancelHeld("m-2") || len(conversation.Pending()) != 1 {
		t.Fatal("Held message was not removed locally")
	}
}

func TestConversationOneHeadPerRisingEdgeAndNoPrefoldDoubleDispatch(t *testing.T) {
	backend := newScriptedSessionBackend()
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{})
	defer orchestrator.Close()
	conversation, err := NewConversationSessionFromSnapshot("s", orchestrator, "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go conversation.RunFlushEngine(ctx)
	block := func(value string) []agentcore.ContentBlock {
		return []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: value}}
	}
	if _, err := conversation.Send(context.Background(), block("one")); err != nil {
		t.Fatal(err)
	}
	_, _ = conversation.Send(context.Background(), block("two"))
	_, _ = conversation.Send(context.Background(), block("three"))
	if backend.dispatchCount() != 1 {
		t.Fatalf("pre-fold/closed gate dispatched %d heads", backend.dispatchCount())
	}

	backend.emit("s", 1, message("done"))
	backend.emit("s", 1, result(1, false, "", agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeEndTurn}))
	waitFor(t, func() bool { return backend.dispatchCount() == 2 })
	if ids := backend.dispatchedClientIDs(); !reflect.DeepEqual(ids, []string{"m-1", "m-2"}) {
		t.Fatalf("first edge order = %#v", ids)
	}
	backend.emit("s", 2, message("done"))
	backend.emit("s", 2, result(2, false, "", agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeEndTurn}))
	waitFor(t, func() bool { return backend.dispatchCount() == 3 })
	if ids := backend.dispatchedClientIDs(); !reflect.DeepEqual(ids, []string{"m-1", "m-2", "m-3"}) {
		t.Fatalf("FIFO order = %#v", ids)
	}
}

func TestConversationQueuedDispatchPreservesMessageContext(t *testing.T) {
	type dispatchContextKey struct{}
	type observedDispatch struct {
		value string
		err   error
	}

	backend := newScriptedSessionBackend()
	observed := make(chan observedDispatch, 2)
	backend.dispatchContext = func(ctx context.Context, command agentcore.RuntimeCommand) {
		if command.Kind != agentcore.RuntimeCommandSend {
			return
		}
		value, _ := ctx.Value(dispatchContextKey{}).(string)
		observed <- observedDispatch{value: value, err: ctx.Err()}
	}
	waitObserved := func(label string) observedDispatch {
		t.Helper()
		select {
		case dispatch := <-observed:
			return dispatch
		case <-time.After(2 * time.Second):
			t.Fatalf("%s dispatch did not run", label)
			return observedDispatch{}
		}
	}
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{})
	defer orchestrator.Close()
	conversation, err := NewConversationSessionFromSnapshot("s", orchestrator, "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	engineCtx, stopEngine := context.WithCancel(context.Background())
	defer stopEngine()
	go conversation.RunFlushEngine(engineCtx)

	content := func(value string) []agentcore.ContentBlock {
		return []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: value}}
	}
	firstCtx := context.WithValue(context.Background(), dispatchContextKey{}, "first")
	if receipt, err := conversation.Send(firstCtx, content("first")); err != nil || receipt.Admission != agentcore.AdmissionStarted {
		t.Fatalf("first send = %#v, %v", receipt, err)
	}
	if first := waitObserved("first"); first.value != "first" || first.err != nil {
		t.Fatalf("first dispatch context = %#v", first)
	}

	queuedBase := context.WithValue(context.Background(), dispatchContextKey{}, "queued")
	queuedCtx, cancelQueued := context.WithCancel(queuedBase)
	if receipt, err := conversation.Send(queuedCtx, content("second")); err != nil || receipt.Admission != agentcore.AdmissionQueued {
		t.Fatalf("queued send = %#v, %v", receipt, err)
	}
	cancelQueued()
	backend.emit("s", 1, message("done"))
	backend.emit("s", 1, result(1, false, "", agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeEndTurn}))

	if second := waitObserved("queued rising-edge"); second.value != "queued" || second.err != nil {
		t.Fatalf("queued dispatch context = %#v", second)
	}
	waitFor(t, func() bool {
		conversation.pendingMu.RLock()
		defer conversation.pendingMu.RUnlock()
		return len(conversation.dispatchContexts) == 0
	})
}

func TestOrchestratorDispatchContextLivesUntilOrchestratorCloses(t *testing.T) {
	type dispatchContextKey struct{}

	backend := newScriptedSessionBackend()
	dispatched := make(chan context.Context, 1)
	backend.dispatchContext = func(ctx context.Context, command agentcore.RuntimeCommand) {
		if command.Kind == agentcore.RuntimeCommandSend {
			dispatched <- ctx
		}
	}
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{})
	closed := false
	defer func() {
		if !closed {
			orchestrator.Close()
		}
	}()

	callerBase := context.WithValue(context.Background(), dispatchContextKey{}, "current-run")
	callerCtx, cancelCaller := context.WithCancel(callerBase)
	content := []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "hello"}}
	if receipt, err := orchestrator.Dispatch(callerCtx, sendRuntimeCommand("command", "client", content)); err != nil || receipt.Admission != agentcore.AdmissionStarted {
		t.Fatalf("dispatch = %#v, %v", receipt, err)
	}
	cancelCaller()

	var runCtx context.Context
	select {
	case runCtx = <-dispatched:
	case <-time.After(2 * time.Second):
		t.Fatal("backend did not receive dispatch context")
	}
	if value, _ := runCtx.Value(dispatchContextKey{}).(string); value != "current-run" {
		t.Fatalf("dispatch context value = %q, want current-run", value)
	}
	if err := runCtx.Err(); err != nil {
		t.Fatalf("dispatch context ended after admission: %v", err)
	}

	orchestrator.Close()
	closed = true
	select {
	case <-runCtx.Done():
		if err := runCtx.Err(); !errors.Is(err, context.Canceled) {
			t.Fatalf("dispatch context close error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch context outlived orchestrator")
	}
}

func TestConversationDuplicateAndDispatchFailurePersistence(t *testing.T) {
	backend := newScriptedSessionBackend()
	backend.dispatch = func(agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "wire details", true)
	}
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{})
	defer orchestrator.Close()
	conversation, err := NewConversationSessionFromSnapshot("s", orchestrator, "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	content := []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "hello"}}
	if _, err := conversation.SendWithIDs(context.Background(), "stable", "client", content); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeTransportClosed) {
		t.Fatalf("dispatch error = %v", err)
	}
	if pending := conversation.Pending(); len(pending) != 1 || pending[0].Status != MessageError || pending[0].DispatchError != agentcore.RuntimeBridgeTransportClosed {
		t.Fatalf("failed pending = %#v", pending)
	}
	if _, err := conversation.SendWithIDs(context.Background(), "stable", "client", content); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeTransportClosed) {
		t.Fatalf("idempotent error = %v", err)
	}
	if backend.dispatchCount() != 1 || len(conversation.Pending()) != 1 {
		t.Fatalf("retry duplicated side effect: dispatch=%d pending=%#v", backend.dispatchCount(), conversation.Pending())
	}
	other := []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "different"}}
	if _, err := conversation.SendWithIDs(context.Background(), "stable", "client", other); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeCommandConflict) {
		t.Fatalf("conflicting retry = %v", err)
	}
}

func TestConversationConcurrentDuplicateWaitsForSameReceipt(t *testing.T) {
	backend := newScriptedSessionBackend()
	release := make(chan struct{})
	backend.dispatch = func(agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
		<-release
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: 1}, nil
	}
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{})
	defer orchestrator.Close()
	conversation, err := NewConversationSessionFromSnapshot("s", orchestrator, "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	content := []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "hello"}}
	type outcome struct {
		receipt agentcore.CommandReceipt
		err     error
	}
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			receipt, err := conversation.SendWithIDs(context.Background(), "stable", "client", content)
			results <- outcome{receipt: receipt, err: err}
		}()
	}
	waitFor(t, func() bool { return backend.dispatchCount() == 1 })
	close(release)
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil || !reflect.DeepEqual(first.receipt, second.receipt) || first.receipt.Admission != agentcore.AdmissionStarted {
		t.Fatalf("concurrent duplicate results = %#v, %#v", first, second)
	}
	if backend.dispatchCount() != 1 || len(conversation.Pending()) != 1 {
		t.Fatalf("concurrent duplicate side effects = dispatch:%d pending:%#v", backend.dispatchCount(), conversation.Pending())
	}
}

func TestConversationCanceledMessageIsNotResurrectedByLateAcceptance(t *testing.T) {
	backend := newScriptedSessionBackend()
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{})
	defer orchestrator.Close()
	conversation, err := NewConversationSessionFromSnapshot("s", orchestrator, "m", []PendingMessage{{
		ClientMsgID: "m-1", CommandID: "send-m-1", Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "hello"}}, Status: MessageSent, EnqueueOrdinal: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if conversation.CancelAllOutstanding() != 1 {
		t.Fatal("expected one outstanding cancellation")
	}
	envelope := agentcore.SessionEnvelope{SessionID: "s", Event: agentcore.SessionEvent{Kind: agentcore.EventPromptAccepted, PromptAccepted: &agentcore.PromptAcceptedEvent{ClientMsgID: "m-1"}}}
	if _, accepted := conversation.HandleEnvelope(envelope); accepted || conversation.Pending()[0].Status != MessageCanceled {
		t.Fatalf("late PromptAccepted resurrected canceled message: %#v", conversation.Pending())
	}
}

func TestConversationRestoreDefensiveCopyAndLostPromptAccepted(t *testing.T) {
	backend := newScriptedSessionBackend()
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{})
	defer orchestrator.Close()
	receipt := agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: 4}
	restored := []PendingMessage{{ClientMsgID: "old-1", CommandID: "old-command", Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "old"}}, Status: MessageSent, EnqueueOrdinal: 7, Receipt: &receipt}}
	conversation, err := NewConversationSessionFromSnapshot("s", orchestrator, "new", restored)
	if err != nil {
		t.Fatal(err)
	}
	restored[0].Content[0].Text = "mutated"
	restored[0].Receipt.TurnGeneration = 99
	pending := conversation.Pending()
	if pending[0].Content[0].Text != "old" || pending[0].Receipt.TurnGeneration != 4 || pending[0].Status != MessageSent {
		t.Fatalf("restore shares memory or lost unconfirmed status: %#v", pending)
	}
	copy := conversation.Snapshot()
	copy.Pending[0].Content[0].Text = "changed"
	if conversation.Pending()[0].Content[0].Text != "old" {
		t.Fatal("conversation snapshot is not defensive")
	}
}

func TestRehydrateAlwaysIdlePreservesGenerationInOrchestrator(t *testing.T) {
	checkpoint := &agentcore.Tier2Checkpoint{SessionID: "s", BackendSessionID: "opaque", TurnGeneration: 9}
	if state := Rehydrate(checkpoint); state.Kind != StateIdle {
		t.Fatalf("rehydrated state = %#v", state)
	}
	backend := newScriptedSessionBackend()
	orchestrator := mustOrchestratorWithCheckpoint(t, backend, checkpoint, OrchestratorOptions{})
	defer orchestrator.Close()
	if snapshot := orchestrator.Snapshot(); snapshot.State.Kind != StateIdle || snapshot.TurnGeneration != 9 {
		t.Fatalf("checkpoint snapshot = %#v", snapshot)
	}
}

type recordingSuspendBinding struct {
	stops atomic.Int32
	done  chan struct{}
	once  sync.Once
}

func newRecordingSuspendBinding() *recordingSuspendBinding {
	return &recordingSuspendBinding{done: make(chan struct{})}
}

func (b *recordingSuspendBinding) Stop(context.Context) error {
	b.stops.Add(1)
	b.once.Do(func() { close(b.done) })
	return nil
}

func TestSuspendControllerOffGuardWakeAndClose(t *testing.T) {
	now := time.Unix(100, 0)
	offBinding := newRecordingSuspendBinding()
	off, err := NewSuspendController(offBinding, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if suspended, err := off.SuspendIfIdle(context.Background(), now.Add(24*time.Hour), false); err != nil || suspended || !off.IsActive() {
		t.Fatalf("off-by-default suspend = %v/%v active=%v", suspended, err, off.IsActive())
	}
	if err := off.Close(context.Background()); err != nil || offBinding.stops.Load() != 1 {
		t.Fatalf("off close = %v stops=%d", err, offBinding.stops.Load())
	}

	first := newRecordingSuspendBinding()
	controller, err := NewSuspendController(first, time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if suspended, _ := controller.SuspendIfIdle(context.Background(), now.Add(2*time.Hour), true); suspended || !controller.IsActive() {
		t.Fatal("active turn was suspended")
	}
	suspended, err := controller.SuspendIfIdle(context.Background(), now.Add(2*time.Hour), false)
	if err != nil || !suspended || controller.IsActive() || first.stops.Load() != 1 {
		t.Fatalf("idle suspend = %v/%v active=%v stops=%d", suspended, err, controller.IsActive(), first.stops.Load())
	}
	second := newRecordingSuspendBinding()
	var wakes atomic.Int32
	if err := controller.EnsureAwake(context.Background(), now.Add(3*time.Hour), func(context.Context) (SuspendBinding, error) {
		wakes.Add(1)
		return second, nil
	}); err != nil {
		t.Fatal(err)
	}
	if !controller.IsActive() || wakes.Load() != 1 {
		t.Fatalf("wake state active=%v wakes=%d", controller.IsActive(), wakes.Load())
	}
	if err := controller.EnsureAwake(context.Background(), now.Add(4*time.Hour), func(context.Context) (SuspendBinding, error) {
		return nil, errors.New("must not wake active binding")
	}); err != nil || wakes.Load() != 1 {
		t.Fatalf("active touch = %v wakes=%d", err, wakes.Load())
	}
	if err := controller.Close(context.Background()); err != nil || second.stops.Load() != 1 {
		t.Fatalf("close = %v stops=%d", err, second.stops.Load())
	}
}

func mustOrchestrator(t *testing.T, backend agentcore.SessionBackend, options OrchestratorOptions) *Orchestrator {
	t.Helper()
	return mustOrchestratorWithCheckpoint(t, backend, nil, options)
}

func mustOrchestratorWithCheckpoint(t *testing.T, backend agentcore.SessionBackend, checkpoint *agentcore.Tier2Checkpoint, options OrchestratorOptions) *Orchestrator {
	t.Helper()
	orchestrator, err := NewOrchestrator("s", backend, checkpoint, options)
	if err != nil {
		t.Fatal(err)
	}
	return orchestrator
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

func TestWorkflowRosterOrderingIsStable(t *testing.T) {
	backend := newScriptedSessionBackend()
	orchestrator := mustOrchestrator(t, backend, OrchestratorOptions{})
	defer orchestrator.Close()
	for _, ref := range []string{"z", "a", "m"} {
		backend.emit("s", 0, agentcore.SessionEvent{Kind: agentcore.EventSubagentDetail, SubagentDetail: &agentcore.SubagentDetailEvent{Ref: ref, LoopState: agentcore.WorkflowLoopProgress}})
	}
	waitFor(t, func() bool { return len(orchestrator.Snapshot().WorkflowRoster) == 3 })
	roster := orchestrator.Snapshot().WorkflowRoster
	refs := []string{roster[0].Ref, roster[1].Ref, roster[2].Ref}
	if !sort.StringsAreSorted(refs) {
		t.Fatalf("roster is not stable: %#v", refs)
	}
}

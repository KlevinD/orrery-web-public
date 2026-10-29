// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package session

import (
	"context"
	"reflect"
	"sort"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

// Reference: aionui-session/backend/orchestrator.rs@v0.1.52.

type OrchestratorOptions struct {
	LaneCapacity      int
	BroadcastCapacity int
	LedgerEntries     int
	Now               func() time.Time
}

type Orchestrator struct {
	sessionID agentcore.SessionID
	backend   agentcore.SessionBackend
	ledger    *CommandLedger
	now       func() time.Time

	ctx       context.Context
	cancel    context.CancelFunc
	lane      chan laneRequest
	done      chan struct{}
	closeOnce sync.Once

	events    *boundedBus[agentcore.SessionEnvelope]
	snapshots *boundedBus[SnapshotUpdate]

	snapshotMu sync.RWMutex
	latest     Snapshot
	terminal   *TerminalRecord

	state          State
	sequence       uint64
	turnGeneration uint64
	lastReason     *TransitionReason
	workflow       map[string]WorkflowAgentState
}

type TerminalRecord struct {
	TurnGeneration uint64
	Reason         TransitionReason
}

type laneRequest struct {
	dispatch  *dispatchLaneRequest
	event     *agentcore.SessionEnvelope
	source    agentcore.EventSource
	reconnect bool
	eof       bool
}

type dispatchLaneRequest struct {
	ctx      context.Context
	command  agentcore.RuntimeCommand
	response chan dispatchLaneResponse
}

type dispatchLaneResponse struct {
	receipt agentcore.CommandReceipt
	err     error
}

type lifetimeBoundValueContext struct {
	lifetime context.Context
	values   context.Context
}

func (c lifetimeBoundValueContext) Deadline() (time.Time, bool) { return c.lifetime.Deadline() }
func (c lifetimeBoundValueContext) Done() <-chan struct{}       { return c.lifetime.Done() }
func (c lifetimeBoundValueContext) Err() error                  { return c.lifetime.Err() }
func (c lifetimeBoundValueContext) Value(key any) any {
	if value := c.values.Value(key); value != nil {
		return value
	}
	return c.lifetime.Value(key)
}

func NewOrchestrator(sessionID agentcore.SessionID, backend agentcore.SessionBackend, checkpoint *agentcore.Tier2Checkpoint, options OrchestratorOptions) (*Orchestrator, error) {
	if sessionID == "" || backend == nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "orchestrator requires session id and backend event stream", false)
	}
	providerEvents := backend.Events()
	if providerEvents == nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "orchestrator requires session id and backend event stream", false)
	}
	capabilities := backend.Capabilities()
	if err := capabilities.Validate(); err != nil {
		return nil, err
	}
	if options.LaneCapacity < 1 {
		options.LaneCapacity = 256
	}
	if options.BroadcastCapacity < 2 {
		options.BroadcastCapacity = 256
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	orchestrator := &Orchestrator{
		sessionID: sessionID,
		backend:   backend,
		ledger:    NewCommandLedger(options.LedgerEntries),
		now:       options.Now,
		ctx:       ctx,
		cancel:    cancel,
		lane:      make(chan laneRequest, options.LaneCapacity),
		done:      make(chan struct{}),
		state:     Rehydrate(checkpoint),
		workflow:  make(map[string]WorkflowAgentState),
	}
	if checkpoint != nil {
		if checkpoint.SessionID != sessionID {
			cancel()
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeSessionMismatch, "checkpoint belongs to another session", false)
		}
		orchestrator.turnGeneration = checkpoint.TurnGeneration
	}
	orchestrator.events = newBoundedBus(options.BroadcastCapacity, func(skipped uint64, latest agentcore.SessionEnvelope) agentcore.SessionEnvelope {
		return agentcore.SessionEnvelope{
			SessionID:      sessionID,
			TurnGeneration: 0,
			Sequence:       latest.Sequence,
			Timestamp:      latest.Timestamp,
			Source:         agentcore.EventSourceOrchestrator,
			Projection:     agentcore.EventProjectionCanonical,
			Event:          agentcore.SessionEvent{Kind: agentcore.EventLagged, Lagged: &agentcore.LaggedEvent{Skipped: skipped}},
		}
	})
	orchestrator.snapshots = newBoundedBus(options.BroadcastCapacity, func(skipped uint64, latest SnapshotUpdate) SnapshotUpdate {
		return SnapshotUpdate{Snapshot: latest.Snapshot.Clone(), Lagged: skipped}
	})
	initial, err := NewSnapshot(sessionID, orchestrator.state, orchestrator.turnGeneration, 0, capabilities, backend.PendingPermissionRequests(), nil, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	orchestrator.latest = initial
	go orchestrator.run()
	go orchestrator.readProviderEvents(providerEvents)
	return orchestrator, nil
}

func (o *Orchestrator) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return o.ledger.Execute(ctx, command, func(dispatchCtx context.Context) (agentcore.CommandReceipt, error) {
		// A command may carry process-local ownership values used by runtime tool
		// callbacks after admission returns. Preserve those values while binding
		// cancellation, deadlines, and errors to the orchestrator lifetime.
		laneCtx := lifetimeBoundValueContext{lifetime: o.ctx, values: context.WithoutCancel(dispatchCtx)}
		response := make(chan dispatchLaneResponse, 1)
		request := laneRequest{dispatch: &dispatchLaneRequest{ctx: laneCtx, command: command, response: response}}
		select {
		case o.lane <- request:
		case <-o.done:
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "orchestrator is closed", false)
		case <-dispatchCtx.Done():
			return agentcore.CommandReceipt{}, dispatchCtx.Err()
		}
		select {
		case result := <-response:
			return result.receipt, result.err
		case <-o.done:
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "orchestrator is closed", false)
		}
	})
}

func (o *Orchestrator) SubscribeEvents() *EventSubscription {
	return &EventSubscription{inner: o.events.subscribe()}
}

func (o *Orchestrator) SubscribeSnapshots() *SnapshotSubscription {
	o.snapshotMu.Lock()
	sub := o.snapshots.subscribe()
	sub.enqueue(SnapshotUpdate{Snapshot: o.latest.Clone()})
	o.snapshotMu.Unlock()
	return &SnapshotSubscription{inner: sub}
}

func (o *Orchestrator) Snapshot() Snapshot {
	o.snapshotMu.RLock()
	defer o.snapshotMu.RUnlock()
	return o.latest.Clone()
}

func (o *Orchestrator) Capabilities() agentcore.Capabilities {
	return o.backend.Capabilities().Clone()
}

func (o *Orchestrator) PendingPermissionRequests() []agentcore.PendingPermissionView {
	return clonePending(o.backend.PendingPermissionRequests())
}

func (o *Orchestrator) LatestTerminal() (TerminalRecord, bool) {
	o.snapshotMu.RLock()
	defer o.snapshotMu.RUnlock()
	if o.terminal == nil {
		return TerminalRecord{}, false
	}
	return TerminalRecord{TurnGeneration: o.terminal.TurnGeneration, Reason: *cloneTransitionReason(&o.terminal.Reason)}, true
}

func (o *Orchestrator) Reconnect(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case o.lane <- laneRequest{reconnect: true}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-o.done:
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "orchestrator is closed", false)
	}
}

func (o *Orchestrator) Close() {
	o.closeOnce.Do(o.cancel)
	<-o.done
}

func (o *Orchestrator) LedgerStats() CommandLedgerStats { return o.ledger.Stats() }

func (o *Orchestrator) readProviderEvents(events <-chan agentcore.SessionEnvelope) {
	for {
		select {
		case <-o.ctx.Done():
			return
		case envelope, ok := <-events:
			if !ok {
				select {
				case o.lane <- laneRequest{eof: true}:
				case <-o.ctx.Done():
				}
				return
			}
			select {
			case o.lane <- laneRequest{event: &envelope, source: agentcore.EventSourceProvider}:
			case <-o.ctx.Done():
				return
			}
		}
	}
}

func (o *Orchestrator) run() {
	defer close(o.done)
	defer o.events.close()
	defer o.snapshots.close()
	for {
		select {
		case <-o.ctx.Done():
			return
		case request := <-o.lane:
			switch {
			case request.dispatch != nil:
				receipt, err := o.dispatchInLane(request.dispatch.ctx, request.dispatch.command)
				request.dispatch.response <- dispatchLaneResponse{receipt: receipt, err: err}
			case request.event != nil:
				if err := o.foldEnvelope(*request.event, request.source); err != nil {
					o.normalizeProtocolFailure()
				}
			case request.eof:
				o.foldLifecycleDetached()
			case request.reconnect:
				o.rebroadcastSnapshot()
			}
		}
	}
}

func (o *Orchestrator) dispatchInLane(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	if err := command.Validate(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	capabilities := o.backend.Capabilities()
	if err := capabilities.Validate(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	if !capabilities.Supports(command) {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, string(command.Kind), false)
	}
	if command.Kind == agentcore.RuntimeCommandRewind && o.state.Kind != StateIdle {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "rewind requires an idle session", false)
	}
	receipt, err := o.backend.Dispatch(ctx, command)
	if err != nil {
		return agentcore.CommandReceipt{}, err
	}
	if err := receipt.Validate(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	if !receipt.Accepted {
		return receipt, nil
	}
	switch command.Kind {
	case agentcore.RuntimeCommandSend:
		if receipt.Admission == agentcore.AdmissionStarted {
			if receipt.TurnGeneration <= o.turnGeneration {
				return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeGenerationMismatch, "send generation did not advance", false)
			}
			o.turnGeneration = receipt.TurnGeneration
			event := agentcore.SessionEvent{Kind: agentcore.EventTurnStarted, TurnStarted: &agentcore.TurnStartedEvent{Epoch: receipt.TurnGeneration}}
			if err := o.foldEvent(event, agentcore.EventSourceOrchestrator, receipt.TurnGeneration); err != nil {
				return agentcore.CommandReceipt{}, err
			}
		}
	case agentcore.RuntimeCommandCancel:
		event := agentcore.SessionEvent{Kind: agentcore.EventCancel, Cancel: &agentcore.CancelEvent{}}
		if err := o.foldEvent(event, agentcore.EventSourceOrchestrator, o.turnGeneration); err != nil {
			return agentcore.CommandReceipt{}, err
		}
	}
	return receipt, nil
}

func (o *Orchestrator) foldEnvelope(envelope agentcore.SessionEnvelope, source agentcore.EventSource) error {
	if envelope.SessionID != o.sessionID {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeSessionMismatch, "provider envelope belongs to another session", false)
	}
	if err := envelope.Event.Validate(); err != nil {
		return err
	}
	if source == agentcore.EventSourceProvider && providerForbiddenEvent(envelope.Event.Kind) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "provider emitted an orchestrator-owned event", false)
	}
	generation := envelope.TurnGeneration
	if generation == 0 && turnScopedEvent(envelope.Event.Kind) {
		generation = o.turnGeneration
	}
	if generation > o.turnGeneration {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeGenerationMismatch, "provider event generation is ahead of the session", false)
	}
	if envelope.Event.TurnResult != nil && envelope.Event.TurnResult.Epoch == 0 {
		event := envelope.Event.Clone()
		event.TurnResult.Epoch = generation
		envelope.Event = event
	}
	if envelope.Event.TurnResult != nil && envelope.Event.TurnResult.Epoch != generation {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeGenerationMismatch, "turn result epoch does not match its envelope", false)
	}
	return o.foldEvent(envelope.Event, source, generation)
}

func (o *Orchestrator) foldEvent(event agentcore.SessionEvent, source agentcore.EventSource, generation uint64) error {
	if err := event.Validate(); err != nil {
		return err
	}
	previous := o.state.Clone()
	previousActivity := HasForegroundActivity(previous) || BackgroundActive(o.workflowSlice())
	step, err := Step(previous, event)
	if err != nil {
		return err
	}
	o.updateWorkflow(event)
	o.state = step.State
	o.sequence++
	projection := agentcore.EventProjectionCanonical
	if event.Kind == agentcore.EventAdapterSpecific {
		projection = agentcore.EventProjectionDiagnostic
	}
	envelope := agentcore.SessionEnvelope{SessionID: o.sessionID, TurnGeneration: generation, Sequence: o.sequence, Timestamp: o.now().UTC(), Source: source, Projection: projection, Event: event.Clone()}
	o.events.publish(envelope)

	if len(step.Transitions) != 0 {
		o.lastReason = deriveTransitionReason(step.Transitions[len(step.Transitions)-1], event)
		if o.state.Kind == StateIdle || o.state.Kind == StateError {
			terminalGeneration := step.Transitions[len(step.Transitions)-1].Epoch
			if terminalGeneration == 0 {
				terminalGeneration = generation
			}
			o.snapshotMu.Lock()
			o.terminal = &TerminalRecord{TurnGeneration: terminalGeneration, Reason: *cloneTransitionReason(o.lastReason)}
			o.snapshotMu.Unlock()
		}
	}
	capabilities := o.backend.Capabilities()
	snapshot, err := NewSnapshot(o.sessionID, o.state, o.turnGeneration, o.sequence, capabilities, o.backend.PendingPermissionRequests(), o.workflowSlice(), o.lastReason)
	if err != nil {
		return err
	}
	nextActivity := snapshot.HasActivity
	o.snapshotMu.Lock()
	changed := len(step.Transitions) != 0 || previousActivity != nextActivity || !reflect.DeepEqual(o.latest.Capabilities, snapshot.Capabilities)
	o.latest = snapshot.Clone()
	if changed {
		o.snapshots.publish(SnapshotUpdate{Snapshot: snapshot.Clone()})
	}
	o.snapshotMu.Unlock()
	return nil
}

func (o *Orchestrator) normalizeProtocolFailure() {
	if o.state.Kind == StateStarting || o.state.Kind == StateRunning {
		event := agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{IsError: true, ResultText: "provider protocol error", Epoch: o.turnGeneration, Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}}}
		_ = o.foldEvent(event, agentcore.EventSourceLifecycle, o.turnGeneration)
		return
	}
	event := agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeWarning, Message: "provider protocol error"}}
	_ = o.foldEvent(event, agentcore.EventSourceLifecycle, 0)
}

func (o *Orchestrator) foldLifecycleDetached() {
	event := agentcore.SessionEvent{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{}}
	_ = o.foldEvent(event, agentcore.EventSourceLifecycle, o.turnGeneration)
}

func (o *Orchestrator) rebroadcastSnapshot() {
	event := agentcore.SessionEvent{Kind: agentcore.EventSnapshot, Snapshot: &agentcore.SnapshotEvent{StateRepresentation: string(o.state.Kind), TurnGeneration: o.turnGeneration}}
	if err := o.foldEvent(event, agentcore.EventSourceRecovery, o.turnGeneration); err != nil {
		return
	}
	o.snapshotMu.Lock()
	o.snapshots.publish(SnapshotUpdate{Snapshot: o.latest.Clone()})
	o.snapshotMu.Unlock()
}

func (o *Orchestrator) updateWorkflow(event agentcore.SessionEvent) {
	if event.Kind == agentcore.EventDetached || event.Kind == agentcore.EventBackendSuspended {
		clear(o.workflow)
		return
	}
	if event.SubagentDetail != nil {
		detail := event.SubagentDetail
		slot := o.workflow[detail.Ref]
		if slot.Ref == "" {
			slot = WorkflowAgentState{Ref: detail.Ref, TaskStatus: WorkflowTaskRunning}
		}
		if detail.Label != "" {
			slot.Label = detail.Label
		}
		if detail.LoopState != "" {
			slot.LoopState = detail.LoopState
		}
		if detail.Model != "" {
			slot.Model = detail.Model
		}
		if detail.Tokens != nil {
			slot.Tokens = cloneUint64(detail.Tokens)
		}
		if detail.ToolCalls != nil {
			slot.ToolCalls = cloneUint64(detail.ToolCalls)
		}
		if detail.LastToolName != "" {
			slot.LastToolName = detail.LastToolName
		}
		o.workflow[detail.Ref] = slot
		return
	}
	if event.SubagentUpdate == nil {
		return
	}
	update := event.SubagentUpdate
	status := workflowStatus(update.Status)
	slot := o.workflow[update.Ref]
	if slot.Ref != "" && slot.TaskStatus.IsTerminal() && !status.IsTerminal() {
		return
	}
	if slot.Ref == "" {
		slot.Ref = update.Ref
	}
	slot.TaskStatus = status
	slot.HasLifecycle = true
	if update.Label != "" {
		slot.Label = update.Label
	}
	o.workflow[update.Ref] = slot
}

func (o *Orchestrator) workflowSlice() []WorkflowAgentState {
	out := make([]WorkflowAgentState, 0, len(o.workflow))
	for _, state := range o.workflow {
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return cloneWorkflowRoster(out)
}

func workflowStatus(status agentcore.SubagentStatus) WorkflowTaskStatus {
	switch status {
	case agentcore.SubagentCompleted:
		return WorkflowTaskCompleted
	case agentcore.SubagentErrored:
		return WorkflowTaskFailed
	case agentcore.SubagentInterrupted, agentcore.SubagentShutdown:
		return WorkflowTaskStopped
	default:
		return WorkflowTaskRunning
	}
}

func deriveTransitionReason(transition Transition, event agentcore.SessionEvent) *TransitionReason {
	reason := &TransitionReason{Kind: TransitionStarted}
	if RequiresAction(transition.To) && !RequiresAction(transition.From) && event.Permission != nil {
		reason.Kind = TransitionPermissionRequested
		reason.PermissionKind = event.Permission.Kind
		return reason
	}
	if transition.To.Kind == StateRunning && event.PermissionResolved != nil {
		reason.Kind = TransitionPermissionResolved
		return reason
	}
	switch transition.To.Kind {
	case StateIdle:
		if event.Kind == agentcore.EventCancel {
			reason.Kind = TransitionCancelled
			reason.CancelReason = agentcore.CancelReasonUserCancel
		} else {
			reason.Kind = TransitionCompleted
			if event.TurnResult != nil {
				outcome := event.TurnResult.Outcome
				reason.Outcome = &outcome
			}
		}
	case StateError:
		reason.Kind = TransitionErrored
		errorReason := transition.To.Error.Reason
		reason.Error = &errorReason
	}
	return reason
}

func turnScopedEvent(kind agentcore.SessionEventKind) bool {
	switch kind {
	case agentcore.EventMessageDelta, agentcore.EventThoughtDelta, agentcore.EventToolCall, agentcore.EventToolResult, agentcore.EventTurnResult, agentcore.EventPlan, agentcore.EventPermission, agentcore.EventPermissionResolved, agentcore.EventPromptAccepted, agentcore.EventUsageDelta, agentcore.EventSubagentUpdate, agentcore.EventSubagentDetail, agentcore.EventToolOutputDelta, agentcore.EventTurnDiffUpdated, agentcore.EventItemStarted, agentcore.EventItemCompleted, agentcore.EventMessageFinalized:
		return true
	default:
		return false
	}
}

func providerForbiddenEvent(kind agentcore.SessionEventKind) bool {
	switch kind {
	case agentcore.EventTurnStarted, agentcore.EventCancel, agentcore.EventSnapshot, agentcore.EventLagged:
		return true
	default:
		return false
	}
}

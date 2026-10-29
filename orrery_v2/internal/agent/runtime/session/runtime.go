package session

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	agentcore "orrery_v2/internal/agent/core"
)

const (
	defaultTerminalCancelWait = 5 * time.Second
	maxRecordedRuntimeEvents  = 4096
)

type runtimePermissionEventKey struct {
	turnGeneration uint64
	eventKind      agentcore.SessionEventKind
	permissionKind agentcore.PermissionKind
	requestID      string
}

type RuntimeAuthorization struct {
	TenantID         string
	RunID            agentcore.AgentRunID
	AgentID          agentcore.AgentID
	RuntimeKind      agentcore.RuntimeKind
	MissionID        string
	TaskID           string
	ContextAgentID   agentcore.AgentID
	ContextMissionID string
	ContextTaskID    string
}

type RuntimeAuthorizer interface {
	AuthorizeRuntime(context.Context, RuntimeAuthorization) error
}

type RuntimeAuthorizerFunc func(context.Context, RuntimeAuthorization) error

func (f RuntimeAuthorizerFunc) AuthorizeRuntime(ctx context.Context, request RuntimeAuthorization) error {
	return f(ctx, request)
}

type RuntimeOptions struct {
	Kind               agentcore.RuntimeKind
	Connection         agentcore.BackendConnection
	Workspaces         *WorkspaceBroker
	Authorizer         RuntimeAuthorizer
	ModelResolver      RuntimeModelResolver
	PermissionHandler  agentcore.SessionPermissionHandler
	AuthHandler        agentcore.SessionAuthHandler
	DefaultEnvironment map[string]string
	Now                func() time.Time
}

type Runtime struct {
	kind        agentcore.RuntimeKind
	connection  agentcore.BackendConnection
	workspaces  *WorkspaceBroker
	authorizer  RuntimeAuthorizer
	models      RuntimeModelResolver
	permissions agentcore.SessionPermissionHandler
	auth        agentcore.SessionAuthHandler
	defaultEnv  map[string]string
	now         func() time.Time

	mu          sync.Mutex
	active      map[agentcore.AgentRunID]*activeSessionRun
	prewarmed   map[agentcore.AgentRunID]*openSessionRun
	records     map[agentcore.AgentRunID]*runtimeRecord
	subscribers map[agentcore.AgentRunID]map[uint64]*runtimeEventSubscriber
	nextSubID   uint64
}

type activeSessionRun struct {
	open *openSessionRun
}

type openSessionRun struct {
	request      agentcore.RuntimeRequest
	sessionID    agentcore.SessionID
	workspaceRef string
	authBinding  RuntimeAuthBinding
	backend      agentcore.SessionBackend
	orchestrator *Orchestrator
	conversation *ConversationSession
}

type backendSessionIDSource interface {
	BackendSessionID() string
}

type runtimeRecord struct {
	request  agentcore.RuntimeRequest
	response agentcore.RuntimeResponse
	events   []agentcore.SessionEnvelope
	skipped  uint64
}

type runtimeEventSubscriber struct {
	ch      chan agentcore.SessionEnvelope
	skipped uint64
}

func NewRuntime(options RuntimeOptions) (*Runtime, error) {
	if !options.Kind.IsValid() || options.Connection == nil || options.Workspaces == nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "canonical runtime requires kind, connection, and workspace broker", false)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Runtime{
		kind: options.Kind, connection: options.Connection, workspaces: options.Workspaces, authorizer: options.Authorizer, models: options.ModelResolver, permissions: options.PermissionHandler, auth: options.AuthHandler, defaultEnv: cloneStringMap(options.DefaultEnvironment), now: options.Now,
		active: make(map[agentcore.AgentRunID]*activeSessionRun), prewarmed: make(map[agentcore.AgentRunID]*openSessionRun), records: make(map[agentcore.AgentRunID]*runtimeRecord), subscribers: make(map[agentcore.AgentRunID]map[uint64]*runtimeEventSubscriber),
	}, nil
}

func (r *Runtime) RunSession(ctx context.Context, request agentcore.RuntimeRequest, observer agentcore.SessionEventObserver) (response agentcore.RuntimeResponse, runErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request = r.prepareRequest(request)
	if err := r.authorize(ctx, request); err != nil {
		return agentcore.RuntimeResponse{}, err
	}
	request, err := r.resolveModel(ctx, request)
	if err != nil {
		return agentcore.RuntimeResponse{}, err
	}
	open, err := r.takeOrOpen(ctx, request)
	if err != nil {
		return agentcore.RuntimeResponse{}, err
	}
	defer func() {
		if source, ok := open.backend.(interface{ CanResumeTask() bool }); ok && source.CanResumeTask() {
			if response.ProviderMetadata == nil {
				response.ProviderMetadata = map[string]string{}
			}
			response.ProviderMetadata["task_resume_ready"] = "true"
		}
	}()
	r.mu.Lock()
	if _, exists := r.active[request.RunID]; exists {
		r.mu.Unlock()
		r.closeOpen(context.Background(), open)
		return agentcore.RuntimeResponse{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "runtime run is already active", false)
	}
	r.active[request.RunID] = &activeSessionRun{open: open}
	r.records[request.RunID] = &runtimeRecord{request: agentcore.RuntimeRequestRecordProjection(request)}
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.active, request.RunID)
		r.mu.Unlock()
		r.closeOpen(context.Background(), open)
	}()

	subscription := open.conversation.SubscribeEvents()
	defer subscription.Close()
	open.conversation.Start(context.Background())
	startedAt := r.now().UTC()
	projection := NewResponseProjection(request, r.kind, startedAt)
	if source, ok := open.backend.(backendSessionIDSource); ok {
		projection.bindBackendSessionID(source.BackendSessionID())
	}
	commandID := "run-" + request.RunID.String()
	clientMsgID := "message-" + request.RunID.String()
	if _, err := open.conversation.SendWithIDs(ctx, commandID, clientMsgID, runtimeRequestContent(request)); err != nil {
		return agentcore.RuntimeResponse{}, err
	}

	deadline, hasDeadline := runtimeDeadline(ctx, request)
	var deadlineTimer *time.Timer
	var deadlineC <-chan time.Time
	if hasDeadline {
		duration := time.Until(deadline)
		if duration < 0 {
			duration = 0
		}
		deadlineTimer = time.NewTimer(duration)
		deadlineC = deadlineTimer.C
		defer deadlineTimer.Stop()
	}

	timedOut := false
	cancelled := false
	seenPermissionEvents := make(map[runtimePermissionEventKey]struct{})
	var cancelWait <-chan time.Time
	contextDone := ctx.Done()
	for {
		select {
		case envelope, ok := <-subscription.Events():
			if !ok {
				response := projection.Response(r.now())
				if !projection.Terminal() {
					return response, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "canonical event stream closed before terminal result", true)
				}
				return r.finishResponse(request.RunID, response, timedOut), nil
			}
			if envelope.Event.Kind == agentcore.EventLagged {
				_ = r.cancelOpen(open, "canonical consumer lagged")
				return projection.Response(r.now()), agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "canonical business consumer lagged behind the authoritative event stream", true)
			}
			if key, identified := runtimePermissionEventIdentity(envelope); identified {
				if _, duplicate := seenPermissionEvents[key]; duplicate {
					continue
				}
				seenPermissionEvents[key] = struct{}{}
			}
			if err := projection.Fold(envelope); err != nil {
				_ = r.cancelOpen(open, "canonical projection rejected event")
				return projection.Response(r.now()), err
			}
			r.recordEnvelope(request.RunID, envelope)
			if envelope.Event.Kind == agentcore.EventPermission && envelope.Event.Permission.Kind == agentcore.PermissionKindTool && r.permissions != nil && open.backend.Capabilities().SupportedCommands.AnswerPermission {
				permission := envelope.Event.Permission
				permissionRequest := agentcore.SessionPermissionRequest{
					TenantID: request.TenantID, RunID: request.RunID, AgentID: request.AgentID, RuntimeKind: r.kind, Envelope: cloneEnvelope(envelope),
				}
				permissionCtx := sessionPermissionContext(ctx, open.backend, permission.RequestID)
				if err := r.permissions.RequestSessionPermission(permissionCtx, permissionRequest, func(answer agentcore.AnswerPermissionCommand) error {
					_, dispatchErr := open.conversation.Dispatch(context.WithoutCancel(ctx), agentcore.RuntimeCommand{
						CommandID: "permission-" + uuid.NewString(), Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &answer,
					})
					return dispatchErr
				}); err != nil {
					_ = r.cancelOpen(open, "application permission handler failed")
					return projection.Response(r.now()), err
				}
			}
			if envelope.Event.Kind == agentcore.EventPermission && envelope.Event.Permission.Kind == agentcore.PermissionKindAuth {
				if r.auth == nil {
					_ = r.cancelOpen(open, "application auth handler is unavailable")
					return projection.Response(r.now()), agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "runtime auth is not configured", false)
				}
				authRequest := agentcore.SessionAuthRequest{
					TenantID: request.TenantID, RunID: request.RunID, AgentID: request.AgentID, RuntimeKind: r.kind,
					Envelope: cloneEnvelope(envelope), MethodID: open.authBinding.MethodID, CredentialRef: open.authBinding.CredentialRef,
				}
				if err := r.auth.RequestSessionAuth(ctx, authRequest, func(answer agentcore.AnswerAuthCommand) error {
					_, dispatchErr := open.conversation.Dispatch(context.WithoutCancel(ctx), agentcore.RuntimeCommand{
						CommandID: "auth-" + uuid.NewString(), Kind: agentcore.RuntimeCommandAnswerAuth, AnswerAuth: &answer,
					})
					return dispatchErr
				}); err != nil {
					_ = r.cancelOpen(open, "application auth handler failed")
					return projection.Response(r.now()), err
				}
			}
			if observer != nil {
				if err := observer(context.WithoutCancel(ctx), envelope); err != nil {
					_ = r.cancelOpen(open, "application observer failed")
					return projection.Response(r.now()), err
				}
			}
			if projection.Terminal() {
				response := projection.Response(r.now())
				return r.finishResponse(request.RunID, response, timedOut), nil
			}
		case <-deadlineC:
			if !cancelled {
				timedOut = true
				cancelled = true
				deadlineC = nil
				contextDone = nil
				_ = r.cancelOpen(open, "application deadline exceeded")
				cancelWait = time.After(defaultTerminalCancelWait)
			}
		case <-contextDone:
			if !cancelled {
				timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
				cancelled = true
				deadlineC = nil
				contextDone = nil
				_ = r.cancelOpen(open, "application context cancelled")
				cancelWait = time.After(defaultTerminalCancelWait)
			}
		case <-cancelWait:
			response := projection.Response(r.now())
			if timedOut {
				response.Status = agentcore.RuntimeStatusTimeout
				response.Error = agentcore.RuntimeFailure{Code: "runtime_timeout", Message: "runtime deadline exceeded", Retryable: true}
			} else {
				response.Status = agentcore.RuntimeStatusCancelled
				response.Error = agentcore.RuntimeFailure{Code: "runtime_cancelled", Message: "runtime context cancelled"}
			}
			response.CompletedAt = r.now().UTC()
			response.DurationMs = response.CompletedAt.Sub(startedAt).Milliseconds()
			return r.finishResponse(request.RunID, response, timedOut), nil
		}
	}
}

func runtimePermissionEventIdentity(envelope agentcore.SessionEnvelope) (runtimePermissionEventKey, bool) {
	key := runtimePermissionEventKey{turnGeneration: envelope.TurnGeneration, eventKind: envelope.Event.Kind}
	switch envelope.Event.Kind {
	case agentcore.EventPermission:
		if envelope.Event.Permission == nil {
			return runtimePermissionEventKey{}, false
		}
		key.permissionKind = envelope.Event.Permission.Kind
		key.requestID = strings.TrimSpace(envelope.Event.Permission.RequestID)
	case agentcore.EventPermissionResolved:
		if envelope.Event.PermissionResolved == nil {
			return runtimePermissionEventKey{}, false
		}
		key.permissionKind = envelope.Event.PermissionResolved.Kind
		key.requestID = strings.TrimSpace(envelope.Event.PermissionResolved.RequestID)
	default:
		return runtimePermissionEventKey{}, false
	}
	return key, key.requestID != ""
}

func (r *Runtime) CancelSession(ctx context.Context, runID agentcore.AgentRunID, reason string) error {
	open, err := r.activeOpen(runID)
	if err != nil {
		return err
	}
	return r.cancelOpenWithContext(ctx, open, reason)
}

func (r *Runtime) SteerSession(ctx context.Context, runID agentcore.AgentRunID, message string) error {
	open, err := r.activeOpen(runID)
	if err != nil {
		return err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "steer message is required", false)
	}
	_, err = open.conversation.Dispatch(ctx, agentcore.RuntimeCommand{
		CommandID: "steer-" + uuid.NewString(), Kind: agentcore.RuntimeCommandSteer,
		Steer: &agentcore.SteerCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: message}}},
	})
	return err
}

func (r *Runtime) PrewarmSession(ctx context.Context, request agentcore.RuntimeRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	request = r.prepareRequest(request)
	if err := r.authorize(ctx, request); err != nil {
		return err
	}
	request, err := r.resolveModel(ctx, request)
	if err != nil {
		return err
	}
	r.mu.Lock()
	_, active := r.active[request.RunID]
	existing := r.prewarmed[request.RunID]
	if existing != nil && !samePrewarmRequest(existing.request, request) {
		delete(r.prewarmed, request.RunID)
	}
	r.mu.Unlock()
	if active {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "runtime run is already active", false)
	}
	if existing != nil && samePrewarmRequest(existing.request, request) {
		return nil
	}
	if existing != nil {
		r.closeOpen(context.Background(), existing)
	}
	open, err := r.open(ctx, request)
	if err != nil {
		return err
	}
	open.conversation.Start(context.Background())
	r.mu.Lock()
	if _, exists := r.prewarmed[request.RunID]; exists {
		r.mu.Unlock()
		r.closeOpen(context.Background(), open)
		return nil
	}
	r.prewarmed[request.RunID] = open
	r.mu.Unlock()
	return nil
}

func (r *Runtime) prepareRequest(request agentcore.RuntimeRequest) agentcore.RuntimeRequest {
	request = cloneRuntimeRequest(request)
	if request.RuntimeOptions.ProviderSpecific == nil {
		request.RuntimeOptions.ProviderSpecific = make(map[string]string)
	}
	request.RuntimeOptions.ProviderSpecific["runtime_kind"] = string(r.kind)
	if len(r.defaultEnv) == 0 {
		return request
	}
	environment := cloneStringMap(r.defaultEnv)
	if environment == nil {
		environment = make(map[string]string)
	}
	for key, value := range request.RuntimeOptions.Environment {
		environment[key] = value
	}
	request.RuntimeOptions.Environment = environment
	return request
}

func (r *Runtime) ReleaseRuntimeWorkspace(ctx context.Context, root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	r.mu.Lock()
	var retire []*openSessionRun
	var cancel []*openSessionRun
	for runID, open := range r.prewarmed {
		if runtimeRequestWorkspaceRoot(open.request) == root {
			retire = append(retire, open)
			delete(r.prewarmed, runID)
		}
	}
	for _, active := range r.active {
		if runtimeRequestWorkspaceRoot(active.open.request) == root {
			cancel = append(cancel, active.open)
		}
	}
	r.mu.Unlock()
	for _, open := range cancel {
		_ = r.cancelOpen(open, "workspace released")
	}
	for _, open := range retire {
		r.closeOpen(ctx, open)
	}
	if releaser, ok := r.connection.(interface {
		ReleaseRuntimeWorkspace(context.Context, string) error
	}); ok {
		return releaser.ReleaseRuntimeWorkspace(ctx, root)
	}
	return nil
}

func (r *Runtime) Capabilities(ctx context.Context) agentcore.Capabilities {
	return r.connection.Capabilities(ctx).Clone()
}

func (r *Runtime) RuntimeRequest(runID agentcore.AgentRunID) (agentcore.RuntimeRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.records[runID]
	if record == nil {
		return agentcore.RuntimeRequest{}, false
	}
	return cloneRuntimeRequest(record.request), true
}

func (r *Runtime) RuntimeResponse(runID agentcore.AgentRunID) (agentcore.RuntimeResponse, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.records[runID]
	if record == nil || record.response.Status == "" {
		return agentcore.RuntimeResponse{}, false
	}
	return cloneRuntimeResponse(record.response), true
}

func (r *Runtime) RuntimeEvents(runID agentcore.AgentRunID) []agentcore.SessionEnvelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.records[runID]
	if record == nil {
		return nil
	}
	events := cloneEnvelopes(record.events)
	if record.skipped > 0 && len(events) > 0 {
		events = append([]agentcore.SessionEnvelope{laggedEnvelope(events[0], record.skipped)}, events...)
	}
	return events
}

func (r *Runtime) SubscribeRuntimeEvents(runID agentcore.AgentRunID) (<-chan agentcore.SessionEnvelope, func()) {
	channel := make(chan agentcore.SessionEnvelope, 256)
	r.mu.Lock()
	record := r.records[runID]
	var replay []agentcore.SessionEnvelope
	var skipped uint64
	if record != nil {
		replay = cloneEnvelopes(record.events)
		skipped = record.skipped
	}
	if len(replay) > cap(channel) {
		skipped += uint64(len(replay) - cap(channel) + 1)
		replay = replay[len(replay)-cap(channel)+1:]
	}
	if skipped > 0 && len(replay) > 0 {
		channel <- laggedEnvelope(replay[0], skipped)
	}
	for _, envelope := range replay {
		channel <- envelope
	}
	r.nextSubID++
	id := r.nextSubID
	if r.subscribers[runID] == nil {
		r.subscribers[runID] = make(map[uint64]*runtimeEventSubscriber)
	}
	r.subscribers[runID][id] = &runtimeEventSubscriber{ch: channel}
	r.mu.Unlock()
	var once sync.Once
	return channel, func() {
		once.Do(func() {
			r.mu.Lock()
			if subscribers := r.subscribers[runID]; subscribers != nil {
				if subscriber := subscribers[id]; subscriber != nil {
					delete(subscribers, id)
					close(subscriber.ch)
				}
				if len(subscribers) == 0 {
					delete(r.subscribers, runID)
				}
			}
			r.mu.Unlock()
		})
	}
}

func (r *Runtime) DeleteRuntimeRecord(_ context.Context, runID agentcore.AgentRunID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.records, runID)
	return nil
}

func (r *Runtime) authorize(ctx context.Context, request agentcore.RuntimeRequest) error {
	if request.RunID == "" || request.AgentID.IsZero() || strings.TrimSpace(request.TenantID) == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime execution requires tenant, run, and agent identity", false)
	}
	if envelope := request.ExecutionEnvelope; envelope != nil {
		if err := envelope.Verify(); err != nil {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime execution envelope is invalid", false)
		}
		if !envelope.ExpiresAt.IsZero() && !envelope.ExpiresAt.After(time.Now().UTC()) {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime execution envelope has expired", false)
		}
		if strings.TrimSpace(envelope.TenantID) != strings.TrimSpace(request.TenantID) ||
			strings.TrimSpace(envelope.RunID) != strings.TrimSpace(request.RunID.String()) ||
			strings.TrimSpace(envelope.AgentID) != strings.TrimSpace(request.AgentID.String()) {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeSessionMismatch, "runtime execution envelope identity does not match request", false)
		}
	}
	if r.authorizer == nil {
		return nil
	}
	return r.authorizer.AuthorizeRuntime(ctx, RuntimeAuthorization{
		TenantID: strings.TrimSpace(request.TenantID), RunID: request.RunID, AgentID: request.AgentID, RuntimeKind: r.kind,
		MissionID: strings.TrimSpace(request.MissionID), TaskID: strings.TrimSpace(request.TaskID), ContextAgentID: request.ContextPack.AgentID,
		ContextMissionID: strings.TrimSpace(string(request.ContextPack.MissionID)), ContextTaskID: strings.TrimSpace(string(request.ContextPack.TaskID)),
	})
}

func (r *Runtime) takeOrOpen(ctx context.Context, request agentcore.RuntimeRequest) (*openSessionRun, error) {
	r.mu.Lock()
	open := r.prewarmed[request.RunID]
	if open != nil {
		delete(r.prewarmed, request.RunID)
	}
	r.mu.Unlock()
	if open != nil {
		if err := r.validateExplicitToolScopeFence(request); err != nil {
			r.closeOpen(context.Background(), open)
			return nil, err
		}
		if !sameRuntimeRequestIdentity(open.request, request) {
			r.closeOpen(context.Background(), open)
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "prewarmed session request identity changed", false)
		}
		if !samePrewarmRequest(open.request, request) {
			r.closeOpen(context.Background(), open)
			return r.open(ctx, request)
		}
		open.request = cloneRuntimeRequest(request)
		return open, nil
	}
	return r.open(ctx, request)
}

func (r *Runtime) open(ctx context.Context, request agentcore.RuntimeRequest) (*openSessionRun, error) {
	if err := r.validateExplicitToolScopeFence(request); err != nil {
		return nil, err
	}
	binding, err := r.workspaces.Bind(ctx, request)
	if err != nil {
		return nil, err
	}
	sessionID := agentcore.SessionID("session-" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	if binder, ok := r.connection.(interface {
		BindRuntimeRequest(agentcore.SessionID, agentcore.RuntimeRequest) error
	}); ok {
		if err := binder.BindRuntimeRequest(sessionID, request); err != nil {
			r.workspaces.Release(binding.Ref)
			return nil, err
		}
	}
	spec := agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: sessionID}
	if backendSessionID := strings.TrimSpace(request.RuntimeOptions.ResumeSessionID); backendSessionID != "" {
		spec.Mode = agentcore.SessionOpenResume
		spec.BackendSessionID = backendSessionID
	}
	config := runtimeSessionConfig(request, binding.Ref, binding.MCP)
	if policy, ok := r.connection.(agentcore.SessionConfigPolicy); ok {
		config, err = policy.PrepareSessionConfig(ctx, spec, config)
		if err != nil {
			r.workspaces.Release(binding.Ref)
			return nil, err
		}
	}
	backend, err := r.connection.OpenSession(ctx, spec, config)
	if err != nil {
		r.workspaces.Release(binding.Ref)
		return nil, err
	}
	orchestrator, err := NewOrchestrator(sessionID, backend, nil, OrchestratorOptions{LaneCapacity: 512, BroadcastCapacity: 4096})
	if err != nil {
		_ = r.connection.CloseSession(context.Background(), sessionID)
		r.workspaces.Release(binding.Ref)
		return nil, err
	}
	conversation, err := NewConversationSession(sessionID, orchestrator)
	if err != nil {
		orchestrator.Close()
		_ = r.connection.CloseSession(context.Background(), sessionID)
		r.workspaces.Release(binding.Ref)
		return nil, err
	}
	return &openSessionRun{request: cloneRuntimeRequest(request), sessionID: sessionID, workspaceRef: binding.Ref, authBinding: cloneRuntimeAuthBinding(binding.Auth), backend: backend, orchestrator: orchestrator, conversation: conversation}, nil
}

func (r *Runtime) closeOpen(ctx context.Context, open *openSessionRun) {
	if open == nil {
		return
	}
	open.orchestrator.Close()
	_ = r.connection.CloseSession(ctx, open.sessionID)
	r.workspaces.Release(open.workspaceRef)
}

func (r *Runtime) cancelOpen(open *openSessionRun, reason string) error {
	cancelCtx, cancel := context.WithTimeout(context.Background(), defaultTerminalCancelWait)
	defer cancel()
	return r.cancelOpenWithContext(cancelCtx, open, reason)
}

func (r *Runtime) cancelOpenWithContext(ctx context.Context, open *openSessionRun, reason string) error {
	if open == nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime run is not active", false)
	}
	_, err := open.conversation.Cancel(ctx, agentcore.CancelTargetTurn, "", boundedCancellationReason(reason))
	return err
}

func (r *Runtime) activeOpen(runID agentcore.AgentRunID) (*openSessionRun, error) {
	if runID == "" {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "run id is required", false)
	}
	r.mu.Lock()
	active := r.active[runID]
	r.mu.Unlock()
	if active == nil || active.open == nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime run is not active", false)
	}
	return active.open, nil
}

func (r *Runtime) finishResponse(runID agentcore.AgentRunID, response agentcore.RuntimeResponse, timedOut bool) agentcore.RuntimeResponse {
	if timedOut && response.Status != agentcore.RuntimeStatusCompleted {
		response.Status = agentcore.RuntimeStatusTimeout
		response.Error = agentcore.RuntimeFailure{Code: "runtime_timeout", Message: "runtime deadline exceeded", Retryable: true}
	}
	r.mu.Lock()
	if record := r.records[runID]; record != nil {
		record.response = cloneRuntimeResponse(response)
	}
	r.mu.Unlock()
	return response
}

func (r *Runtime) recordEnvelope(runID agentcore.AgentRunID, envelope agentcore.SessionEnvelope) {
	r.mu.Lock()
	record := r.records[runID]
	if record == nil {
		record = &runtimeRecord{}
		r.records[runID] = record
	}
	if len(record.events) >= maxRecordedRuntimeEvents {
		record.events = append([]agentcore.SessionEnvelope(nil), record.events[len(record.events)-maxRecordedRuntimeEvents+1:]...)
		record.skipped++
	}
	record.events = append(record.events, cloneEnvelope(envelope))
	for _, subscriber := range r.subscribers[runID] {
		if subscriber.skipped > 0 {
			lag := laggedEnvelope(envelope, subscriber.skipped)
			select {
			case subscriber.ch <- lag:
				subscriber.skipped = 0
			default:
				subscriber.skipped++
				continue
			}
		}
		select {
		case subscriber.ch <- cloneEnvelope(envelope):
		default:
			subscriber.skipped++
		}
	}
	r.mu.Unlock()
}

func runtimeSessionConfig(request agentcore.RuntimeRequest, workspaceRef string, mcp []RuntimeMCPBinding) agentcore.SessionConfig {
	options := make(map[string]string)
	for _, key := range []string{"reasoning_effort", "effort"} {
		if value := strings.TrimSpace(request.RuntimeOptions.ProviderSpecific[key]); value != "" {
			options["reasoning_effort"] = value
			break
		}
	}
	if len(options) == 0 {
		options = nil
	}
	servers := make([]agentcore.MCPServerSpec, len(mcp))
	for index, binding := range mcp {
		servers[index] = binding.Spec
		servers[index].HeaderRefs = cloneStringMap(binding.Spec.HeaderRefs)
	}
	return agentcore.SessionConfig{
		WorkspaceRef:  workspaceRef,
		Model:         strings.TrimSpace(request.RuntimeOptions.Model),
		Mode:          strings.TrimSpace(request.RuntimeOptions.ProviderSpecific["mode"]),
		ConfigOptions: options,
		Preset:        strings.TrimSpace(request.RuntimeOptions.SystemPrompt),
		MCPServers:    servers,
	}
}

func runtimeRequestContent(request agentcore.RuntimeRequest) []agentcore.ContentBlock {
	prompt := strings.TrimSpace(agentcore.RuntimePromptMessagesFallbackPrompt(request.InputMessages))
	content := make([]agentcore.ContentBlock, 0, len(request.InputMedia)+1)
	if prompt != "" {
		content = append(content, agentcore.ContentBlock{Kind: agentcore.ContentBlockText, Text: prompt})
	}
	content = append(content, cloneContent(request.InputMedia)...)
	return content
}

func runtimeDeadline(ctx context.Context, request agentcore.RuntimeRequest) (time.Time, bool) {
	deadline := request.Deadline
	if request.RuntimeOptions.Timeout > 0 {
		candidate := time.Now().Add(request.RuntimeOptions.Timeout)
		if deadline.IsZero() || candidate.Before(deadline) {
			deadline = candidate
		}
	}
	if contextDeadline, ok := ctx.Deadline(); ok && (deadline.IsZero() || contextDeadline.Before(deadline)) {
		deadline = contextDeadline
	}
	return deadline, !deadline.IsZero()
}

func runtimeRequestWorkspaceRoot(request agentcore.RuntimeRequest) string {
	if value := strings.TrimSpace(request.RuntimeOptions.WorkspacePolicy.Root); value != "" {
		return value
	}
	if value := strings.TrimSpace(request.RuntimeOptions.SandboxProfile.Root); value != "" {
		return value
	}
	return strings.TrimSpace(request.RuntimeOptions.WorkingDirectory)
}

func sameRuntimeRequestIdentity(left, right agentcore.RuntimeRequest) bool {
	if left.RunID != right.RunID || left.AgentID != right.AgentID || strings.TrimSpace(left.TenantID) != strings.TrimSpace(right.TenantID) {
		return false
	}
	if left.ExecutionEnvelope == nil || right.ExecutionEnvelope == nil {
		return left.ExecutionEnvelope == right.ExecutionEnvelope
	}
	return strings.TrimSpace(left.ExecutionEnvelope.EnvelopeID) == strings.TrimSpace(right.ExecutionEnvelope.EnvelopeID) &&
		strings.TrimSpace(left.ExecutionEnvelope.EnvelopeDigest) == strings.TrimSpace(right.ExecutionEnvelope.EnvelopeDigest)
}

func samePrewarmRequest(left, right agentcore.RuntimeRequest) bool {
	return sameRuntimeRequestIdentity(left, right) &&
		left.MissionID == right.MissionID && left.TaskID == right.TaskID &&
		reflect.DeepEqual(left.ExecutionPrincipal, right.ExecutionPrincipal) &&
		reflect.DeepEqual(left.RuntimeOptions, right.RuntimeOptions) &&
		reflect.DeepEqual(left.ContextPack, right.ContextPack) &&
		reflect.DeepEqual(left.ToolBindings, right.ToolBindings) &&
		reflect.DeepEqual(left.SkillBindings, right.SkillBindings)
}

func (r *Runtime) validateExplicitToolScopeFence(request agentcore.RuntimeRequest) error {
	responsibility := request.ContextPack.ResponsibilityContext
	if responsibility == nil || !responsibility.RequireExplicitToolScopes || r.kind.SupportsExplicitToolScopeFence() {
		return nil
	}
	if r.kind == agentcore.RuntimeKindManaged {
		if fenced, ok := r.connection.(interface{ SupportsExplicitToolScopeFence() bool }); ok && fenced.SupportsExplicitToolScopeFence() {
			return nil
		}
	}
	return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "runtime kind cannot enforce explicit task tool scopes", false)
}

func boundedCancellationReason(reason string) string {
	reason = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(reason, "\r", " "), "\n", " "))
	if len(reason) > 240 {
		reason = reason[:240]
	}
	return reason
}

func cloneEnvelope(envelope agentcore.SessionEnvelope) agentcore.SessionEnvelope {
	out := envelope
	out.Event = envelope.Event.Clone()
	return out
}

func cloneEnvelopes(envelopes []agentcore.SessionEnvelope) []agentcore.SessionEnvelope {
	out := make([]agentcore.SessionEnvelope, len(envelopes))
	for index, envelope := range envelopes {
		out[index] = cloneEnvelope(envelope)
	}
	return out
}

func laggedEnvelope(latest agentcore.SessionEnvelope, skipped uint64) agentcore.SessionEnvelope {
	return agentcore.SessionEnvelope{
		SessionID: latest.SessionID, TurnGeneration: latest.TurnGeneration, Sequence: latest.Sequence, Timestamp: latest.Timestamp,
		Source: agentcore.EventSourceOrchestrator, Projection: agentcore.EventProjectionCanonical,
		Event: agentcore.SessionEvent{Kind: agentcore.EventLagged, Lagged: &agentcore.LaggedEvent{Skipped: skipped}},
	}
}

func (r RuntimeAuthorization) validate() error {
	if strings.TrimSpace(r.TenantID) == "" || r.RunID == "" || r.AgentID.IsZero() || !r.RuntimeKind.IsValid() {
		return fmt.Errorf("runtime authorization identity is incomplete")
	}
	return nil
}

var _ agentcore.SessionRuntime = (*Runtime)(nil)
var _ agentcore.SessionRuntimeRecordReader = (*Runtime)(nil)

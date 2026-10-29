package providers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/agent/runtime/providers/pi"
	runtimesession "orrery_v2/internal/agent/runtime/session"
)

type compatibilityRunner struct {
	closed atomic.Bool
	run    func(context.Context, agentcore.SendCommand, kernelcompat.Emit) (agentcore.TurnResultEvent, error)
}

func (r *compatibilityRunner) Run(ctx context.Context, input agentcore.SendCommand, emit kernelcompat.Emit) (agentcore.TurnResultEvent, error) {
	if r.run != nil {
		return r.run(ctx, input, emit)
	}
	if err := emit(agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "answer", Text: "kernel response"}}); err != nil {
		return agentcore.TurnResultEvent{}, err
	}
	return agentcore.TurnResultEvent{ResultText: "kernel response", Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonEndTurn}}}, nil
}
func (r *compatibilityRunner) Close(context.Context) error { r.closed.Store(true); return nil }

func TestManagedKernelRegistrationPreservesNativePiAndDoesNotOpenEitherEngine(t *testing.T) {
	var opens atomic.Int32
	config := &kernelcompat.Config{Engine: kernelcompat.EngineFunc(func(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (kernelcompat.Runner, error) {
		opens.Add(1)
		return nil, errors.New("engine unavailable")
	})}
	for _, managed := range []*kernelcompat.Config{nil, config} {
		registry, err := NewSessionRegistry(SessionRegistryConfig{ManagedKernel: managed})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = registry.Close() })
		connection, err := registry.Resolve(agentcore.RuntimeKindPi)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := connection.(*pi.NativeConnection); !ok {
			t.Fatalf("PI was wrapped or replaced: %T", connection)
		}
		caps, ok := registry.Capabilities(context.Background(), agentcore.RuntimeKindPi)
		if !ok || !caps.SupportsResume || !caps.SupportedCommands.AnswerPermission || caps.Transport.Protocol != "rpc-jsonl-stdio" {
			t.Fatalf("PI capabilities changed: %+v", caps)
		}
		connection, err = registry.Resolve(agentcore.RuntimeKindManaged)
		if managed == nil {
			if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeWorkspaceUnavailable) {
				t.Fatalf("unconfigured kernel became available: %v", err)
			}
		} else if _, ok := connection.(*kernelcompat.Connection); err != nil || !ok {
			t.Fatalf("explicit kernel = %T, %v", connection, err)
		}
	}
	if opens.Load() != 0 {
		t.Fatal("registration or PI resolution eagerly opened managed engine")
	}
}

func TestManagedKernelRunsThroughCanonicalRuntimeAndAuthorization(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "denied", true: "allowed"}[allow], func(t *testing.T) {
			var opens atomic.Int32
			var authorized atomic.Bool
			runner := &compatibilityRunner{}
			workspaces := runtimesession.NewWorkspaceBroker()
			registry, err := NewSessionRegistry(SessionRegistryConfig{
				Workspaces: workspaces,
				Authorizer: runtimesession.RuntimeAuthorizerFunc(func(_ context.Context, auth runtimesession.RuntimeAuthorization) error {
					if auth.TenantID != "tenant-public" || auth.AgentID != "A000014" || auth.RuntimeKind != agentcore.RuntimeKindManaged {
						t.Errorf("authorization lost identity: %+v", auth)
					}
					if !allow {
						return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeSessionMismatch, "denied", false)
					}
					authorized.Store(true)
					return nil
				}),
				ManagedKernel: &kernelcompat.Config{Engine: kernelcompat.EngineFunc(func(_ context.Context, _ agentcore.SessionSpec, config agentcore.SessionConfig) (kernelcompat.Runner, error) {
					opens.Add(1)
					if !authorized.Load() {
						t.Error("engine opened before authorization")
					}
					binding, err := workspaces.Resolve(config.WorkspaceRef)
					if err != nil || binding.Request.TenantID != "tenant-public" || binding.Request.AgentID != "A000014" {
						t.Errorf("workspace identity changed: %+v, %v", binding.Request, err)
					}
					return runner, nil
				})},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = registry.Close() })
			runtime, err := registry.ResolveRuntime(context.Background(), agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			response, err := runtime.RunSession(ctx, agentcore.RuntimeRequest{TenantID: "tenant-public", AgentID: "A000014", RunID: "run-kernel-test",
				ContextPack: agentcore.ContextPack{AgentID: "A000014"}, InputMessages: []agentcore.RuntimeChatMessage{{Role: "user", Content: "hello"}},
				RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir()}}, nil)
			if !allow {
				if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeSessionMismatch) || opens.Load() != 0 {
					t.Fatalf("denied run reached engine: %v, opens=%d", err, opens.Load())
				}
				return
			}
			if err != nil || response.Status != agentcore.RuntimeStatusCompleted || opens.Load() != 1 || !runner.closed.Load() {
				t.Fatalf("canonical runtime response = %+v, %v, opens=%d, closed=%v", response, err, opens.Load(), runner.closed.Load())
			}
		})
	}
}

func TestManagedKernelFailureNeverFallsBackOrDisablesPi(t *testing.T) {
	var opens atomic.Int32
	registry, err := NewSessionRegistry(SessionRegistryConfig{ManagedKernel: &kernelcompat.Config{Engine: kernelcompat.EngineFunc(func(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (kernelcompat.Runner, error) {
		opens.Add(1)
		return nil, errors.New("unavailable")
	})}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	runtime, err := registry.ResolveRuntime(context.Background(), agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.RunSession(context.Background(), agentcore.RuntimeRequest{TenantID: "tenant", AgentID: "A000017", RunID: "failed-kernel", InputMessages: []agentcore.RuntimeChatMessage{{Role: "user", Content: "hello"}}, RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir()}}, nil)
	if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeBackendRejected) || opens.Load() != 1 {
		t.Fatalf("failure did not stay on selected engine: %v, opens=%d", err, opens.Load())
	}
	if connection, err := registry.Resolve(agentcore.RuntimeKindPi); err != nil {
		t.Fatal(err)
	} else if _, ok := connection.(*pi.NativeConnection); !ok {
		t.Fatalf("PI changed after kernel failure: %T", connection)
	}
}

func TestManagedKernelCanonicalCancellationDoesNotBecomeAnEmptyTurnFailure(t *testing.T) {
	started := make(chan struct{})
	runner := &compatibilityRunner{run: func(ctx context.Context, _ agentcore.SendCommand, _ kernelcompat.Emit) (agentcore.TurnResultEvent, error) {
		close(started)
		<-ctx.Done()
		return agentcore.TurnResultEvent{}, ctx.Err()
	}}
	registry, err := NewSessionRegistry(SessionRegistryConfig{ManagedKernel: &kernelcompat.Config{Engine: kernelcompat.EngineFunc(func(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (kernelcompat.Runner, error) {
		return runner, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	runtime, err := registry.ResolveRuntime(context.Background(), agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type runResult struct {
		response agentcore.RuntimeResponse
		err      error
	}
	results := make(chan runResult, 1)
	request := agentcore.RuntimeRequest{TenantID: "tenant", AgentID: "A000017", RunID: "cancel-kernel",
		InputMessages:  []agentcore.RuntimeChatMessage{{Role: "user", Content: "hello"}},
		RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir()}}
	go func() {
		response, err := runtime.RunSession(ctx, request, nil)
		results <- runResult{response: response, err: err}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("kernel did not start")
	}
	if err := runtime.(*runtimesession.Runtime).CancelSession(ctx, request.RunID, "user cancelled"); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-results:
		if result.err != nil || result.response.Status != agentcore.RuntimeStatusCancelled || result.response.Error.Code != "runtime_cancelled" || !runner.closed.Load() {
			t.Fatalf("canonical cancellation = %+v, %v, closed=%v", result.response, result.err, runner.closed.Load())
		}
	case <-ctx.Done():
		t.Fatal("canonical cancellation did not settle")
	}
}

func TestManagedKernelCannotBypassExplicitToolScopeFence(t *testing.T) {
	var opens atomic.Int32
	registry, err := NewSessionRegistry(SessionRegistryConfig{ManagedKernel: &kernelcompat.Config{Engine: kernelcompat.EngineFunc(func(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (kernelcompat.Runner, error) {
		opens.Add(1)
		return &compatibilityRunner{}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	runtime, err := registry.ResolveRuntime(context.Background(), agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.RunSession(context.Background(), agentcore.RuntimeRequest{TenantID: "tenant-public", AgentID: "A000014", RunID: "scope-kernel",
		ContextPack:    agentcore.ContextPack{ResponsibilityContext: &agentcore.ResponsibilityContext{RequireExplicitToolScopes: true}},
		InputMessages:  []agentcore.RuntimeChatMessage{{Role: "user", Content: "search internal knowledge"}},
		RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir()}}, nil)
	if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeUnsupportedCommand) || opens.Load() != 0 {
		t.Fatalf("unsupported tool scope reached engine: %v, opens=%d", err, opens.Load())
	}
}

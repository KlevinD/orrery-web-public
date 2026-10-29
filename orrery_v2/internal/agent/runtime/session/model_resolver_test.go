package session

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

type testModelResolver func(context.Context, agentcore.RuntimeKind, agentcore.RuntimeRequest) (agentcore.RuntimeRequest, error)

func (f testModelResolver) ResolveRuntimeModel(ctx context.Context, kind agentcore.RuntimeKind, request agentcore.RuntimeRequest) (agentcore.RuntimeRequest, error) {
	return f(ctx, kind, request)
}

func TestRuntimeRoutesModelAfterAuthorizationForPrewarmAndRun(t *testing.T) {
	connection := &prewarmBackendSessionConnection{}
	authorized, resolved := 0, 0
	runtime, err := NewRuntime(RuntimeOptions{
		Kind: agentcore.RuntimeKindPi, Connection: connection, Workspaces: NewWorkspaceBroker(),
		Authorizer: RuntimeAuthorizerFunc(func(context.Context, RuntimeAuthorization) error { authorized++; return nil }),
		ModelResolver: testModelResolver(func(_ context.Context, kind agentcore.RuntimeKind, req agentcore.RuntimeRequest) (agentcore.RuntimeRequest, error) {
			resolved++
			if authorized != resolved || kind != agentcore.RuntimeKindPi {
				t.Fatal("model routing preceded authorization")
			}
			req.RuntimeOptions.Model = "routed-model"
			req.RuntimeOptions.ProviderSpecific["resolved_llm_profile_id"] = "profile"
			return req, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := canonicalTestRequest(t, "model-routing")
	if err := runtime.PrewarmSession(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if runtime.prewarmed[request.RunID].request.RuntimeOptions.Model != "routed-model" {
		t.Fatal("prewarm did not route model")
	}
	response, err := runtime.RunSession(context.Background(), request, nil)
	if err != nil || response.Status != agentcore.RuntimeStatusCompleted {
		t.Fatalf("response=%+v error=%v", response, err)
	}
	if resolved != 2 || response.ProviderMetadata["resolved_llm_profile_id"] != "profile" {
		t.Fatalf("resolved=%d metadata=%v", resolved, response.ProviderMetadata)
	}
	if request.RuntimeOptions.Model != "" {
		t.Fatal("routing mutated caller")
	}
}

func TestRuntimeModelRoutingFailureNeverOpensBackend(t *testing.T) {
	connection := &prewarmBackendSessionConnection{}
	want := errors.New("missing model")
	runtime, err := NewRuntime(RuntimeOptions{Kind: agentcore.RuntimeKindPi, Connection: connection, Workspaces: NewWorkspaceBroker(), ModelResolver: testModelResolver(func(_ context.Context, _ agentcore.RuntimeKind, req agentcore.RuntimeRequest) (agentcore.RuntimeRequest, error) {
		return req, want
	})})
	if err != nil {
		t.Fatal(err)
	}
	request := canonicalTestRequest(t, "missing-model")
	if err := runtime.PrewarmSession(context.Background(), request); !errors.Is(err, want) {
		t.Fatalf("prewarm error=%v", err)
	}
	if _, err := runtime.RunSession(context.Background(), request, nil); !errors.Is(err, want) {
		t.Fatalf("run error=%v", err)
	}
	if connection.backend != nil {
		t.Fatal("backend opened after failed routing")
	}
}

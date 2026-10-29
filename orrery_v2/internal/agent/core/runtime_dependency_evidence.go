package core

import "context"

const RuntimeDependencyProtocol = "orrery-runtime-dispatch-dependencies-v1"

// RuntimeDependencyObservation is a host observation at callback entry, not an
// acknowledgement by a remote service. Digests contain no arguments or content.
// Tool dispatch does not prove success, skill loading or a knowledge revision.
type RuntimeDependencyObservation struct {
	TenantID      string `json:"tenant_id"`
	RunID         string `json:"run_id"`
	AgentID       string `json:"agent_id"`
	Kind          string `json:"kind"`
	InputSHA256   string `json:"input_sha256,omitempty"`
	ToolsSHA256   string `json:"tools_sha256,omitempty"`
	ModelIdentity string `json:"model_identity_sha256,omitempty"`
	ToolID        string `json:"tool_id,omitempty"`
	Version       string `json:"version,omitempty"`
	BindingSHA256 string `json:"binding_sha256,omitempty"`
	CallSHA256    string `json:"call_sha256,omitempty"`
}

type RuntimeDependencyTraceSession interface {
	RecordDependency(RuntimeDependencyObservation)
}

type runtimeDependencyObserverKey struct{}

// The observer exists only in host context. Request JSON, prompts and model
// events cannot install it. Collection performs no I/O or authorization changes.
func WithRuntimeDependencyObserver(ctx context.Context, observer func(RuntimeDependencyObservation)) context.Context {
	return context.WithValue(ctx, runtimeDependencyObserverKey{}, observer)
}

func HasRuntimeDependencyObserver(ctx context.Context) bool {
	observer, _ := ctx.Value(runtimeDependencyObserverKey{}).(func(RuntimeDependencyObservation))
	return observer != nil
}

func ObserveRuntimeDependency(ctx context.Context, observation RuntimeDependencyObservation) {
	if observer, ok := ctx.Value(runtimeDependencyObserverKey{}).(func(RuntimeDependencyObservation)); ok && observer != nil {
		observer(observation)
	}
}

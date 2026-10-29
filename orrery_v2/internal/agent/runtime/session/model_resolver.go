package session

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
)

// RuntimeModelResolver resolves model routing after authorization and before
// opening a backend. It must preserve execution identity and permission scope.
type RuntimeModelResolver interface {
	ResolveRuntimeModel(context.Context, agentcore.RuntimeKind, agentcore.RuntimeRequest) (agentcore.RuntimeRequest, error)
}

func (r *Runtime) resolveModel(ctx context.Context, request agentcore.RuntimeRequest) (agentcore.RuntimeRequest, error) {
	if r.models == nil {
		return request, nil
	}
	return r.models.ResolveRuntimeModel(ctx, r.kind, request)
}

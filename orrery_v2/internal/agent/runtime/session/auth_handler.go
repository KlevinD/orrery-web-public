package session

import (
	"context"
	"encoding/json"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

type RuntimeCredentialBroker interface {
	ResolveRuntimeCredential(context.Context, string, string) (json.RawMessage, error)
}

type CredentialAuthHandler struct {
	Broker RuntimeCredentialBroker
}

func (h CredentialAuthHandler) RequestSessionAuth(ctx context.Context, request agentcore.SessionAuthRequest, respond agentcore.SessionAuthResponder) error {
	if respond == nil || request.Envelope.Event.Permission == nil || request.Envelope.Event.Permission.Kind != agentcore.PermissionKindAuth {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime auth request is invalid", false)
	}
	if h.Broker == nil || strings.TrimSpace(request.CredentialRef) == "" || strings.TrimSpace(request.MethodID) == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "runtime auth requires an application credential binding", false)
	}
	credentials, err := h.Broker.ResolveRuntimeCredential(ctx, request.TenantID, request.CredentialRef)
	if err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "runtime auth credential could not be resolved", false)
	}
	var object map[string]any
	if len(credentials) == 0 || json.Unmarshal(credentials, &object) != nil || len(object) == 0 {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime auth credential is not a non-empty JSON object", false)
	}
	return respond(agentcore.AnswerAuthCommand{
		RequestID:   request.Envelope.Event.Permission.RequestID,
		MethodID:    strings.TrimSpace(request.MethodID),
		Credentials: append(json.RawMessage(nil), credentials...),
	})
}

var _ agentcore.SessionAuthHandler = CredentialAuthHandler{}

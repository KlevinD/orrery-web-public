package session

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestCanonicalRuntimePersistsContinuationMetadataAfterRealEventFlow(t *testing.T) {
	for _, kind := range []agentcore.RuntimeKind{agentcore.RuntimeKindPi, agentcore.RuntimeKindCodex, agentcore.RuntimeKindClaude, agentcore.RuntimeKindOpenClaw, agentcore.RuntimeKindACP} {
		t.Run(string(kind), func(t *testing.T) {
			runtime, err := NewRuntime(RuntimeOptions{Kind: kind, Connection: &prewarmBackendSessionConnection{}, Workspaces: NewWorkspaceBroker()})
			if err != nil {
				t.Fatal(err)
			}
			request := canonicalTestRequest(t, "continuation-metadata")
			request.RuntimeOptions.ProviderSpecific = map[string]string{"discussion_context_fingerprint": "fingerprint", "discussion_persona_revision": "personal-assistant-v1", "discussion_history_mode": "full"}
			response, err := runtime.RunSession(context.Background(), request, nil)
			if err != nil {
				t.Fatal(err)
			}
			if response.Status != agentcore.RuntimeStatusCompleted || response.ProviderMetadata["session_id"] != "provider-session-1" || response.ProviderMetadata["discussion_persona_revision"] != "personal-assistant-v1" || response.ProviderMetadata["discussion_context_fingerprint"] != "fingerprint" {
				t.Fatal("canonical event flow lost continuation metadata")
			}
		})
	}
}

func TestResponseProjectionPreservesOnlyContinuationMetadata(t *testing.T) {
	for _, kind := range []agentcore.RuntimeKind{agentcore.RuntimeKindPi, agentcore.RuntimeKindCodex, agentcore.RuntimeKindClaude, agentcore.RuntimeKindOpenClaw, agentcore.RuntimeKindACP} {
		t.Run(string(kind), func(t *testing.T) {
			request := agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
				"discussion_context_fingerprint": "fingerprint", "discussion_persona_revision": "personal-assistant-v1",
				"discussion_history_mode": "delta", "discussion_history_since_run_id": "previous-run",
				"discussion_history_since_completed_at":      "2026-09-07T00:00:00Z",
				agentcore.RuntimeContextTruncatedProviderKey: "true",
				"api_key": "test-secret", "approval_decision": "approved", "session_id": "caller-session",
			}}}
			projection := NewResponseProjection(request, kind, time.Now())
			projection.bindBackendSessionID("actual-session")
			metadata := projection.Response(time.Now()).ProviderMetadata
			for _, key := range []string{"discussion_context_fingerprint", "discussion_persona_revision", "discussion_history_mode", "discussion_history_since_run_id", "discussion_history_since_completed_at", agentcore.RuntimeContextTruncatedProviderKey} {
				if metadata[key] != request.RuntimeOptions.ProviderSpecific[key] {
					t.Fatalf("continuation metadata %s was lost", key)
				}
			}
			if len(metadata) != 7 || metadata["api_key"] != "" || metadata["approval_decision"] != "" || metadata["session_id"] != "actual-session" {
				t.Fatal("projection copied untrusted settings or replaced the backend session")
			}
			metadata["discussion_persona_revision"] = "modified"
			if projection.Response(time.Now()).ProviderMetadata["discussion_persona_revision"] != "personal-assistant-v1" {
				t.Fatal("returned metadata aliases the projection")
			}
		})
	}
}

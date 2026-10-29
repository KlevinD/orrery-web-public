package discussionsession

import (
	"errors"
	"fmt"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestRuntimeSetupErrorPreservesBridgeRetryMetadata(t *testing.T) {
	cause := agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "runtime is missing", false)
	setup := NewRuntimeSetupErrorFromCause("pi", cause)
	event := streamErrorEvent(setup)

	if event.ErrorCode != string(agentcore.RuntimeBridgeWorkspaceUnavailable) || event.Recoverable {
		t.Fatalf("stream error metadata = %+v", event)
	}
}

func TestStreamErrorEventClassifiesDeterministicOrchestrationFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{
			name: "permission text",
			err:  newOrchestrationIncompleteError("knowledge_search failed: mcp gateway permission denied: missing knowledge.read"),
			code: StreamErrorCodePermissionDenied,
		},
		{
			name: "wrapped discussion forbidden",
			err: missionToolingError([]ToolCallDTO{{
				Name: "mission_prepare", Status: "failed",
			}}, ErrForbidden),
			code: StreamErrorCodePermissionDenied,
		},
		{
			name: "serialized discussion forbidden",
			err:  errors.New("discussion orchestration incomplete: original error: discussion forbidden"),
			code: StreamErrorCodePermissionDenied,
		},
		{
			name: "wrapped mission invalid request",
			err: missionToolingError([]ToolCallDTO{{
				Name: "mission_prepare", Status: "failed",
			}}, fmt.Errorf("%w: task idempotency key was reused with a different request", missioncontrol.ErrInvalidRequest)),
			code: StreamErrorCodeInvalidRequest,
		},
		{
			name: "serialized mission invalid request",
			err:  errors.New("discussion orchestration incomplete: original error: invalid mission control request: task conflict"),
			code: StreamErrorCodeInvalidRequest,
		},
		{
			name: "pageindex prompt budget",
			err:  newOrchestrationIncompleteError("PageIndex retrieval prompt exceeded its safe model context budget"),
			code: streamErrorCodeRetrievalPromptBudget,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := streamErrorEvent(test.err)
			if event.ErrorCode != test.code || event.Recoverable {
				t.Fatalf("event = %+v", event)
			}
		})
	}
}

func TestStreamErrorEventLeavesUnclassifiedOrchestrationFailureRetryableByGatewayPolicy(t *testing.T) {
	event := streamErrorEvent(errors.New("temporary provider connection reset"))
	if event.ErrorCode != "" || event.Recoverable {
		t.Fatalf("event = %+v", event)
	}
}

package missioncontrol

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRuntimeContinuationDefaultIsAvailableNativeProvider(t *testing.T) {
	if got := continuationRuntimeKind(""); got != string(agentcore.RuntimeKindPi) {
		t.Fatalf("continuation runtime = %q, want %q", got, agentcore.RuntimeKindPi)
	}
	if got := continuationRuntimeKind("codex"); got != "codex" {
		t.Fatalf("configured continuation runtime = %q", got)
	}
}

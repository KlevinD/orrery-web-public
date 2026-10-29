package main

import (
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	httptransport "orrery_v2/internal/transport/http"
)

func TestProductionSettingsImplicitRuntimeSelectionIgnoresScheduledPiProfile(t *testing.T) {
	profiles := []httptransport.SettingsRuntimeProfileDTO{
		{
			ProfileID: "runtime-pi-orrery-native", RuntimeConfigRef: "runtime-pi-orrery-native",
			RuntimeKind: "pi", RuntimeConfig: map[string]any{"executable": "/opt/orrery/pi"}, Enabled: true,
		},
		{
			ProfileID: nativeScheduledRuntimeProfileID, RuntimeConfigRef: nativeScheduledRuntimeProfileID,
			RuntimeKind: "pi", RuntimeConfig: map[string]any{"executable": "pi"}, Enabled: true,
		},
	}

	resolved, err := resolveRuntimeSpecFromOnlyEnabledProfileForKind(agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}, profiles)
	if err != nil {
		t.Fatalf("resolve runtime spec: %v", err)
	}
	if resolved.ConfigRef != "runtime-pi-orrery-native" || !strings.Contains(string(resolved.Config), "/opt/orrery/pi") {
		t.Fatalf("implicit PI runtime selected the wrong profile: %+v config=%s", resolved, resolved.Config)
	}
}

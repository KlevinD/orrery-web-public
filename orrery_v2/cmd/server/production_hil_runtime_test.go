package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
	httptransport "orrery_v2/internal/transport/http"
)

func TestProductionHILRuntimeApprovalHoldTimeoutCoversMarketingReviewWindow(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		RuntimeProfiles: []httptransport.SettingsRuntimeProfileDTO{{
			ProfileID:        "runtime-pi-local",
			DisplayName:      "PI",
			RuntimeKind:      "pi",
			RuntimeConfigRef: "runtime-pi-local",
			RuntimeConfig: map[string]any{
				"approval_timeout_ms": 600000,
			},
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	if timeout := productionHILRuntimeApprovalHoldTimeout(ctx, settings); timeout != 24*time.Hour {
		t.Fatalf("production HIL approval hold timeout = %s, want 24h", timeout)
	}
}

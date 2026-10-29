package main

import (
	"testing"
	"time"

	"orrery_v2/internal/platform/config"
)

func TestConfiguredDiscussionAgentTimeoutDefaultsToRuntimeTimeout(t *testing.T) {
	if got := configuredDiscussionAgentTimeout(config.Config{}); got != 20*time.Minute {
		t.Fatalf("configured discussion timeout = %v, want 20m", got)
	}
}

package daemon

import (
	"testing"
	"time"
)

func TestHeartbeatPolicyDerivesTTLFromInterval(t *testing.T) {
	if got := (HeartbeatPolicy{Interval: 10 * time.Second}).EffectiveTTL(); got != 30*time.Second {
		t.Fatalf("ttl = %s", got)
	}
}

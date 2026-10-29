package websockettransport

import "testing"

func TestBackpressureRequestsCompactSnapshotWhenAckLagExceedsThreshold(t *testing.T) {
	policy := BackpressurePolicy{MaxAckLag: 2, MaxQueueDepth: 4}
	state := ClientState{LastSentVersion: 10, LastAckedVersion: 7, QueueDepth: 1}

	decision := policy.Decide(state)

	if decision.Action != BackpressureCompactSnapshot || !decision.DropIntermediatePatches {
		t.Fatalf("decision = %+v, want compact snapshot", decision)
	}
}

func TestBackpressureDropsIntermediatePatchesWhenQueueFull(t *testing.T) {
	policy := BackpressurePolicy{MaxAckLag: 10, MaxQueueDepth: 2}
	state := ClientState{LastSentVersion: 4, LastAckedVersion: 4, QueueDepth: 3}

	decision := policy.Decide(state)

	if decision.Action != BackpressureDropPatches || !decision.DropIntermediatePatches {
		t.Fatalf("decision = %+v, want drop patches", decision)
	}
}

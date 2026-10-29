package websockettransport

type BackpressureAction string

const (
	BackpressureSendPatch       BackpressureAction = "send_patch"
	BackpressureCompactSnapshot BackpressureAction = "compact_snapshot"
	BackpressureDropPatches     BackpressureAction = "drop_patches"
)

type BackpressurePolicy struct {
	MaxAckLag     int
	MaxQueueDepth int
}

type ClientState struct {
	LastSentVersion  int
	LastAckedVersion int
	QueueDepth       int
}

type BackpressureDecision struct {
	Action                  BackpressureAction
	DropIntermediatePatches bool
}

func (p BackpressurePolicy) Decide(state ClientState) BackpressureDecision {
	if p.MaxQueueDepth > 0 && state.QueueDepth > p.MaxQueueDepth {
		return BackpressureDecision{Action: BackpressureDropPatches, DropIntermediatePatches: true}
	}
	if p.MaxAckLag > 0 && state.LastSentVersion-state.LastAckedVersion > p.MaxAckLag {
		return BackpressureDecision{Action: BackpressureCompactSnapshot, DropIntermediatePatches: true}
	}
	return BackpressureDecision{Action: BackpressureSendPatch}
}

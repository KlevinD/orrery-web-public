package websockettransport

import (
	"context"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

type StreamSource interface {
	Snapshot(ctx context.Context, missionID string, req SubscribeRequest) (missioncontrol.MissionControlViewDTO, error)
	Patches(ctx context.Context, missionID string, req SubscribeRequest, afterVersion int) ([]missioncontrol.MissionControlPatchDTO, error)
}

type MissionSubscription struct {
	subscriptionID string
	missionID      string
	source         StreamSource
	policy         BackpressurePolicy
	state          ClientState
}

func NewMissionSubscription(subscriptionID, missionID string, source StreamSource, policy BackpressurePolicy) *MissionSubscription {
	return &MissionSubscription{subscriptionID: subscriptionID, missionID: missionID, source: source, policy: policy}
}

func (s *MissionSubscription) Start(ctx context.Context, req SubscribeRequest) ([][]byte, error) {
	if req.MissionID == "" {
		req.MissionID = s.missionID
	}
	snapshot, err := s.source.Snapshot(ctx, req.MissionID, req)
	if err != nil {
		return nil, err
	}
	snapshotFrame, err := EncodeSnapshot(s.subscriptionID, snapshot)
	if err != nil {
		return nil, err
	}
	frames := [][]byte{snapshotFrame}
	patches, err := s.source.Patches(ctx, req.MissionID, req, snapshot.Version)
	if err != nil {
		return nil, err
	}
	decision := s.policy.Decide(s.state)
	if decision.Action == BackpressureCompactSnapshot {
		return frames, nil
	}
	if decision.Action == BackpressureDropPatches && len(patches) > 1 {
		patches = patches[len(patches)-1:]
	}
	for _, patch := range patches {
		frame, err := EncodePatch(s.subscriptionID, patch)
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
		s.RecordSent(patch.NextVersion)
	}
	return frames, nil
}

func (s *MissionSubscription) RecordSent(version int) {
	if version > s.state.LastSentVersion {
		s.state.LastSentVersion = version
	}
	s.state.QueueDepth++
}

func (s *MissionSubscription) Ack(version int) {
	if version > s.state.LastAckedVersion {
		s.state.LastAckedVersion = version
	}
	if s.state.QueueDepth > 0 {
		s.state.QueueDepth--
	}
}

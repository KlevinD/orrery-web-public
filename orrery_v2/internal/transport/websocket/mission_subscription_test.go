package websockettransport

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestMissionSubscriptionSendsSnapshotThenPatchFrames(t *testing.T) {
	source := &fakeStreamSource{
		snapshot: missioncontrol.MissionControlViewDTO{
			Mission: missioncontrol.MissionDTO{MissionID: "mission-1", Title: "Transport"},
			Version: 3,
		},
		patches: []missioncontrol.MissionControlPatchDTO{{
			PatchID:      "patch-4",
			MissionID:    "mission-1",
			BaseVersion:  3,
			NextVersion:  4,
			ChangedViews: []string{"discussion"},
			Payload:      missioncontrol.MissionControlViewDTO{Version: 4},
			EmittedAt:    time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC),
		}},
	}
	sub := NewMissionSubscription("sub-1", "mission-1", source, BackpressurePolicy{MaxAckLag: 10, MaxQueueDepth: 10})

	frames, err := sub.Start(context.Background(), SubscribeRequest{MissionID: "mission-1", ViewNames: []string{"discussion"}})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want snapshot and patch", len(frames))
	}
	var snapshot ServerEnvelope
	if err := json.Unmarshal(frames[0], &snapshot); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if snapshot.Type != MessageSnapshot || snapshot.SubscriptionID != "sub-1" || snapshot.Version != 3 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	var patch ServerEnvelope
	if err := json.Unmarshal(frames[1], &patch); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if patch.Type != MessagePatch || patch.Version != 4 {
		t.Fatalf("patch = %+v", patch)
	}
}

func TestMissionSubscriptionCompactsSnapshotWhenAckLagging(t *testing.T) {
	source := &fakeStreamSource{
		snapshot: missioncontrol.MissionControlViewDTO{Mission: missioncontrol.MissionDTO{MissionID: "mission-1"}, Version: 9},
		patches:  []missioncontrol.MissionControlPatchDTO{{PatchID: "patch-9", MissionID: "mission-1", BaseVersion: 8, NextVersion: 9, ChangedViews: []string{"team"}, Payload: missioncontrol.MissionControlViewDTO{Version: 9}}},
	}
	sub := NewMissionSubscription("sub-1", "mission-1", source, BackpressurePolicy{MaxAckLag: 1, MaxQueueDepth: 10})
	sub.RecordSent(8)
	sub.Ack(6)

	frames, err := sub.Start(context.Background(), SubscribeRequest{MissionID: "mission-1"})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	var envelope ServerEnvelope
	if err := json.Unmarshal(frames[len(frames)-1], &envelope); err != nil {
		t.Fatalf("unmarshal last frame: %v", err)
	}
	if envelope.Type != MessageSnapshot || envelope.Version != 9 {
		t.Fatalf("last frame = %+v, want compact snapshot", envelope)
	}
}

type fakeStreamSource struct {
	snapshot missioncontrol.MissionControlViewDTO
	patches  []missioncontrol.MissionControlPatchDTO
}

func (f *fakeStreamSource) Snapshot(ctx context.Context, missionID string, req SubscribeRequest) (missioncontrol.MissionControlViewDTO, error) {
	return f.snapshot, nil
}

func (f *fakeStreamSource) Patches(ctx context.Context, missionID string, req SubscribeRequest, afterVersion int) ([]missioncontrol.MissionControlPatchDTO, error) {
	return f.patches, nil
}

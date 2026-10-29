package websockettransport

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestPatchEncoderWrapsMissionControlPatchWithoutRawEvents(t *testing.T) {
	patch := missioncontrol.MissionControlPatchDTO{
		PatchID:      "patch-1",
		MissionID:    "mission-1",
		BaseVersion:  7,
		NextVersion:  8,
		ChangedViews: []string{"task_board"},
		Payload:      missioncontrol.MissionControlViewDTO{Version: 8},
		EmittedAt:    time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC),
		RawEvents:    map[string]any{"MissionEvent": "must-not-leak"},
	}

	frame, err := EncodePatch("sub-1", patch)
	if err != nil {
		t.Fatalf("EncodePatch returned error: %v", err)
	}
	if strings.Contains(string(frame), "MissionEvent") || strings.Contains(string(frame), "RawEvents") {
		t.Fatalf("encoded frame leaked raw events: %s", frame)
	}
	var envelope ServerEnvelope
	if err := json.Unmarshal(frame, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if envelope.Type != MessagePatch || envelope.SubscriptionID != "sub-1" || envelope.Version != 8 {
		t.Fatalf("envelope = %+v", envelope)
	}
	var body MissionControlPatchEnvelope
	raw, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if body.PatchID != "patch-1" || body.ViewName != "task_board" || len(body.Operations) == 0 {
		t.Fatalf("patch payload = %+v", body)
	}
}

func TestPatchEncoderMapsChangedViewsFromMissionStreamDiffs(t *testing.T) {
	patch := missioncontrol.MissionControlPatchDTO{
		PatchID:      "patch-report",
		MissionID:    "mission-1",
		BaseVersion:  8,
		NextVersion:  9,
		ChangedViews: []string{"tasks", "progress", "summary"},
		Payload: missioncontrol.MissionControlViewDTO{
			Version:       9,
			Summary:       missioncontrol.MissionControlSummaryDTO{ActiveTaskCount: 2},
			TaskBoard:     missioncontrol.TaskBoardDTO{Tasks: []missioncontrol.TaskCardDTO{{TaskID: "task-1"}}},
			ProgressGraph: missioncontrol.ProgressGraphDTO{Nodes: []missioncontrol.ProgressNodeDTO{{ID: "report-1", Details: "streamed report"}}},
		},
		EmittedAt: time.Date(2026, 6, 1, 13, 1, 0, 0, time.UTC),
	}

	frame, err := EncodePatch("sub-1", patch)
	if err != nil {
		t.Fatalf("EncodePatch returned error: %v", err)
	}
	var envelope ServerEnvelope
	if err := json.Unmarshal(frame, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	var body MissionControlPatchEnvelope
	raw, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	got := make([]string, 0, len(body.Operations))
	for _, op := range body.Operations {
		got = append(got, op.Type)
	}
	want := []string{"replace_task_board", "replace_progress_graph", "replace_summary"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("operations = %v, want %v", got, want)
	}
	if body.ViewName != "tasks" {
		t.Fatalf("view name = %q", body.ViewName)
	}
}

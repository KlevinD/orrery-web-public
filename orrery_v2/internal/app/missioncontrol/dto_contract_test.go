package missioncontrol_test

import (
	"encoding/json"
	"testing"
	"time"

	"orrery_v2/internal/app/missioncontrol"
)

func TestMissionControlDTOContractUsesStableJSONFields(t *testing.T) {
	dto := missioncontrol.MissionControlViewDTO{
		Mission:   missioncontrol.MissionDTO{MissionID: "mission-1", Title: "Contract", FinalGoal: "Stable DTO", Status: "active", CreatedAt: time.Date(2026, 6, 1, 15, 0, 0, 0, time.UTC)},
		TaskBoard: missioncontrol.TaskBoardDTO{Tasks: []missioncontrol.TaskCardDTO{{TaskID: "task-1", Status: "in_progress"}}},
		Version:   12,
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal dto: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal dto: %v", err)
	}
	required := []string{"mission", "team", "task_board", "progress_graph", "evidence", "qa", "discussion", "inbox_summary", "version"}
	for _, field := range required {
		if _, ok := got[field]; !ok {
			t.Fatalf("field %q missing from dto json: %s", field, raw)
		}
	}
	if _, ok := got["Mission"]; ok {
		t.Fatalf("domain-style field leaked into json: %s", raw)
	}
	if jsonContains(raw, "internal_trace_ref") {
		t.Fatalf("internal trace field leaked into dto contract: %s", raw)
	}
}

func TestPatchDTOContractOmitsRawEvents(t *testing.T) {
	patch := missioncontrol.MissionControlPatchDTO{
		PatchID:     "patch-1",
		MissionID:   "mission-1",
		BaseVersion: 1,
		NextVersion: 2,
		RawEvents:   map[string]string{"event": "raw"},
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	if string(raw) == "" || jsonContains(raw, "RawEvents") || jsonContains(raw, "raw_events") {
		t.Fatalf("raw events leaked from patch contract: %s", raw)
	}
}

func jsonContains(raw []byte, needle string) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return containsJSONValue(value, needle)
}

func containsJSONValue(value any, needle string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if key == needle || containsJSONValue(nested, needle) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsJSONValue(nested, needle) {
				return true
			}
		}
	case string:
		return typed == needle
	}
	return false
}

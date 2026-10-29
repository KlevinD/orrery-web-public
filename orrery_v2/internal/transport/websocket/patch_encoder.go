package websockettransport

import (
	"encoding/json"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

type MissionControlPatchEnvelope struct {
	PatchID     string           `json:"patch_id"`
	MissionID   string           `json:"mission_id"`
	BaseVersion int              `json:"base_version"`
	NextVersion int              `json:"next_version"`
	ViewName    string           `json:"view_name"`
	Operations  []PatchOperation `json:"operations"`
	GeneratedAt string           `json:"generated_at"`
}

type PatchOperation struct {
	Type  string `json:"type"`
	Value any    `json:"value,omitempty"`
}

func EncodeSnapshot(subscriptionID string, view missioncontrol.MissionControlViewDTO) ([]byte, error) {
	return json.Marshal(ServerEnvelope{Type: MessageSnapshot, SubscriptionID: subscriptionID, Payload: view, Version: view.Version})
}

func EncodePatch(subscriptionID string, patch missioncontrol.MissionControlPatchDTO) ([]byte, error) {
	envelope := MissionControlPatchEnvelope{
		PatchID:     patch.PatchID,
		MissionID:   patch.MissionID,
		BaseVersion: patch.BaseVersion,
		NextVersion: patch.NextVersion,
		ViewName:    firstViewName(patch.ChangedViews),
		Operations:  operationsForPatch(patch),
		GeneratedAt: patch.EmittedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	return json.Marshal(ServerEnvelope{Type: MessagePatch, SubscriptionID: subscriptionID, Payload: envelope, Version: patch.NextVersion})
}

func firstViewName(values []string) string {
	if len(values) == 0 {
		return "mission"
	}
	return values[0]
}

func operationsForPatch(patch missioncontrol.MissionControlPatchDTO) []PatchOperation {
	var operations []PatchOperation
	for _, view := range patch.ChangedViews {
		switch view {
		case "mission":
			operations = append(operations, PatchOperation{Type: "replace_mission_summary", Value: patch.Payload.Mission})
		case "team":
			operations = append(operations, PatchOperation{Type: "replace_team", Value: patch.Payload.Team})
		case "tasks", "task_board":
			operations = append(operations, PatchOperation{Type: "replace_task_board", Value: patch.Payload.TaskBoard})
		case "progress", "progress_graph":
			operations = append(operations, PatchOperation{Type: "replace_progress_graph", Value: patch.Payload.ProgressGraph})
		case "summary":
			operations = append(operations, PatchOperation{Type: "replace_summary", Value: patch.Payload.Summary})
		case "evidence":
			operations = append(operations, PatchOperation{Type: "replace_evidence", Value: patch.Payload.Evidence})
		case "qa":
			operations = append(operations, PatchOperation{Type: "replace_qa", Value: patch.Payload.QA})
		case "discussion":
			operations = append(operations, PatchOperation{Type: "replace_discussion", Value: patch.Payload.Discussion})
		case "inbox_summary":
			operations = append(operations, PatchOperation{Type: "replace_inbox_summary", Value: patch.Payload.InboxSummary})
		default:
			operations = append(operations, PatchOperation{Type: "replace_view", Value: view})
		}
	}
	if len(operations) == 0 {
		operations = append(operations, PatchOperation{Type: "replace_mission_control_view", Value: patch.Payload})
	}
	return operations
}

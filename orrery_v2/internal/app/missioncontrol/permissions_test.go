package missioncontrol_test

import (
	"testing"

	"orrery_v2/internal/app/missioncontrol"
)

func TestViewerMissionReadScopeCanSeeMissionVisibility(t *testing.T) {
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser, PermissionScope: []string{"mission.read"}}

	if !viewer.CanSeeAny([]string{"mission"}) {
		t.Fatalf("mission.read should allow mission visibility")
	}
}

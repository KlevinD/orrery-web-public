package httptransport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestMissionViewRoutesReturnPermissionTrimmedSubViews(t *testing.T) {
	query := &fakeMissionQuery{view: missioncontrol.MissionControlViewDTO{
		Mission:       missioncontrol.MissionDTO{MissionID: "mission-1", Title: "Stage 10"},
		Team:          missioncontrol.TeamDTO{Members: []missioncontrol.TeamMemberDTO{{AgentID: "agent-1"}}},
		TaskBoard:     missioncontrol.TaskBoardDTO{Tasks: []missioncontrol.TaskCardDTO{{TaskID: "task-1"}}},
		ProgressGraph: missioncontrol.ProgressGraphDTO{Nodes: []missioncontrol.ProgressNodeDTO{{ID: "node-1"}}},
		Evidence:      missioncontrol.EvidenceDTO{EvidenceItems: []missioncontrol.EvidenceItemDTO{{EvidenceID: "evidence-1"}}},
		QA:            missioncontrol.QADTO{Checks: []missioncontrol.QACheckDTO{{QARef: "qa-1"}}},
		Discussion:    missioncontrol.DiscussionDTO{DiscussionGroupID: "discussion-1"},
		Version:       11,
	}}
	handler := newTestRouter(RouterOptions{MissionQuery: query})
	cases := []struct {
		path string
		key  string
	}{
		{"/api/missions/mission-1/team", "members"},
		{"/api/missions/mission-1/tasks", "tasks"},
		{"/api/missions/mission-1/progress", "nodes"},
		{"/api/missions/mission-1/evidence", "evidence_items"},
		{"/api/missions/mission-1/qa", "checks"},
		{"/api/missions/mission-1/discussion", "discussion_group_id"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := authedHTTPTestRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("X-User-ID", "agent-1")
			req.Header.Set("X-Viewer-Kind", "agent")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if _, ok := body[tc.key]; !ok {
				t.Fatalf("body for %s missing %q: %+v", tc.path, tc.key, body)
			}
			if got := rec.Header().Get("ETag"); got != `"11"` {
				t.Fatalf("ETag = %q", got)
			}
		})
	}
}

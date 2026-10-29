package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
)

func TestMissionControlHandlerReturnsViewFromAppService(t *testing.T) {
	at := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	query := &fakeMissionQuery{view: missioncontrol.MissionControlViewDTO{
		Mission: missioncontrol.MissionDTO{MissionID: "mission-1", Title: "Ship transport", Status: "active", Phase: "execution", CreatedAt: at},
		Summary: missioncontrol.MissionControlSummaryDTO{ActiveTaskCount: 2, TeamSize: 3, UpdatedAt: at},
		Version: 42,
	}}
	handler := newTestRouter(RouterOptions{MissionQuery: query})

	req := authedHTTPTestRequest(http.MethodGet, "/api/missions/mission-1/control", nil)
	req.Header.Set("X-User-ID", "agent-1")
	req.Header.Set("X-Viewer-Kind", "agent")
	req.Header.Set("X-Permission-Scope", "mission.read")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if query.calledMissionID != missioncore.MissionID("mission-1") {
		t.Fatalf("mission id = %q", query.calledMissionID)
	}
	if query.viewer.ViewerID != "agent-1" || query.viewer.ViewerKind != missioncontrol.ViewerKindAgent {
		t.Fatalf("viewer = %+v", query.viewer)
	}
	if got := rec.Header().Get("ETag"); got != `"42"` {
		t.Fatalf("ETag = %q, want version token", got)
	}
	var body missioncontrol.MissionControlViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Mission.MissionID != "mission-1" || body.Version != 42 {
		t.Fatalf("body = %+v", body)
	}
	if body.Mission.Phase != "execution" || body.Summary.ActiveTaskCount != 2 || body.Summary.TeamSize != 3 {
		t.Fatalf("operations fields missing from body: %+v", body)
	}
}

func TestMissionOpsHandlerReturnsExecutionViewFromAppService(t *testing.T) {
	at := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	query := &fakeMissionQuery{ops: missioncontrol.MissionOpsViewDTO{
		MissionID: "mission-1",
		CurrentFocus: []missioncontrol.OpsFocusDTO{{
			FocusID:   "focus-qa-1",
			Kind:      "approval",
			Priority:  "high",
			Title:     "Review QA",
			MissionID: "mission-1",
			TaskID:    "task-1",
			CreatedAt: at,
		}},
		AgentLanes: []missioncontrol.AgentLaneDTO{{
			AgentID:        "agent-1",
			DisplayName:    "Ari",
			AgentKind:      "ai",
			Status:         "working",
			ActiveTaskIDs:  []string{"task-1"},
			InboxOpenCount: 1,
		}},
		ActionCards: []missioncontrol.ActionCardDTO{{
			ActionID:  "action-qa-1",
			Kind:      "approve_qa",
			Priority:  "high",
			Title:     "Approve QA",
			MissionID: "mission-1",
			TaskID:    "task-1",
			Status:    "open",
			CreatedAt: at,
		}},
		UpdatedAt: at,
		Version:   43,
	}}
	handler := newTestRouter(RouterOptions{MissionQuery: query})

	req := authedHTTPTestRequest(http.MethodGet, "/api/missions/mission-1/ops", nil)
	req.Header.Set("X-User-ID", "agent-1")
	req.Header.Set("X-Viewer-Kind", "agent")
	req.Header.Set("X-Permission-Scope", "mission.read")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if query.calledMissionID != missioncore.MissionID("mission-1") {
		t.Fatalf("mission id = %q", query.calledMissionID)
	}
	if got := rec.Header().Get("ETag"); got != `"43"` {
		t.Fatalf("ETag = %q, want ops version token", got)
	}
	var body missioncontrol.MissionOpsViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.MissionID != "mission-1" || len(body.CurrentFocus) != 1 || len(body.AgentLanes) != 1 || len(body.ActionCards) != 1 {
		t.Fatalf("ops body = %+v", body)
	}
}

func TestMissionControlHandlerMapsMissingViewerToStableUnauthorizedError(t *testing.T) {
	handler := newTestRouter(RouterOptions{MissionQuery: &fakeMissionQuery{}})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/missions/mission-1/control", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Code != "unauthorized" || body.CorrelationID == "" || body.Retryable {
		t.Fatalf("error response = %+v", body)
	}
}

type fakeMissionQuery struct {
	view            missioncontrol.MissionControlViewDTO
	ops             missioncontrol.MissionOpsViewDTO
	err             error
	viewer          missioncontrol.ViewerRef
	calledMissionID missioncore.MissionID
}

func (f *fakeMissionQuery) GetMissionControlView(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) (missioncontrol.MissionControlViewDTO, error) {
	f.viewer = viewer
	f.calledMissionID = missionID
	return f.view, f.err
}

func (f *fakeMissionQuery) GetMissionOpsView(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) (missioncontrol.MissionOpsViewDTO, error) {
	f.viewer = viewer
	f.calledMissionID = missionID
	return f.ops, f.err
}

func (f *fakeMissionQuery) ListMissions(ctx context.Context, viewer missioncontrol.ViewerRef, query missioncontrol.ListMissionsQuery) (missioncontrol.MissionListDTO, error) {
	return missioncontrol.MissionListDTO{}, f.err
}

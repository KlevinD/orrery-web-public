package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestHTTPRoutesListAndCreateMissionsThroughAppServices(t *testing.T) {
	query := &fakeMissionCatalogQuery{page: missioncontrol.MissionListDTO{Missions: []missioncontrol.MissionDTO{{MissionID: "mission-1", Title: "Stage 10"}}}}
	command := &fakeFullMissionCommand{mission: missioncontrol.MissionDTO{MissionID: "mission-2", Title: "New mission"}}
	handler := newTestRouter(RouterOptions{MissionQuery: query, MissionCommand: command})

	req := authedRequest(http.MethodGet, "/api/missions?limit=10&offset=3&search=ship&status=active&origin_kind=todo&origin_id=todo-1", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	if query.query.Limit != 10 || query.query.Offset != 3 || query.query.Search != "ship" || query.query.Status != "active" || query.query.OriginKind != "todo" || query.query.OriginID != "todo-1" {
		t.Fatalf("query = %+v", query.query)
	}

	create := authedRequest(http.MethodPost, "/api/missions", `{"title":"New mission","final_goal":"Ship stage 10","initial_human_agent_id":"human-user-1"}`)
	create.Header.Set("Idempotency-Key", "mission-key")
	create.Header.Set("X-User-Display-Name", "%E6%9C%AC%E5%9C%B0%E7%94%A8%E6%88%B7")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.createMission.FinalGoal != "Ship stage 10" || command.createMission.IdempotencyKey != "mission-key" || command.createMission.InitialHumanAgentID != "human-user-1" || command.createMission.InitialHumanDisplayName != "本地用户" {
		t.Fatalf("create command = %+v", command.createMission)
	}

	deleteReq := authedRequest(http.MethodDelete, "/api/missions/mission-2", "")
	deleteReq.Header.Set("Idempotency-Key", "delete-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, deleteReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.deleteMission != "mission-2" {
		t.Fatalf("delete mission = %s", command.deleteMission)
	}
}

func TestHTTPRoutesCancelMissionThroughAppService(t *testing.T) {
	command := &fakeFullMissionCommand{receipt: missioncontrol.CommandReceiptDTO{
		CommandID: "cancel-key", Status: "accepted", MissionID: "mission-2",
	}}
	handler := newTestRouter(RouterOptions{MissionCommand: command})
	req := authedRequest(http.MethodPost, "/api/missions/mission-2/cancel", `{"reason":"polluted verification Mission"}`)
	req.Header.Set("Idempotency-Key", "cancel-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.cancelMission.MissionID != "mission-2" || command.cancelMission.IdempotencyKey != "cancel-key" || command.cancelMission.Reason != "polluted verification Mission" {
		t.Fatalf("cancel command = %+v", command.cancelMission)
	}
}

func TestHTTPCreateMissionRejectsInternalOriginFields(t *testing.T) {
	command := &fakeFullMissionCommand{mission: missioncontrol.MissionDTO{MissionID: "mission-forged"}}
	handler := newTestRouter(RouterOptions{MissionCommand: command})
	req := authedRequest(http.MethodPost, "/api/missions", `{"title":"Forged","final_goal":"Forge Todo origin","origin_kind":"todo","origin_todo_id":"todo-1","existing_conversation_id":"conversation-1"}`)
	req.Header.Set("Idempotency-Key", "forged-origin")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.createMission.IdempotencyKey != "" {
		t.Fatalf("Mission command received internal origin fields: %+v", command.createMission)
	}
}

func TestHTTPRoutesCreateAndAssignTasksThroughAppServices(t *testing.T) {
	command := &fakeFullMissionCommand{
		task:    missioncontrol.TaskCardDTO{TaskID: "task-1", Goal: "Build"},
		receipt: missioncontrol.CommandReceiptDTO{CommandID: "assign-1", Status: "accepted"},
	}
	handler := newTestRouter(RouterOptions{MissionCommand: command})

	create := authedRequest(http.MethodPost, "/api/missions/mission-1/tasks", `{"task_id":"frontend-task-id-must-be-ignored","assignee_id":"agent-1","goal":"Build","worker_personal_goal":"Own the route","priority":"high","due_at":"2026-06-03T09:30:00Z","expected_deliverables":[{"id":"expected-brief","title":"Launch brief"}]}`)
	create.Header.Set("Idempotency-Key", "task-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task status = %d body = %s", rec.Code, rec.Body.String())
	}
	var task missioncontrol.TaskCardDTO
	if err := json.NewDecoder(rec.Body).Decode(&task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if task.TaskID != "task-1" || command.createTask.AssigneeID != "agent-1" {
		t.Fatalf("task/command = %+v / %+v", task, command.createTask)
	}
	if command.createTask.TaskID != "" {
		t.Fatalf("create task should not accept frontend task_id: %+v", command.createTask)
	}
	if command.createTask.Priority != "high" || !command.createTask.DueAt.Equal(time.Date(2026, 6, 3, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("task metadata command = %+v", command.createTask)
	}
	if len(command.createTask.ExpectedDeliverables) != 1 || command.createTask.ExpectedDeliverables[0].Title != "Launch brief" {
		t.Fatalf("task deliverables command = %+v", command.createTask.ExpectedDeliverables)
	}

	assign := authedRequest(http.MethodPost, "/api/missions/mission-1/tasks/task-1/assign", `{"assignee_id":"agent-2","reason":"rebalance"}`)
	assign.Header.Set("Idempotency-Key", "assign-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, assign)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("assign status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.assignTask.TaskID != "task-1" || command.assignTask.AssigneeID != "agent-2" {
		t.Fatalf("assign command = %+v", command.assignTask)
	}
}

type fakeMissionCatalogQuery struct {
	fakeMissionQuery
	page  missioncontrol.MissionListDTO
	query missioncontrol.ListMissionsQuery
}

func (f *fakeMissionCatalogQuery) ListMissions(ctx context.Context, viewer missioncontrol.ViewerRef, query missioncontrol.ListMissionsQuery) (missioncontrol.MissionListDTO, error) {
	f.query = query
	return f.page, nil
}

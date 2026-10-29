package missioncontrol_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
)

func TestQueryServiceAllowsDevelopmentMonitorWithoutMissionRead(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	model.Mission.OriginDevelopmentProjectID = "project-1"
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	service := missioncontrol.NewQueryService(repo)
	service.SetAuthorizationService(authorization.NewService(missionMonitorAuthorizationAdapter{}))
	viewer := missioncontrol.ViewerRef{ViewerID: "maintainer", ViewerKind: missioncontrol.ViewerKindUser, PermissionScope: []string{"development.monitor"}}

	view, err := service.GetMissionControlView(ctx, viewer, model.Mission.MissionID)
	if err != nil || view.Mission.MissionID != model.Mission.MissionID.String() {
		t.Fatalf("development monitor view=%+v err=%v", view.Mission, err)
	}
	if _, err := service.GetMissionOpsView(ctx, viewer, model.Mission.MissionID); err != nil {
		t.Fatalf("development monitor ops: %v", err)
	}
}

func TestDevelopmentMonitorCanOpenKnownMissionButCannotDiscoverItInMissionList(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	model := appReadModel()
	model.Mission.OriginDevelopmentProjectID = "project-1"
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	authz := authorization.NewService(missionMonitorAuthorizationAdapter{})
	catalog := missioncontrol.NewLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{AuthorizationService: authz})
	service := missioncontrol.NewQueryServiceWithCatalog(repo, catalog)
	service.SetAuthorizationService(authz)
	viewer := missioncontrol.ViewerRef{ViewerID: "maintainer", ViewerKind: missioncontrol.ViewerKindUser, PermissionScope: []string{"development.monitor"}}

	page, err := service.ListMissions(ctx, viewer, missioncontrol.ListMissionsQuery{Limit: 20})
	if err != nil {
		t.Fatalf("ListMissions returned error: %v", err)
	}
	if page.Page.Total != 0 || len(page.Missions) != 0 {
		t.Fatalf("development monitor discovered Missions through the global list: %+v", page)
	}
	view, err := service.GetMissionControlView(ctx, viewer, model.Mission.MissionID)
	if err != nil || view.Mission.MissionID != model.Mission.MissionID.String() {
		t.Fatalf("known Mission control view=%+v err=%v", view.Mission, err)
	}
}

type missionMonitorAuthorizationAdapter struct{}

func (missionMonitorAuthorizationAdapter) Check(_ context.Context, request authorization.CheckRequest) (authorization.Decision, error) {
	return authorization.Decision{Allowed: request.Action == "mission.monitor", Source: "test"}, nil
}

func (a missionMonitorAuthorizationAdapter) BatchCheck(ctx context.Context, requests []authorization.CheckRequest) ([]authorization.Decision, error) {
	out := make([]authorization.Decision, 0, len(requests))
	for _, request := range requests {
		decision, err := a.Check(ctx, request)
		if err != nil {
			return nil, err
		}
		out = append(out, decision)
	}
	return out, nil
}

func (missionMonitorAuthorizationAdapter) ListObjects(context.Context, identity.Principal, string, string, string) ([]identity.ObjectRef, error) {
	return nil, nil
}

func (missionMonitorAuthorizationAdapter) WriteRelations(context.Context, []authorization.RelationTuple) error {
	return nil
}

func (missionMonitorAuthorizationAdapter) DeleteRelations(context.Context, []authorization.RelationTuple) error {
	return nil
}

func TestQueryServiceReturnsPermissionTrimmedMissionControlViewDTO(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	repo.Save(ctx, model)
	service := newAuthorizedQueryService(repo)

	dto, err := service.GetMissionControlView(ctx, missioncontrol.ViewerRef{
		ViewerID:        "agent-1",
		ViewerKind:      missioncontrol.ViewerKindAgent,
		MissionRoles:    []string{"worker"},
		PermissionScope: []string{"mission", "evidence:public"},
	}, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}

	if dto.Mission.MissionID != "mission-1" || dto.Mission.FinalGoal != "Build app DTOs" {
		t.Fatalf("unexpected mission dto: %+v", dto.Mission)
	}
	if dto.Summary.TeamSize != 1 {
		t.Fatalf("team size summary = %d, want 1", dto.Summary.TeamSize)
	}
	if dto.Summary.PendingApprovalCount != 1 {
		t.Fatalf("pending approval summary = %d, want 1", dto.Summary.PendingApprovalCount)
	}
	if dto.Summary.BlockerCount != 1 || dto.Summary.RiskCount != 1 {
		t.Fatalf("risk/blocker summary mismatch: %+v", dto.Summary)
	}
	if dto.Summary.DeliveryRate != 0 {
		t.Fatalf("delivery rate = %v, want 0 before approved tasks", dto.Summary.DeliveryRate)
	}
	if dto.Mission.Phase == "" {
		t.Fatalf("mission phase was not derived: %+v", dto.Mission)
	}
	task := dto.TaskBoard.Tasks[0]
	if task.Title == "" || task.AssigneeDisplayName != "Ari" || task.AssigneeType != "agent" {
		t.Fatalf("task operations fields missing: %+v", task)
	}
	if task.BlockedReason != "Waiting" {
		t.Fatalf("blocked reason = %q, want blocker summary", task.BlockedReason)
	}
	message := dto.Discussion.Messages[0]
	if message.Kind == "" || message.AuthorDisplayName != "Ari" || message.AuthorType != "agent" {
		t.Fatalf("message operations fields missing: %+v", message)
	}
	inbox := dto.InboxSummary.Items[0]
	if inbox.ItemType == "" || inbox.SourceType == "" || len(inbox.AvailableActions) == 0 {
		t.Fatalf("inbox operations fields missing: %+v", inbox)
	}
	if len(dto.Evidence.EvidenceItems) != 1 || dto.Evidence.EvidenceItems[0].EvidenceID != "evidence-visible" {
		t.Fatalf("evidence was not permission trimmed: %+v", dto.Evidence.EvidenceItems)
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal dto: %v", err)
	}
	if strings.Contains(string(raw), "internal_trace_ref") || dto.Team.Members[0].PersonalMemoryRef != "" {
		t.Fatalf("dto leaked internal trace or personal memory: json=%s team=%+v", raw, dto.Team.Members)
	}
	if len(dto.Discussion.Messages) != 1 || dto.Discussion.Messages[0].MessageID != "message-public" {
		t.Fatalf("discussion was not permission trimmed: %+v", dto.Discussion.Messages)
	}
	if len(dto.ProgressGraph.Nodes) != 1 || dto.ProgressGraph.Nodes[0].EventID == "" {
		t.Fatalf("progress graph missing from dto: %+v", dto.ProgressGraph)
	}
	if len(dto.QA.ReworkRequests) != 1 || dto.QA.ReworkRequests[0].Status != string(projection.ReworkStatusResolved) || dto.QA.ReworkRequests[0].ResolvedAt.IsZero() {
		t.Fatalf("rework status was not exposed as dto state: %+v", dto.QA.ReworkRequests)
	}
}

func TestQueryServiceMapsProgressNodeDetails(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	model.ProgressGraph.Nodes = []projection.ProgressNodeView{{
		ID:         "evt-report",
		EventID:    "evt-report",
		EventType:  "AcceptanceReportCreated",
		Label:      "AcceptanceReportCreated",
		Status:     "succeeded",
		OccurredAt: model.UpdatedAt,
		RefType:    "qa",
		RefID:      "qa-1",
		Details:    "QA acceptance report streamed summary",
	}}
	model.ProgressGraph.CurrentFocus = []string{"evt-report"}
	repo.Save(ctx, model)
	service := newAuthorizedQueryService(repo)

	dto, err := service.GetMissionControlView(ctx, missioncontrol.ViewerRef{
		ViewerID:        "agent-1",
		ViewerKind:      missioncontrol.ViewerKindAgent,
		PermissionScope: []string{"mission"},
	}, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}

	node := dto.ProgressGraph.Nodes[0]
	if node.RefType != "qa" || node.RefID != "qa-1" || node.Details != "QA acceptance report streamed summary" {
		t.Fatalf("progress node details missing from dto: %+v", node)
	}
}

func TestQueryServiceReturnsMissionOpsViewWithFocusAgentLanesAndActions(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	service := newAuthorizedQueryService(repo)

	dto, err := service.GetMissionOpsView(ctx, missioncontrol.ViewerRef{
		ViewerID:        "agent-1",
		ViewerKind:      missioncontrol.ViewerKindAgent,
		PermissionScope: []string{"mission", "evidence:public"},
	}, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionOpsView returned error: %v", err)
	}

	if dto.MissionID != "mission-1" || dto.Version != model.Checkpoint.Version {
		t.Fatalf("ops identity = %+v, want mission and projection version", dto)
	}
	if len(dto.CurrentFocus) == 0 || dto.CurrentFocus[0].Kind != "approval" || dto.CurrentFocus[0].TaskID != "task-1" {
		t.Fatalf("current focus = %+v, want QA approval focus for task-1", dto.CurrentFocus)
	}
	if len(dto.AgentLanes) != 1 {
		t.Fatalf("agent lanes = %+v, want one lane", dto.AgentLanes)
	}
	lane := dto.AgentLanes[0]
	if lane.AgentID != "agent-1" || lane.DisplayName != "Ari" || lane.InboxOpenCount != 1 || len(lane.ActiveTaskIDs) != 1 || lane.ActiveTaskIDs[0] != "task-1" {
		t.Fatalf("agent lane = %+v, want active task and inbox count", lane)
	}
	if len(dto.ActionCards) < 2 {
		t.Fatalf("action cards = %+v, want QA and blocker actions", dto.ActionCards)
	}
	if !hasAction(dto.ActionCards, "approve_qa", "task-1") || !hasAction(dto.ActionCards, "resolve_blocker", "task-1") {
		t.Fatalf("action cards missing expected execution actions: %+v", dto.ActionCards)
	}
	if len(dto.ApprovalGates) != 1 || dto.ApprovalGates[0].ApprovalType != "qa" {
		t.Fatalf("approval gates = %+v, want QA gate", dto.ApprovalGates)
	}
	if len(dto.EvidenceGates) != 1 || dto.EvidenceGates[0].SubmittedCount != 1 {
		t.Fatalf("evidence gates = %+v, want visible evidence count for task", dto.EvidenceGates)
	}
	if dto.AutonomyPolicy.Mode != "manual" || dto.AutonomyPolicy.AutoStartAllowed || len(dto.AutonomyPolicy.RequiredApprovals) == 0 {
		t.Fatalf("autonomy policy = %+v, want stable manual defaults", dto.AutonomyPolicy)
	}
}

func TestQueryServiceProjectsMissionOpsAgentLaneEffectiveResources(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	service := newAuthorizedQueryService(repo)
	service.SetAgentResourcePort(fakeAgentResourcePort{bindings: map[string][]missioncontrol.ResourceRefDTO{
		"agent-1": {
			{ResourceType: "skill", ResourceID: "skill-release", Alias: "release", Source: "profile"},
			{ResourceType: "tool", ResourceID: "tool-shell", Alias: "shell", Source: "profile"},
			{ResourceType: "mcp_server", ResourceID: "mcp-github", Alias: "github", Source: "skill_required_tool"},
		},
	}})

	dto, err := service.GetMissionOpsView(ctx, missioncontrol.ViewerRef{
		ViewerID:        "agent-1",
		ViewerKind:      missioncontrol.ViewerKindAgent,
		PermissionScope: []string{"mission", "resource.read"},
	}, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionOpsView returned error: %v", err)
	}

	if len(dto.AgentLanes) != 1 {
		t.Fatalf("agent lanes = %+v", dto.AgentLanes)
	}
	bindings := dto.AgentLanes[0].ResourceBindings
	if len(bindings) != 3 {
		t.Fatalf("resource bindings = %+v", bindings)
	}
	if !hasResourceBinding(bindings, "skill", "skill-release") ||
		!hasResourceBinding(bindings, "tool", "tool-shell") ||
		!hasResourceBinding(bindings, "mcp_server", "mcp-github") {
		t.Fatalf("resource bindings missing effective resources: %+v", bindings)
	}
}

func TestQueryServiceReturnsMissionOpsRuntimeSessionsFromProjection(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	heartbeatAt := model.UpdatedAt.Add(2 * time.Minute)
	leaseExpiresAt := model.UpdatedAt.Add(7 * time.Minute)
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID:        "run-1",
		AgentID:      "agent-1",
		TaskID:       "task-1",
		RuntimeKind:  "codex",
		QueueID:      "mission:mission-1",
		Status:       projection.RuntimeSessionRunning,
		WorkerID:     "worker-1",
		LastEvent:    "AgentRunStarted",
		LastMessage:  "claimed",
		ErrorSummary: "",
		QueuedAt:     model.UpdatedAt,
		StartedAt:    model.UpdatedAt.Add(time.Minute),
		UpdatedAt:    model.UpdatedAt.Add(time.Minute),
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	service := newAuthorizedQueryService(repo)
	service.SetRuntimeHealthPort(fakeRuntimeHealthPort{snapshots: map[string]missioncontrol.RuntimeHealthSnapshot{
		"agent-run:run-1": {
			RuntimeID:       "agent-run:run-1",
			LastHeartbeatAt: heartbeatAt,
			LeaseExpiresAt:  leaseExpiresAt,
			Alive:           true,
		},
	}})

	dto, err := service.GetMissionOpsView(ctx, missioncontrol.ViewerRef{
		ViewerID:        "agent-1",
		ViewerKind:      missioncontrol.ViewerKindAgent,
		PermissionScope: []string{"mission", "evidence:public"},
	}, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionOpsView returned error: %v", err)
	}

	if len(dto.RuntimeSessions) != 1 {
		t.Fatalf("runtime sessions = %+v, want one projected session", dto.RuntimeSessions)
	}
	session := dto.RuntimeSessions[0]
	if session.RunID != "run-1" || session.AgentID != "agent-1" || session.TaskID != "task-1" || session.Status != "running" || session.RuntimeKind != "codex" || session.QueueID != "mission:mission-1" {
		t.Fatalf("runtime session dto = %+v", session)
	}
	if !session.LastHeartbeatAt.Equal(heartbeatAt) || !session.LeaseExpiresAt.Equal(leaseExpiresAt) || !session.IsAlive || session.IsStale || session.LaneHealth != "healthy" {
		t.Fatalf("runtime health fields = %+v", session)
	}
	if len(dto.AgentLanes) != 1 || len(dto.AgentLanes[0].RuntimeSessions) != 1 || dto.AgentLanes[0].RuntimeSessions[0].RunID != "run-1" {
		t.Fatalf("agent lane runtime sessions = %+v", dto.AgentLanes)
	}
	if dto.AgentLanes[0].RuntimeSessions[0].LaneHealth != "healthy" {
		t.Fatalf("agent lane runtime health = %+v", dto.AgentLanes[0].RuntimeSessions[0])
	}
}

func TestQueryServiceMarksMissionOpsRuntimeSessionStaleWhenLeaseExpired(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID:       "run-stale",
		AgentID:     "agent-1",
		TaskID:      "task-1",
		RuntimeKind: "codex",
		QueueID:     "discussion:discussion-1",
		Status:      projection.RuntimeSessionRunning,
		WorkerID:    "worker-1",
		LastEvent:   "AgentRunStarted",
		StartedAt:   model.UpdatedAt,
		UpdatedAt:   model.UpdatedAt,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	service := newAuthorizedQueryService(repo)
	service.SetRuntimeHealthPort(fakeRuntimeHealthPort{snapshots: map[string]missioncontrol.RuntimeHealthSnapshot{
		"agent-run:run-stale": {
			RuntimeID:       "agent-run:run-stale",
			LastHeartbeatAt: model.UpdatedAt.Add(time.Minute),
			LeaseExpiresAt:  model.UpdatedAt.Add(2 * time.Minute),
			Alive:           false,
		},
	}})

	dto, err := service.GetMissionOpsView(ctx, missioncontrol.ViewerRef{
		ViewerID:        "agent-1",
		ViewerKind:      missioncontrol.ViewerKindAgent,
		PermissionScope: []string{"mission"},
	}, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionOpsView returned error: %v", err)
	}

	if len(dto.RuntimeSessions) != 1 {
		t.Fatalf("runtime sessions = %+v", dto.RuntimeSessions)
	}
	session := dto.RuntimeSessions[0]
	if session.RunID != "run-stale" || session.IsAlive || !session.IsStale || session.LaneHealth != "stale" {
		t.Fatalf("stale runtime session = %+v", session)
	}
}

func hasAction(cards []missioncontrol.ActionCardDTO, kind string, taskID string) bool {
	for _, card := range cards {
		if card.Kind == kind && card.TaskID == taskID {
			return true
		}
	}
	return false
}

type fakeRuntimeHealthPort struct {
	snapshots map[string]missioncontrol.RuntimeHealthSnapshot
}

type fakeAgentResourcePort struct {
	bindings map[string][]missioncontrol.ResourceRefDTO
}

func (p fakeAgentResourcePort) GetAgentResources(ctx context.Context, agentIDs []string) (map[string][]missioncontrol.ResourceRefDTO, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := map[string][]missioncontrol.ResourceRefDTO{}
	for _, agentID := range agentIDs {
		if bindings, ok := p.bindings[agentID]; ok {
			out[agentID] = append([]missioncontrol.ResourceRefDTO(nil), bindings...)
		}
	}
	return out, nil
}

func hasResourceBinding(bindings []missioncontrol.ResourceRefDTO, resourceType, resourceID string) bool {
	for _, binding := range bindings {
		if binding.ResourceType == resourceType && binding.ResourceID == resourceID {
			return true
		}
	}
	return false
}

func (p fakeRuntimeHealthPort) GetRuntimeHealth(ctx context.Context, runtimeIDs []string) (map[string]missioncontrol.RuntimeHealthSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := map[string]missioncontrol.RuntimeHealthSnapshot{}
	for _, runtimeID := range runtimeIDs {
		if snapshot, ok := p.snapshots[runtimeID]; ok {
			out[runtimeID] = snapshot
		}
	}
	return out, nil
}

func TestQueryServiceHidesEvidenceWithoutExplicitVisibility(t *testing.T) {
	ctx := context.Background()
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appReadModel()
	model.Evidence.EvidenceItems = append(model.Evidence.EvidenceItems, projection.EvidenceItemView{
		EvidenceID:  "evidence-default-hidden",
		TaskID:      "task-1",
		AgentID:     "agent-1",
		Kind:        agentcore.EvidenceKindToolLog,
		ToolLogRef:  agentcore.ToolLogRef{ID: "tool-log-default"},
		SubmittedAt: model.UpdatedAt,
	})
	repo.Save(ctx, model)
	service := newAuthorizedQueryService(repo)

	dto, err := service.GetMissionControlView(ctx, missioncontrol.ViewerRef{
		ViewerID:        "agent-1",
		ViewerKind:      missioncontrol.ViewerKindAgent,
		PermissionScope: []string{"mission", "evidence:public"},
	}, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}
	for _, item := range dto.Evidence.EvidenceItems {
		if item.EvidenceID == "evidence-default-hidden" {
			t.Fatalf("evidence without explicit visibility leaked: %+v", dto.Evidence.EvidenceItems)
		}
	}
}

func TestQueryServiceRejectsMissingProjection(t *testing.T) {
	ctx := context.Background()
	service := newAuthorizedQueryService(missioncontrol.NewMemoryProjectionRepository())

	_, err := service.GetMissionControlView(ctx, missioncontrol.ViewerRef{ViewerID: "user-1", ViewerKind: missioncontrol.ViewerKindUser}, "missing")
	if err == nil {
		t.Fatalf("GetMissionControlView returned nil error for missing projection")
	}
}

func TestDBProjectionRepositoryPersistsThroughCoreLogicalTable(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	model := appReadModel()

	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	loaded, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatalf("GetMissionControl returned error: %v", err)
	}
	if loaded.Mission.FinalGoal != model.Mission.FinalGoal || loaded.Checkpoint.Version != model.Checkpoint.Version {
		t.Fatalf("loaded projection mismatch: %+v", loaded)
	}
	if _, ok := env.DBRouter.Get("core.mission_control_projection", "global/mission-1"); !ok {
		t.Fatalf("projection was not stored in core.mission_control_projection")
	}
	eventsStore, err := env.Databases.Store(db.RoleEvents)
	if err != nil {
		t.Fatalf("lookup events store: %v", err)
	}
	if _, ok := eventsStore.Get("mission_control_projection", "global/mission-1"); ok {
		t.Fatalf("projection leaked into events database")
	}
}

func TestDBProjectionRepositoryReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)

	_, err := repo.GetMissionControl(ctx, "missing")
	if !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
		t.Fatalf("GetMissionControl error = %v, want ErrProjectionNotFound", err)
	}
}

func TestProjectionRepositoriesIsolateSameMissionIDByTenant(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	tests := []struct {
		name string
		repo interface {
			missioncontrol.ProjectionRepository
			missioncontrol.TenantProjectionRepository
			missioncontrol.TenantProjectionSaveRepository
		}
	}{
		{name: "memory", repo: missioncontrol.NewMemoryProjectionRepository()},
		{name: "database", repo: missioncontrol.NewDBProjectionRepository(env.DBRouter)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, tenantID := range []string{"tenant-a", "tenant-b"} {
				model := appReadModel()
				model.TenantID = tenantID
				model.Mission.TenantID = tenantID
				model.Mission.FinalGoal = "goal for " + tenantID
				if err := test.repo.SaveForTenant(ctx, tenantID, model); err != nil {
					t.Fatalf("SaveForTenant(%s): %v", tenantID, err)
				}
			}
			for _, tenantID := range []string{"tenant-a", "tenant-b"} {
				model, err := test.repo.GetMissionControlForTenant(ctx, tenantID, "mission-1")
				if err != nil || model.Mission.FinalGoal != "goal for "+tenantID {
					t.Fatalf("GetMissionControlForTenant(%s) = %+v, err=%v", tenantID, model.Mission, err)
				}
			}
			if _, err := test.repo.GetMissionControl(ctx, "mission-1"); !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
				t.Fatalf("legacy global GetMissionControl error = %v, want ErrProjectionNotFound", err)
			}
		})
	}
}

func TestProjectionRepositoriesRejectNonGlobalLegacySave(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	repositories := []interface {
		Save(context.Context, projection.ReadModel) error
	}{missioncontrol.NewMemoryProjectionRepository(), missioncontrol.NewDBProjectionRepository(env.DBRouter)}
	for _, repo := range repositories {
		model := appReadModel()
		model.TenantID = "tenant-a"
		model.Mission.TenantID = "tenant-a"
		if err := repo.Save(ctx, model); !errors.Is(err, missioncontrol.ErrServiceDependency) {
			t.Fatalf("legacy Save error = %v, want ErrServiceDependency", err)
		}
	}
}

func TestStreamServiceEmitsPatchDTOWithoutRawEvents(t *testing.T) {
	ctx := context.Background()
	model := appReadModel()
	stream := missioncontrol.NewStreamService()
	viewer := missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent, PermissionScope: []string{"mission", "evidence:public"}}

	patch, err := stream.BuildPatch(ctx, viewer, model, 3)
	if err != nil {
		t.Fatalf("BuildPatch returned error: %v", err)
	}
	if patch.MissionID != "mission-1" || patch.BaseVersion != 3 || patch.NextVersion != model.Checkpoint.Version {
		t.Fatalf("unexpected patch versions: %+v", patch)
	}
	if len(patch.ChangedViews) == 0 || patch.Payload.Mission.MissionID != "mission-1" {
		t.Fatalf("patch missing payload: %+v", patch)
	}
	if patch.RawEvents != nil {
		t.Fatalf("patch leaked raw events: %+v", patch.RawEvents)
	}
	if len(patch.Payload.Evidence.EvidenceItems) != 1 {
		t.Fatalf("patch did not apply permission trimming: %+v", patch.Payload.Evidence)
	}
}

func TestStreamServiceComputesChangedViewsFromBaseModel(t *testing.T) {
	ctx := context.Background()
	base := appReadModel()
	next := base
	next.TaskBoard.Tasks = append([]projection.TaskCardView(nil), base.TaskBoard.Tasks...)
	next.TaskBoard.Tasks[0].Status = projection.TaskStatusApproved
	next.TaskBoard.Tasks[0].QAStatus = projection.QAStatusApproved
	next.Checkpoint.Version = base.Checkpoint.Version + 1
	next.UpdatedAt = base.UpdatedAt.Add(time.Minute)
	stream := missioncontrol.NewStreamService()
	viewer := missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent, PermissionScope: []string{"mission", "evidence:public"}}

	patch, err := stream.BuildPatchFromBase(ctx, viewer, base, next)
	if err != nil {
		t.Fatalf("BuildPatchFromBase returned error: %v", err)
	}
	want := []string{"tasks", "summary"}
	if len(patch.ChangedViews) != len(want) || patch.ChangedViews[0] != want[0] || patch.ChangedViews[1] != want[1] {
		t.Fatalf("changed views = %#v, want %#v", patch.ChangedViews, want)
	}
	if patch.Payload.TaskBoard.Tasks[0].Status != string(projection.TaskStatusApproved) {
		t.Fatalf("patch payload did not contain next task board: %+v", patch.Payload.TaskBoard)
	}
	if patch.RawEvents != nil {
		t.Fatalf("patch leaked raw events: %+v", patch.RawEvents)
	}
}

func TestStreamServiceBuildLatestPatchLoadsCurrentProjection(t *testing.T) {
	ctx := context.Background()
	base := appReadModel()
	next := base
	next.Discussion.Messages = append([]projection.MessageView(nil), base.Discussion.Messages...)
	next.Discussion.Messages[0].Body = "Visible update"
	next.Checkpoint.Version = base.Checkpoint.Version + 1
	next.UpdatedAt = base.UpdatedAt.Add(time.Minute)
	repo := missioncontrol.NewMemoryProjectionRepository()
	if err := repo.Save(ctx, next); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	stream := missioncontrol.NewRepositoryStreamService(repo)
	viewer := missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent, PermissionScope: []string{"mission", "evidence:public"}}

	patch, err := stream.BuildLatestPatch(ctx, viewer, "mission-1", base)
	if err != nil {
		t.Fatalf("BuildLatestPatch returned error: %v", err)
	}
	if patch.NextVersion != next.Checkpoint.Version {
		t.Fatalf("next version = %d, want %d", patch.NextVersion, next.Checkpoint.Version)
	}
	if len(patch.ChangedViews) != 1 || patch.ChangedViews[0] != "discussion" {
		t.Fatalf("changed views = %#v, want discussion", patch.ChangedViews)
	}
}

func TestMapViewDTOExposesStableTeamMemberIdentity(t *testing.T) {
	at := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	model := projection.ReadModel{
		Mission: projection.MissionView{MissionID: "mission-1", Title: "Stage 4", FinalGoal: "Build app DTOs", Status: projection.MissionStatusActive, CreatedAt: at},
		Team: projection.TeamView{
			Members: []projection.TeamMemberView{{
				AgentID: "profile-worker", AgentProfileID: "profile-worker", AgentInstanceID: "profile-worker:mission-1", AgentKind: "ai", DisplayName: "Ari", RoleName: "Engineer", MissionRole: "worker", Status: projection.TeamMemberJoined, JoinedAt: at,
			}},
			Assignments: []projection.AssignmentView{{
				AssignmentID: "assignment-1", AgentID: "profile-worker", AgentInstanceID: "profile-worker:mission-1", TaskID: "task-1", MissionRole: "worker", ChangedAt: at,
			}},
		},
		TaskBoard: projection.TaskBoardView{Tasks: []projection.TaskCardView{{
			TaskID: "task-1", Assignee: "profile-worker", Goal: "Build DTOs", Status: projection.TaskStatusAssigned, UpdatedAt: at,
		}}},
		Checkpoint: projection.Checkpoint{MissionID: "mission-1", Version: 1, UpdatedAt: at},
		UpdatedAt:  at,
	}

	dto := missioncontrol.MapViewDTO(model, missioncontrol.ViewerRef{ViewerID: "viewer", ViewerKind: missioncontrol.ViewerKindSystem})

	if len(dto.Team.Members) != 1 {
		t.Fatalf("team member count = %d, want 1", len(dto.Team.Members))
	}
	member := dto.Team.Members[0]
	if member.AgentID != "profile-worker" || member.AgentProfileID != "profile-worker" || member.AgentInstanceID != "profile-worker:mission-1" || member.AgentKind != "ai" {
		t.Fatalf("member dto identity = %+v, want stable profile identity with scoped instance ref and kind", member)
	}
	if len(dto.Team.Assignments) != 1 || dto.Team.Assignments[0].AgentID != "profile-worker" || dto.Team.Assignments[0].AgentInstanceID != "profile-worker:mission-1" {
		t.Fatalf("assignment dto identity = %+v, want stable profile id plus scoped instance ref", dto.Team.Assignments)
	}
	if len(dto.TaskBoard.Tasks) != 1 || dto.TaskBoard.Tasks[0].Assignee != "profile-worker" || dto.TaskBoard.Tasks[0].AssigneeType != "ai" || dto.TaskBoard.Tasks[0].AssigneeDisplayName != "Ari" {
		t.Fatalf("task dto assignee = %+v, want team lookup by stable profile id", dto.TaskBoard.Tasks)
	}
}

func appReadModel() projection.ReadModel {
	at := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	return projection.ReadModel{
		Mission: projection.MissionView{MissionID: missioncore.MissionID("mission-1"), Title: "Stage 4", FinalGoal: "Build app DTOs", Status: projection.MissionStatusActive, CreatedAt: at},
		Team: projection.TeamView{Members: []projection.TeamMemberView{{
			AgentID: "agent-1", DisplayName: "Ari", RealName: "Ari Chen", RoleName: "Engineer", MissionRole: "worker", Status: projection.TeamMemberJoined, JoinedAt: at,
		}}},
		TaskBoard: projection.TaskBoardView{Tasks: []projection.TaskCardView{{
			TaskID: "task-1", Assignee: "agent-1", Goal: "Build DTOs", Status: projection.TaskStatusSubmitted, LatestResultRef: "result-1", QAStatus: projection.QAStatusRequested, BlockerRefs: []string{"blocker-1"}, UpdatedAt: at,
		}}},
		Evidence: projection.EvidenceView{EvidenceItems: []projection.EvidenceItemView{
			{EvidenceID: "evidence-visible", TaskID: "task-1", AgentID: "agent-1", Kind: agentcore.EvidenceKindTestLog, ArtifactRef: agentcore.ArtifactRef{ID: "log-1", URI: "artifact://log"}, VisibleToPermissionKeys: []string{"evidence:public"}, SubmittedAt: at},
			{EvidenceID: "evidence-hidden", TaskID: "task-1", AgentID: "agent-1", Kind: agentcore.EvidenceKindToolLog, ToolLogRef: agentcore.ToolLogRef{ID: "tool-log-1"}, VisibleToPermissionKeys: []string{"evidence:qa"}, SubmittedAt: at},
		}},
		QA: projection.QAView{
			Checks:         []projection.QACheckView{{QARef: "qa-1", TaskID: "task-1", Status: projection.QAStatusRequested, Message: "Check", CreatedAt: at}},
			ReworkRequests: []projection.ReworkRequestView{{TaskID: "task-1", QARef: "rework-1", Reason: "missing edge case", Status: projection.ReworkStatusResolved, CreatedAt: at, ResolvedAt: at.Add(time.Minute)}},
		},
		Discussion: projection.DiscussionView{DiscussionGroupID: "discussion-1", Messages: []projection.MessageView{
			{MessageID: "message-public", DiscussionGroupID: "discussion-1", AuthorID: "agent-1", Body: "Visible", Visibility: []string{"mission"}, PostedAt: at},
			{MessageID: "message-hidden", DiscussionGroupID: "discussion-1", AuthorID: "agent-1", Body: "Hidden", Visibility: []string{"qa-only"}, PostedAt: at},
		}},
		InboxSummary:    projection.InboxSummaryView{OpenCount: 1, Items: []projection.InboxItemView{{InboxItemID: "inbox-1", OwnerID: "agent-1", MessageID: "message-public", Status: "open", CreatedAt: at}}},
		ProgressGraph:   projection.ProgressGraphView{Nodes: []projection.ProgressNodeView{{ID: "evt-1", EventID: "evt-1", EventType: "MissionCreated", Label: "MissionCreated", Status: "succeeded", OccurredAt: at}}},
		LatestDecisions: []projection.DecisionView{{DecisionID: "decision-1", Summary: "Proceed", SourceRefs: []string{"message-public"}, RecordedAt: at}},
		Blockers:        []projection.BlockerView{{BlockerID: "blocker-1", TaskID: "task-1", RaisedBy: "agent-1", Summary: "Waiting", RaisedAt: at}},
		UpdatedAt:       at,
		Checkpoint:      projection.Checkpoint{MissionID: "mission-1", Version: 7, UpdatedAt: at},
	}
}

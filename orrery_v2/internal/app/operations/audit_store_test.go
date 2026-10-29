package operations_test

import (
	"context"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/rebac"
	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/app/userauth"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestAuditStoreAuditIDsAreIdempotentAndImmutable(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	store := operations.NewAuditStore(env.DBRouter, env.Clock, env.IDGenerator)
	ctx := context.Background()
	event := operations.AuditEventDTO{
		AuditID: "audit-stable", TenantID: "tenant-1", ActorID: "agent-1", EntityType: "task", EntityID: "task-1",
		Action: "task.assign", Result: "succeeded", Severity: "info", CorrelationID: "assign-1", OccurredAt: env.Clock.Now().UTC(),
	}
	if err := store.RecordAuditEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAuditEvent(ctx, event); err != nil {
		t.Fatalf("identical audit replay: %v", err)
	}
	changed := event
	changed.ActorID = "agent-2"
	if err := store.RecordAuditEvent(ctx, changed); err != nil {
		t.Fatalf("changed replay should observe the immutable original event: %v", err)
	}
	page, err := store.ListAuditEvents(ctx, operations.AuditLogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].ActorID != "agent-1" {
		t.Fatalf("immutable audit event = %+v", page.Events)
	}
}

func TestAuditStoreRecordsAndFiltersCommandAuditEvents(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	store := operations.NewAuditStore(env.DBRouter, env.Clock, env.IDGenerator)
	at := env.Clock.Now().Add(2*time.Hour + 30*time.Minute)
	env.Clock.Advance(2*time.Hour + 30*time.Minute)

	if err := store.RecordAuditEvent(ctx, operations.AuditEventDTO{
		AuditID:       "audit-task-1",
		TenantID:      "tenant-a",
		ActorID:       "agent-1",
		ViewerID:      "user-1",
		MissionID:     "mission-1",
		EntityType:    "task",
		EntityID:      "task-1",
		Action:        "task.submit_result",
		Result:        "succeeded",
		Severity:      "info",
		CorrelationID: "corr-1",
		Summary:       "agent submitted task result",
		Metadata:      map[string]string{"agent_id": "agent-1", "tool_id": "tool.deploy", "scope": "deploy:write"},
	}); err != nil {
		t.Fatalf("RecordAuditEvent: %v", err)
	}
	if err := store.RecordAuditEvent(ctx, operations.AuditEventDTO{
		AuditID:    "audit-task-failed",
		ActorID:    "agent-1",
		ViewerID:   "user-1",
		MissionID:  "mission-1",
		EntityType: "task",
		EntityID:   "task-2",
		Action:     "task.submit_result",
		Result:     "failed",
		Severity:   "warning",
		Summary:    "agent task result failed",
	}); err != nil {
		t.Fatalf("RecordAuditEvent failed result: %v", err)
	}
	if err := store.RecordAuditEvent(ctx, operations.AuditEventDTO{
		ActorID:       "admin",
		EntityType:    "resource",
		EntityID:      "tool.deploy",
		Action:        "resource.tool.register",
		Result:        "succeeded",
		Severity:      "info",
		CorrelationID: "corr-2",
	}); err != nil {
		t.Fatalf("RecordAuditEvent resource: %v", err)
	}
	if _, ok := mustAuditRecord(t, env.Databases, db.RoleEvents, "audit-task-1"); ok {
		t.Fatalf("audit event leaked into events database")
	}
	persisted, ok := mustAuditRecord(t, env.Databases, db.RoleAudit, "audit-task-1")
	if !ok {
		t.Fatalf("audit event was not written to audit database")
	}
	if persisted["tenant_id"] != "tenant-a" {
		t.Fatalf("audit tenant was not persisted: %+v", persisted)
	}

	page, err := store.ListAuditEvents(ctx, operations.AuditLogQuery{MissionID: "mission-1", ActorID: "agent-1", Result: "succeeded"})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("events = %+v", page.Events)
	}
	event := page.Events[0]
	if event.AuditID == "" || event.TenantID != "tenant-a" || event.Action != "task.submit_result" || event.Result != "succeeded" || event.CorrelationID != "corr-1" || !event.OccurredAt.Equal(at) {
		t.Fatalf("event = %+v", event)
	}
	if event.Metadata["tool_id"] != "tool.deploy" || event.Metadata["scope"] != "deploy:write" {
		t.Fatalf("metadata = %+v", event.Metadata)
	}
	if page.Page.Total != 1 || page.Page.Returned != 1 {
		t.Fatalf("page = %+v", page.Page)
	}
}

func TestAuditStoreRecordsConcurrentIPFailuresAtomically(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	store := operations.NewAuditStore(env.DBRouter, env.Clock, env.IDGenerator)

	var wg sync.WaitGroup
	errorsSeen := make(chan error, 3)
	for attempt := 0; attempt < 3; attempt++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorsSeen <- store.RecordIPFailure(ctx, "198.51.100.26", "operator", "invalid_password", env.Clock.Now(), 15*time.Minute, 3)
		}()
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("RecordIPFailure: %v", err)
		}
	}

	lock, ok, err := store.GetIPLock(ctx, "198.51.100.26")
	if err != nil {
		t.Fatalf("GetIPLock: %v", err)
	}
	if !ok || lock.Status != "active" || lock.FailureCount != 3 {
		t.Fatalf("IP lock = %+v, ok=%v", lock, ok)
	}
	events, err := store.ListAuditEvents(ctx, operations.AuditLogQuery{Action: "auth.ip.lock"})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events.Events) != 1 {
		t.Fatalf("IP lock audit events = %+v", events.Events)
	}
}

func TestAuditStorePersistsAuthIPLocksAndRecordsUnlockAudit(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	store := operations.NewAuditStore(env.DBRouter, env.Clock, env.IDGenerator)
	lockedAt := env.Clock.Now()

	if err := store.SaveIPLock(ctx, userauth.IPBlockRecord{
		IPAddress:    "198.51.100.25",
		Status:       "active",
		FailureCount: 3,
		LastUsername: "superadmin",
		LastReason:   "invalid_password",
		LockedAt:     lockedAt,
		UpdatedAt:    lockedAt,
	}); err != nil {
		t.Fatalf("SaveIPLock: %v", err)
	}
	if _, ok := mustStoreRecord(t, env.Databases, db.RoleAudit, "auth_ip_locks", "198.51.100.25"); !ok {
		t.Fatalf("auth IP lock was not written to audit database")
	}
	if _, ok := mustStoreRecord(t, env.Databases, db.RoleCore, "auth_ip_locks", "198.51.100.25"); ok {
		t.Fatalf("auth IP lock leaked into core database")
	}
	locks, err := store.ListIPLocks(ctx, userauth.IPBlockQuery{Status: "active"})
	if err != nil {
		t.Fatalf("ListIPLocks: %v", err)
	}
	if len(locks.Locks) != 1 || locks.Locks[0].IPAddress != "198.51.100.25" || locks.Locks[0].FailureCount != 3 {
		t.Fatalf("locks = %+v", locks.Locks)
	}
	lockEvents, err := store.ListAuditEvents(ctx, operations.AuditLogQuery{Action: "auth.ip.lock"})
	if err != nil {
		t.Fatalf("ListAuditEvents lock: %v", err)
	}
	if len(lockEvents.Events) != 1 || lockEvents.Events[0].Severity != "warning" {
		t.Fatalf("lock audit events = %+v", lockEvents.Events)
	}

	env.Clock.Advance(time.Minute)
	if err := store.UnlockIP(ctx, "198.51.100.25", "superadmin", "operator reviewed"); err != nil {
		t.Fatalf("UnlockIP: %v", err)
	}
	if _, ok, err := store.GetIPLock(ctx, "198.51.100.25"); err != nil || ok {
		t.Fatalf("GetIPLock after unlock ok=%v err=%v", ok, err)
	}
	unlockEvents, err := store.ListAuditEvents(ctx, operations.AuditLogQuery{Action: "auth.ip.unlock"})
	if err != nil {
		t.Fatalf("ListAuditEvents unlock: %v", err)
	}
	if len(unlockEvents.Events) != 1 || unlockEvents.Events[0].ActorID != "superadmin" || unlockEvents.Events[0].Metadata["reason"] != "operator reviewed" {
		t.Fatalf("unlock audit events = %+v", unlockEvents.Events)
	}
}

func TestCommandServicesRecordAuditEventsIntoOperationsAuditStore(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	store := operations.NewAuditStore(env.DBRouter, env.Clock, env.IDGenerator)

	missionCommands := missioncontrol.NewCommandService(&auditDeliveryPort{submission: missiontask.TaskSubmission{ID: "submission-1", TaskID: "task-1", AgentID: "agent-1", SubmittedAt: env.Clock.Now(), TaskVersionAccepted: 1}})
	missionAuthorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	if err := missionAuthorizer.WriteRelations(ctx, []authorization.RelationTuple{{
		TenantID: "global", Subject: authorization.SubjectRef{Kind: identity.ViewerKindAgent, ID: "agent-1"}, Relation: "participant",
		Object: identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "global"},
	}}); err != nil {
		t.Fatalf("write mission authorization relation: %v", err)
	}
	missionCommands.SetAuthorizationService(missionAuthorizer)
	missionCommands.SetAuditRecorder(operations.NewMissionCommandAuditRecorder(store))
	if _, err := missionCommands.SubmitAgentResult(ctx, missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent, PermissionScope: []string{"mission.read"}}, missioncontrol.SubmitResultCommand{
		RequestID:      "corr-mission",
		IdempotencyKey: "submit-1",
		MissionID:      "mission-1",
		TaskID:         "task-1",
		Result:         agentcore.AgentResult{ID: "result-1", Summary: agentcore.Summary{Text: "Done"}},
	}); err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}

	resourceCommands := resourceadmin.NewCommandService(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry())
	resourceCommands.SetAuditRecorder(operations.NewResourceCommandAuditRecorder(store))
	if _, err := resourceCommands.RegisterTool(ctx, resourceadmin.ViewerRef{ViewerID: "admin", PermissionScope: []string{resourceadmin.PermissionResourceManage}}, resourceadmin.ToolDefinitionRequest{
		ToolID:  "tool.deploy",
		Kind:    string(toolres.KindCLITool),
		Name:    "Deploy",
		Version: "1.0.0",
		Command: "deploy",
	}); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	audits, err := store.ListAuditEvents(ctx, operations.AuditLogQuery{})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(audits.Events) != 2 {
		t.Fatalf("events = %+v", audits.Events)
	}
	if audits.Events[0].Action == "" || audits.Events[1].Action == "" {
		t.Fatalf("events missing action: %+v", audits.Events)
	}
}

type auditDeliveryPort struct {
	submission missiontask.TaskSubmission
}

func (p *auditDeliveryPort) SubmitAgentResult(ctx context.Context, taskID missiontask.TaskID, result agentcore.AgentResult, opts missiontask.SubmitOptions) (missiontask.TaskSubmission, error) {
	p.submission.TaskID = missioncore.TaskID(taskID)
	return p.submission, nil
}

func mustAuditRecord(t *testing.T, registry *db.Registry, role string, auditID string) (db.Record, bool) {
	t.Helper()
	return mustStoreRecord(t, registry, role, "audit_log", auditID)
}

func mustStoreRecord(t *testing.T, registry *db.Registry, role string, table string, key string) (db.Record, bool) {
	t.Helper()
	store, err := registry.Store(role)
	if err != nil {
		t.Fatalf("lookup %s database: %v", role, err)
	}
	return store.Get(table, key)
}

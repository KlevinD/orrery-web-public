package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/operations"
	schedulerapp "orrery_v2/internal/app/scheduler"
	knowledgecore "orrery_v2/internal/knowledge/core"
	memorycore "orrery_v2/internal/memory/core"
	memoryStore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

type fakeOntologyInitializationAdapter struct {
	name string
	run  func(context.Context, ontologyInitializationRequest) (ontologyInitializationDomainReport, error)
}

func (a fakeOntologyInitializationAdapter) Name() string { return a.name }

func (a fakeOntologyInitializationAdapter) Reconcile(ctx context.Context, request ontologyInitializationRequest) (ontologyInitializationDomainReport, error) {
	return a.run(ctx, request)
}

func TestOntologyInitializationCoordinatorRunsAdaptersInOrder(t *testing.T) {
	calls := []string{}
	adapters := []ontologyInitializationAdapter{
		fakeOntologyInitializationAdapter{name: "baseline", run: func(context.Context, ontologyInitializationRequest) (ontologyInitializationDomainReport, error) {
			calls = append(calls, "baseline")
			return ontologyInitializationDomainReport{Name: "baseline", Source: 3, Projected: 3}, nil
		}},
		fakeOntologyInitializationAdapter{name: "scheduler", run: func(context.Context, ontologyInitializationRequest) (ontologyInitializationDomainReport, error) {
			calls = append(calls, "scheduler")
			return ontologyInitializationDomainReport{Name: "scheduler", Source: 2, Unchanged: 2}, nil
		}},
	}

	report, err := newOntologyInitializationCoordinator(adapters).Run(context.Background(), ontologyInitializationRequest{
		Mode:     ontologyInitializationDryRun,
		Operator: "operator-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]string{"baseline", "scheduler"}, calls) {
		t.Fatalf("calls = %v", calls)
	}
	if report.Source != 5 || report.Projected != 3 || report.Unchanged != 2 {
		t.Fatalf("report = %+v", report)
	}
}

func TestOntologyInitializationCoordinatorRequiresOperatorForApply(t *testing.T) {
	_, err := newOntologyInitializationCoordinator(nil).Run(context.Background(), ontologyInitializationRequest{Mode: ontologyInitializationApply})
	if err == nil || !strings.Contains(err.Error(), "operator") {
		t.Fatalf("error = %v", err)
	}
}

func TestOntologyInitializationCoordinatorBoundsIssues(t *testing.T) {
	issues := make([]ontologyInitializationIssue, 101)
	for index := range issues {
		issues[index] = ontologyInitializationIssue{Domain: "scheduler", SourceRef: "task", Code: "historical_gap"}
	}
	adapter := fakeOntologyInitializationAdapter{name: "scheduler", run: func(context.Context, ontologyInitializationRequest) (ontologyInitializationDomainReport, error) {
		return ontologyInitializationDomainReport{Name: "scheduler", Issues: issues}, nil
	}}

	report, err := newOntologyInitializationCoordinator([]ontologyInitializationAdapter{adapter}).Run(context.Background(), ontologyInitializationRequest{Mode: ontologyInitializationDryRun})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(report.Domains[0].Issues); got != 100 {
		t.Fatalf("issues = %d, want 100", got)
	}
}

func TestOntologyInitializationDryRunDoesNotMutateProductionGraph(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	runner, err := newOntologyInitializationProjector(ctx, core, ontologyInitializationDryRun)
	if err != nil {
		t.Fatalf("new preview projector: %v", err)
	}
	event := bootstrapOntologyEvent(defaultSystemSupportOrganizationID, "preview-agent", "people.agent.updated", "preview-agent", map[string]any{
		"id": "preview-agent", "display_name": "Preview Agent", "runtime_kind": "codex",
	})
	report, err := runner.ApplyEvent(ctx, event)
	if err != nil {
		t.Fatalf("apply preview event: %v", err)
	}
	if report.Applied == 0 {
		t.Fatalf("preview event was not projected: %+v", report)
	}

	objects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, defaultSystemSupportOrganizationID)
	if err != nil {
		t.Fatalf("production graph snapshot: %v", err)
	}
	for _, object := range objects {
		if object.ID == "preview-agent" {
			t.Fatalf("dry run mutated production graph: %+v", object)
		}
	}
}

func TestOntologyInitializationReconcilesBaselineToolFromCatalog(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	runner, err := newOntologyInitializationProjector(ctx, core, ontologyInitializationDryRun)
	if err != nil {
		t.Fatalf("new preview projector: %v", err)
	}
	tools := defaultSystemToolDefinitions()
	if len(tools) == 0 {
		t.Fatal("default system tools are empty")
	}
	source := fakeOntologyInitializationCatalogSource{tools: []toolres.ToolDefinition{tools[0]}}
	coordinator := newOntologyInitializationCoordinator([]ontologyInitializationAdapter{
		newOntologyInitializationBaselineAdapter(config.Config{}, runner),
		newOntologyInitializationResourceAdapter(source, source, runner),
	})

	if _, err := coordinator.Run(ctx, ontologyInitializationRequest{Mode: ontologyInitializationDryRun, PageSize: 100}); err != nil {
		t.Fatalf("reconcile catalog tool already projected by baseline: %v", err)
	}
}

func TestOntologyInitializationReconcilesBaselineMemoryProfileFromCatalog(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	runner, err := newOntologyInitializationProjector(ctx, core, ontologyInitializationDryRun)
	if err != nil {
		t.Fatalf("new preview projector: %v", err)
	}
	profiles, err := defaultCoordinatorAgentProfilesFromConfig(config.Config{})
	if err != nil {
		t.Fatalf("default coordinator profiles: %v", err)
	}
	if len(profiles) == 0 {
		t.Fatal("default coordinator profiles are empty")
	}
	memoryProfile := defaultCoordinatorMemoryProfile(profiles[0].ID.String(), profiles[0].RoleName)
	source := fakeOntologyInitializationCatalogSource{profiles: []memorycore.MemoryProfile{memoryProfile}}
	coordinator := newOntologyInitializationCoordinator([]ontologyInitializationAdapter{
		newOntologyInitializationBaselineAdapter(config.Config{}, runner),
		newOntologyInitializationMemoryAdapter(source, runner),
	})

	if _, err := coordinator.Run(ctx, ontologyInitializationRequest{Mode: ontologyInitializationDryRun, PageSize: 100}); err != nil {
		t.Fatalf("reconcile memory profile already projected by baseline: %v", err)
	}
}

func TestOntologyInitializationBaselineAndSchedulerAreIdempotentAndTenantScoped(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "ontology-initialization-scheduler")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	taskA := schedulerapp.ScheduledTask{
		TenantID: "tenant-a", ScheduledTaskID: "schedule-a", Title: "Tenant A schedule",
		Target:   schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-a"},
		Schedule: schedulerapp.Schedule{Type: schedulerapp.ScheduleTypeCron, Cron: "0 9 * * *", Timezone: "UTC"},
		Status:   schedulerapp.TaskStatusEnabled, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	taskB := schedulerapp.ScheduledTask{
		TenantID: "tenant-b", ScheduledTaskID: "schedule-b", Title: "Tenant B schedule",
		Target:   schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-b"},
		Schedule: schedulerapp.Schedule{Type: schedulerapp.ScheduleTypeCron, Cron: "0 10 * * *", Timezone: "UTC"},
		Status:   schedulerapp.TaskStatusEnabled, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	runA := schedulerapp.ScheduledRun{
		ScheduledRunID: "run-a", ScheduledTaskID: taskA.ScheduledTaskID, Status: schedulerapp.RunStatusCompleted,
		TargetMode: schedulerapp.TargetModeAgentTask, CommandID: "runtime-a", CreatedAt: now, StartedAt: now, CompletedAt: now.Add(time.Minute),
	}
	source := fakeOntologyInitializationSchedulerSource{
		tasks: []schedulerapp.ScheduledTask{taskB, taskA},
		runs:  map[string][]schedulerapp.ScheduledRun{taskA.ScheduledTaskID: {runA}},
	}
	coordinator := newOntologyInitializationCoordinator([]ontologyInitializationAdapter{
		newOntologyInitializationBaselineAdapter(config.Config{}, runner),
		newOntologyInitializationSchedulerAdapter(source, runner),
	})
	request := ontologyInitializationRequest{Mode: ontologyInitializationApply, Operator: "operator-a"}
	first, err := coordinator.Run(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Source == 0 || first.Projected == 0 || first.Unresolved != 1 {
		t.Fatalf("first report = %+v", first)
	}

	objectsA, linksA, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, taskA.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objectsA, "agent_task", schedulerapp.AgentTaskIDForScheduledTask(taskA.ScheduledTaskID))
	assertContextGraphObject(t, objectsA, "scheduled_task", taskA.ScheduledTaskID)
	assertContextGraphObject(t, objectsA, "scheduled_run", runA.ScheduledRunID)
	for _, object := range objectsA {
		if object.ID == taskB.ScheduledTaskID {
			t.Fatalf("tenant-a contains tenant-b object: %+v", object)
		}
	}
	objectsBefore, linksBefore := len(objectsA), len(linksA)
	if _, err := coordinator.Run(ctx, request); err != nil {
		t.Fatal(err)
	}
	objectsA, linksA, _, _, err = contextgraph.NewPlatformStore(core).Snapshot(ctx, taskA.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(objectsA) != objectsBefore || len(linksA) != linksBefore {
		t.Fatalf("second pass changed graph size: objects %d->%d links %d->%d", objectsBefore, len(objectsA), linksBefore, len(linksA))
	}

	registry := ontology.NewPlatformRegistry(core)
	audit, err := ontology.AuditDefaultOntologyDefinitions(ctx, registry)
	if err != nil {
		t.Fatal(err)
	}
	if audit.MissingObjectTypes != 0 || audit.MissingLinkTypes != 0 || audit.MissingMappingRules != 0 {
		t.Fatalf("managed ontology is incomplete: %+v", audit)
	}
}

type fakeOntologyInitializationSchedulerSource struct {
	tasks []schedulerapp.ScheduledTask
	runs  map[string][]schedulerapp.ScheduledRun
}

func (f fakeOntologyInitializationSchedulerSource) ListTasks(context.Context, bool) ([]schedulerapp.ScheduledTask, error) {
	return f.tasks, nil
}

func (f fakeOntologyInitializationSchedulerSource) ListRunsForTask(_ context.Context, taskID string) ([]schedulerapp.ScheduledRun, error) {
	return f.runs[taskID], nil
}

type fakeOntologyInitializationCatalogSource struct {
	organizations []operations.OrganizationDTO
	graphs        map[string]operations.OrgGraphDTO
	agents        []agentcore.AgentDirectoryEntry
	tools         []toolres.ToolDefinition
	skills        []skillres.SkillDefinition
	bases         []knowledgecore.KnowledgeBase
	documents     map[knowledgecore.KnowledgeBaseID][]knowledgecore.KnowledgeDocument
	profiles      []memorycore.MemoryProfile
	records       []memorycore.MemoryRecord
}

func (f fakeOntologyInitializationCatalogSource) ListOrganizationCatalog(_ context.Context, afterID string, limit int) ([]operations.OrganizationDTO, bool, error) {
	start := 0
	for start < len(f.organizations) && f.organizations[start].OrganizationID <= afterID {
		start++
	}
	end := min(len(f.organizations), start+limit)
	return append([]operations.OrganizationDTO(nil), f.organizations[start:end]...), end < len(f.organizations), nil
}

func (f fakeOntologyInitializationCatalogSource) ListOrganizations(_ context.Context, query operations.OrgGraphQuery) (operations.OrgGraphDTO, error) {
	return f.graphs[query.OrganizationID], nil
}

func (f fakeOntologyInitializationCatalogSource) QueryDirectory(_ context.Context, query agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	start := min(query.Page.Offset, len(f.agents))
	end := min(len(f.agents), start+query.Page.Limit)
	return agentcore.AgentDirectoryPage{Entries: append([]agentcore.AgentDirectoryEntry(nil), f.agents[start:end]...), Total: len(f.agents), Limit: query.Page.Limit, Offset: query.Page.Offset}, nil
}

func (f fakeOntologyInitializationCatalogSource) ListCatalog(_ context.Context, afterID string, limit int) (toolres.CatalogPage, error) {
	start := 0
	for start < len(f.tools) && string(f.tools[start].ID) <= afterID {
		start++
	}
	end := min(len(f.tools), start+limit)
	next := afterID
	if end > start {
		next = string(f.tools[end-1].ID)
	}
	return toolres.CatalogPage{Definitions: append([]toolres.ToolDefinition(nil), f.tools[start:end]...), NextAfterID: next, HasMore: end < len(f.tools)}, nil
}

func (f fakeOntologyInitializationCatalogSource) List(context.Context) ([]skillres.SkillDefinition, error) {
	return append([]skillres.SkillDefinition(nil), f.skills...), nil
}

func (f fakeOntologyInitializationCatalogSource) ListKnowledgeBases(context.Context, knowledgecore.KnowledgeBaseQuery) (knowledgecore.KnowledgeBasePage, error) {
	return knowledgecore.KnowledgeBasePage{Items: append([]knowledgecore.KnowledgeBase(nil), f.bases...), Total: len(f.bases)}, nil
}

func (f fakeOntologyInitializationCatalogSource) ListDocuments(_ context.Context, query knowledgecore.DocumentQuery) (knowledgecore.DocumentPage, error) {
	items := f.documents[query.KnowledgeBaseID]
	return knowledgecore.DocumentPage{Items: append([]knowledgecore.KnowledgeDocument(nil), items...), Total: len(items)}, nil
}

func (f fakeOntologyInitializationCatalogSource) ListProfiles(context.Context) ([]memorycore.MemoryProfile, error) {
	return append([]memorycore.MemoryProfile(nil), f.profiles...), nil
}

func (f fakeOntologyInitializationCatalogSource) FindRecords(context.Context, memoryStore.RecordQuery) ([]memorycore.MemoryRecord, error) {
	return append([]memorycore.MemoryRecord(nil), f.records...), nil
}

func TestOntologyInitializationSkipsPersonalAssistantMemory(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "ontology-initialization-private-assistant-memory")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	assistantID := "A009993"
	if err := agentcore.RegisterPersonalAssistantProfileID("tenant-a", "owner-initialization-memory", assistantID); err != nil {
		t.Fatal(err)
	}
	profileID := agentcore.MemoryProfileID(discussionsession.PersonalAssistantMemoryProfileID(assistantID))
	source := fakeOntologyInitializationCatalogSource{
		profiles: []memorycore.MemoryProfile{{
			ID: profileID, OwnerRef: memorycore.OwnerRef{AgentID: agentcore.AgentID(assistantID)},
			MemoryKind: agentcore.MemoryKindPersonal, DefaultScope: memorycore.MemoryScope{Kind: memorycore.ScopeAgent, ID: assistantID}, Enabled: true,
		}},
		records: []memorycore.MemoryRecord{{
			ID: "memory-private", ProfileID: profileID,
			Scope: memorycore.MemoryScope{Kind: memorycore.ScopeAgent, ID: assistantID}, Summary: "Private preference",
		}},
	}
	report, err := newOntologyInitializationMemoryAdapter(source, runner).Reconcile(ctx, ontologyInitializationRequest{})
	if err != nil {
		t.Fatalf("reconcile memory: %v", err)
	}
	if report.Source != 0 || report.Projected != 0 || report.Skipped != 0 {
		t.Fatalf("personal assistant memory report = %+v", report)
	}
	objects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "agent:"+assistantID)
	if err != nil || len(objects) != 0 {
		t.Fatalf("personal assistant ontology objects = %+v, err = %v", objects, err)
	}
}

func TestOntologyInitializationReportsMissingHistoricalPosition(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "ontology-initialization-missing-position")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	source := fakeOntologyInitializationCatalogSource{agents: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: "legacy-agent", DisplayName: "Legacy Agent", AgentKind: agentcore.AgentKindAI, Enabled: true,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-legacy"},
			PositionRef:     agentcore.PositionRef{ID: "position-removed"},
			RoleRefs:        []agentcore.RoleRef{{ID: "operator"}},
		},
	}}}

	report, err := newOntologyInitializationWorkforceAdapter(source, source, runner).Reconcile(ctx, ontologyInitializationRequest{Mode: ontologyInitializationApply, PageSize: 100})
	if err != nil {
		t.Fatalf("reconcile workforce: %v", err)
	}
	if report.Source != 3 || report.Projected != 2 || report.Unresolved != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Issues) != 1 || report.Issues[0].SourceRef != "legacy-agent" || report.Issues[0].Code != "historical_position_unavailable" {
		t.Fatalf("issues = %+v", report.Issues)
	}
}

func TestOntologyInitializationReconcilesCatalogHistoryWithoutSensitiveContent(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "ontology-initialization-catalogs")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	source := fakeOntologyInitializationCatalogSource{
		organizations: []operations.OrganizationDTO{{OrganizationID: "org-1", Name: "Organization", Status: "active"}},
		graphs: map[string]operations.OrgGraphDTO{"org-1": {
			Organizations: []operations.OrganizationDTO{{OrganizationID: "org-1", Name: "Organization", Status: "active"}},
			Departments:   []operations.DepartmentDTO{{DepartmentID: "dept-1", OrganizationID: "org-1", Name: "Department", Status: "active"}},
			Positions:     []operations.PositionDTO{{PositionID: "position-1", DepartmentID: "dept-1", Name: "Position"}},
		}},
		agents: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: "profile-1", DisplayName: "Employee", RoleName: "operator", AgentKind: agentcore.AgentKindAI, Enabled: true,
			OrgIdentity:        agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "org-1"}, DepartmentRef: agentcore.DepartmentRef{ID: "dept-1"}, PositionRef: agentcore.PositionRef{ID: "position-1"}, RoleRefs: []agentcore.RoleRef{{ID: "operator"}}},
			RuntimeSpecSummary: agentcore.RuntimeSpecSummary{Kind: agentcore.RuntimeKind("pi")}, UpdatedAt: now,
		}},
		tools:     []toolres.ToolDefinition{{ID: "tool-1", Name: "Tool", RiskLevel: toolres.RiskLow, Enabled: true, UpdatedAt: now, MCP: &toolres.MCPServerDefinition{ServerName: "mcp-1", Transport: toolres.TransportStreamHTTP}}},
		skills:    []skillres.SkillDefinition{{ID: "skill-1", Name: "Skill", Version: "1", RiskLevel: skillres.RiskLow, Enabled: true, UpdatedAt: now}},
		bases:     []knowledgecore.KnowledgeBase{{ID: "kb-1", Name: "Knowledge", OwnerType: knowledgecore.OwnerOrganization, OwnerID: "org-1", Visibility: knowledgecore.VisibilityScoped, Provider: knowledgecore.ProviderLocal, Status: knowledgecore.KnowledgeActive, UpdatedAt: now}},
		documents: map[knowledgecore.KnowledgeBaseID][]knowledgecore.KnowledgeDocument{"kb-1": {{ID: "doc-1", KnowledgeBaseID: "kb-1", Filename: "Document", ContentType: "text/plain", Checksum: "sha256:doc", Status: knowledgecore.DocumentIndexed, UpdatedAt: now}}},
		profiles:  []memorycore.MemoryProfile{{ID: "memory-profile-1", OwnerRef: memorycore.OwnerRef{OrganizationID: "org-1", AgentID: "profile-1"}, MemoryKind: "personal", DefaultScope: memorycore.MemoryScope{Kind: memorycore.ScopeOrganization, ID: "org-1"}, Enabled: true}},
		records: []memorycore.MemoryRecord{{
			ID: "memory-1", ProfileID: "memory-profile-1", Scope: memorycore.MemoryScope{Kind: memorycore.ScopeOrganization, ID: "org-1"},
			Layer: memorycore.MemoryLayerFact, Topic: "operations", Content: "TOP-SECRET-CONTENT", Summary: "TOP-SECRET-SUMMARY",
			LifecycleStatus: memorycore.MemoryLifecycleActive, Confidence: 0.9, UpdatedAt: now,
		}},
	}
	coordinator := newOntologyInitializationCoordinator([]ontologyInitializationAdapter{
		newOntologyInitializationWorkforceAdapter(source, source, runner),
		newOntologyInitializationResourceAdapter(source, source, runner),
		newOntologyInitializationKnowledgeAdapter(source, runner),
		newOntologyInitializationMemoryAdapter(source, runner),
	})
	report, err := coordinator.Run(ctx, ontologyInitializationRequest{Mode: ontologyInitializationApply, Operator: "operator-a", PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "TOP-SECRET") {
		t.Fatalf("initialization report leaked source content: %s", encoded)
	}

	objects, _, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatal(err)
	}
	for objectType, id := range map[string]string{
		"organization": "org-1", "department": "dept-1", "position": "position-1",
		"person": "person:profile-1", "agent": "agent:profile-1", "role_assignment": "profile-1:position-1:operator",
		"knowledge_base": "kb-1", "knowledge_document": "doc-1", "memory_profile": "memory-profile-1", "memory_record": "memory-1",
	} {
		assertContextGraphObject(t, objects, objectType, id)
	}
	for _, fact := range facts {
		if strings.Contains(fmt.Sprint(fact.Value), "TOP-SECRET") {
			t.Fatalf("initialization graph leaked memory content: %+v", fact)
		}
	}
	resources, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, defaultSystemSupportOrganizationID)
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, resources, "tool", "tool-1")
	assertContextGraphObject(t, resources, "mcp_server", "mcp-1")
	assertContextGraphObject(t, resources, "skill", "skill-1")
}

func TestOntologyInitializationReconcilesCompletedActionHistory(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "ontology-initialization-actions")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	repository := actioncontract.NewMemoryRepository()
	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	completedAt := now.Add(time.Minute)
	run := actioncontract.ActionRun{
		ID: "action-history-1", TenantID: "org-action", ActionTypeID: "mcp.tool.call", Status: actioncontract.RunStatusCompleted,
		Actor:     actioncontract.ActionSubject{Kind: actioncontract.SubjectAgent, ID: "agent-1"},
		Runtime:   actioncontract.RuntimeRef{Kind: actioncontract.RuntimeAgent, ID: "runtime-1"},
		RiskLevel: actioncontract.RiskHigh, PolicyDecisionID: "policy-1", Approval: actioncontract.Ref{Kind: "approval", ID: "approval-1"},
		HILTokenID: "hil-1", EvidenceRefs: []actioncontract.Ref{{Kind: "evidence", ID: "evidence-1"}},
		Purpose: "TOP-SECRET-ACTION-PURPOSE", ArgsHash: "sha256:args", CreatedAt: now, CompletedAt: &completedAt,
	}
	if err := repository.SaveActionRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	coordinator := newOntologyInitializationCoordinator([]ontologyInitializationAdapter{
		newOntologyInitializationActionAdapter(repository, runner),
	})
	report, err := coordinator.Run(ctx, ontologyInitializationRequest{Mode: ontologyInitializationApply, Operator: "operator-a", PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Source != 1 || report.Projected != 1 || report.Unresolved != 2 {
		t.Fatalf("action report = %+v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "TOP-SECRET") {
		t.Fatalf("action report leaked source content: %s", encoded)
	}

	objects, links, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, run.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "action_run", string(run.ID))
	assertContextGraphObject(t, objects, "policy_decision", run.PolicyDecisionID)
	assertContextGraphObject(t, objects, "approval_case", "approval-case:"+run.Approval.ID)
	if !contextGraphLinksContain(links, "action_run_authorized_by_policy", string(run.ID), run.PolicyDecisionID) {
		t.Fatalf("policy link missing: %+v", links)
	}
	if !contextGraphLinksContain(links, "action_run_requires_approval_case", string(run.ID), "approval-case:"+run.Approval.ID) {
		t.Fatalf("approval link missing: %+v", links)
	}
}

func TestRunRejectsConflictingOntologyInitializationModes(t *testing.T) {
	err := run(context.Background(), []string{"-ontology-initialize", "-ontology-initialize-apply"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRequiresOperatorForOntologyInitializationApply(t *testing.T) {
	err := run(context.Background(), []string{"-ontology-initialize-apply", "-ontology-initialize-changed-by", ""}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "operator") {
		t.Fatalf("error = %v", err)
	}
}

func TestOntologyInitializationInventoryCountsDefinitionsAndGraph(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "ontology-initialization-inventory")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.ApplyEvent(ctx, bootstrapOntologyEvent("org-inventory", "inventory-agent", "people.agent.updated", "inventory-agent", map[string]any{
		"id": "inventory-agent", "display_name": "Inventory Agent", "runtime_kind": "pi",
	})); err != nil {
		t.Fatal(err)
	}
	inventory, err := readOntologyInitializationInventory(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Definitions.ExpectedObjectTypes == 0 || inventory.Definitions.MissingObjectTypes != 0 {
		t.Fatalf("definition inventory = %+v", inventory.Definitions)
	}
	if inventory.Objects["agent"] != 1 {
		t.Fatalf("object inventory = %+v", inventory.Objects)
	}
}

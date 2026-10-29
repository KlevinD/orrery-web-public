package integration_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	discussionsession "orrery_v2/internal/app/discussionsession"
	operations "orrery_v2/internal/app/operations"
	scheduler "orrery_v2/internal/app/scheduler"
	todo "orrery_v2/internal/app/todo"
	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memorycore "orrery_v2/internal/memory/core"
	memorystore "orrery_v2/internal/memory/store"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/db"
	doccore "orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/uow"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestPostgresBusinessPersistenceSmoke(t *testing.T) {
	ctx := context.Background()
	store := openPostgresBusinessStore(t)
	defer store.Close()
	router := db.NewRouter(openPostgresBusinessRegistry(t, store))
	unit := uow.New(store)
	prefix := postgresSmokePrefix(t)

	t.Run("agent profile repository", func(t *testing.T) {
		repo := agentcore.NewPlatformProfileRepository(unit)
		profile := postgresSmokeAgentProfile(prefix + "-profile")
		if err := repo.SaveProfile(ctx, profile); err != nil {
			t.Fatalf("SaveProfile: %v", err)
		}
		if err := repo.SaveAvailability(ctx, agentcore.AgentAvailability{
			AgentProfileID:  profile.ID,
			AgentInstanceID: agentcore.AgentInstanceID(profile.ID),
			Status:          agentcore.AvailabilityAvailable,
			Capacity:        3,
			CurrentLoad:     1,
			UpdatedAt:       postgresSmokeNow(),
		}); err != nil {
			t.Fatalf("SaveAvailability: %v", err)
		}
		if err := repo.SaveCostProfile(ctx, agentcore.AgentCostProfile{AgentProfileID: profile.ID, EstimatedCostPerHour: 11}); err != nil {
			t.Fatalf("SaveCostProfile: %v", err)
		}
		if err := repo.SaveQualitySignal(ctx, agentcore.AgentQualitySignal{AgentProfileID: profile.ID, Score: 0.91, SampleSize: 7}); err != nil {
			t.Fatalf("SaveQualitySignal: %v", err)
		}
		if err := repo.SaveRiskProfile(ctx, agentcore.AgentRiskProfile{AgentProfileID: profile.ID, RiskLevel: agentcore.RiskLevelLow}); err != nil {
			t.Fatalf("SaveRiskProfile: %v", err)
		}
		page, err := repo.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{IncludeAI: true, EnabledOnly: true})
		if err != nil {
			t.Fatalf("QueryDirectory: %v", err)
		}
		if !directoryContains(page.Entries, profile.ID) {
			t.Fatalf("directory entries do not contain %s: %+v", profile.ID, page.Entries)
		}
		if err := repo.DeleteProfile(ctx, profile.ID, "postgres smoke cleanup"); err != nil {
			t.Fatalf("DeleteProfile: %v", err)
		}
		if deleted, err := repo.IsProfileDeleted(ctx, profile.ID); err != nil || !deleted {
			t.Fatalf("IsProfileDeleted deleted=%v err=%v", deleted, err)
		}
	})

	t.Run("mission task repository", func(t *testing.T) {
		repo := missiontask.NewPlatformRepository()
		record := postgresSmokeTaskRecord(prefix + "-task")
		submission := missiontask.TaskSubmission{
			ID:             missiontask.TaskSubmissionID(prefix + "-submission"),
			TaskID:         record.Contract.ID,
			AgentID:        agentcore.AgentID(prefix + "-agent"),
			RunID:          agentcore.AgentRunID(prefix + "-run"),
			AgentResultRef: agentcore.AgentResultID(prefix + "-result"),
			Summary:        agentcore.Summary{Text: "submitted"},
			SubmittedAt:    postgresSmokeNow(),
			IdempotencyKey: prefix + "-submission-key",
		}
		if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
			if err := repo.SaveTask(ctx, tx, record); err != nil {
				return err
			}
			return repo.SaveSubmission(ctx, tx, submission, nil, nil)
		}); err != nil {
			t.Fatalf("save task records: %v", err)
		}
		if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
			got, err := repo.GetTask(ctx, tx, record.Contract.ID)
			if err != nil {
				return err
			}
			if got.Contract.ID != record.Contract.ID || got.Status != missiontask.TaskStatusInProgress {
				return fmt.Errorf("loaded task mismatch: %+v", got)
			}
			gotSubmission, ok, err := repo.FindSubmissionByIdempotencyKey(ctx, tx, record.Contract.ID, submission.IdempotencyKey)
			if err != nil {
				return err
			}
			if !ok || gotSubmission.ID != submission.ID {
				return fmt.Errorf("submission by key = %+v ok=%v", gotSubmission, ok)
			}
			return repo.DeleteTask(ctx, tx, record.Contract.ID)
		}); err != nil {
			t.Fatalf("load/delete task records: %v", err)
		}
		if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
			if _, err := repo.GetTask(ctx, tx, record.Contract.ID); err == nil {
				return fmt.Errorf("task still exists after delete")
			}
			return nil
		}); err != nil {
			t.Fatalf("verify task delete: %v", err)
		}
	})

	t.Run("todo scheduler discussion repositories", func(t *testing.T) {
		todos := todo.NewRepository(router)
		todoID := prefix + "-todo"
		if err := todos.SaveTodo(ctx, todo.TodoItem{TodoID: todoID, Title: "Postgres todo", Status: todo.TodoStatusInbox, Priority: todo.TodoPriorityMedium, CreatedBy: "user-1", CreatedAt: postgresSmokeNow(), UpdatedAt: postgresSmokeNow()}); err != nil {
			t.Fatalf("SaveTodo: %v", err)
		}
		if loaded, err := todos.GetTodo(ctx, todoID); err != nil || loaded.Title != "Postgres todo" {
			t.Fatalf("GetTodo loaded=%+v err=%v", loaded, err)
		}

		schedules := scheduler.NewRepository(router)
		scheduledTaskID := prefix + "-schedule"
		nextRunAt := postgresSmokeNow().Add(time.Hour)
		if err := schedules.SaveTask(ctx, scheduler.ScheduledTask{
			ScheduledTaskID: scheduledTaskID,
			Title:           "Postgres scheduled task",
			Schedule:        scheduler.Schedule{Type: scheduler.ScheduleTypeOnce, RunAt: nextRunAt},
			Target:          scheduler.Target{Mode: scheduler.TargetModeDiscussionWorkflow, Message: "Run postgres smoke."},
			Policy:          scheduler.Policy{Misfire: scheduler.MisfireRunOnce, Concurrency: scheduler.ConcurrencySkipIfRunning},
			Status:          scheduler.TaskStatusEnabled,
			Enabled:         true,
			NextRunAt:       nextRunAt,
			CreatedBy:       "user-1",
			CreatedByKind:   scheduler.ViewerKindHuman,
			CreatedAt:       postgresSmokeNow(),
			UpdatedAt:       postgresSmokeNow(),
		}); err != nil {
			t.Fatalf("SaveTask: %v", err)
		}
		due, err := schedules.ListDueTasks(ctx, nextRunAt, 5)
		if err != nil {
			t.Fatalf("ListDueTasks: %v", err)
		}
		if !scheduledTasksContain(due, scheduledTaskID) {
			t.Fatalf("due tasks do not contain %s: %+v", scheduledTaskID, due)
		}

		discussions := discussionsession.NewRepository(router)
		conversationID := prefix + "-conversation"
		if err := discussions.SaveConversation(ctx, discussionsession.ConversationRecord{ConversationID: conversationID, Title: "Postgres discussion", Status: "running", CreatedBy: "user-1", CreatedAt: postgresSmokeNow(), UpdatedAt: postgresSmokeNow()}); err != nil {
			t.Fatalf("SaveConversation: %v", err)
		}
		if err := discussions.SaveMessage(ctx, discussionsession.MessageRecord{MessageID: prefix + "-message", ConversationID: conversationID, Role: "user", AuthorID: "user-1", Content: "hello pg", CreatedAt: postgresSmokeNow()}); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
		view, err := discussions.GetConversation(ctx, conversationID)
		if err != nil {
			t.Fatalf("GetConversation: %v", err)
		}
		if view.ConversationID != conversationID || len(view.Messages) != 1 || view.Messages[0].Content != "hello pg" {
			t.Fatalf("conversation view mismatch: %+v", view)
		}
	})

	t.Run("resource operations knowledge memory repositories", func(t *testing.T) {
		tools := toolres.NewPlatformRegistry(store)
		toolID := agentcore.ToolID(prefix + ".tool")
		if err := tools.Register(ctx, toolres.ToolDefinition{
			ID:        toolID,
			Kind:      toolres.KindMCPServer,
			Name:      "Postgres Tool",
			Version:   "1.0.0",
			Enabled:   true,
			RiskLevel: toolres.RiskMedium,
			MCP:       &toolres.MCPServerDefinition{ServerName: "postgres-smoke", Transport: toolres.TransportStdio, Command: "postgres-smoke"},
			Tags:      []string{"postgres-smoke"},
		}); err != nil {
			t.Fatalf("Register tool: %v", err)
		}
		if listed, err := tools.ListByTag(ctx, "postgres-smoke"); err != nil || !toolDefinitionsContain(listed, toolID) {
			t.Fatalf("ListByTag tools=%+v err=%v", listed, err)
		}

		skills := skillres.NewPlatformRegistry(store)
		skillID := agentcore.SkillID(prefix + ".skill")
		if err := skills.Register(ctx, skillres.SkillDefinition{
			ID:           skillID,
			Name:         "Postgres Skill",
			Version:      "1.0.0",
			Description:  "Smoke skill",
			TaskTypeTags: []string{"postgres-smoke"},
			PromptAssets: []skillres.PromptAsset{{Path: "SKILL.md", ContentRef: "artifact://postgres-smoke/SKILL.md", Kind: skillres.PromptAssetMarkdown}},
			Enabled:      true,
		}); err != nil {
			t.Fatalf("Register skill: %v", err)
		}
		if listed, err := skills.ListByTag(ctx, "postgres-smoke"); err != nil || !skillDefinitionsContain(listed, skillID) {
			t.Fatalf("ListByTag skills=%+v err=%v", listed, err)
		}

		orgs := operations.NewOrganizationCatalogStore(router)
		orgID := prefix + "-org"
		if err := orgs.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: orgID, Name: "Postgres Org", Status: "active", Code: "01"}); err != nil {
			t.Fatalf("SaveOrganization: %v", err)
		}
		graph, err := orgs.ListOrganizations(ctx, operations.OrgGraphQuery{OrganizationID: orgID})
		if err != nil {
			t.Fatalf("ListOrganizations: %v", err)
		}
		if len(graph.Organizations) != 1 || graph.Organizations[0].OrganizationID != orgID {
			t.Fatalf("org graph mismatch: %+v", graph)
		}

		knowledge := knowledgestore.NewPlatformStore(store)
		baseID := knowledgecore.KnowledgeBaseID(prefix + "-kb")
		base := knowledgecore.KnowledgeBase{
			ID:            baseID,
			Name:          "Postgres Knowledge",
			OwnerType:     knowledgecore.OwnerAgent,
			OwnerID:       prefix + "-agent",
			Visibility:    knowledgecore.VisibilityPrivate,
			VectorBackend: knowledgecore.VectorBackendLanceDB,
			ChunkPolicy:   doccore.ChunkPolicy{MaxRunes: 1200, OverlapRunes: 120, MinRunes: 100},
			Status:        knowledgecore.KnowledgeActive,
			CreatedAt:     postgresSmokeNow(),
			UpdatedAt:     postgresSmokeNow(),
		}
		if err := knowledge.SaveKnowledgeBase(ctx, base); err != nil {
			t.Fatalf("SaveKnowledgeBase: %v", err)
		}
		if page, err := knowledge.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{OwnerType: knowledgecore.OwnerAgent, OwnerID: base.OwnerID}); err != nil || !knowledgeBasesContain(page.Items, baseID) {
			t.Fatalf("ListKnowledgeBases page=%+v err=%v", page, err)
		}

		memories := memorystore.NewPlatformStore(store)
		profileID := agentcore.MemoryProfileID(prefix + "-memory-profile")
		if err := memories.SaveProfile(ctx, memorycore.MemoryProfile{
			ID:               profileID,
			OwnerRef:         memorycore.OwnerRef{AgentID: agentcore.AgentID(prefix + "-agent")},
			MemoryKind:       agentcore.MemoryKindPersonal,
			VisibilityPolicy: memorycore.VisibilityPolicy{Mode: memorycore.VisibilityOwnerOnly},
			Enabled:          true,
		}); err != nil {
			t.Fatalf("SaveProfile memory: %v", err)
		}
		candidateID := prefix + "-memory-candidate"
		if err := memories.SaveCandidate(ctx, memorycore.MemoryCandidate{ID: candidateID, Status: memorycore.CandidateAccepted}); err != nil {
			t.Fatalf("SaveCandidate: %v", err)
		}
		recordID := prefix + "-memory-record"
		if err := memories.SaveRecord(ctx, memorycore.MemoryRecord{
			ID:              recordID,
			ProfileID:       profileID,
			CandidateID:     candidateID,
			Scope:           memorycore.MemoryScope{Kind: memorycore.ScopeMission, ID: prefix + "-mission"},
			Layer:           memorycore.MemoryLayerFact,
			Topic:           "postgres",
			Summary:         "Postgres smoke persisted memory.",
			Facts:           []string{"Postgres memory works."},
			LifecycleStatus: memorycore.MemoryLifecycleActive,
			Visibility:      memorycore.Visibility{Mode: memorycore.VisibilityOwnerOnly},
			CreatedAt:       postgresSmokeNow(),
			UpdatedAt:       postgresSmokeNow(),
		}); err != nil {
			t.Fatalf("SaveRecord memory: %v", err)
		}
		records, err := memories.FindRecords(ctx, memorystore.RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{profileID}, Topics: []string{"postgres"}})
		if err != nil {
			t.Fatalf("FindRecords: %v", err)
		}
		if len(records) != 1 || records[0].ID != recordID {
			t.Fatalf("memory records mismatch: %+v", records)
		}
	})
}

func openPostgresBusinessStore(t *testing.T) *db.Store {
	t.Helper()
	raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverPostgres, URL: raw, Name: "postgres-business-smoke"})
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	return store
}

func openPostgresBusinessRegistry(t *testing.T, store *db.Store) *db.Registry {
	t.Helper()
	registry, err := db.OpenRegistry(context.Background(), db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverPostgres, URL: os.Getenv("ORRERY_TEST_POSTGRES_URL"), Name: "postgres-business-smoke-router"},
	}})
	if err != nil {
		t.Fatalf("open postgres business registry: %v", err)
	}
	t.Cleanup(func() {
		_ = registry.Close()
	})
	if store.Dialect() != db.DialectPostgres {
		t.Fatalf("business smoke requires postgres store, got %s", store.Dialect())
	}
	return registry
}

func postgresSmokePrefix(t *testing.T) string {
	t.Helper()
	name := strings.NewReplacer("/", "-", "\\", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	return fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
}

func postgresSmokeNow() time.Time {
	return time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
}

func postgresSmokeAgentProfile(id string) agentcore.AgentProfile {
	profileID := agentcore.AgentProfileID(id)
	return agentcore.AgentProfile{
		ID:          profileID,
		RealName:    "秦若兰",
		DisplayName: "秦若兰",
		RoleName:    "Postgres Smoke Agent",
		AgentKind:   agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: "postgres-org"},
			DepartmentRef:     agentcore.DepartmentRef{ID: "postgres-dept"},
			PositionRef:       agentcore.PositionRef{ID: "postgres-position"},
			RoleRefs:          []agentcore.RoleRef{{ID: "worker"}},
			ManagerAgentRef:   agentcore.AgentProfileRef{ID: "postgres-manager"},
			EmploymentType:    agentcore.EmploymentTypeAIAgent,
			SupervisionPolicy: agentcore.SupervisionPolicyManaged,
		},
		Persona: agentcore.AgentPersona{ResponsibilityBoundary: "负责验证 PostgreSQL 下的业务持久化路径。"},
		BaseAgent: agentcore.BaseAgentSpec{
			InheritsBaseAgent: true,
			AuditRequired:     true,
			SystemPrompt:      "你是 PostgreSQL 业务烟测 Agent。",
			LLMAPIKeyRef:      "secret://system/llm-api-key",
			SkillScope:        "profile_bound",
			ToolScope:         "profile_bound",
			MCPScope:          "profile_bound",
		},
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
			MemoryProfileID: agentcore.MemoryProfileID(id + "-memory"),
			OwnerAgentID:    agentcore.AgentID(profileID),
			MemoryKind:      agentcore.MemoryKindPersonal,
		},
		SkillRefs:   []agentcore.SkillRef{{SkillID: "postgres-smoke"}},
		ToolRefs:    []agentcore.ToolRef{{ToolID: "postgres-smoke"}},
		RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex, ConfigRef: "runtime-config"},
		Enabled:     true,
	}
}

func postgresSmokeTaskRecord(id string) missiontask.TaskRecord {
	taskID := missiontask.TaskID(id)
	return missiontask.TaskRecord{
		Contract: missiontask.TaskContract{
			ID:                 taskID,
			MissionID:          missioncore.MissionID(id + "-mission"),
			Assignee:           missiontask.TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: agentcore.AgentProfileID(id + "-agent")}},
			Title:              "Postgres task",
			Goal:               "Verify mission task persistence on PostgreSQL.",
			WorkerPersonalGoal: "Persist task data.",
			WorkerPersonalGoalSources: []missiontask.WorkerPersonalGoalSourceRef{{
				Kind:             missiontask.WorkerPersonalGoalSourceGoal,
				ContractFieldRef: missiontask.ContractFieldRef{Field: missiontask.ContractFieldGoal},
			}},
			ExpectedDeliverables: []missiontask.ExpectedDeliverable{{ID: "deliverable-1", Title: "Smoke result"}},
			ContextPolicy: missiontask.ContextPolicy{
				RequireMissionGoal:        true,
				RequireDiscussionGoal:     true,
				RequireWorkerPersonalGoal: true,
			},
		},
		Status:          missiontask.TaskStatusInProgress,
		Version:         1,
		StatusChangedAt: postgresSmokeNow(),
	}
}

func directoryContains(entries []agentcore.AgentDirectoryEntry, id agentcore.AgentProfileID) bool {
	for _, entry := range entries {
		if entry.AgentProfileID == id {
			return true
		}
	}
	return false
}

func scheduledTasksContain(tasks []scheduler.ScheduledTask, id string) bool {
	for _, task := range tasks {
		if task.ScheduledTaskID == id {
			return true
		}
	}
	return false
}

func toolDefinitionsContain(definitions []toolres.ToolDefinition, id agentcore.ToolID) bool {
	for _, definition := range definitions {
		if definition.ID == id {
			return true
		}
	}
	return false
}

func skillDefinitionsContain(definitions []skillres.SkillDefinition, id agentcore.SkillID) bool {
	for _, definition := range definitions {
		if definition.ID == id {
			return true
		}
	}
	return false
}

func knowledgeBasesContain(bases []knowledgecore.KnowledgeBase, id knowledgecore.KnowledgeBaseID) bool {
	for _, base := range bases {
		if base.ID == id {
			return true
		}
	}
	return false
}

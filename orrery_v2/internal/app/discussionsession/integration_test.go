package discussionsession

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/rebac"
	missioncore "orrery_v2/internal/mission/core"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform/testkit"
)

func TestDiscussionSessionToolCallsCreateRealMissionNativeRecords(t *testing.T) {
	for _, preparation := range []string{"mission_brief_create", "mission_prepare"} {
		t.Run(preparation, func(t *testing.T) {
			ctx := context.Background()
			env := testkit.New(t)
			defer env.Close()
			profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
			seedLeadIntegrationProfileWithName(t, ctx, profiles, agentcore.AgentProfileID("lead-agent"), agentcore.AgentInstanceID("lead-agent"), "顾明澈", "Lead")
			seedLeadIntegrationProfileWithName(t, ctx, profiles, agentcore.AgentProfileID("staffing-agent"), agentcore.AgentInstanceID("staffing-agent"), "秦若兰", "Staffing")
			seedLeadIntegrationProfile(t, ctx, profiles, agentcore.AgentProfileID("profile-worker"), agentcore.AgentInstanceID("profile-worker"))
			seedLeadIntegrationProfile(t, ctx, profiles, agentcore.AgentProfileID("profile-worker-2"), agentcore.AgentInstanceID("profile-worker-2"))
			missionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
			authzAdapter := authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil)
			authzService := authorization.NewService(authzAdapter)
			authzRelations := authorization.NewRelationProjector(authzAdapter)
			staffing := missionteam.NewService(missionteam.ServiceOptions{
				UnitOfWork:  env.UnitOfWork,
				ProfileRepo: profiles,
				Provisioner: agentcore.NewStaticInstanceProvisioner(map[agentcore.AgentProfileID]agentcore.AgentInstanceRef{
					"profile-worker":   {ID: "profile-worker"},
					"profile-worker-2": {ID: "profile-worker-2"},
				}),
				Clock:       env.Clock,
				IDGenerator: env.IDGenerator,
				Logger:      env.Logger,
			})
			lifecycle := missioncontrol.NewLifecycleService(env.UnitOfWork, env.DBRouter, missionRepo, missioncontrol.LifecycleOptions{
				Clock:                  env.Clock,
				IDGenerator:            env.IDGenerator,
				AgentDirectory:         profiles,
				TeamBootstrap:          staffing,
				HumanProfiles:          &integrationHumanProfiles{profiles: profiles},
				HumanBootstrap:         missioncontrol.HumanAgentBootstrap{Enabled: true},
				AuthorizationService:   authzService,
				AuthorizationRelations: authzRelations,
			})
			runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
				AssistantMessage: "I created the Mission and queued coordinator worker lanes.",
				Status:           "completed",
				RuntimeKind:      "openclaw",
				ToolCalls: []ToolCallDTO{
					{
						ToolCallID: "tool-mission",
						Name:       "mission_brief_create",
						Status:     "completed",
						Arguments: map[string]string{
							"title":              "Launch plan",
							"final_goal":         "Coordinate v2 launch",
							"business_direction": "Growth",
							"idempotency_key":    "mission-from-agent",
						},
					},
					{
						ToolCallID: "tool-root",
						Name:       "mission_task_create_root",
						Status:     "completed",
						Arguments: map[string]string{
							"assignee_id":           "profile-worker",
							"goal":                  "Coordinate v2 launch execution",
							"worker_personal_goal":  "Own launch coordination",
							"expected_deliverables": "Launch brief\nRollout checklist",
							"idempotency_key":       "root-task-from-agent",
						},
					},
					{
						ToolCallID: "tool-coordinator",
						Name:       "mission_coordinator_plan_parallel",
						Status:     "completed",
						Arguments: map[string]string{
							"goal":              "Split follow-through to the staffed worker",
							"worker_lanes_json": `[{"role":"launch worker","agent_id":"profile-worker-2","goal":"Coordinate v2 launch execution","scope":"Own launch follow-through","idempotency_key":"lane-from-agent"}]`,
							"idempotency_key":   "coordinator-from-agent",
						},
					},
				},
			}}
			if preparation == "mission_prepare" {
				prepare := runner.response.ToolCalls[0]
				prepare.Name = "mission_prepare"
				for key, value := range runner.response.ToolCalls[1].Arguments {
					if key != "idempotency_key" {
						prepare.Arguments[key] = value
					}
				}
				runner.response.ToolCalls = []ToolCallDTO{prepare, runner.response.ToolCalls[2]}
			}
			service := NewService(ServiceOptions{
				Repository:      NewRepository(env.DBRouter),
				DiscussionAgent: runner,
				InitialAgentMembers: []InitialAgentMemberSeed{
					{AgentID: "lead-agent", Role: "Lead"},
					{AgentID: "staffing-agent", Role: "Staffing"},
				},
				MissionTooling: NewMissionToolApplier(lifecycle),
				AgentDirectory: profiles,
				HumanProfiles:  &integrationHumanProfiles{profiles: profiles},
				Clock:          env.Clock,
				IDGenerator:    env.IDGenerator,
			})

			conversation, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{
				Content:        "Plan a v2 launch",
				IdempotencyKey: "send-1",
			})
			if err != nil {
				t.Fatal(err)
			}

			if conversation.ConversationID == "" || len(conversation.Messages) != 2 || len(conversation.LinkedMissions) != 1 {
				t.Fatalf("conversation = %+v", conversation)
			}
			missionID := conversation.LinkedMissions[0].MissionID
			if missionID == "" || conversation.LinkedMissions[0].Title != "Launch plan" {
				t.Fatalf("linked missions = %+v", conversation.LinkedMissions)
			}
			persisted, err := service.GetConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, conversation.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			if len(persisted.Messages) != 2 || len(persisted.LinkedMissions) != 1 || persisted.LinkedMissions[0].MissionID != missionID {
				t.Fatalf("persisted conversation = %+v", persisted)
			}
			page, err := service.ListConversations(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, ListConversationsQuery{})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Conversations) != 1 || page.Conversations[0].ConversationID != conversation.ConversationID || page.Conversations[0].LinkedMissions != 1 {
				t.Fatalf("conversation page = %+v", page)
			}
			query := missioncontrol.NewQueryService(missionRepo)
			query.SetAuthorizationService(authzService)
			view, err := query.GetMissionControlView(ctx, missioncontrol.ViewerRef{ViewerID: "lead-agent", ViewerKind: missioncontrol.ViewerKindAgent, PermissionScope: []string{"mission.read"}}, missioncore.MissionID(missionID))
			if err != nil {
				t.Fatal(err)
			}
			if view.Mission.Title != "Launch plan" || len(view.TaskBoard.Tasks) != 2 {
				t.Fatalf("mission view = %+v", view)
			}
			humanAgentID := "human-user-1"
			foundHuman := false
			for _, member := range view.Team.Members {
				if member.AgentID == humanAgentID && member.AgentInstanceID == humanAgentID && member.MissionRole == string(missionteam.MissionRoleRealHumanContributor) {
					foundHuman = true
					break
				}
			}
			if !foundHuman {
				t.Fatalf("initial human agent was not team-managed: %+v", view.Team.Members)
			}
			taskByAssignee := map[string]missioncontrol.TaskCardDTO{}
			for _, task := range view.TaskBoard.Tasks {
				taskByAssignee[task.Assignee] = task
			}
			if taskByAssignee["profile-worker"].Goal != "Coordinate v2 launch execution" {
				t.Fatalf("root task missing from task board = %+v", view.TaskBoard)
			}
			if taskByAssignee["profile-worker-2"].Goal != "launch worker: Coordinate v2 launch execution" || taskByAssignee["profile-worker-2"].Status != "assigned" {
				t.Fatalf("task board = %+v", view.TaskBoard)
			}
			team, err := staffing.ListTeam(ctx, missioncore.MissionID(missionID))
			if err != nil {
				t.Fatal(err)
			}
			if len(team.Members) != 1 {
				t.Fatalf("team = %+v", team)
			}
			teamRoles := map[agentcore.AgentProfileID]missionteam.MissionRole{}
			for _, member := range team.Members {
				if member.Status != missionteam.MemberStatusActive || len(member.AssignmentRefs) != 1 {
					t.Fatalf("team member = %+v", member)
				}
				teamRoles[member.AgentProfileID] = member.AssignmentRefs[0].Role
			}
			if teamRoles["human-user-1"] != missionteam.MissionRoleRealHumanContributor {
				t.Fatalf("team roles = %+v team=%+v", teamRoles, team)
			}
		})
	}
}

type integrationHumanProfiles struct {
	profiles *agentcore.PlatformProfileRepository
}

func (p *integrationHumanProfiles) EnsureHumanProfile(ctx context.Context, cmd missioncontrol.EnsureHumanProfileCommand) (agentcore.AgentProfile, error) {
	return p.ensureProfile(ctx, cmd.HumanAgentID, cmd.DisplayName)
}

func (p *integrationHumanProfiles) EnsureDiscussionHumanProfile(ctx context.Context, viewer ViewerRef) (agentcore.AgentProfile, error) {
	return p.ensureProfile(ctx, viewer.ViewerID, viewer.DisplayName)
}

func (p *integrationHumanProfiles) ensureProfile(ctx context.Context, humanID string, displayName string) (agentcore.AgentProfile, error) {
	id := agentcore.AgentProfileID("human-" + humanID)
	name := firstNonBlank(displayName, "本地用户")
	profile := agentcore.AgentProfile{
		ID:          id,
		RealName:    name,
		DisplayName: name,
		RoleName:    "Real human contributor",
		AgentKind:   agentcore.AgentKindHuman,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: "orrery"},
			DepartmentRef:     agentcore.DepartmentRef{ID: "human"},
			PositionRef:       agentcore.PositionRef{ID: "contributor"},
			RoleRefs:          []agentcore.RoleRef{{ID: "realhumancontributor"}},
			AuthorityScope:    "mission_collaboration",
			EmploymentType:    agentcore.EmploymentTypeHuman,
			SupervisionPolicy: agentcore.SupervisionPolicyRoot,
		},
		Persona:                  agentcore.AgentPersona{ResponsibilityBoundary: "Provide original human intent and collaboration context."},
		BaseAgent:                integrationBaseAgentSpec(name, "Real human contributor"),
		SkillRefs:                []agentcore.SkillRef{},
		ToolRefs:                 []agentcore.ToolRef{},
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{MemoryProfileID: agentcore.MemoryProfileID("memory-" + id.String()), OwnerAgentID: agentcore.AgentID(humanID), MemoryKind: agentcore.MemoryKindPersonal},
		RuntimeSpec:              agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindHuman},
		Enabled:                  true,
	}
	if err := p.profiles.SaveProfile(ctx, profile); err != nil {
		return agentcore.AgentProfile{}, err
	}
	if err := p.profiles.SaveAvailability(ctx, agentcore.AgentAvailability{AgentProfileID: id, AgentInstanceID: agentcore.AgentInstanceID(id), Status: agentcore.AvailabilityAvailable, Capacity: 1, UpdatedAt: time.Now().UTC()}); err != nil {
		return agentcore.AgentProfile{}, err
	}
	return profile, nil
}

func integrationBaseAgentSpec(name, roleName string) agentcore.BaseAgentSpec {
	return agentcore.BaseAgentSpec{
		InheritsBaseAgent: true,
		AuditRequired:     true,
		SystemPrompt:      "全局 BaseAgent 模板：所有 Orrery Agent 必须经同一审计壳运行。Agent name: " + name + ". Agent role: " + roleName + ". Memory 占位：TODO(memory-team)。Knowledge 占位：TODO(knowledge-team)。",
		LLMAPIKeyRef:      "secret://system/llm-api-key",
		SkillScope:        "profile_bound",
		ToolScope:         "profile_bound",
		MCPScope:          "profile_bound",
	}
}

func seedLeadIntegrationProfile(t *testing.T, ctx context.Context, repo *agentcore.PlatformProfileRepository, profileID agentcore.AgentProfileID, instanceID agentcore.AgentInstanceID) {
	t.Helper()
	seedLeadIntegrationProfileWithName(t, ctx, repo, profileID, instanceID, "协作员", "Worker")
}

func seedLeadIntegrationProfileWithName(t *testing.T, ctx context.Context, repo *agentcore.PlatformProfileRepository, profileID agentcore.AgentProfileID, instanceID agentcore.AgentInstanceID, name string, role string) {
	t.Helper()
	profile := agentcore.AgentProfile{
		ID:          profileID,
		RealName:    name,
		DisplayName: name,
		RoleName:    role,
		AgentKind:   agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: "orrery"},
			DepartmentRef:     agentcore.DepartmentRef{ID: "engineering"},
			PositionRef:       agentcore.PositionRef{ID: "backend"},
			RoleRefs:          []agentcore.RoleRef{{ID: "worker"}},
			ManagerAgentRef:   agentcore.AgentProfileRef{ID: "profile-manager-001"},
			AuthorityScope:    "repo:write",
			EmploymentType:    agentcore.EmploymentTypeAIAgent,
			SupervisionPolicy: agentcore.SupervisionPolicyRoot,
		},
		Persona:                  agentcore.AgentPersona{ResponsibilityBoundary: "负责执行 Mission 任务、提交交付物和证据。"},
		BaseAgent:                integrationManagedBaseAgentSpec(),
		SkillRefs:                []agentcore.SkillRef{{SkillID: "launch"}},
		ToolRefs:                 []agentcore.ToolRef{{ToolID: "git"}},
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{MemoryProfileID: agentcore.MemoryProfileID("memory-" + profileID.String()), MemoryKind: agentcore.MemoryKindPersonal},
		RuntimeSpec:              agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex},
		Enabled:                  true,
	}
	if err := repo.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	now := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	if err := repo.SaveAvailability(ctx, agentcore.AgentAvailability{AgentProfileID: profileID, AgentInstanceID: instanceID, Status: agentcore.AvailabilityAvailable, AvailableFrom: now.Add(-time.Hour), AvailableUntil: now.Add(8 * time.Hour), Capacity: 4, CurrentLoad: 1, UpdatedAt: now}); err != nil {
		t.Fatalf("save availability: %v", err)
	}
	if err := repo.SaveCostProfile(ctx, agentcore.AgentCostProfile{AgentProfileID: profileID, EstimatedCostPerHour: 10, EstimatedCostPerTask: 25, BudgetClass: "standard", Source: "test"}); err != nil {
		t.Fatalf("save cost profile: %v", err)
	}
	if err := repo.SaveQualitySignal(ctx, agentcore.AgentQualitySignal{AgentProfileID: profileID, Score: 0.95, SampleSize: 20, LastSuccessAt: now}); err != nil {
		t.Fatalf("save quality signal: %v", err)
	}
	if err := repo.SaveRiskProfile(ctx, agentcore.AgentRiskProfile{AgentProfileID: profileID, RiskLevel: agentcore.RiskLevelLow, Mitigations: []string{"mission scoped permissions"}}); err != nil {
		t.Fatalf("save risk profile: %v", err)
	}
}

func integrationManagedBaseAgentSpec() agentcore.BaseAgentSpec {
	return agentcore.BaseAgentSpec{
		InheritsBaseAgent: true,
		AuditRequired:     true,
		SystemPrompt:      "你是测试用托管 Agent，必须经 BaseAgent 审计壳执行。",
		LLMAPIKeyRef:      "secret://system/llm-api-key",
		SkillScope:        "profile_bound",
		ToolScope:         "profile_bound",
		MCPScope:          "profile_bound",
	}
}

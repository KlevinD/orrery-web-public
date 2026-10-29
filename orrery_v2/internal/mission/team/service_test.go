package team

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/testkit"
)

func newServiceFixture(t *testing.T, env *testkit.Env) *Service {
	t.Helper()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	return NewService(ServiceOptions{
		UnitOfWork:  env.UnitOfWork,
		Repository:  NewPlatformTeamRepository(),
		ProfileRepo: profiles,
		Provisioner: agentcore.NewStaticInstanceProvisioner(nil),
		EventSink:   NewStaffingEventSink(),
		Clock:       env.Clock,
		IDGenerator: env.IDGenerator,
		Logger:      env.Logger,
		Metrics:     env.MetricRecorder,
		Tracer:      env.Tracer,
	})
}

func seedProfile(t *testing.T, ctx context.Context, repo *agentcore.PlatformProfileRepository, profile agentcore.AgentProfile) {
	t.Helper()
	if err := repo.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	now := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	if err := repo.SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: agentcore.AgentInstanceID(profile.ID.String() + ":fixture"),
		Status:          agentcore.AvailabilityAvailable,
		AvailableFrom:   now.Add(-time.Hour),
		AvailableUntil:  now.Add(8 * time.Hour),
		Capacity:        4,
		CurrentLoad:     1,
		Source:          "test",
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("save availability: %v", err)
	}
	if err := repo.SaveCostProfile(ctx, agentcore.AgentCostProfile{AgentProfileID: profile.ID, EstimatedCostPerHour: 10, EstimatedCostPerTask: 25, BudgetClass: "standard", Source: "test"}); err != nil {
		t.Fatalf("save cost profile: %v", err)
	}
	if err := repo.SaveQualitySignal(ctx, agentcore.AgentQualitySignal{AgentProfileID: profile.ID, Score: 0.95, SampleSize: 20, LastSuccessAt: now}); err != nil {
		t.Fatalf("save quality signal: %v", err)
	}
	if err := repo.SaveRiskProfile(ctx, agentcore.AgentRiskProfile{AgentProfileID: profile.ID, RiskLevel: agentcore.RiskLevelLow, Mitigations: []string{"mission scoped permissions"}}); err != nil {
		t.Fatalf("save risk profile: %v", err)
	}
}

func serviceProfile(id agentcore.AgentProfileID, kind agentcore.AgentKind) agentcore.AgentProfile {
	return agentcore.AgentProfile{
		ID:          id,
		RealName:    "艾达",
		DisplayName: "艾达",
		RoleName:    "Worker",
		AgentKind:   kind,
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
		BaseAgent:                serviceBaseAgentSpec(kind),
		SkillRefs:                []agentcore.SkillRef{{SkillID: "go"}},
		ToolRefs:                 []agentcore.ToolRef{{ToolID: "git"}},
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{MemoryProfileID: agentcore.MemoryProfileID("memory-" + id.String()), MemoryKind: agentcore.MemoryKindPersonal},
		RuntimeSpec:              agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex},
		Enabled:                  true,
	}
}

func serviceBaseAgentSpec(kind agentcore.AgentKind) agentcore.BaseAgentSpec {
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

func assertEventTypes(t *testing.T, events []eventstore.Envelope, want []missionevent.EventType) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %#v", len(events), len(want), events)
	}
	for i := range want {
		if events[i].EventType != string(want[i]) {
			t.Fatalf("event[%d] = %s, want %s", i, events[i].EventType, want[i])
		}
	}
}

func TestServiceEmitsStaffingObservability(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-ada", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)

	if _, err := service.Appoint(ctx, AppointmentCommand{IDempotencyKey: "appoint-001", MissionID: "mission-001", ProfileID: profile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}}); err != nil {
		t.Fatalf("appoint: %v", err)
	}
	metrics, err := env.MetricRecorder.Drain(10)
	if err != nil {
		t.Fatalf("drain metrics: %v", err)
	}
	var sawMetric bool
	for _, event := range metrics {
		if event.Name == "mission_team.staffing_mutation" && event.Fields["operation"] == "appoint" && event.Fields["status"] == "success" {
			sawMetric = true
		}
	}
	if !sawMetric {
		t.Fatalf("staffing mutation metric missing: %#v", metrics)
	}
	var sawSpan bool
	for _, span := range env.Traces.Spans() {
		if span.Name == "mission_team.appoint" && span.Error == "" {
			sawSpan = true
		}
	}
	if !sawSpan {
		t.Fatalf("appoint span missing")
	}
	if got := env.LogBuffer.String(); !containsAll(got, "mission team staffing mutation completed", "appoint", "mission-001") {
		t.Fatalf("staffing log missing expected fields: %s", got)
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}

type failingProvisioner struct{}

func (failingProvisioner) GetOrCreateInstance(ctx context.Context, profile agentcore.AgentProfile, scope agentcore.AgentInstanceScope) (agentcore.AgentInstanceRef, error) {
	return agentcore.AgentInstanceRef{}, fmt.Errorf("runtime unavailable")
}

type scopedIdentityProvisioner struct {
	id agentcore.AgentInstanceID
}

func (p scopedIdentityProvisioner) GetOrCreateInstance(ctx context.Context, profile agentcore.AgentProfile, scope agentcore.AgentInstanceScope) (agentcore.AgentInstanceRef, error) {
	return agentcore.AgentInstanceRef{ID: p.id}, ctx.Err()
}

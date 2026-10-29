package team

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestServiceAppointCreatesMemberAssignmentAndEvents(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-ada", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)

	assignment, err := service.Appoint(ctx, AppointmentCommand{
		IDempotencyKey: "appoint-001",
		MissionID:      "mission-001",
		ProfileID:      profile.ID,
		Role:           MissionRoleWorker,
		Responsibility: "implement backend",
		AuthorityScope: "repo:write",
		AppointedBy:    ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent},
		Reason:         "best candidate",
	})
	if err != nil {
		t.Fatalf("Appoint: %v", err)
	}
	if assignment.ID == "" || assignment.AgentProfileID != profile.ID || assignment.Status != AssignmentStatusActive {
		t.Fatalf("assignment = %#v", assignment)
	}
	team, err := service.ListTeam(ctx, "mission-001")
	if err != nil {
		t.Fatalf("ListTeam: %v", err)
	}
	if len(team.Members) != 1 || team.Members[0].Status != MemberStatusActive {
		t.Fatalf("team = %#v", team)
	}
	events, err := eventstore.New(env.DB).Load(ctx, "mission-001")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	assertEventTypes(t, events, []missionevent.EventType{
		missionevent.EventStaffingDecisionRecorded,
		missionevent.EventAgentJoined,
		missionevent.EventMissionAssignmentCreated,
	})
}

func TestServiceAppointRejectsScopedProvisionedAgentIdentity(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	service.provisioner = scopedIdentityProvisioner{id: "profile-ada:mission-001"}
	profile := serviceProfile("profile-ada", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)

	_, err := service.Appoint(ctx, AppointmentCommand{
		IDempotencyKey: "appoint-scoped-identity",
		MissionID:      "mission-001",
		ProfileID:      profile.ID,
		Role:           MissionRoleWorker,
		AppointedBy:    ActorRef{ID: "agent-staffing", Kind: ActorKindAgent},
	})

	if !errors.Is(err, ErrProvisioningFailed) {
		t.Fatalf("Appoint error = %v, want provisioning failure for scoped agent identity", err)
	}
	team, err := service.ListTeam(ctx, "mission-001")
	if err != nil {
		t.Fatalf("ListTeam: %v", err)
	}
	if len(team.Members) != 0 {
		t.Fatalf("scoped identity appointment committed team: %#v", team)
	}
}

func TestServiceAppointRecordsDecisionAndAudit(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-ada", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)

	_, err := service.Appoint(ctx, AppointmentCommand{
		IDempotencyKey:     "appoint-001",
		RequestID:          "request-001",
		MissionID:          "mission-001",
		ProfileID:          profile.ID,
		Role:               MissionRoleWorker,
		RequiredSkills:     []agentcore.SkillRef{{SkillID: "go"}},
		RequiredTools:      []agentcore.ToolRef{{ToolID: "git"}},
		QualityConstraints: QualityConstraints{MinScore: 0.9, MinSampleSize: 10},
		AppointedBy:        ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent},
		Reason:             "best candidate",
	})
	if err != nil {
		t.Fatalf("Appoint: %v", err)
	}
	var decisionCount, auditCount int
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		decisionCount = len(tx.Snapshot()[storage.TeamStaffingDecisions])
		auditCount = len(tx.Snapshot()[storage.TeamStaffingAudits])
		return nil
	}); err != nil {
		t.Fatalf("snapshot staffing records: %v", err)
	}
	if decisionCount != 1 || auditCount != 1 {
		t.Fatalf("decision/audit counts = %d/%d, want 1/1", decisionCount, auditCount)
	}
	events, err := eventstore.New(env.DB).Load(ctx, "mission-001")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	assertEventTypes(t, events, []missionevent.EventType{
		missionevent.EventStaffingDecisionRecorded,
		missionevent.EventAgentJoined,
		missionevent.EventMissionAssignmentCreated,
	})
}

func TestServiceAppointRejectsPolicyDeniedCandidateAndAuditsReason(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-ada", agentcore.AgentKindAI)
	profile.Enabled = false
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)

	_, err := service.Appoint(ctx, AppointmentCommand{
		IDempotencyKey:     "appoint-disabled",
		RequestID:          "request-disabled",
		MissionID:          "mission-001",
		ProfileID:          profile.ID,
		Role:               MissionRoleWorker,
		RequiredSkills:     []agentcore.SkillRef{{SkillID: "go"}},
		RequiredTools:      []agentcore.ToolRef{{ToolID: "git"}},
		QualityConstraints: QualityConstraints{MinScore: 0.9},
		AppointedBy:        ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent},
		Reason:             "direct appointment should still obey policy",
	})
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("err = %v, want policy denied", err)
	}
	team, err := service.ListTeam(ctx, "mission-001")
	if err != nil {
		t.Fatalf("ListTeam: %v", err)
	}
	if len(team.Members) != 0 {
		t.Fatalf("policy denial committed team: %#v", team)
	}
	var audits []StaffingAudit
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		for _, record := range tx.Snapshot()[storage.TeamStaffingAudits] {
			var audit StaffingAudit
			if err := decodeTeamRecord(record, &audit); err != nil {
				return err
			}
			audits = append(audits, audit)
		}
		return nil
	}); err != nil {
		t.Fatalf("load audits: %v", err)
	}
	if len(audits) != 1 || len(audits[0].RejectedEvaluations) != 1 {
		t.Fatalf("audits = %#v", audits)
	}
	if got := audits[0].RejectedEvaluations[0].RejectionReasons; len(got) != 1 || got[0] != CodeProfileDisabled {
		t.Fatalf("rejection reasons = %#v, want profile disabled", got)
	}
	events, err := eventstore.New(env.DB).Load(ctx, "mission-001")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	assertEventTypes(t, events, []missionevent.EventType{missionevent.EventCandidateRejected})
}

func TestServiceAppointPolicyDeniedErrorNamesRejectedProfile(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-incomplete", agentcore.AgentKindAI)
	profile.OrgIdentity.RoleRefs = nil
	profile.OrgIdentity.AuthorityScope = ""
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)

	_, err := service.Appoint(ctx, AppointmentCommand{
		IDempotencyKey: "appoint-incomplete",
		RequestID:      "request-incomplete",
		MissionID:      "mission-001",
		ProfileID:      profile.ID,
		Role:           MissionRoleWorker,
		AppointedBy:    ActorRef{ID: "mission-bootstrap", Kind: ActorKindSystem},
		Reason:         "system role bootstrap",
	})
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("err = %v, want policy denied", err)
	}
	if got := err.Error(); !strings.Contains(got, profile.ID.String()) || !strings.Contains(got, string(CodeOrgIdentityIncomplete)) {
		t.Fatalf("policy error should name rejected profile and code, got %q", got)
	}
}

func TestServiceAppointAllowsSystemToBridgeOrganizationalRoleIntoMissionRole(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-legal-orchestrator", agentcore.AgentKindAI)
	profile.RoleName = "Legal Orchestrator"
	profile.OrgIdentity.RoleRefs = []agentcore.RoleRef{{ID: "legal-orchestrator"}}
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)

	assignment, err := service.Appoint(ctx, AppointmentCommand{
		IDempotencyKey:                "appoint-scheduled-legal-agent",
		MissionID:                     "mission-001",
		ProfileID:                     profile.ID,
		Role:                          MissionRoleWorker,
		AppointedBy:                   ActorRef{ID: "mission-bootstrap", Kind: ActorKindSystem},
		Reason:                        "scheduled Mission Agent bootstrap",
		AllowUnknownAvailability:      true,
		AllowAI:                       true,
		AllowOrganizationalRoleBridge: true,
	})
	if err != nil {
		t.Fatalf("Appoint: %v", err)
	}
	if assignment.AgentProfileID != profile.ID || assignment.Role != MissionRoleWorker {
		t.Fatalf("assignment = %#v", assignment)
	}
}

func TestServiceAppointRejectsOrganizationalRoleBridgeFromNonSystemActor(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)

	_, err := service.Appoint(ctx, AppointmentCommand{
		IDempotencyKey:                "appoint-forged-role-bridge",
		MissionID:                     "mission-001",
		ProfileID:                     "profile-legal-orchestrator",
		Role:                          MissionRoleWorker,
		AppointedBy:                   ActorRef{ID: "staffing-agent", Kind: ActorKindAgent},
		AllowOrganizationalRoleBridge: true,
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Appoint error = %v, want ErrInvalidArgument", err)
	}
}

func TestServiceAppointRejectsUnavailableCandidate(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-ada", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)
	if err := service.profileRepo.(*agentcore.PlatformProfileRepository).SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: agentcore.AgentInstanceID(profile.ID.String() + ":fixture"),
		Status:          agentcore.AvailabilityBusy,
		Capacity:        1,
		CurrentLoad:     1,
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save busy availability: %v", err)
	}

	now := time.Now().UTC()
	_, err := service.Appoint(ctx, AppointmentCommand{
		IDempotencyKey:     "appoint-busy",
		RequestID:          "request-busy",
		MissionID:          "mission-001",
		ProfileID:          profile.ID,
		Role:               MissionRoleWorker,
		AvailabilityWindow: AvailabilityWindow{From: now, Until: now.Add(time.Hour)},
		AppointedBy:        ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent},
		Reason:             "direct appointment should check availability",
		QualityConstraints: QualityConstraints{MinScore: 0.9},
		RiskConstraints:    RiskConstraints{AllowedLevels: []agentcore.RiskLevel{agentcore.RiskLevelLow}},
		BudgetConstraints:  BudgetConstraints{MaxCostPerHour: 20},
		RequiredSkills:     []agentcore.SkillRef{{SkillID: "go"}},
		RequiredTools:      []agentcore.ToolRef{{ToolID: "git"}},
	})
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("err = %v, want policy denied", err)
	}
}

func TestServiceAppointRequiresExplicitHumanAllowance(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-human", agentcore.AgentKindHuman)
	profile.RuntimeSpec = agentcore.RuntimeSpec{}
	profile.OrgIdentity.EmploymentType = agentcore.EmploymentTypeHuman
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)
	cmd := AppointmentCommand{
		IDempotencyKey: "appoint-human-denied",
		MissionID:      "mission-001",
		ProfileID:      profile.ID,
		Role:           MissionRoleWorker,
		AppointedBy:    ActorRef{ID: "staffing-agent-001", Kind: ActorKindHuman},
		Reason:         "human handoff",
	}
	if _, err := service.Appoint(ctx, cmd); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("human appointment without allowance err = %v, want policy denied", err)
	}
	cmd.IDempotencyKey = "appoint-human-allowed"
	cmd.AllowHuman = true
	cmd.AllowAI = false
	assignment, err := service.Appoint(ctx, cmd)
	if err != nil {
		t.Fatalf("human appointment with allowance: %v", err)
	}
	if assignment.AgentProfileID != profile.ID || assignment.Status != AssignmentStatusActive {
		t.Fatalf("assignment = %#v", assignment)
	}
}

func TestServiceAppointIsIdempotentForSameKeyAndConflictsForDifferentKey(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-ada", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)
	cmd := AppointmentCommand{IDempotencyKey: "appoint-001", MissionID: "mission-001", ProfileID: profile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}}

	first, err := service.Appoint(ctx, cmd)
	if err != nil {
		t.Fatalf("first appoint: %v", err)
	}
	second, err := service.Appoint(ctx, cmd)
	if err != nil {
		t.Fatalf("second appoint same key: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("idempotent appointment returned %s, want %s", second.ID, first.ID)
	}
	cmd.IDempotencyKey = "appoint-002"
	_, err = service.Appoint(ctx, cmd)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate different key err = %v, want conflict", err)
	}
}

func TestServiceConcurrentAppointCreatesOneAssignmentForIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-concurrent", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)
	cmd := AppointmentCommand{IDempotencyKey: "appoint-concurrent", MissionID: "mission-concurrent", ProfileID: profile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}}

	const workers = 32
	start := make(chan struct{})
	results := make(chan MissionAssignment, workers)
	errorsFound := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			assignment, err := service.Appoint(ctx, cmd)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- assignment
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatalf("Appoint: %v", err)
		}
	}
	var assignmentID MissionAssignmentID
	for assignment := range results {
		if assignmentID == "" {
			assignmentID = assignment.ID
		}
		if assignment.ID != assignmentID || assignment.IdempotencyKey != cmd.IDempotencyKey {
			t.Fatalf("concurrent assignment = %+v, canonical assignment_id=%s", assignment, assignmentID)
		}
	}
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := storage.Snapshot(ctx, tx, storage.TableTeamAssignments)
		if err != nil {
			return err
		}
		if len(records) != 1 {
			t.Fatalf("assignment records = %d, want 1", len(records))
		}
		return nil
	}); err != nil {
		t.Fatalf("inspect assignments: %v", err)
	}
}

func TestServiceScopesAppointmentIdempotencyKeyToMission(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-mission-scope", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)

	first, err := service.Appoint(ctx, AppointmentCommand{IDempotencyKey: "shared-key", MissionID: "mission-a", ProfileID: profile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}})
	if err != nil {
		t.Fatalf("appoint mission-a: %v", err)
	}
	second, err := service.Appoint(ctx, AppointmentCommand{IDempotencyKey: "shared-key", MissionID: "mission-b", ProfileID: profile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}})
	if err != nil {
		t.Fatalf("appoint mission-b: %v", err)
	}
	if first.ID == second.ID || second.MissionID != "mission-b" {
		t.Fatalf("cross-mission idempotency collision: first=%+v second=%+v", first, second)
	}
}

func TestServiceAppointProvisionFailureAbortsCommit(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	service.provisioner = failingProvisioner{}
	profile := serviceProfile("profile-ada", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)

	_, err := service.Appoint(ctx, AppointmentCommand{IDempotencyKey: "appoint-001", MissionID: "mission-001", ProfileID: profile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}})
	if !errors.Is(err, ErrProvisioningFailed) {
		t.Fatalf("err = %v, want provisioning failure", err)
	}
	team, err := service.ListTeam(ctx, "mission-001")
	if err != nil {
		t.Fatalf("ListTeam: %v", err)
	}
	if len(team.Members) != 0 {
		t.Fatalf("provision failure committed team: %#v", team)
	}
}

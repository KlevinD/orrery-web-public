package team

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestPlatformTeamRepositoryPersistsTeamAssignmentRequestDecisionAndAudit(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformTeamRepository()
	request := validStaffingRequest()
	team := MissionTeam{ID: "team-001", MissionID: request.MissionID, Version: 1}
	member := MissionTeamMember{
		ID:              "member-001",
		MissionID:       request.MissionID,
		AgentProfileID:  "profile-001",
		AgentInstanceID: "instance-001",
		Status:          MemberStatusActive,
		Visibility:      TeamVisibilityMission,
	}
	team.Members = []MissionTeamMember{member}
	assignment := MissionAssignment{
		ID:              "assignment-001",
		MissionID:       request.MissionID,
		MemberID:        member.ID,
		AgentProfileID:  member.AgentProfileID,
		AgentInstanceID: member.AgentInstanceID,
		Role:            MissionRoleWorker,
		Status:          AssignmentStatusActive,
		AssignedBy:      request.RequestedBy,
	}
	decision := StaffingDecision{
		ID:                      "decision-001",
		RequestID:               request.ID,
		SelectedAgentProfileID:  member.AgentProfileID,
		SelectedAgentInstanceID: member.AgentInstanceID,
		Rationale:               "best score",
		AuditRef:                "audit-001",
	}
	audit := StaffingAudit{ID: decision.AuditRef, MissionID: request.MissionID, RequestID: request.ID, DecisionID: decision.ID, SelectedRationale: decision.Rationale}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := repo.SaveTeam(ctx, tx, team); err != nil {
			return err
		}
		if err := repo.SaveAssignment(ctx, tx, assignment); err != nil {
			return err
		}
		if err := repo.SaveStaffingRequest(ctx, tx, request); err != nil {
			return err
		}
		if err := repo.SaveStaffingDecision(ctx, tx, decision); err != nil {
			return err
		}
		return repo.AppendAudit(ctx, tx, audit)
	}); err != nil {
		t.Fatalf("save records: %v", err)
	}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		gotTeam, err := repo.GetTeam(ctx, tx, request.MissionID)
		if err != nil {
			return err
		}
		if gotTeam.ID != team.ID || len(gotTeam.Members) != 1 {
			t.Fatalf("team = %#v", gotTeam)
		}
		gotAssignment, err := repo.GetAssignment(ctx, tx, assignment.ID)
		if err != nil {
			return err
		}
		if gotAssignment.AgentProfileID != assignment.AgentProfileID {
			t.Fatalf("assignment = %#v", gotAssignment)
		}
		gotAudit, ok, err := repo.GetAudit(ctx, tx, audit.ID)
		if err != nil {
			return err
		}
		if !ok || gotAudit.SelectedRationale != "best score" {
			t.Fatalf("audit = %#v ok=%v", gotAudit, ok)
		}
		return nil
	}); err != nil {
		t.Fatalf("load records: %v", err)
	}
}

func TestPlatformTeamRepositoryReturnsEmptyTeamWhenMissing(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformTeamRepository()

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		team, err := repo.GetTeam(ctx, tx, "mission-missing")
		if err != nil {
			return err
		}
		if team.MissionID != "mission-missing" || len(team.Members) != 0 {
			t.Fatalf("team = %#v", team)
		}
		return nil
	}); err != nil {
		t.Fatalf("get missing team: %v", err)
	}
}

var _ = agentcore.AgentProfileID("compile-import-guard")

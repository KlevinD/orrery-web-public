package responsibility

import (
	"context"
	"fmt"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestRepositoryListsActivePeopleResponsibilityPortfoliosWithProgressAcrossEmptyMatchPage(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	ctx := context.Background()
	now := env.Clock.Now().UTC()
	portfolios := []PeopleResponsibilityPortfolio{
		batchTestPortfolio("portfolio-00-other-tenant", "tenant-other", "people-target", PeoplePortfolioStatusActive, now),
		batchTestPortfolio("portfolio-01-paused", "tenant-target", "people-target", PeoplePortfolioStatusPaused, now),
		batchTestPortfolio("portfolio-02-other-people", "tenant-target", "people-other", PeoplePortfolioStatusActive, now),
		batchTestPortfolio("portfolio-03-target", "tenant-target", "people-target", PeoplePortfolioStatusActive, now),
	}
	for index, portfolio := range portfolios {
		if _, _, err := repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, now.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatalf("save portfolio %s: %v", portfolio.ID, err)
		}
	}

	first, err := repository.ListActivePeopleResponsibilityPortfoliosPage(ctx, "tenant-target", "people-target", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 0 || !first.HasMore || first.NextCursor != "portfolio-01-paused" {
		t.Fatalf("empty matching page = %+v", first)
	}
	second, err := repository.ListActivePeopleResponsibilityPortfoliosPage(ctx, "tenant-target", "people-target", first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != "portfolio-03-target" || second.HasMore || second.NextCursor != "" {
		t.Fatalf("second page = %+v", second)
	}
}

func TestRepositoryListAssignmentsForAgentsGroupsAllMatchesBeyondOneHundred(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	ctx := context.Background()
	now := env.Clock.Now().UTC()
	err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		seed := func(tenantID, agentID, taskID string, priority int) error {
			_, _, err := repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
				TenantID: tenantID, MissionID: "mission-" + tenantID, TaskID: taskID, AgentProfileID: agentID,
				Priority: priority, AcceptBy: now.Add(time.Hour), ActivationCause: ActivationCauseAssigned,
			}, now)
			return err
		}
		for index := 0; index < 105; index++ {
			if err := seed("tenant-a", "agent-a", fmt.Sprintf("task-a-%03d", index), index); err != nil {
				return err
			}
		}
		for index := 0; index < 3; index++ {
			if err := seed("tenant-a", "agent-b", fmt.Sprintf("task-b-%03d", index), 10+index); err != nil {
				return err
			}
		}
		for index := 0; index < 4; index++ {
			if err := seed("tenant-other", "agent-a", fmt.Sprintf("task-other-%03d", index), 1000+index); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	grouped, err := repository.ListAssignmentsForAgents(
		ctx,
		"tenant-a",
		[]string{"agent-a", "agent-b", "agent-empty"},
		[]AssignmentState{AssignmentStateOffered},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped["agent-a"]) != 105 || len(grouped["agent-b"]) != 3 || len(grouped["agent-empty"]) != 0 {
		t.Fatalf("assignment groups: agent-a=%d agent-b=%d empty=%d", len(grouped["agent-a"]), len(grouped["agent-b"]), len(grouped["agent-empty"]))
	}
	if grouped["agent-a"][0].Priority != 104 || grouped["agent-a"][104].Priority != 0 || grouped["agent-b"][0].AccountableAgent != "agent-b" {
		t.Fatalf("grouped assignments are not sorted or isolated: agent-a first=%+v last=%+v agent-b first=%+v", grouped["agent-a"][0], grouped["agent-a"][104], grouped["agent-b"][0])
	}
	for _, assignment := range grouped["agent-a"] {
		if assignment.TenantID != "tenant-a" || assignment.AccountableAgent != "agent-a" || assignment.State != AssignmentStateOffered {
			t.Fatalf("unexpected agent-a assignment = %+v", assignment)
		}
	}
}

func batchTestPortfolio(id, tenantID, peopleTenantID, status string, now time.Time) PeopleResponsibilityPortfolio {
	return PeopleResponsibilityPortfolio{
		ID: id, TenantID: tenantID, BindingID: "binding-" + id, PeopleTenantID: peopleTenantID,
		SubjectID: "subject-" + id, AgentProfileID: "agent-" + id,
		SourceRevision: "revision-" + id, SourceDigest: "digest-" + id,
		Status: status, SourceAsOf: now,
	}
}

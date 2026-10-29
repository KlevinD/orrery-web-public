package responsibility

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestRepositoryImportsHistoricalMissionTaskWithoutActivation(t *testing.T) {
	states := []AssignmentState{AssignmentStateSubmitted, AssignmentStateFulfilled, AssignmentStateTerminated}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			repository := NewRepository(env.DBRouter, env.UnitOfWork)
			input := HistoricalMissionTaskAssignmentInput{
				TenantID: "tenant-history", MissionID: "mission-history", TaskID: "task-" + string(state),
				SourceKind: "scheduler_agent_task", SourceID: "schedule-history", SourceStepID: "run-" + string(state),
				AgentProfileID: "agent-history", State: state, ActorID: "scheduler-history-backfill", Reason: "stored scheduled run",
			}
			var first, second AssignmentRecord
			for index := 0; index < 2; index++ {
				err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
					assignment, err := repository.ImportHistoricalMissionTaskTx(ctx, tx, input, env.Clock.Now())
					if index == 0 {
						first = assignment
					} else {
						second = assignment
					}
					return err
				})
				if err != nil {
					t.Fatalf("import historical assignment %d: %v", index, err)
				}
			}
			wantID := AssignmentID(input.TenantID, WorkKindMissionTask, input.TaskID)
			if first.ID != wantID || second.ID != wantID || first != second || first.State != state || first.Version != 1 ||
				first.SourceKind != input.SourceKind || first.SourceID != input.SourceID || first.SourceStepID != input.SourceStepID {
				t.Fatalf("historical assignments = first:%+v second:%+v want ID:%q state:%q", first, second, wantID, state)
			}
			ready, err := repository.ListReadyActivations(context.Background(), env.Clock.Now(), 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(ready) != 0 {
				t.Fatalf("historical import created ready activations: %+v", ready)
			}
			audits, err := repository.ListAudits(context.Background(), input.TenantID, first.ID, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(audits) != 1 || audits[0].Action != AuditHistoricalAssignmentImported || audits[0].ToState != string(state) {
				t.Fatalf("historical import audits = %+v", audits)
			}
		})
	}
}

func TestRepositoryRejectsDispatchableHistoricalAssignment(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		_, err := repository.ImportHistoricalMissionTaskTx(ctx, tx, HistoricalMissionTaskAssignmentInput{
			TenantID: "tenant-history", MissionID: "mission-history", TaskID: "task-active",
			AgentProfileID: "agent-history", State: AssignmentStateActive,
		}, env.Clock.Now())
		return err
	})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("dispatchable historical assignment error = %v, want ErrInvalidContract", err)
	}
}

func TestRepositoryRejectsConflictingHistoricalAssignment(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	input := HistoricalMissionTaskAssignmentInput{
		TenantID: "tenant-history", MissionID: "mission-history", TaskID: "task-conflict",
		AgentProfileID: "agent-history", State: AssignmentStateSubmitted,
	}
	if err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		_, err := repository.ImportHistoricalMissionTaskTx(ctx, tx, input, env.Clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}

	for _, mutate := range []func(*HistoricalMissionTaskAssignmentInput){
		func(changed *HistoricalMissionTaskAssignmentInput) { changed.MissionID = "other-mission" },
		func(changed *HistoricalMissionTaskAssignmentInput) { changed.AgentProfileID = "other-agent" },
		func(changed *HistoricalMissionTaskAssignmentInput) { changed.State = AssignmentStateFulfilled },
	} {
		changed := input
		mutate(&changed)
		err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
			_, err := repository.ImportHistoricalMissionTaskTx(ctx, tx, changed, env.Clock.Now())
			return err
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("conflicting historical assignment %+v error = %v, want ErrConflict", changed, err)
		}
	}
	stored, err := repository.GetAssignmentForWork(context.Background(), input.TenantID, WorkKindMissionTask, input.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MissionID != input.MissionID || stored.AccountableAgent != input.AgentProfileID || stored.State != input.State {
		t.Fatalf("conflicting import overwrote assignment: %+v", stored)
	}
}

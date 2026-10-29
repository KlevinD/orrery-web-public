package team

import (
	"context"
	"testing"

	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestStaffingEventSinkAppendsMissionEventsInUnitOfWork(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	sink := NewStaffingEventSink()
	request := validStaffingRequest()
	assignment := MissionAssignment{
		ID:              "assignment-001",
		MissionID:       request.MissionID,
		MemberID:        "member-001",
		AgentProfileID:  "profile-001",
		AgentInstanceID: "instance-001",
		Role:            MissionRoleWorker,
		Status:          AssignmentStatusActive,
		AssignedBy:      request.RequestedBy,
	}

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := sink.Emit(ctx, tx, StaffingEventCandidate{
			Type:           missionevent.EventStaffingRequested,
			MissionID:      request.MissionID,
			RequestID:      request.ID,
			Actor:          request.RequestedBy,
			IdempotencyKey: "request-001",
		}); err != nil {
			return err
		}
		return sink.Emit(ctx, tx, StaffingEventCandidate{
			Type:            missionevent.EventMissionAssignmentCreated,
			MissionID:       assignment.MissionID,
			RequestID:       request.ID,
			AssignmentID:    assignment.ID,
			MemberID:        assignment.MemberID,
			AgentProfileID:  assignment.AgentProfileID,
			AgentInstanceID: assignment.AgentInstanceID,
			MissionRole:     assignment.Role,
			Actor:           assignment.AssignedBy,
			IdempotencyKey:  "assignment-001",
		})
	}); err != nil {
		t.Fatalf("emit events: %v", err)
	}

	events, err := eventstore.New(env.DB).Load(ctx, request.MissionID.String())
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 2 || events[0].EventType != string(missionevent.EventStaffingRequested) || events[1].StreamVersion != 2 {
		t.Fatalf("events = %#v", events)
	}
}

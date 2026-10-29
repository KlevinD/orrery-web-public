package team

import (
	"context"
	"testing"

	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/testkit"
)

func TestServiceRequestStaffingPersistsRequestAndEmitsEvent(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	request := validStaffingRequest()

	id, err := service.RequestStaffing(ctx, request)
	if err != nil {
		t.Fatalf("RequestStaffing: %v", err)
	}
	if id != request.ID {
		t.Fatalf("request id = %s, want %s", id, request.ID)
	}
	events, err := eventstore.New(env.DB).Load(ctx, request.MissionID.String())
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != string(missionevent.EventStaffingRequested) {
		t.Fatalf("events = %#v", events)
	}
}

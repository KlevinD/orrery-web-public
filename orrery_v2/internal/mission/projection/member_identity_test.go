package projection

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missionevent "orrery_v2/internal/mission/event"
)

func TestProjectorPreservesWorkforceMemberIDForAgentIdentity(t *testing.T) {
	model := NewReadModel("mission-1")
	e := memberIdentityEvent(1, "member-joined", missionevent.EventAgentJoined, missionevent.StaffingFactPayload{
		MemberID: "member-1", AgentProfileID: "profile-1", AgentInstanceID: "profile-1", AgentKind: "ai",
	}, model.Mission.CreatedAt)
	if err := NewProjector().Apply(context.Background(), model, e); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if len(model.Team.Members) != 1 || model.Team.Members[0].MemberID != "member-1" {
		t.Fatalf("projected members = %+v", model.Team.Members)
	}
}

func TestProjectorCarriesResponsibilityContextAcrossRuntimeFailure(t *testing.T) {
	model := NewReadModel("mission-1")
	responsibility := &agentcore.ResponsibilityContext{
		ContextID: "snapshot-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 3,
		ActorMemberID: "member-1", AgentProfileID: "profile-1",
	}
	at := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	queued := memberIdentityEvent(1, "run-queued", missionevent.EventAgentRunQueued, missionevent.RuntimeLifecyclePayload{
		TaskID: "task-1", AgentID: "profile-1", RunID: "run-1", RuntimeKind: "codex", Status: "queued", ResponsibilityContext: responsibility,
	}, at)
	failed := memberIdentityEvent(2, "run-failed", missionevent.EventAgentRunFailed, missionevent.RuntimeFailurePayload{
		TaskID: "task-1", AgentID: "profile-1", RunID: "run-1", Code: "failed", Message: "failed",
	}, at.Add(time.Minute))
	projector := NewProjector()
	for _, item := range []missionevent.Envelope{queued, failed} {
		if err := projector.Apply(context.Background(), model, item); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", item.EventType, err)
		}
	}
	if len(model.RuntimeSessions) != 1 || model.RuntimeSessions[0].ResponsibilityContext == nil || model.RuntimeSessions[0].ResponsibilityContext.ContextID != "snapshot-1" {
		t.Fatalf("runtime session responsibility context = %+v", model.RuntimeSessions)
	}
}

func memberIdentityEvent(version int, id string, eventType missionevent.EventType, payload missionevent.Payload, at time.Time) missionevent.Envelope {
	return missionevent.Envelope{
		EventID:        id,
		MissionID:      "mission-1",
		StreamID:       "mission-1",
		StreamVersion:  version,
		EventType:      eventType,
		EventVersion:   1,
		Actor:          missionevent.ActorRef{Kind: missionevent.ActorKindAgent, ID: "agent-1"},
		CorrelationID:  "corr-1",
		IdempotencyKey: id + ":idem",
		OccurredAt:     at,
		Payload:        payload,
	}
}

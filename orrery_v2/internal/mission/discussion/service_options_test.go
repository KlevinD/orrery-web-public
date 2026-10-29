package discussion

import (
	"bytes"
	"context"
	"strings"
	"testing"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/testkit"
)

func TestDiscussionServiceRequiresIDGenerator(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	t.Cleanup(env.Close)
	svc := NewService(ServiceOptions{UnitOfWork: env.UnitOfWork, Clock: env.Clock})

	_, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate")
	if err == nil || !strings.Contains(err.Error(), "id generator is required") {
		t.Fatalf("CreateGroup error = %v, want missing id generator", err)
	}
}

func TestDiscussionServiceLogsStructuredMutationWithoutMessageContent(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	env := testkit.New(t)
	t.Cleanup(env.Close)
	logger := logging.NewJSONLogger(&buf, logging.LevelDebug)
	svc := NewService(ServiceOptions{UnitOfWork: env.UnitOfWork, Clock: env.Clock, IDGenerator: env.IDGenerator, Logger: logger})
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate stage 9")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	if err := svc.AddMember(ctx, group.ID, testAgentDiscussionMember(group.ID, "profile-agent-1", "assignment-1")); err != nil {
		t.Fatalf("AddMember returned error: %v", err)
	}
	secretBody := "token secret body must stay out of logs"
	if _, err := svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           secretBody,
		MessageKind:       MessageKindRequest,
	}); err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}

	logged := buf.String()
	for _, want := range []string{
		`"operation":"post_message"`,
		`"mission_id":"mission-1"`,
		`"discussion_group_id":"test-000001"`,
		`"status":"completed"`,
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("discussion log missing %s in %s", want, logged)
		}
	}
	if strings.Contains(logged, secretBody) {
		t.Fatalf("discussion log leaked message content: %s", logged)
	}
}

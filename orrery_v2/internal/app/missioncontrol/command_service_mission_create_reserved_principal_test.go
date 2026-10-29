package missioncontrol

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPrepareMissionCreationDoesNotBootstrapReservedPrincipalAsHuman(t *testing.T) {
	for _, subjectID := range []string{"system-superadmin", "human-superadmin", "human-local-admin", "human-local-user"} {
		prepared, err := prepareMissionCreationCommand(ViewerRef{
			ViewerID:               "superadmin",
			ViewerKind:             ViewerKindUser,
			AuthorizationSubjectID: subjectID,
		}, CreateMissionCommand{IdempotencyKey: "mission-1", InitialHumanAgentID: "A000017"})
		if err != nil {
			t.Fatalf("prepare mission for %s: %v", subjectID, err)
		}
		if prepared.InitialHumanAgentID != "" {
			t.Fatalf("reserved principal %s bootstrapped as human %q", subjectID, prepared.InitialHumanAgentID)
		}
	}
}

func TestPrepareMissionCreationUsesCanonicalViewerHumanProfile(t *testing.T) {
	prepared, err := prepareMissionCreationCommand(ViewerRef{
		ViewerID:               "ding",
		ViewerKind:             ViewerKindUser,
		AuthorizationSubjectID: "H000001",
	}, CreateMissionCommand{IdempotencyKey: "mission-1", InitialHumanAgentID: "A000017"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.InitialHumanAgentID != "H000001" {
		t.Fatalf("initial human = %q, want canonical viewer profile", prepared.InitialHumanAgentID)
	}
}

func TestLifecycleServiceDoesNotPersistSystemViewerAsDiscussionMember(t *testing.T) {
	service := &LifecycleService{}
	viewer := ViewerRef{ViewerID: "system-superadmin", ViewerKind: ViewerKindSystem}
	if service.shouldAddViewerDiscussionMember(viewer, CreateMissionCommand{}) {
		t.Fatal("system viewer must not be persisted as a Mission discussion member")
	}
	if _, err := service.discussionMemberForViewer(context.Background(), viewer, time.Now()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("system viewer member conversion error = %v, want ErrInvalidRequest", err)
	}
}

func TestBreakglassSystemViewerRequiresExactCanonicalIdentity(t *testing.T) {
	if !isBreakglassSystemViewer(ViewerRef{ViewerID: "system-superadmin", ViewerKind: ViewerKindSystem, AuthorizationSubjectID: "system-superadmin"}) {
		t.Fatal("canonical breakglass viewer was not recognized")
	}
	for _, viewer := range []ViewerRef{
		{ViewerID: "human-superadmin", ViewerKind: ViewerKindSystem, AuthorizationSubjectID: "human-superadmin"},
		{ViewerID: "system-superadmin", ViewerKind: ViewerKindUser, AuthorizationSubjectID: "system-superadmin"},
		{ViewerID: "system-superadmin", ViewerKind: ViewerKindSystem, AuthorizationSubjectID: "other-user"},
	} {
		if isBreakglassSystemViewer(viewer) {
			t.Fatalf("non-canonical viewer recognized as breakglass: %+v", viewer)
		}
	}
}

package discussionsession

import (
	"errors"
	"testing"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestRuntimeToolPrincipalForConversationMapsAccountOwnerToHumanProfile(t *testing.T) {
	view := ConversationRecordView{ConversationRecord: ConversationRecord{
		TenantID:  "tenant-a",
		CreatedBy: "human-1",
		OwnerID:   "account-1",
	}}

	principal, err := runtimeToolPrincipalForConversation(view, ViewerRef{
		ViewerID:        "account-1",
		ViewerKind:      ViewerKindUser,
		TenantID:        "tenant-a",
		PermissionScope: []string{"mission.read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if principal.ViewerID != "human-1" || principal.OwnerID != "account-1" || principal.TenantID != "tenant-a" {
		t.Fatalf("normalized principal = %+v", principal)
	}
	missionViewer := runtimeToolMissionViewer("technical-agent", "tenant-a", principal)
	if missionViewer.ViewerID != "account-1" || missionViewer.AuthorizationSubjectID != "human-1" || missionViewer.ViewerKind != missioncontrol.ViewerKindUser {
		t.Fatalf("mission viewer identity = %+v", missionViewer)
	}
	if len(missionViewer.PermissionScope) != 1 || missionViewer.PermissionScope[0] != "mission.read" {
		t.Fatalf("mission viewer permissions = %+v", missionViewer.PermissionScope)
	}
}

func TestRuntimeToolPrincipalForConversationRejectsOtherAccount(t *testing.T) {
	view := ConversationRecordView{ConversationRecord: ConversationRecord{
		TenantID:  "tenant-a",
		CreatedBy: "human-1",
		OwnerID:   "account-1",
	}}

	_, err := runtimeToolPrincipalForConversation(view, ViewerRef{
		ViewerID:   "account-2",
		ViewerKind: ViewerKindUser,
		TenantID:   "tenant-a",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

func TestRuntimeToolPrincipalForConversationRejectsOtherTenant(t *testing.T) {
	view := ConversationRecordView{ConversationRecord: ConversationRecord{
		TenantID:  "tenant-a",
		CreatedBy: "human-1",
		OwnerID:   "account-1",
	}}

	_, err := runtimeToolPrincipalForConversation(view, ViewerRef{
		ViewerID:   "account-1",
		ViewerKind: ViewerKindUser,
		TenantID:   "tenant-b",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

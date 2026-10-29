package discussionsession

import (
	"testing"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestDiscussionMentionMissionToolViewerUsesPAHostPrincipal(t *testing.T) {
	view := ConversationRecordView{ConversationRecord: ConversationRecord{
		ConversationID:                   "conv-pa",
		TenantID:                         "tenant-a",
		PersonalAssistantOriginOwnerID:   "account-1",
		PersonalAssistantOriginSubjectID: "user-1",
	}}
	viewer := discussionMentionMissionToolViewer("lead-agent", view, DiscussionAgentMentionRequest{
		PersonalAssistantHost:            true,
		ConversationTenantID:             "tenant-a",
		PersonalAssistantOriginOwnerID:   "account-1",
		PersonalAssistantOriginSubjectID: "user-1",
	})
	if viewer.ViewerKind != missioncontrol.ViewerKindUser || viewer.ViewerID != "account-1" || viewer.AuthorizationSubjectID != "user-1" || viewer.TenantID != "tenant-a" {
		t.Fatalf("PA host mission viewer = %+v", viewer)
	}

	physical := discussionMentionMissionToolViewer("worker-agent", view, DiscussionAgentMentionRequest{})
	if physical.ViewerKind != missioncontrol.ViewerKindAgent || physical.ViewerID != "worker-agent" || physical.AuthorizationSubjectID != "" {
		t.Fatalf("worker mission viewer = %+v", physical)
	}
}

func TestDiscussionMissionToolViewerUsesPAAccountOwnerAndProfileSubject(t *testing.T) {
	viewer := discussionMissionToolViewer(RunRecord{
		AgentID:        "lead-agent",
		LogicalAgentID: PersonalAssistantAgentID("tenant-a", "account-1"),
	}, DiscussionAgentRequest{
		ConversationKind: ConversationKindPersonalAssistant,
		Viewer: ViewerRef{
			ViewerID: "user-1", OwnerID: "account-1", AgentProfileID: "user-1",
			ViewerKind: ViewerKindUser, TenantID: "tenant-a",
		},
	}, ConversationRecordView{ConversationRecord: ConversationRecord{
		ConversationID: "conv-pa", TenantID: "tenant-a", CreatedBy: "user-1", OwnerID: "account-1",
	}})
	if viewer.ViewerKind != missioncontrol.ViewerKindUser || viewer.ViewerID != "account-1" || viewer.AuthorizationSubjectID != "user-1" || viewer.TenantID != "tenant-a" {
		t.Fatalf("PA runtime mission viewer = %+v", viewer)
	}
}

package discussionsession

import (
	"context"
	"testing"
	"time"
)

func TestRepositoryDeleteMissionLinksRemovesEveryConversationReference(t *testing.T) {
	ctx := context.Background()
	repository := NewRepository(newRepositoryTestRouter(t))
	createdAt := time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC)
	for _, link := range []LinkedMissionRecord{
		{ConversationID: "conversation-1", MissionID: "mission-delete", Title: "Delete", Status: "active", CreatedAt: createdAt},
		{ConversationID: "conversation-2", MissionID: "mission-delete", Title: "Delete", Status: "active", CreatedAt: createdAt},
		{ConversationID: "conversation-1", MissionID: "mission-keep", Title: "Keep", Status: "active", CreatedAt: createdAt},
	} {
		if err := repository.SaveMissionLink(ctx, link); err != nil {
			t.Fatalf("SaveMissionLink(%s, %s): %v", link.ConversationID, link.MissionID, err)
		}
	}

	if err := repository.DeleteMissionLinks(ctx, "mission-delete"); err != nil {
		t.Fatalf("DeleteMissionLinks: %v", err)
	}

	for _, conversationID := range []string{"conversation-1", "conversation-2"} {
		links, err := repository.listMissionLinks(ctx, conversationID)
		if err != nil {
			t.Fatalf("listMissionLinks(%s): %v", conversationID, err)
		}
		for _, link := range links {
			if link.MissionID == "mission-delete" {
				t.Fatalf("deleted Mission link remains: %+v", link)
			}
		}
	}
	links, err := repository.listMissionLinks(ctx, "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].MissionID != "mission-keep" {
		t.Fatalf("unrelated links = %+v, want mission-keep", links)
	}
}

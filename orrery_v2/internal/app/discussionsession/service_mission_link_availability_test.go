package discussionsession

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fixedMissionLinkAvailability struct {
	exists map[string]bool
	errors map[string]error
}

func (f fixedMissionLinkAvailability) MissionExists(_ context.Context, _, missionID string) (bool, error) {
	if err := f.errors[missionID]; err != nil {
		return false, err
	}
	return f.exists[missionID], nil
}

func TestServiceFiltersMissingMissionLinksAndKeepsUncertainLinks(t *testing.T) {
	ctx := context.Background()
	repository := NewRepository(newRepositoryTestRouter(t))
	createdAt := time.Date(2026, 8, 31, 14, 10, 0, 0, time.UTC)
	if err := repository.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conversation-links", TenantID: "tenant-1", Title: "Mission links", Status: "active",
		CreatedBy: "owner-1", CreatedAt: createdAt, UpdatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	for _, missionID := range []string{"mission-live", "mission-missing", "mission-uncertain"} {
		if err := repository.SaveMissionLink(ctx, LinkedMissionRecord{
			ConversationID: "conversation-links", MissionID: missionID, Title: missionID, Status: "active", CreatedAt: createdAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(ServiceOptions{
		Repository: repository,
		MissionLinkAvailability: fixedMissionLinkAvailability{
			exists: map[string]bool{"mission-live": true},
			errors: map[string]error{"mission-uncertain": errors.New("projection temporarily unavailable")},
		},
	})
	viewer := ViewerRef{ViewerID: "owner-1", ViewerKind: ViewerKindUser, TenantID: "tenant-1"}

	detail, err := service.GetConversation(ctx, viewer, "conversation-links")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.LinkedMissions) != 2 || detail.LinkedMissions[0].MissionID != "mission-live" || detail.LinkedMissions[1].MissionID != "mission-uncertain" {
		t.Fatalf("filtered detail links = %+v", detail.LinkedMissions)
	}
	page, err := service.ListConversations(ctx, viewer, ListConversationsQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Conversations) != 1 || page.Conversations[0].LinkedMissions != 2 {
		t.Fatalf("filtered summary = %+v", page.Conversations)
	}
}

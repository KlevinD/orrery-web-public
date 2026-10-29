package discussionsession

import (
	"context"
	"strings"
)

func (r *Repository) DeleteMissionLinks(ctx context.Context, missionID string) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	missionID = strings.TrimSpace(missionID)
	if missionID == "" {
		return ErrInvalidRequest
	}
	records, err := snapshot(ctx, r.router, missionLinkTable)
	if err != nil {
		return err
	}
	for _, record := range records {
		if got, _ := record["mission_id"].(string); strings.TrimSpace(got) != missionID {
			continue
		}
		conversationID, _ := record["conversation_id"].(string)
		if conversationID = strings.TrimSpace(conversationID); conversationID == "" {
			return ErrInvalidRequest
		}
		if err := r.DeleteMissionLink(ctx, conversationID, missionID); err != nil {
			return err
		}
	}
	return nil
}

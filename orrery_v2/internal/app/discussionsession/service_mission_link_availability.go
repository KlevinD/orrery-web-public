package discussionsession

import (
	"context"
	"strings"
)

func (s *Service) withAvailableMissionLinks(ctx context.Context, view ConversationRecordView) ConversationRecordView {
	if s == nil || s.missionLinks == nil || len(view.LinkedMissions) == 0 {
		return view
	}
	tenantID := strings.TrimSpace(view.TenantID)
	available := make([]LinkedMissionRecord, 0, len(view.LinkedMissions))
	for _, link := range view.LinkedMissions {
		missionID := strings.TrimSpace(link.MissionID)
		exists, err := s.missionLinks.MissionExists(ctx, tenantID, missionID)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn(ctx, "discussion mission link validation failed", "conversation_id", view.ConversationID, "mission_id", missionID, "error", err)
			}
			available = append(available, link)
			continue
		}
		if exists {
			available = append(available, link)
		}
	}
	view.LinkedMissions = available
	return view
}

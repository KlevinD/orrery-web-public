package discussionsession

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (s *Service) persistRuntimeToolCallSideEffects(ctx context.Context, conversationID string, runID string, call ToolCallDTO, linkedMissions []LinkedMissionDTO, at time.Time) error {
	if s == nil || s.repo == nil {
		return nil
	}
	if at.IsZero() {
		at = s.now()
	}
	for _, mission := range linkedMissions {
		if strings.TrimSpace(mission.MissionID) == "" {
			continue
		}
		if replacedID := strings.TrimSpace(mission.ReplacesMissionID); replacedID != "" && replacedID != strings.TrimSpace(mission.MissionID) {
			if err := s.repo.DeleteMissionLink(ctx, conversationID, replacedID); err != nil {
				return err
			}
		}
		if err := s.repo.SaveMissionLink(ctx, LinkedMissionRecord{
			ConversationID: conversationID,
			MissionID:      mission.MissionID,
			Title:          mission.Title,
			Status:         mission.Status,
			CreatedAt:      firstTime(mission.CreatedAt, at),
		}); err != nil {
			return err
		}
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil
	}
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(run.ConversationID) != strings.TrimSpace(conversationID) {
		return nil
	}
	if call.CreatedAt.IsZero() {
		call.CreatedAt = at
	}
	run.ToolCalls = upsertStreamedToolCall(run.ToolCalls, call)
	return s.repo.SaveRun(ctx, run)
}

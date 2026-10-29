package discussionsession

import (
	"context"
	"errors"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func (s *Service) rollbackAfterMessage(ctx context.Context, viewer ViewerRef, current ConversationRecordView, message MessageRecord) error {
	unlock := s.lockRunLifecycle(current.ConversationID)
	defer unlock()
	return s.rollbackAfterMessageLocked(ctx, viewer, current, message)
}

func (s *Service) rollbackAfterMessageLocked(ctx context.Context, viewer ViewerRef, current ConversationRecordView, message MessageRecord) error {
	if latest, err := s.repo.GetConversation(ctx, current.ConversationID); err == nil {
		current = latest
	}
	cutoff := message.CreatedAt
	plan := retryRollbackExecutionDeletePlan(current.Runs, cutoff)
	if !plan.empty() && s.executionDeleter == nil {
		return ErrServiceDependency
	}
	for _, missionID := range plan.missionIDs {
		if err := s.executionDeleter.DeleteMission(ctx, discussionAgentViewer(s.discussionAgentID()), missionID); err != nil && !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
			return err
		}
	}
	for missionID, taskIDs := range plan.tasksByMission {
		for _, taskID := range taskIDs {
			if err := s.executionDeleter.DeleteMissionTask(ctx, discussionAgentViewer(s.discussionAgentID()), missionID, taskID); err != nil && !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
				return err
			}
		}
	}
	for _, record := range current.Messages {
		if record.CreatedAt.After(cutoff) {
			if err := s.repo.DeleteMessage(ctx, record.MessageID); err != nil {
				return err
			}
		}
	}
	for _, run := range current.Runs {
		if !run.CreatedAt.Before(cutoff) {
			s.invalidateRun(run.RunID)
			if err := s.deleteRunDerivedRecords(ctx, current.ConversationID, run.RunID); err != nil {
				return err
			}
			if err := s.repo.DeleteRun(ctx, run.RunID); err != nil {
				return err
			}
		}
	}
	for _, event := range current.Events {
		if event.CreatedAt.After(cutoff) && event.Kind != "message_retry_requested" {
			if err := s.repo.DeleteEvent(ctx, event.EventID); err != nil {
				return err
			}
		}
	}
	for _, link := range current.LinkedMissions {
		if link.CreatedAt.After(cutoff) {
			if err := s.repo.DeleteMissionLink(ctx, current.ConversationID, link.MissionID); err != nil {
				return err
			}
		}
	}
	return nil
}

func retryRollbackExecutionDeletePlan(runs []RunRecord, cutoff time.Time) conversationExecutionDeletePlan {
	var prunedRuns []RunRecord
	for _, run := range runs {
		if !run.CreatedAt.Before(cutoff) {
			prunedRuns = append(prunedRuns, run)
		}
	}
	missionIDs := missionIDsCreatedByRuns(prunedRuns)
	missionSet := makeStringSet(missionIDs)
	tasksByMission := taskIDsCreatedByRuns(prunedRuns, cutoff)
	for missionID := range missionSet {
		delete(tasksByMission, missionID)
	}
	return conversationExecutionDeletePlan{missionIDs: missionIDs, tasksByMission: tasksByMission}
}

func (s *Service) invalidateRunsAfterOrAtMessage(current ConversationRecordView, message MessageRecord) {
	cutoff := message.CreatedAt
	for _, run := range current.Runs {
		if !run.CreatedAt.Before(cutoff) {
			s.invalidateRun(run.RunID)
		}
	}
}

package discussionsession

import (
	"context"
	"errors"
	"fmt"
	"strings"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

type conversationExecutionDeletePlan struct {
	missionIDs     []string
	tasksByMission map[string][]string
}

func (s *Service) deleteConversationLocked(ctx context.Context, conversationID string) error {
	current, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return fmt.Errorf("load conversation for deletion: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(current.PurposeKind), "todo") {
		return ErrForbidden
	}
	plan := conversationDeletePlan(current)
	if err := s.applyConversationExecutionDeletePlan(ctx, plan); err != nil {
		return fmt.Errorf("delete conversation executions: %w", err)
	}
	for _, run := range current.Runs {
		s.invalidateRun(run.RunID)
	}
	if s.attachments != nil {
		if releaser, ok := s.runtimeRecordCleaner.(DiscussionRuntimeWorkspaceReleaser); ok {
			workspace, err := s.attachments.SessionWorkspace(ctx, conversationID, current.WorkspaceRootDir)
			if err != nil {
				return fmt.Errorf("resolve conversation workspace for runtime release: %w", err)
			}
			if err := releaser.ReleaseRuntimeWorkspace(ctx, workspace.RootDir); err != nil {
				return fmt.Errorf("release conversation runtime workspace: %w", err)
			}
		}
	}
	if s.artifactCleaner != nil {
		if err := s.artifactCleaner.DeleteConversationArtifacts(ctx, conversationID); err != nil {
			return fmt.Errorf("delete conversation artifacts: %w", err)
		}
	}
	if s.attachments != nil {
		if err := s.attachments.DeleteConversationWorkspace(ctx, conversationID, current.WorkspaceRootDir); err != nil {
			return fmt.Errorf("delete conversation workspace: %w", err)
		}
	}
	for _, run := range current.Runs {
		if err := s.deleteRunDerivedRecords(ctx, conversationID, run.RunID); err != nil {
			return fmt.Errorf("delete derived records for run %q: %w", run.RunID, err)
		}
	}
	if err := s.repo.DeleteConversation(ctx, conversationID); err != nil {
		return fmt.Errorf("delete conversation records: %w", err)
	}
	s.deleteConversationAuthorization(ctx, current)
	if err := s.appendRealtimeSnapshotInvalidated(ctx, conversationID, "delete", "delete"); err != nil {
		return fmt.Errorf("publish deleted conversation snapshot invalidation: %w", err)
	}
	if err := s.appendRealtimeConversationListInvalidated(ctx, current, "delete-"+conversationID); err != nil {
		return fmt.Errorf("publish deleted conversation list invalidation: %w", err)
	}
	return nil
}

func (s *Service) deleteRunDerivedRecords(ctx context.Context, conversationID string, runID string) error {
	if s == nil {
		return nil
	}
	if s.artifactCleaner != nil {
		if err := s.artifactCleaner.DeleteRunArtifacts(ctx, conversationID, runID); err != nil {
			return err
		}
	}
	if s.runtimeRecordCleaner != nil {
		if err := s.runtimeRecordCleaner.DeleteDiscussionRuntimeRecord(ctx, runID); err != nil {
			return err
		}
	}
	return nil
}

func conversationDeletePlan(current ConversationRecordView) conversationExecutionDeletePlan {
	missionIDs := missionIDsCreatedByRuns(current.Runs)
	missionSet := makeStringSet(missionIDs)
	tasksByMission := taskIDsCreatedByRuns(current.Runs, current.ConversationRecord.CreatedAt)
	for missionID := range missionSet {
		delete(tasksByMission, missionID)
	}
	return conversationExecutionDeletePlan{missionIDs: missionIDs, tasksByMission: tasksByMission}
}

func missionIDsCreatedByRuns(runs []RunRecord) []string {
	var out []string
	seen := map[string]bool{}
	for _, run := range runs {
		for _, call := range run.ToolCalls {
			if !isMissionBriefCreateTool(call.Name) {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(call.Result["operation"]), "updated") {
				continue
			}
			missionID := strings.TrimSpace(firstNonBlank(call.Result["mission_id"], call.Arguments["mission_id"], call.Arguments["mission"]))
			if missionID == "" || seen[missionID] {
				continue
			}
			seen[missionID] = true
			out = append(out, missionID)
		}
	}
	return out
}

func (s *Service) applyConversationExecutionDeletePlan(ctx context.Context, plan conversationExecutionDeletePlan) error {
	if s.executionDeleter == nil {
		if plan.empty() {
			return nil
		}
		return ErrServiceDependency
	}
	if plan.empty() {
		return nil
	}
	viewer := discussionAgentViewer(s.discussionAgentID())
	for _, missionID := range plan.missionIDs {
		if err := s.executionDeleter.DeleteMission(ctx, viewer, missionID); err != nil && !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
			return err
		}
	}
	for missionID, taskIDs := range plan.tasksByMission {
		for _, taskID := range taskIDs {
			if err := s.executionDeleter.DeleteMissionTask(ctx, viewer, missionID, taskID); err != nil && !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
				return err
			}
		}
	}
	return nil
}

func (p conversationExecutionDeletePlan) empty() bool {
	if len(p.missionIDs) > 0 {
		return false
	}
	for _, taskIDs := range p.tasksByMission {
		if len(taskIDs) > 0 {
			return false
		}
	}
	return true
}

func makeStringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

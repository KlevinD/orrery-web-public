package discussionsession

import (
	"context"
	"strings"
	"time"
)

func (e AgentTurnExecutor) finish(ctx context.Context, command DiscussionTurnCommand, cause error) (result ConversationDTO, err error) {
	finishStage := e.service.startDiscussionStage(ctx, command.ConversationID, command.TurnID, "", "turn_finalize")
	defer func() { finishStage(err) }()
	finishLoad := e.service.startDiscussionStage(ctx, command.ConversationID, command.TurnID, "", "turn_finalize_load")
	record, err := e.service.repo.GetConversationRecord(ctx, command.ConversationID)
	if err != nil {
		finishLoad(err)
		return ConversationDTO{}, err
	}
	runs, err := e.service.repo.listRuns(ctx, command.ConversationID)
	finishLoad(err)
	if err != nil {
		return ConversationDTO{}, err
	}
	view := ConversationRecordView{ConversationRecord: record, Runs: runs}
	status, runIDs, completedAt := discussionTurnCompletion(view.Runs, command.TurnID, cause, command.SourceMessage.CreatedAt, e.service.now())
	turn := TurnRecord{
		TurnID:         command.TurnID,
		ConversationID: command.ConversationID,
		RootMessageID:  command.SourceMessage.MessageID,
		IdempotencyKey: command.IdempotencyKey,
		Mode:           string(command.Targets.Mode),
		Status:         status,
		RunIDs:         runIDs,
		StartedAt:      firstTime(command.SourceMessage.CreatedAt, completedAt),
		CompletedAt:    completedAt,
		CreatedAt:      firstTime(command.SourceMessage.CreatedAt, completedAt),
		UpdatedAt:      completedAt,
	}
	if cause != nil {
		turn.ErrorCode = discussionTurnErrorCode(cause)
		turn.ErrorMessage = cause.Error()
	}
	view.Status = status
	view.UpdatedAt = completedAt
	terminalRuns := terminalDiscussionTurnRuns(view.Runs, command.TurnID, status, completedAt)
	if command.Targets.Mode == discussionTurnTargetDirectAgent || command.Targets.Mode == discussionTurnTargetDirectPassive {
		if len(command.Targets.Members) > 0 {
			applyDirectTargetMetadata(&view.ConversationRecord, command.Targets.Members[0])
		}
	}
	if isCompletedConversationStatus(status) {
		view.ViewedAt = completedAt
	}
	finishCommit := e.service.startDiscussionStage(ctx, command.ConversationID, command.TurnID, "", "turn_finalize_commit")
	committed, err := e.service.repo.ApplyTurnMutation(ctx, TurnAggregateMutation{
		Conversation:     view.ConversationRecord,
		ExpectedRevision: view.Revision,
		Turn:             &turn,
		Runs:             terminalRuns,
		CompleteTurnID:   command.TurnID,
	})
	finishCommit(err)
	if err != nil {
		return ConversationDTO{}, err
	}
	view.ConversationRecord = committed
	finishSnapshot := e.service.startDiscussionStage(ctx, command.ConversationID, command.TurnID, "", "turn_finalize_snapshot")
	view, err = e.service.repo.GetConversation(ctx, command.ConversationID)
	finishSnapshot(err)
	if err != nil {
		return ConversationDTO{}, err
	}
	if status == "failed" || status == "cancelled" {
		err = e.service.appendRealtimeTurnFailed(ctx, turn)
	} else {
		err = e.service.appendRealtimeTurnCompleted(ctx, turn)
	}
	if err != nil {
		return ConversationDTO{}, err
	}
	if err := e.service.appendRealtimeConversationListInvalidated(ctx, view, "turn-finished-"+command.TurnID); err != nil {
		return ConversationDTO{}, err
	}
	dto := mapConversationDTO(view)
	kind := StreamEventTurnCompleted
	if status == "failed" || status == "cancelled" {
		kind = StreamEventTurnFailed
	}
	e.emit(command, StreamEventDTO{
		Kind:           kind,
		EventID:        "discussion-" + strings.ReplaceAll(kind, "_", "-") + "-" + command.TurnID,
		TurnID:         command.TurnID,
		ConversationID: command.ConversationID,
		Revision:       dto.Revision,
		Conversation:   &dto,
		Error:          turn.ErrorMessage,
	})
	e.emit(command, StreamEventDTO{
		Kind:           StreamEventSnapshot,
		EventID:        "discussion-turn-final-snapshot-" + command.TurnID,
		TurnID:         command.TurnID,
		ConversationID: command.ConversationID,
		Revision:       dto.Revision,
		Conversation:   &dto,
	})
	return dto, nil
}

func discussionTurnCompletion(runs []RunRecord, turnID string, cause error, sourceAt time.Time, now time.Time) (string, []string, time.Time) {
	status := "completed"
	if cause != nil {
		status = "failed"
	}
	latest := firstTimeAfter(now, sourceAt)
	runIDs := make([]string, 0)
	for _, run := range runs {
		if strings.TrimSpace(run.TurnID) != strings.TrimSpace(turnID) {
			continue
		}
		runIDs = append(runIDs, run.RunID)
		if run.CompletedAt.After(latest) {
			latest = run.CompletedAt
		}
		switch strings.ToLower(strings.TrimSpace(run.Status)) {
		case "failed":
			status = "failed"
		case "cancelled", "canceled":
			if status != "failed" {
				status = "cancelled"
			}
		case "blocked":
			if status == "completed" {
				status = "blocked"
			}
		}
	}
	return status, runIDs, latest
}

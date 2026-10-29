package discussionsession

import "context"

func (s *Service) prepareDiscussionAgentRuntimeRequestOrFailRun(ctx context.Context, conversationID string, run RunRecord, request *DiscussionAgentRequest) error {
	if err := s.prepareDiscussionAgentRequestOrFailRun(ctx, conversationID, run, request); err != nil {
		return err
	}
	finishStage := s.startDiscussionStage(ctx, conversationID, run.TurnID, run.RunID, "preferences_prepare")
	err := s.attachPersonalAssistantPreferences(ctx, request)
	finishStage(err)
	if err != nil && s.logger != nil {
		s.logger.Warn(ctx, "personal assistant preferences unavailable", "conversation_id", conversationID, "error", err)
	}
	return nil
}

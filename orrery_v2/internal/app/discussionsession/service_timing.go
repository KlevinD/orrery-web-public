package discussionsession

import (
	"context"
	"time"
)

// Stage records contain identifiers and durations, never prompts, notification
// bodies, model credentials or tool arguments. Timings use the monotonic clock.
func (s *Service) startDiscussionStage(ctx context.Context, conversationID, turnID, runID, stage string) func(error) {
	started := time.Now()
	return func(err error) {
		if s == nil || s.logger == nil {
			return
		}
		status := "completed"
		if err != nil {
			status = "failed"
		}
		fields := []any{"operation", "discussion.stage", "stage", stage,
			"conversation_id", conversationID, "turn_id", turnID, "run_id", runID,
			"duration_ms", float64(time.Since(started).Microseconds()) / 1000, "status", status}
		if logger, ok := s.logger.(interface {
			Info(context.Context, string, ...any)
		}); ok {
			logger.Info(ctx, "discussion stage completed", fields...)
		} else {
			s.logger.Debug(ctx, "discussion stage completed", fields...)
		}
	}
}

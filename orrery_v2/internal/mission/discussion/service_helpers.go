package discussion

import (
	"context"
	"fmt"
	"time"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/logging"
)

func (s *Service) newID(kind string) (string, error) {
	if s.ids == nil {
		return "", fmt.Errorf("generate %s id: id generator is required", kind)
	}
	id, err := s.ids.NewIDContext()
	if err != nil {
		return "", fmt.Errorf("generate %s id: %w", kind, err)
	}
	return id, nil
}

func (s *Service) observeMutation(ctx context.Context, operation string, missionID missioncore.MissionID, groupID DiscussionGroupID, idempotencyKey string, start time.Time, err error) {
	if s.logger == nil {
		return
	}
	fields := []logging.Field{
		logging.String("operation", operation),
		logging.String("mission_id", missionID.String()),
		logging.String("discussion_group_id", string(groupID)),
		logging.String("idempotency_key", idempotencyKey),
		logging.String("status", "completed"),
		logging.Int("duration_ms", int(s.now().Sub(start).Milliseconds())),
	}
	if err != nil {
		fields[4] = logging.String("status", "failed")
		fields = append(fields, logging.String("error", err.Error()))
		s.logger.Warn(ctx, "mission discussion mutation failed", fields...)
		return
	}
	s.logger.Info(ctx, "mission discussion mutation completed", fields...)
}

func firstMissionID(values ...missioncore.MissionID) missioncore.MissionID {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return ""
}

func discussionGroupEventKey(group DiscussionGroup) string {
	if !group.MissionID.IsZero() {
		return "discussion-group|" + group.MissionID.String()
	}
	if group.ID != "" {
		return "discussion-group|" + string(group.ID)
	}
	return "discussion-group"
}

func firstGroupID(values ...DiscussionGroupID) DiscussionGroupID {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func scopedKey(commandKey, fallback, event string) string {
	base := firstNonEmpty(commandKey, fallback)
	if base == "" {
		return event
	}
	return base + "|" + event
}

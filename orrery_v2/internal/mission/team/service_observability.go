package team

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

func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

func (s *Service) startSpan(ctx context.Context, name string) (context.Context, func(error)) {
	if s.tracer == nil {
		return ctx, func(error) {}
	}
	return s.tracer.Start(ctx, name)
}

func (s *Service) observeMutation(ctx context.Context, operation string, missionID missioncore.MissionID, requestID StaffingRequestID, idempotencyKey string, err error) {
	status := "success"
	if err != nil {
		status = "error"
	}
	fields := map[string]any{
		"operation": operation,
		"status":    status,
	}
	if !missionID.IsZero() {
		fields["mission_id"] = missionID.String()
	}
	if requestID != "" {
		fields["request_id"] = string(requestID)
	}
	if idempotencyKey != "" {
		fields["idempotency_key"] = idempotencyKey
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	if s.metrics != nil {
		s.metrics.CaptureContext(ctx, "mission_team.staffing_mutation", fields)
	}
	if s.logger == nil {
		return
	}
	logFields := []logging.Field{
		logging.String("operation", operation),
		logging.String("status", status),
	}
	if !missionID.IsZero() {
		logFields = append(logFields, logging.String("mission_id", missionID.String()))
	}
	if requestID != "" {
		logFields = append(logFields, logging.String("request_id", string(requestID)))
	}
	if err != nil {
		logFields = append(logFields, logging.String("error", err.Error()))
		s.logger.Warn(ctx, "mission team staffing mutation failed", logFields...)
		return
	}
	s.logger.Info(ctx, "mission team staffing mutation completed", logFields...)
}

package notification

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"orrery_v2/internal/app/scheduler"
)

// SetReportReader configures a read-only durable report source before workers
// start. The notification service never holds a recovery or model capability.
func (s *Service) SetReportReader(reports ReportReader) { s.reports = reports }

func (s *Service) report(ctx context.Context, run scheduler.ScheduledRun) (scheduler.AgentReport, bool, error) {
	// A malformed existing report is withheld, never replaced by a supplement.
	if run.Result.AgentReport != nil || s.reports == nil {
		report, ok := scheduler.PublicAgentReport(run)
		return report, ok, nil
	}
	if !validReportRun(run) {
		return scheduler.AgentReport{}, false, nil
	}
	report, ok, err := s.reports.Report(ctx, run)
	return report, ok && report.AgentID == run.AgentID && report.RuntimeRunID != "", err
}

func validReportRun(run scheduler.ScheduledRun) bool {
	if run.TargetMode != scheduler.TargetModeAgentTask || run.ScheduledRunID == "" || run.ScheduledTaskID == "" || run.AgentID == "" || run.RuntimeRunID == "" || run.CompletedAt.IsZero() ||
		(run.Status != scheduler.RunStatusCompleted && run.Status != scheduler.RunStatusFailed) {
		return false
	}
	if snapshot := run.ConfigurationSnapshot; snapshot != nil {
		return snapshot.ScheduledTaskID == run.ScheduledTaskID && snapshot.TenantID == run.TenantID && snapshot.Target.Mode == scheduler.TargetModeAgentTask && snapshot.Target.AgentID == run.AgentID
	}
	return true
}

func reportHash(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func reportOriginFingerprint(run scheduler.ScheduledRun) (string, error) {
	// Delivery receipts and trace availability may advance after publication.
	// All business identity, configuration, result and outcome fields stay bound.
	run.TraceAvailable, run.TraceStatus = false, ""
	run.DeliveryStatus, run.DeliveryError, run.ExternalMessageID = "", "", ""
	data, err := json.Marshal(run)
	if err != nil {
		return "", err
	}
	return reportHash(data), nil
}

func (s *Service) reportStillCurrent(ctx context.Context, source Source, event Event) (bool, error) {
	var fingerprint, eventHash string
	err := s.store.QueryRow(ctx, "SELECT origin_sha256,event_sha256 FROM notification_report_origins WHERE event_id=?", event.ID).Scan(&fingerprint, &eventHash)
	// Old events cannot be safely backfilled from a possibly changed execution.
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if reportHash([]byte(encode(event))) != eventHash {
		return false, nil
	}
	run, err := s.scheduler.GetRun(ctx, event.RunID)
	if errors.Is(err, scheduler.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !validReportRun(run) || run.ScheduledRunID != event.RunID || run.ScheduledTaskID != source.TaskID ||
		run.TenantID != source.OriginTenant || run.TenantID != event.OriginTenant || run.AgentID != event.AgentID ||
		!run.CompletedAt.Equal(event.CreatedAt) || run.CreatedAt.Before(source.ActivatedAt) {
		return false, nil
	}
	current, err := reportOriginFingerprint(run)
	if err != nil || current != fingerprint {
		return false, err
	}
	report, ok, err := s.report(ctx, run)
	if err != nil || !ok {
		return false, err
	}
	return event.AgentReport != nil && report == *event.AgentReport && report.Body == event.Summary, nil
}

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/db"
)

const (
	scheduledReportTable       = "scheduled_agent_reports"
	scheduledReportMaxAttempts = 3
	scheduledReportWindow      = 30 * time.Minute
	scheduledReportLease       = time.Minute
	scheduledReportRetry       = 5 * time.Minute
)

// Report state is independent of both business acceptance and delivery state.
// A supplemental run never replaces the originating run's formal result.
type scheduledReportRecord struct {
	TenantID           string    `json:"tenant_id"`
	ScheduledRunID     string    `json:"scheduled_run_id"`
	OriginRuntimeRunID string    `json:"origin_runtime_run_id"`
	AgentID            string    `json:"agent_id"`
	SourceSHA256       string    `json:"source_sha256"`
	State              string    `json:"state"`
	Attempts           int       `json:"attempts"`
	LeaseUntil         time.Time `json:"lease_until,omitempty"`
	RetryAfter         time.Time `json:"retry_after,omitempty"`
	Deadline           time.Time `json:"deadline"`
	AuthorRuntimeRunID string    `json:"author_runtime_run_id,omitempty"`
	Body               string    `json:"body,omitempty"`
	BodySHA256         string    `json:"body_sha256,omitempty"`
	Source             string    `json:"source,omitempty"`
	FailureCode        string    `json:"failure_code,omitempty"`
	TraceRef           string    `json:"trace_ref,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type scheduledReportAuthor struct {
	RuntimeRunID, Body, Source, TraceRef string
}

type scheduledReportRecovery interface {
	Recover(context.Context, schedulerapp.ScheduledRun, string) (scheduledReportAuthor, string)
}

type productionScheduledReports struct {
	store    *db.Store
	owner    *productionPersonalAssistantScheduledResultSweeper
	recovery scheduledReportRecovery
	since    time.Time
}

func scheduledReportSHA(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func scheduledReportIdentity(run schedulerapp.ScheduledRun) (string, string) {
	// Mutable delivery receipts and trace availability are deliberately excluded.
	material, _ := json.Marshal(struct {
		Tenant, ID, Task, Agent, Runtime, Mission, MissionTask, Failure string
		Mode                                                            schedulerapp.TargetMode
		Status                                                          schedulerapp.RunStatus
		CompletedAt                                                     time.Time
		Configuration                                                   *schedulerapp.ScheduledTask
		Result                                                          schedulerapp.RunResult
	}{personalAssistantScheduledTenant(run.TenantID), run.ScheduledRunID, run.ScheduledTaskID, run.AgentID, run.RuntimeRunID, run.MissionID, run.TaskID, run.FailureReason,
		run.TargetMode, run.Status, run.CompletedAt, run.ConfigurationSnapshot, run.Result})
	fingerprint := scheduledReportSHA(material)
	return scheduledReportSHA([]byte(personalAssistantScheduledTenant(run.TenantID)+"\x00"+run.ScheduledRunID)) + ":" + fingerprint, fingerprint
}

func scheduledReportDBRecord(record scheduledReportRecord) db.Record {
	data, _ := json.Marshal(record)
	return db.Record{"report": string(data)}
}

func decodeScheduledReport(record db.Record) (scheduledReportRecord, error) {
	var result scheduledReportRecord
	value, ok := record["report"].(string)
	if !ok {
		return result, errors.New("invalid persisted report record")
	}
	err := json.Unmarshal([]byte(value), &result)
	return result, err
}

func validScheduledReportOrigin(run schedulerapp.ScheduledRun) bool {
	if run.TargetMode != schedulerapp.TargetModeAgentTask || !schedulerapp.ResultNotificationTerminal(run) ||
		run.ScheduledRunID == "" || run.AgentID == "" || run.RuntimeRunID == "" || run.CompletedAt.IsZero() {
		return false
	}
	if snapshot := run.ConfigurationSnapshot; snapshot != nil {
		return snapshot.Target.Mode == schedulerapp.TargetModeAgentTask && snapshot.Target.AgentID == run.AgentID &&
			snapshot.ScheduledTaskID == run.ScheduledTaskID && personalAssistantScheduledTenant(snapshot.TenantID) == personalAssistantScheduledTenant(run.TenantID)
	}
	return true
}

// Report only reads durable content. It never invokes a model or business tool.
func (s *productionScheduledReports) Report(ctx context.Context, run schedulerapp.ScheduledRun) (schedulerapp.AgentReport, bool, error) {
	if report, ok := schedulerapp.PublicAgentReport(run); ok {
		return report, personalAssistantScheduledPublicText(report.Body) == report.Body, nil
	}
	if s == nil || s.store == nil || !validScheduledReportOrigin(run) {
		return schedulerapp.AgentReport{}, false, nil
	}
	key, fingerprint := scheduledReportIdentity(run)
	raw, found, err := s.store.GetRecord(ctx, scheduledReportTable, key)
	if err != nil || !found {
		return schedulerapp.AgentReport{}, false, err
	}
	record, err := decodeScheduledReport(raw)
	if err != nil {
		return schedulerapp.AgentReport{}, false, err
	}
	valid := record.State == "ready" && record.SourceSHA256 == fingerprint && record.TenantID == personalAssistantScheduledTenant(run.TenantID) &&
		record.ScheduledRunID == run.ScheduledRunID && record.OriginRuntimeRunID == run.RuntimeRunID && record.AgentID == run.AgentID &&
		record.AuthorRuntimeRunID != "" && record.Body != "" && record.BodySHA256 == scheduledReportSHA([]byte(record.Body)) &&
		personalAssistantScheduledPublicText(record.Body) == record.Body &&
		((record.Source == "trace_final" && record.AuthorRuntimeRunID == run.RuntimeRunID) ||
			(record.Source == "supplement" && record.AuthorRuntimeRunID != run.RuntimeRunID && strings.HasPrefix(record.AuthorRuntimeRunID, "scheduled-report-")))
	return schedulerapp.AgentReport{AgentID: record.AgentID, RuntimeRunID: record.AuthorRuntimeRunID, Body: record.Body}, valid, nil
}

// The persisted activation fence is not reset by a restart or a deployment.
// It intentionally does not enroll old missing reports, including replay IDs.
func (s *productionScheduledReports) activate(ctx context.Context) error {
	const key = "recovery_activation_v1"
	_, err := s.store.CreateRecord(ctx, scheduledReportTable, key, db.Record{"since": s.owner.now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	raw, found, err := s.store.GetRecord(ctx, scheduledReportTable, key)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("report recovery activation is missing")
	}
	value, _ := raw["since"].(string)
	s.since, err = time.Parse(time.RFC3339Nano, value)
	if err != nil || s.since.IsZero() {
		return errors.New("invalid report recovery activation")
	}
	return nil
}

func (s *productionScheduledReports) authorized(ctx context.Context, run schedulerapp.ScheduledRun) (bool, error) {
	for _, recipient := range s.owner.config.recipients {
		if recipient.TenantID != personalAssistantScheduledTenant(run.TenantID) {
			continue
		}
		_, allowed, err := s.owner.recipient(ctx, personalAssistantScheduledResultReference{recipient: recipient})
		if err != nil || allowed {
			return allowed, err
		}
	}
	return false, nil
}

// Sweep is an internal report stage, not a user ScheduledTask or a delivery
// callback. At most one recovery attempt is started per pass.
func (s *productionScheduledReports) Sweep(ctx context.Context) error {
	if s == nil || s.store == nil || s.owner == nil || s.since.IsZero() {
		return nil
	}
	runs, err := s.owner.repository.ListResultNotificationRuns(ctx, s.owner.config.since, s.owner.config.replay)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if !validScheduledReportOrigin(run) || !s.owner.eligible(run) {
			continue
		}
		allowed, err := s.authorized(ctx, run)
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		canonical, authored := schedulerapp.PublicAgentReport(run)
		if !authored && run.CompletedAt.Before(s.since) {
			continue
		}
		now := s.owner.now().UTC()
		key, fingerprint := scheduledReportIdentity(run)
		record := scheduledReportRecord{TenantID: personalAssistantScheduledTenant(run.TenantID), ScheduledRunID: run.ScheduledRunID,
			OriginRuntimeRunID: run.RuntimeRunID, AgentID: run.AgentID, SourceSHA256: fingerprint, State: "pending", Deadline: run.CompletedAt.Add(scheduledReportWindow), UpdatedAt: now}
		if authored {
			record.State, record.Source = "ready", "agent_final"
			record.AuthorRuntimeRunID, record.Body = canonical.RuntimeRunID, canonical.Body
			record.BodySHA256 = scheduledReportSHA([]byte(canonical.Body))
			if personalAssistantScheduledPublicText(canonical.Body) != canonical.Body {
				record.State, record.FailureCode, record.Body = "withheld", "unsafe_original_report", ""
			}
		}
		if _, err := s.store.CreateRecord(ctx, scheduledReportTable, key, scheduledReportDBRecord(record)); err != nil {
			return err
		}
		raw, found, err := s.store.GetRecord(ctx, scheduledReportTable, key)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		record, err = decodeScheduledReport(raw)
		if err != nil {
			return err
		}
		if record.State == "ready" || record.State == "failed" || record.State == "withheld" || now.Before(record.LeaseUntil) || now.Before(record.RetryAfter) {
			continue
		}
		if record.Attempts >= scheduledReportMaxAttempts || !now.Before(record.Deadline) {
			record.State, record.FailureCode, record.UpdatedAt = "failed", "recovery_exhausted", now
			if _, err := s.store.CompareAndSwapRecord(ctx, scheduledReportTable, key, raw, scheduledReportDBRecord(record)); err != nil {
				return err
			}
			continue
		}
		if s.recovery == nil {
			continue
		}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		reportRunID := "scheduled-report-" + hex.EncodeToString(nonce[:])
		record.State, record.Attempts, record.LeaseUntil = "generating", record.Attempts+1, now.Add(scheduledReportLease)
		record.AuthorRuntimeRunID, record.UpdatedAt = reportRunID, now
		claim := scheduledReportDBRecord(record)
		claimed, err := s.store.CompareAndSwapRecord(ctx, scheduledReportTable, key, raw, claim)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, min(20*time.Second, record.Deadline.Sub(now)))
		author, failure := s.recovery.Recover(attemptCtx, run, reportRunID)
		cancel()
		// A cancelled sweep still commits its bounded attempt using a short cleanup
		// context. A crashed process is recovered by the persisted lease instead.
		commitCtx, commitCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		fresh, readErr := s.owner.repository.GetRun(commitCtx, run.ScheduledRunID)
		_, freshFingerprint := scheduledReportIdentity(fresh)
		stillAllowed, authErr := s.authorized(commitCtx, run)
		if readErr != nil || authErr != nil || !stillAllowed || freshFingerprint != fingerprint {
			failure = "origin_or_authority_changed"
		}
		record.UpdatedAt, record.LeaseUntil = s.owner.now().UTC(), time.Time{}
		if !record.UpdatedAt.Before(record.Deadline) {
			failure = "recovery_deadline"
		}
		record.TraceRef = author.TraceRef
		if failure == "" && author.Body != "" && personalAssistantScheduledPublicText(author.Body) == author.Body &&
			((author.Source == "trace_final" && author.RuntimeRunID == run.RuntimeRunID) || (author.Source == "supplement" && author.RuntimeRunID == reportRunID)) {
			record.State, record.FailureCode, record.Source = "ready", "", author.Source
			record.AuthorRuntimeRunID, record.Body, record.BodySHA256 = author.RuntimeRunID, author.Body, scheduledReportSHA([]byte(author.Body))
		} else {
			if failure == "" {
				failure = "report_not_public"
			}
			record.State, record.FailureCode, record.RetryAfter = "pending", failure, record.UpdatedAt.Add(scheduledReportRetry)
			if failure == "origin_or_authority_changed" {
				record.State = "withheld"
			}
			if record.Attempts >= scheduledReportMaxAttempts || !record.UpdatedAt.Before(record.Deadline) {
				record.State = "failed"
			}
		}
		_, err = s.store.CompareAndSwapRecord(commitCtx, scheduledReportTable, key, claim, scheduledReportDBRecord(record))
		commitCancel()
		if err != nil {
			return fmt.Errorf("persist scheduled report attempt: %w", err)
		}
		return nil
	}
	return nil
}

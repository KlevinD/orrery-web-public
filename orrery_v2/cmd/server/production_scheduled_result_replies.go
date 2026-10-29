package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"orrery_v2/internal/agent/outputpolicy"
	schedulerapp "orrery_v2/internal/app/scheduler"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
)

const scheduledResultReplyTable = "personal_assistant_scheduled_replies"

// This component is a durable relay, never a writer or model client.
type productionScheduledResultReplies struct {
	store   *db.Store
	reports *productionScheduledReports
}

func newProductionScheduledResultReplies(store *db.Store) *productionScheduledResultReplies {
	return &productionScheduledResultReplies{store: store}
}

func (s *productionScheduledResultReplies) Reply(ctx context.Context, key string, runs []schedulerapp.ScheduledRun) (string, error) {
	if s == nil || s.store == nil || len(runs) != 1 {
		return "", errors.New("scheduled result requires one executing Agent's report")
	}
	report, ok, err := s.Report(ctx, runs[0])
	if err != nil {
		return "", err
	}
	if !ok || personalAssistantScheduledPublicText(report.Body) != report.Body {
		return "", errors.New("scheduled result has no verified public Agent report")
	}
	fingerprint, err := scheduledReportFingerprint(runs[0], report)
	if err != nil {
		return "", err
	}
	if record, found, err := s.store.GetRecord(ctx, scheduledResultReplyTable, key); err != nil {
		return "", err
	} else if found {
		if !scheduledReplyMatchesReport(record, report, fingerprint) {
			return "", errors.New("committed reply does not match the executing Agent's report")
		}
		return report.Body, nil
	}
	record := db.Record{"body": report.Body, "source": "agent_report", "agent_id": report.AgentID, "runtime_run_id": report.RuntimeRunID, "material_sha256": fingerprint}
	created, err := s.store.CreateRecord(ctx, scheduledResultReplyTable, key, record)
	if err != nil {
		return "", err
	}
	if !created {
		return s.Reply(ctx, key, runs)
	}
	return report.Body, nil
}

func scheduledReportFingerprint(run schedulerapp.ScheduledRun, report schedulerapp.AgentReport) (string, error) {
	material, err := json.Marshal(struct {
		Tenant string                   `json:"tenant_id"`
		RunID  string                   `json:"scheduled_run_id"`
		Status schedulerapp.RunStatus   `json:"status"`
		Report schedulerapp.AgentReport `json:"report"`
	}{run.TenantID, run.ScheduledRunID, run.Status, report})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(material)
	return hex.EncodeToString(digest[:]), nil
}

func scheduledReplyMatchesReport(record db.Record, report schedulerapp.AgentReport, fingerprint string) bool {
	return record["source"] == "agent_report" && record["body"] == report.Body &&
		record["agent_id"] == report.AgentID && record["runtime_run_id"] == report.RuntimeRunID && record["material_sha256"] == fingerprint
}

// Recheck source identity and output policy immediately before every retry.
func (s *productionScheduledResultReplies) ValidateReport(ctx context.Context, delivery gwoutbound.Delivery, run schedulerapp.ScheduledRun) (bool, error) {
	if s == nil || s.store == nil {
		return false, nil
	}
	report, ok, err := s.Report(ctx, run)
	if err != nil {
		return false, err
	}
	if !ok || delivery.Body != report.Body || personalAssistantScheduledPublicText(report.Body) != report.Body {
		return false, nil
	}
	fingerprint, err := scheduledReportFingerprint(run, report)
	if err != nil {
		return false, err
	}
	record, found, err := s.store.GetRecord(ctx, scheduledResultReplyTable, delivery.IdempotencyKey)
	return err == nil && found && scheduledReplyMatchesReport(record, report, fingerprint), err
}

func (s *productionScheduledResultReplies) Report(ctx context.Context, run schedulerapp.ScheduledRun) (schedulerapp.AgentReport, bool, error) {
	if s != nil && s.reports != nil {
		return s.reports.Report(ctx, run)
	}
	report, ok := schedulerapp.PublicAgentReport(run)
	return report, ok, nil
}

// Retries read exactly the Agent-authored text committed before enqueueing.
// Previously queued model explanations and platform templates fail closed.
func (s *productionScheduledResultReplies) Validate(ctx context.Context, delivery gwoutbound.Delivery) (bool, error) {
	if s == nil || s.store == nil || delivery.Body == "" || delivery.Title != "" {
		return false, nil
	}
	record, found, err := s.store.GetRecord(ctx, scheduledResultReplyTable, delivery.IdempotencyKey)
	if err != nil || !found {
		return false, err
	}
	return record["body"] == delivery.Body && record["source"] == "agent_report", nil
}

// Privacy filtering can withhold unsafe text; it never supplies new dialogue.
func personalAssistantScheduledPublicText(value string) string {
	return outputpolicy.PublicReportText(value)
}

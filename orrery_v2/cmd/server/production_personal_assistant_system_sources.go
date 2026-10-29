package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/logging"
)

type personalAssistantSystemCursor struct {
	At int64  `json:"at"`
	ID string `json:"id"`
}

type personalAssistantSystemSourceRow struct {
	ID       string
	Position personalAssistantSystemCursor
}

type personalAssistantSystemUpdate struct {
	ID, TenantID, Kind, State, Category string
	Revision                            int64
	At                                  time.Time
	Access                              graphprotocol.AccessRef
	Facts                               map[string]any
	DiagnosticsHidden                   bool
}

type personalAssistantSystemSource interface {
	List(context.Context, string, string, personalAssistantSystemCursor) ([]personalAssistantSystemSourceRow, error)
	Current(context.Context, string, string, string, bool) (personalAssistantSystemUpdate, bool, error)
}

type personalAssistantAutomaticRepairReader interface {
	AutomaticRepair(string, string) (*reliability.AutomaticRepairReceipt, error)
}

// Read the original ledgers. Notifications do not create another issue, RSI
// case or repair, and never use a log line as a business-success verdict.
type productionPersonalAssistantSystemSource struct {
	core    *db.Store
	repairs personalAssistantAutomaticRepairReader
}

func (s productionPersonalAssistantSystemSource) List(ctx context.Context, tenant, kind string, after personalAssistantSystemCursor) ([]personalAssistantSystemSourceRow, error) {
	var statement string
	var args []any
	switch kind {
	case "failure", "execution":
		table, id := "reliability_failure_incidents", "id"
		if kind == "execution" {
			table, id = "reliability_repair_executions", "action_run_id"
		}
		statement = fmt.Sprintf(`SELECT %s,updated_at FROM %s WHERE tenant_id=? AND (updated_at>? OR (updated_at=? AND %s>?)) ORDER BY updated_at,%s LIMIT 100`, id, table, id, id)
		args = []any{tenant, after.At, after.At, after.ID}
	case "rsi":
		payload := `json_extract(payload_json,'$.payload')`
		clock := `CAST((julianday(json_extract(p,'$.updated_at'))-2440587.5)*86400000000000 AS INTEGER)`
		tenantExpr, stateExpr := `json_extract(p,'$.tenant_id')`, `json_extract(p,'$.state')`
		if s.core.Dialect() == db.DialectPostgres {
			payload = `(payload_json::jsonb->>'payload')::jsonb`
			clock = `CAST(extract(epoch from (p->>'updated_at')::timestamptz)*1000000000 AS BIGINT)`
			tenantExpr, stateExpr = `p->>'tenant_id'`, `p->>'state'`
		}
		statement = fmt.Sprintf(`WITH cases AS (SELECT record_key,%s p FROM platform_records WHERE table_name='ontology_improvement_cases'), positions AS (SELECT record_key,p,%s pos FROM cases WHERE %s=? AND %s<>'observed') SELECT record_key,pos,p FROM positions WHERE pos>? OR (pos=? AND record_key>?) ORDER BY pos,record_key LIMIT 100`, payload, clock, tenantExpr, stateExpr)
		args = []any{tenant, after.At, after.At, after.ID}
	default:
		return nil, errors.New("unknown system update source")
	}
	rows, err := s.core.Query(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []personalAssistantSystemSourceRow
	for rows.Next() {
		var row personalAssistantSystemSourceRow
		if kind == "rsi" {
			var payload string
			if err := rows.Scan(&row.Position.ID, &row.Position.At, &payload); err != nil {
				return nil, err
			}
			var c improvement.Case
			if err := json.Unmarshal([]byte(payload), &c); err != nil {
				return nil, err
			}
			row.ID = c.ID
		} else {
			if err := rows.Scan(&row.ID, &row.Position.At); err != nil {
				return nil, err
			}
			row.Position.ID = row.ID
		}
		out = append(out, row)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return nil, rowsErr
	}
	if kind == "failure" {
		repo := reliability.NewPlatformRepository(s.core)
		for index := range out {
			incident, err := repo.GetIncident(ctx, tenant, out[index].ID)
			if err != nil {
				return nil, err
			}
			if group := postgresNoticeID(incident); group != "" {
				out[index].ID = group
			}
		}
	}
	return out, nil
}

func (s productionPersonalAssistantSystemSource) Current(ctx context.Context, tenant, kind, id string, includeDiagnostics bool) (personalAssistantSystemUpdate, bool, error) {
	u := personalAssistantSystemUpdate{ID: id, TenantID: tenant, Kind: kind, Facts: map[string]any{}, DiagnosticsHidden: !includeDiagnostics}
	repo := reliability.NewPlatformRepository(s.core)
	switch kind {
	case "failure":
		if strings.HasPrefix(id, postgresNoticePrefix) {
			return s.postgresGroup(ctx, tenant, id, includeDiagnostics)
		}
		i, err := repo.GetIncident(ctx, tenant, id)
		if errors.Is(err, reliability.ErrNotFound) {
			return u, false, nil
		}
		if err != nil {
			return u, false, err
		}
		// Pending individual notices are superseded by the dependency group and
		// are rejected again by the existing send-time source validator.
		if postgresNoticeID(i) != "" {
			return u, false, nil
		}
		// A broken notification must not generate notifications about itself.
		component := strings.ReplaceAll(strings.ToLower(i.Component), "_", "-")
		if i.Source == "gateway_notification" || strings.Contains(component, "notification") || strings.Contains(component, "agent-notice") || strings.Contains(component, "scheduled-report") || (i.Severity == reliability.FailureSeverityWarning && i.Component != "storage_capacity") {
			return u, false, nil
		}
		if i.Source == "agent_runtime" {
			var observationID string
			if err := s.core.QueryRow(ctx, `SELECT id FROM reliability_failure_observations WHERE tenant_id=? AND incident_id=? ORDER BY occurred_at DESC,id DESC LIMIT 1`, tenant, id).Scan(&observationID); err != nil {
				return u, false, err
			}
			if strings.HasPrefix(observationID, "runtime:agent-notice-") || strings.HasPrefix(observationID, "runtime:scheduled-report-") {
				return u, false, nil
			}
		}
		u.State, u.Revision, u.At, u.Category = string(i.Status)+":"+string(i.Severity), i.Version, i.UpdatedAt, "system_error"
		u.Facts = map[string]any{"status": i.Status, "severity": i.Severity, "component": logging.RedactDiagnostic(i.Component, 160), "category": i.Category, "error_code": logging.RedactDiagnostic(i.ErrorCode, 160), "summary": logging.RedactDiagnostic(i.RedactedSummary, 800), "occurrences": i.OccurrenceCount}
		if includeDiagnostics && s.repairs != nil {
			r, err := s.repairs.AutomaticRepair(tenant, id)
			if err != nil {
				return u, false, err
			}
			if r != nil && r.CurrentResult(i) && !r.UpdatedAt.IsZero() {
				valid := r.Status == "pending" || r.Status == "running" || r.Status == "retry_wait" || r.Status == "technical_recovered" || r.Status == "needs_attention"
				valid = valid || (r.Status == "resolved" && r.Verified && r.RepairRunID != "" && len(r.Evidence) > 0)
				if valid {
					u.State, u.Category, u.At = r.Status, "automatic_repair", r.UpdatedAt
					u.Facts["repair_status"], u.Facts["host_verified"], u.Facts["repair_summary"] = r.Status, r.Verified, logging.RedactDiagnostic(r.Summary, 800)
					u.Facts["evidence_count"] = len(r.Evidence)
					u.Facts["business_acceptance"] = "separate_from_technical_repair_receipt"
				}
			}
		}
		if includeDiagnostics && i.Status == reliability.IncidentStatusResolved {
			proof, err := repo.DependencyRecovery(ctx, i)
			if err != nil {
				return u, false, err
			}
			if proof != nil {
				u.Facts["host_verified"] = true
				u.Facts["dependency_recovery"] = proof
				u.Facts["business_acceptance"] = "not_established_by_dependency_recovery"
			}
		}
	case "execution":
		e, err := repo.GetRepairExecution(ctx, tenant, id)
		if errors.Is(err, reliability.ErrNotFound) {
			return u, false, nil
		}
		if err != nil {
			return u, false, err
		}
		u.State, u.Revision, u.At, u.Category = string(e.Status), e.Version, e.UpdatedAt, "automatic_repair"
		u.Facts = map[string]any{"repair_status": e.Status, "strategy": e.Strategy, "failure_code": logging.RedactDiagnostic(e.FailureCode, 160), "has_retry_run": e.RetryRunID != "", "business_acceptance": "not_established_by_execution_status"}
	case "rsi":
		c, err := improvement.NewStore(s.core).Get(ctx, tenant, id)
		if errors.Is(err, improvement.ErrNotFound) {
			return u, false, nil
		}
		if err != nil {
			return u, false, err
		}
		if c.State == improvement.Observed || strings.HasPrefix(c.Observation.Trigger, "managed_repair_") || strings.Contains(c.Observation.Run.ID, "agent-notice-") || strings.Contains(c.Observation.Run.ID, "scheduled-report-") {
			return u, false, nil
		}
		u.State, u.Revision, u.At, u.Category, u.Access = string(c.State), c.Revision, c.UpdatedAt, "rsi_progress", c.Observation.SourceAccessRef
		if u.Access.Kind == "" || u.Access.ID == "" || u.Access.TenantID == "" {
			return u, false, nil
		}
		u.Facts = map[string]any{"stage": c.State, "summary": logging.RedactDiagnostic(c.Observation.Summary, 800), "reason": logging.RedactDiagnostic(c.Reason, 800), "has_change_set": c.ChangeSet != nil, "has_experiment": c.Experiment != nil, "has_evaluation": c.Evaluation != nil, "has_activation_receipt": c.Release != nil, "has_outcome_observation": c.Outcome != nil, "has_rollback_receipt": c.Rollback != nil, "business_acceptance": "separate_from_rsi_stage"}
		if c.Diagnosis != nil {
			u.Facts["diagnosis"] = logging.RedactDiagnostic(c.Diagnosis.Summary, 800)
		}
		if c.ChangeSet != nil {
			u.Facts["goal_metric"] = logging.RedactDiagnostic(c.ChangeSet.Goal.Metric, 160)
			u.Facts["minimum_gain"] = c.ChangeSet.Goal.MinimumGain
		}
		if c.Evaluation != nil {
			u.Facts["evaluation"] = map[string]any{"baseline_score": c.Evaluation.BaselineScore, "candidate_score": c.Evaluation.CandidateScore, "cost": c.Evaluation.Cost, "duration_ms": c.Evaluation.DurationMS, "held_out_passed": c.Evaluation.HeldOutPassed, "business_constraints_passed": c.Evaluation.BusinessConstraintsPassed, "safety_passed": c.Evaluation.SafetyPassed}
		}
	default:
		return u, false, errors.New("unknown system update source")
	}
	return u, u.Revision > 0 && !u.At.IsZero(), nil
}

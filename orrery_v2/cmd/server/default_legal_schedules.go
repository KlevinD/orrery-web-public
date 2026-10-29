package main

import (
	"context"
	"errors"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
)

type defaultLegalScheduleSeed struct {
	id          string
	title       string
	description string
	cron        string
	agentID     string
	taskGoal    string
	prompt      string
}

func ensureDefaultLegalScheduledTasks(ctx context.Context, repo *schedulerapp.Repository, now time.Time) error {
	if repo == nil {
		return nil
	}
	for _, seed := range defaultLegalScheduleSeeds() {
		if existing, err := repo.GetTask(ctx, seed.id); err == nil {
			if existing.CreatorProfileID == "" {
				existing.CreatorProfileID = seed.agentID
				existing.CreatedByKind = schedulerapp.ViewerKindSystem
				existing.CreatedByName = "Legal scheduler bootstrap"
				existing.UpdatedAt = now.UTC()
				if err := repo.SaveTask(ctx, existing); err != nil {
					return err
				}
			}
			continue
		} else if !errors.Is(err, schedulerapp.ErrNotFound) {
			return err
		}
		if err := repo.SaveTask(ctx, defaultLegalScheduledTask(seed, now)); err != nil {
			return err
		}
	}
	return nil
}

func defaultLegalScheduleSeeds() []defaultLegalScheduleSeed {
	return []defaultLegalScheduleSeed{
		{
			id:          "legal-schedule-policy-intelligence-daily",
			title:       "Legal policy intelligence daily",
			description: "Disabled default Legal Agent schedule for daily policy intelligence and legal knowledge pipeline review.",
			cron:        "30 9 * * *",
			agentID:     defaultLegalKnowledgeAgentID.String(),
			taskGoal:    "Review Legal MCP policy intelligence, PageIndex/RAG pipeline status, and legal knowledge source gaps.",
			prompt:      "Use Legal MCP tools legal_policy_intelligence_summary, legal_policy_pipeline_status, legal_enterprise_search, and legal_audit_trail. Produce evidence refs, missing-source gaps, and human review gates only; do not execute external connectors.",
		},
		{
			id:          "legal-schedule-contract-queue-daily",
			title:       "Legal contract queue daily",
			description: "Disabled default Legal Agent schedule for daily contract queue readiness, renewal, obligation, and review risk triage.",
			cron:        "0 10 * * *",
			agentID:     defaultLegalContractAgentID.String(),
			taskGoal:    "Review Legal MCP contract queue readiness, renewal/obligation signals, and high-risk review items.",
			prompt:      "Use Legal MCP tools legal_contract_review_run, legal_enterprise_search, legal_human_review_run, and legal_audit_trail. Return queue summary, evidence refs, risk tiers, and required human review gates.",
		},
		{
			id:          "legal-schedule-compliance-risk-weekly",
			title:       "Legal compliance risk weekly",
			description: "Disabled default Legal Agent schedule for weekly compliance risk and mitigation review.",
			cron:        "0 11 * * 1",
			agentID:     defaultLegalComplianceAgentID.String(),
			taskGoal:    "Review Legal MCP compliance risk, risk register, mitigation, policy changes, and open evidence gaps.",
			prompt:      "Use Legal MCP tools legal_compliance_risk_run, legal_policy_intelligence_summary, legal_enterprise_search, legal_human_review_run, and legal_audit_trail. Return evidence-backed risks, mitigations, and human review gates.",
		},
		{
			id:          "legal-schedule-operations-summary-daily",
			title:       "Legal operations summary daily",
			description: "Disabled default Legal Agent schedule for daily Legal MCP operational queue and audit summary.",
			cron:        "30 17 * * *",
			agentID:     defaultLegalOrchestratorAgentID.String(),
			taskGoal:    "Summarize Legal MCP operations, intake queue, contract/compliance workload, audit events, and blocked handoffs.",
			prompt:      "Use Legal MCP tools legal_operations_summary, legal_intake_run, legal_audit_trail, legal_connector_handoff_record, and legal_agent_toolbox. Return operational summary, blocked items, evidence refs, and next recommended agent actions.",
		},
	}
}

func defaultLegalScheduledTask(seed defaultLegalScheduleSeed, now time.Time) schedulerapp.ScheduledTask {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	return schedulerapp.ScheduledTask{
		TenantID:        "system",
		ScheduledTaskID: seed.id,
		Title:           seed.title,
		Description:     seed.description,
		Schedule: schedulerapp.Schedule{
			Type:     schedulerapp.ScheduleTypeCron,
			Cron:     seed.cron,
			Timezone: "Asia/Shanghai",
		},
		Target: schedulerapp.Target{
			Mode:     schedulerapp.TargetModeAgentTask,
			AgentID:  seed.agentID,
			TaskGoal: seed.taskGoal,
			Prompt:   seed.prompt,
		},
		Policy: schedulerapp.Policy{
			Misfire:     schedulerapp.MisfireRunOnce,
			Concurrency: schedulerapp.ConcurrencyAllowParallel,
			MaxRetries:  1,
			RetryDelay:  "10m",
			FailureMode: "record_failure",
		},
		Status:           schedulerapp.TaskStatusDisabled,
		Enabled:          false,
		NextRunAt:        nextLegalScheduleRun(seed.cron, now),
		CreatedBy:        "system:legal-bootstrap",
		CreatedByKind:    schedulerapp.ViewerKindSystem,
		CreatedByName:    "Legal scheduler bootstrap",
		CreatorProfileID: seed.agentID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func nextLegalScheduleRun(cron string, now time.Time) time.Time {
	switch cron {
	case "30 9 * * *":
		return nextDailyLegalSchedule(now, 9, 30)
	case "0 10 * * *":
		return nextDailyLegalSchedule(now, 10, 0)
	case "0 11 * * 1":
		return nextWeeklyLegalSchedule(now, time.Monday, 11, 0)
	case "30 17 * * *":
		return nextDailyLegalSchedule(now, 17, 30)
	default:
		return time.Time{}
	}
}

func nextDailyLegalSchedule(now time.Time, hour int, minute int) time.Time {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.Time{}
	}
	localNow := now.In(location)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, location)
	if !next.After(localNow) {
		next = next.AddDate(0, 0, 1)
	}
	return next.UTC()
}

func nextWeeklyLegalSchedule(now time.Time, weekday time.Weekday, hour int, minute int) time.Time {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.Time{}
	}
	localNow := now.In(location)
	daysUntil := (int(weekday) - int(localNow.Weekday()) + 7) % 7
	nextDate := localNow.AddDate(0, 0, daysUntil)
	next := time.Date(nextDate.Year(), nextDate.Month(), nextDate.Day(), hour, minute, 0, 0, location)
	if !next.After(localNow) {
		next = next.AddDate(0, 0, 7)
	}
	return next.UTC()
}

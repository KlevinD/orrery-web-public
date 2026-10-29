package main

import (
	"context"
	"errors"
	"strings"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
)

type ontologyStewardScheduleSeed struct {
	id, title, description, cron, goal, prompt string
}

const (
	defaultOntologyStewardScheduleCreatedBy   = "system:ontology-steward-bootstrap"
	ontologyStewardResultProtocolMarker       = "Orrery result manifest v4."
	legacyOntologyStewardResultProtocolPrompt = `Orrery result manifest: expected_deliverable_id must be exactly "deliverable-result"; evidence_requirement_id must be exactly "evidence-acceptance-1"; acceptance_criterion_id must be exactly "acceptance-1". Finish with an orrery-agent-result JSON block, and use evidence source_kind "tool_log" with an actual successful tool call ID in source_ref. Do not invent evidence or report completion when required evidence is unavailable.`
	ontologyStewardResultProtocolV2Prompt     = "Orrery result manifest v2. Finish with exactly one result manifest. The opening fence must be exactly ```orrery-agent-result (never ```json).\n" +
		"Use this exact structure and literal contract IDs; replace only the summary and ACTUAL_SUCCESSFUL_TOOL_CALL_ID:\n" +
		"```orrery-agent-result\n" +
		`{"version":1,"summary":"Brief factual result.","deliverables":[{"expected_deliverable_id":"deliverable-result","source_kind":"runtime_output","source_ref":""}],"evidence":[{"evidence_requirement_id":"evidence-acceptance-1","acceptance_criterion_id":"acceptance-1","source_kind":"tool_log","source_ref":"ACTUAL_SUCCESSFUL_TOOL_CALL_ID"}]}` + "\n" +
		"```\n" +
		"Do not add expected_deliverable_id at the top level. The deliverable must use runtime_output with an empty source_ref; only evidence uses tool_log with an actual successful tool call ID from this run. Do not invent evidence or report completion when required evidence is unavailable."
	ontologyStewardResultProtocolV3Prompt = "Orrery result manifest v3. Finish with exactly one result manifest. The opening fence must be exactly ```orrery-agent-result (never ```json).\n" +
		"Use this exact structure and literal contract IDs; replace only the summary and call_ACTUAL_SUCCESSFUL_TOOL_CALL_ID:\n" +
		"```orrery-agent-result\n" +
		`{"version":1,"summary":"Brief factual result.","deliverables":[{"expected_deliverable_id":"deliverable-result","source_kind":"runtime_output","source_ref":""}],"evidence":[{"evidence_requirement_id":"evidence-acceptance-1","acceptance_criterion_id":"acceptance-1","source_kind":"tool_log","source_ref":"call_ACTUAL_SUCCESSFUL_TOOL_CALL_ID"}]}` + "\n" +
		"```\n" +
		"Do not add expected_deliverable_id at the top level. The deliverable must use runtime_output with an empty source_ref. Only evidence uses tool_log. Prefer the actual ToolCall.ID in source_ref; it starts with call_. Never confuse an output document_id or artifact_id with ToolCall.ID. If the runtime exposes only a unique document_id or artifact_id returned by the successful call, use that exact returned identifier; Orrery accepts it only when it uniquely maps to one non-failed call from this run. Do not invent evidence or report completion when required evidence is unavailable."
	ontologyStewardResultProtocolPrompt = "Orrery result manifest v4. Finish with exactly one result manifest. The opening fence must be exactly ```orrery-agent-result (never ```json).\n" +
		"Use this exact structure and literal contract IDs for completed; replace only the summary and call_ACTUAL_SUCCESSFUL_TOOL_CALL_ID:\n" +
		"```orrery-agent-result\n" +
		`{"version":1,"outcome":"completed","summary":"Brief factual result.","deliverables":[{"expected_deliverable_id":"deliverable-result","source_kind":"runtime_output","source_ref":""}],"evidence":[{"evidence_requirement_id":"evidence-acceptance-1","acceptance_criterion_id":"acceptance-1","source_kind":"tool_log","source_ref":"call_ACTUAL_SUCCESSFUL_TOOL_CALL_ID"}]}` + "\n" +
		"```\n" +
		"Set outcome to completed only when every acceptance criterion is satisfied. Otherwise set outcome to blocked, state the concrete blocker in summary, and set deliverables and evidence to empty arrays. Do not add expected_deliverable_id at the top level. For completed, the deliverable must use runtime_output with an empty source_ref. Only evidence uses tool_log. Prefer the actual ToolCall.ID in source_ref; it starts with call_. Never confuse an output document_id or artifact_id with ToolCall.ID. If the runtime exposes only a unique document_id or artifact_id returned by the successful call, use that exact returned identifier; Orrery accepts it only when it uniquely maps to one non-failed call from this run. Do not invent evidence or report completion when required evidence is unavailable."
)

func ensureDefaultOntologyStewardScheduledTasks(ctx context.Context, repo *schedulerapp.Repository, now time.Time) error {
	if repo == nil {
		return nil
	}
	for _, seed := range ontologyStewardScheduleSeeds() {
		if task, err := repo.GetTask(ctx, seed.id); err == nil {
			if task.CreatedBy != defaultOntologyStewardScheduleCreatedBy {
				continue
			}
			prompt := ontologyStewardPromptWithResultProtocol(task.Target.Prompt)
			if prompt == task.Target.Prompt {
				continue
			}
			task.Target.Prompt = prompt
			if now.IsZero() {
				task.UpdatedAt = time.Now().UTC()
			} else {
				task.UpdatedAt = now.UTC()
			}
			if err := repo.SaveTask(ctx, task); err != nil {
				return err
			}
			continue
		} else if !errors.Is(err, schedulerapp.ErrNotFound) {
			return err
		}
		task, err := defaultOntologyStewardScheduledTask(seed, now)
		if err != nil {
			return err
		}
		if err := repo.SaveTask(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

func ontologyStewardPromptWithResultProtocol(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if strings.Contains(prompt, ontologyStewardResultProtocolMarker) {
		return prompt
	}
	for _, previous := range []string{ontologyStewardResultProtocolV3Prompt, ontologyStewardResultProtocolV2Prompt, legacyOntologyStewardResultProtocolPrompt} {
		if strings.Contains(prompt, previous) {
			return strings.Replace(prompt, previous, ontologyStewardResultProtocolPrompt, 1)
		}
	}
	if prompt == "" {
		return ontologyStewardResultProtocolPrompt
	}
	return prompt + "\n\n" + ontologyStewardResultProtocolPrompt
}

func ontologyStewardScheduleSeeds() []ontologyStewardScheduleSeed {
	return []ontologyStewardScheduleSeed{
		{
			id: "ontology-steward-integrity-hourly", title: "Ontology integrity review", cron: "15 * * * *",
			description: "Checks concrete ontology, mapping, projection, permission, and knowledge binding failures.",
			goal:        "Find and resolve concrete ontology integrity failures observed since the previous review.",
			prompt:      "Inspect authorized enterprise graph state and current failure evidence. Repair only evidence-backed, reversible issues through the governed ontology tools. Do not invent missing business meaning. Record changed and unresolved items in knowledge; request ding approval for high-risk changes.",
		},
		{
			id: "ontology-steward-business-alignment-daily", title: "Ontology business alignment", cron: "30 8 * * *",
			description: "Daily comparison of ontology semantics with current Minvia business ownership and workflows.",
			goal:        "Reconcile ontology semantics with current business responsibilities and authoritative systems.",
			prompt:      "Use enterprise graph and all readable Minvia knowledge to verify object definitions, relationships, actions, owners, source systems, and mapping rules. Create governed proposals only for evidence-backed drift. Apply approved plans and archive evidence; route conflicts or material changes to ding.",
		},
		{
			id: "ontology-steward-source-onboarding-daily", title: "Ontology source onboarding review", cron: "30 23 * * *",
			description: "Reviews new or changed source systems and closes ontology and knowledge catalog gaps.",
			goal:        "Identify newly connected or changed systems that need ontology and knowledge coverage.",
			prompt:      "Review authorized source-system, knowledge-provider, and enterprise graph evidence added since the previous run. For each concrete system such as people or marketing, identify its business boundary, authoritative objects, relationships, actions, owner, permissions, and mapping rules before creating governed proposals. Do not apply high-risk changes without ding approval.",
		},
	}
}

func defaultOntologyStewardScheduledTask(seed ontologyStewardScheduleSeed, now time.Time) (schedulerapp.ScheduledTask, error) {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	schedule := schedulerapp.Schedule{Type: schedulerapp.ScheduleTypeCron, Cron: seed.cron, Timezone: "Asia/Shanghai"}
	next, err := schedulerapp.NextOccurrence(schedule, now)
	if err != nil {
		return schedulerapp.ScheduledTask{}, err
	}
	return schedulerapp.ScheduledTask{
		TenantID: "system", ScheduledTaskID: seed.id, Title: seed.title, Description: seed.description,
		Schedule: schedule,
		Target:   schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: defaultOntologyStewardAgentID, TaskGoal: seed.goal, Prompt: ontologyStewardPromptWithResultProtocol(seed.prompt)},
		Policy:   schedulerapp.Policy{Misfire: schedulerapp.MisfireRunOnce, Concurrency: schedulerapp.ConcurrencyAllowParallel, MaxRetries: 1, RetryDelay: "10m", FailureMode: "record_failure"},
		Status:   schedulerapp.TaskStatusEnabled, Enabled: true, NextRunAt: next,
		CreatedBy: defaultOntologyStewardScheduleCreatedBy, CreatedByKind: schedulerapp.ViewerKindSystem,
		CreatedByName: "Ontology steward bootstrap", CreatorProfileID: defaultOntologyStewardAgentID,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/graphprotocol"
)

type ontologyInitializationBaselineAdapter struct {
	config config.Config
	runner *ontology.MappingRuleProjectionRunner
}

func newOntologyInitializationBaselineAdapter(cfg config.Config, runner *ontology.MappingRuleProjectionRunner) ontologyInitializationAdapter {
	return ontologyInitializationBaselineAdapter{config: cfg, runner: runner}
}

func (ontologyInitializationBaselineAdapter) Name() string { return "managed_baseline" }

func (a ontologyInitializationBaselineAdapter) Reconcile(ctx context.Context, _ ontologyInitializationRequest) (ontologyInitializationDomainReport, error) {
	report := ontologyInitializationDomainReport{Name: a.Name()}
	if a.runner == nil {
		return report, fmt.Errorf("ontology initialization baseline projector is required")
	}
	events, err := defaultOntologyBootstrapEvents(a.config)
	if err != nil {
		return report, err
	}
	report.Source = len(events)
	for _, event := range events {
		if err := applyOntologyInitializationEvents(ctx, a.runner, []graphprotocol.DomainEvent{event}); err != nil {
			report.Failed++
			return report, err
		}
		report.Projected++
	}
	return report, nil
}

type ontologyInitializationSchedulerSource interface {
	ListTasks(context.Context, bool) ([]schedulerapp.ScheduledTask, error)
	ListRunsForTask(context.Context, string) ([]schedulerapp.ScheduledRun, error)
}

type ontologyInitializationSchedulerAdapter struct {
	source ontologyInitializationSchedulerSource
	runner *ontology.MappingRuleProjectionRunner
}

func newOntologyInitializationSchedulerAdapter(source ontologyInitializationSchedulerSource, runner *ontology.MappingRuleProjectionRunner) ontologyInitializationAdapter {
	return ontologyInitializationSchedulerAdapter{source: source, runner: runner}
}

func (ontologyInitializationSchedulerAdapter) Name() string { return "scheduler" }

func (a ontologyInitializationSchedulerAdapter) Reconcile(ctx context.Context, _ ontologyInitializationRequest) (ontologyInitializationDomainReport, error) {
	report := ontologyInitializationDomainReport{Name: a.Name()}
	if a.source == nil || a.runner == nil {
		return report, fmt.Errorf("ontology initialization scheduler dependencies are required")
	}
	tasks, err := a.source.ListTasks(ctx, true)
	if err != nil {
		return report, err
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ScheduledTaskID < tasks[j].ScheduledTaskID })
	for _, task := range tasks {
		task.AgentTaskID = firstNonBlank(task.AgentTaskID, schedulerapp.AgentTaskIDForScheduledTask(task.ScheduledTaskID))
		report.Source++
		if err := applyOntologyInitializationEvents(ctx, a.runner, scheduledTaskOntologyEvents(task)); err != nil {
			report.Failed++
			return report, err
		}
		report.Projected++

		runs, err := a.source.ListRunsForTask(ctx, task.ScheduledTaskID)
		if err != nil {
			return report, err
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i].ScheduledRunID < runs[j].ScheduledRunID })
		for _, run := range runs {
			report.Source++
			if err := applyOntologyInitializationEvents(ctx, a.runner, scheduledRunOntologyEvents(task, run)); err != nil {
				report.Failed++
				return report, err
			}
			report.Projected++
			if run.TargetMode == schedulerapp.TargetModeAgentTask && schedulerRunIsTerminal(run.Status) {
				report.Unresolved++
				report.Issues = append(report.Issues, ontologyInitializationIssue{
					Domain: a.Name(), SourceRef: strings.TrimSpace(run.ScheduledRunID), Code: "historical_agent_run_source_unavailable",
				})
			}
		}
	}
	return report, nil
}

func schedulerRunIsTerminal(status schedulerapp.RunStatus) bool {
	switch status {
	case schedulerapp.RunStatusCompleted, schedulerapp.RunStatusFailed, schedulerapp.RunStatusSkipped:
		return true
	default:
		return false
	}
}

func applyOntologyInitializationEvents(ctx context.Context, runner *ontology.MappingRuleProjectionRunner, events []graphprotocol.DomainEvent) error {
	for _, event := range events {
		if _, err := runner.ApplyEvent(ctx, event); err != nil {
			return fmt.Errorf("project ontology initialization event %s: %w", event.Type, err)
		}
	}
	return nil
}

type ontologyInitializationActionSource interface {
	ListActionRuns(context.Context, actioncontract.ActionRunQuery) (actioncontract.ActionRunPage, error)
}

type ontologyInitializationActionAdapter struct {
	source ontologyInitializationActionSource
	runner *ontology.MappingRuleProjectionRunner
	graph  contextgraph.Store
}

func newOntologyInitializationActionAdapter(source ontologyInitializationActionSource, runner *ontology.MappingRuleProjectionRunner, graphs ...contextgraph.Store) ontologyInitializationAdapter {
	adapter := ontologyInitializationActionAdapter{source: source, runner: runner}
	if len(graphs) > 0 {
		adapter.graph = graphs[0]
	}
	return adapter
}

func (ontologyInitializationActionAdapter) Name() string { return "actions_hil" }

func (a ontologyInitializationActionAdapter) Reconcile(ctx context.Context, request ontologyInitializationRequest) (ontologyInitializationDomainReport, error) {
	report := ontologyInitializationDomainReport{Name: a.Name()}
	if a.source == nil || a.runner == nil {
		return report, fmt.Errorf("ontology initialization action dependencies are required")
	}
	for offset := 0; ; {
		page, err := a.source.ListActionRuns(ctx, actioncontract.ActionRunQuery{Limit: request.PageSize, Offset: offset})
		if err != nil {
			return report, err
		}
		sort.Slice(page.Runs, func(i, j int) bool { return page.Runs[i].ID < page.Runs[j].ID })
		for _, run := range page.Runs {
			report.Source++
			events, err := actionRunOntologyEvents(run)
			if err != nil {
				report.Failed++
				return report, err
			}
			events, issues, err := a.filterHistoricalReferences(ctx, run, events)
			if err != nil {
				return report, err
			}
			report.Unresolved += len(issues)
			report.Issues = append(report.Issues, issues...)
			if err := applyOntologyInitializationEvents(ctx, a.runner, events); err != nil {
				report.Failed++
				return report, err
			}
			report.Projected++
		}
		offset += page.Page.Returned
		if offset >= page.Page.Total || page.Page.Returned == 0 {
			break
		}
	}
	return report, nil
}

func (a ontologyInitializationActionAdapter) filterHistoricalReferences(ctx context.Context, run actioncontract.ActionRun, events []graphprotocol.DomainEvent) ([]graphprotocol.DomainEvent, []ontologyInitializationIssue, error) {
	objects := []contextgraph.Object(nil)
	if a.graph != nil {
		var err error
		objects, _, _, _, err = a.graph.Snapshot(ctx, run.TenantID)
		if err != nil {
			return nil, nil, err
		}
	}
	filtered := make([]graphprotocol.DomainEvent, 0, len(events))
	issues := make([]ontologyInitializationIssue, 0)
	for _, event := range events {
		objectType, objectID, code := actionHistoryEventEndpoint(event)
		if objectType == "" || contextGraphObjectExistsInSnapshot(objects, objectType, objectID) {
			filtered = append(filtered, event)
			continue
		}
		issues = append(issues, ontologyInitializationIssue{Domain: a.Name(), SourceRef: string(run.ID), Code: code})
	}
	return filtered, issues, nil
}

func actionHistoryEventEndpoint(event graphprotocol.DomainEvent) (string, string, string) {
	switch event.Type {
	case "agent.runtime.action.requested":
		return "agent_run", strings.TrimSpace(fmt.Sprint(event.Payload["agent_run_id"])), "historical_agent_run_source_unavailable"
	case "action.run.evidence.attached":
		return "evidence_item", strings.TrimSpace(fmt.Sprint(event.Payload["evidence_id"])), "historical_evidence_payload_unavailable"
	default:
		if strings.HasPrefix(event.Type, "action.run.targeted.") {
			return strings.TrimPrefix(event.Type, "action.run.targeted."), strings.TrimSpace(fmt.Sprint(event.Payload["object_id"])), "historical_target_unavailable"
		}
		return "", "", ""
	}
}

func contextGraphObjectExistsInSnapshot(objects []contextgraph.Object, objectType, objectID string) bool {
	for _, object := range objects {
		if object.ObjectType == objectType && object.ID == objectID {
			return true
		}
	}
	return false
}

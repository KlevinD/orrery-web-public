package main

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestProductionOntologyStateEventClassification(t *testing.T) {
	t.Parallel()
	for _, eventType := range []string{
		"agent.task.upserted",
		"scheduler.task.created",
		"scheduler.task.updated",
		"scheduler.task.cancelled",
		"people.agent.updated",
		"scheduler.task.agent_targeted",
		"agent.version.observed",
	} {
		if !isProductionOntologyStateEvent(eventType) {
			t.Errorf("%s should be classified as state", eventType)
		}
	}
	for _, eventType := range []string{
		"action.run.started",
		"scheduler.run.started",
		"scheduler.run.completed",
		"agent.runtime.approval.required",
		"security.policy.denied",
		"delivery.completed",
	} {
		if isProductionOntologyStateEvent(eventType) {
			t.Errorf("%s should remain a critical event", eventType)
		}
	}
}

func TestProductionOntologyStateEventRejectsBlankEntityID(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-ontology-state-blank-entity")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	projector := productionOntologyStateProjector{
		runner: runner,
		store:  ontology.NewPlatformStateProjectionStore(core),
		now:    func() time.Time { return time.Date(2026, 8, 21, 2, 0, 0, 0, time.UTC) },
	}
	_, err = projector.Apply(ctx, graphprotocol.DomainEvent{
		TenantID: "tenant-state", Type: "agent.task.upserted", Version: 1,
		OccurredAt: time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC),
		Payload:    map[string]any{"id": "agent-task-1"},
	})
	if err == nil {
		t.Fatal("blank state entity ID should fail")
	}
}

func TestProductionAgentExecutionStateProjectionBoundsEventHistory(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-agent-execution-state-projection")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 21, 2, 0, 0, 0, time.UTC)
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, func() time.Time { return now })
	task := schedulerapp.ScheduledTask{
		TenantID:        "tenant-state-projection",
		ScheduledTaskID: "schedule-state-1",
		AgentTaskID:     "scheduled-task:schedule-state-1",
		Title:           "Bound repeated state",
		Target:          schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-state-1"},
		Schedule:        schedulerapp.Schedule{Type: schedulerapp.ScheduleTypeCron, Cron: "0 9 * * *", Timezone: "UTC"},
		Status:          schedulerapp.TaskStatusEnabled,
		Enabled:         true,
		CreatedAt:       now.Add(-time.Hour),
		UpdatedAt:       now.Add(-time.Hour),
	}
	for range 100 {
		if err := sink.ProjectScheduledTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := sink.events.List(ctx); err != nil {
		t.Fatal(err)
	}
	var stateEvents int
	err = core.QueryRow(ctx, `
SELECT COUNT(*) FROM platform_events
WHERE event_type IN (
	'ontology.agent.task.upserted',
	'ontology.scheduler.task.created',
	'ontology.scheduler.task.updated',
	'ontology.scheduler.task.cancelled',
	'ontology.scheduler.task.agent_targeted',
	'ontology.people.agent.updated',
	'ontology.agent.version.observed'
)`).Scan(&stateEvents)
	if err != nil {
		t.Fatal(err)
	}
	if stateEvents != 0 {
		t.Fatalf("state events appended to history = %d, want 0", stateEvents)
	}

	objects, _, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, task.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "scheduled_task", task.ScheduledTaskID)
	assertContextGraphFact(t, facts, "scheduled_task", task.ScheduledTaskID, "title", task.Title)
	var snapshots int
	if err := core.QueryRow(ctx, `
SELECT COUNT(*) FROM ontology_daily_snapshots
WHERE tenant_id = ? AND snapshot_date = ?`, task.TenantID, now.Format("2006-01-02")).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if snapshots != 4 {
		t.Fatalf("daily state snapshots = %d, want 4", snapshots)
	}

	run := schedulerapp.ScheduledRun{
		ScheduledRunID: "run-state-1", ScheduledTaskID: task.ScheduledTaskID,
		Status: schedulerapp.RunStatusRunning, TargetMode: schedulerapp.TargetModeAgentTask,
		CommandID: "run-state-1", AgentID: "agent-state-1", StartedAt: now, CreatedAt: now,
	}
	if err := sink.ProjectScheduledRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	var criticalEvents int
	if err := core.QueryRow(ctx, `
SELECT COUNT(*) FROM platform_events
WHERE event_type = 'ontology.scheduler.run.started'`).Scan(&criticalEvents); err != nil {
		t.Fatal(err)
	}
	if criticalEvents != 1 {
		t.Fatalf("critical scheduled run events = %d, want 1", criticalEvents)
	}
}

func TestProductionOntologyStateMetricContainsOnlyEventTypeAndOutcome(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-ontology-state-metric")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &capturingOntologyMetricRecorder{}
	now := time.Date(2026, 8, 21, 2, 0, 0, 0, time.UTC)
	projector := productionOntologyStateProjector{
		runner:  runner,
		store:   ontology.NewPlatformStateProjectionStore(core),
		now:     func() time.Time { return now },
		metrics: recorder,
	}
	event := scheduledTaskOntologyEvents(ontologyStateRefreshTask(now))[0]
	report, err := projector.Apply(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	metric, found := recorder.last("ontology.state_projection")
	if !found {
		t.Fatal("ontology.state_projection metric was not captured")
	}
	want := map[string]any{"event_type": event.Type, "outcome": string(report.Outcome)}
	if !reflect.DeepEqual(metric.fields, want) {
		t.Fatalf("metric fields = %#v, want %#v", metric.fields, want)
	}
}

type capturedOntologyMetric struct {
	name   string
	fields map[string]any
}

type capturingOntologyMetricRecorder struct {
	mu      sync.Mutex
	metrics []capturedOntologyMetric
}

func (r *capturingOntologyMetricRecorder) CaptureContext(_ context.Context, name string, fields map[string]any) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	copyFields := make(map[string]any, len(fields))
	for key, value := range fields {
		copyFields[key] = value
	}
	r.metrics = append(r.metrics, capturedOntologyMetric{name: name, fields: copyFields})
	return true
}

func (r *capturingOntologyMetricRecorder) last(name string) (capturedOntologyMetric, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := len(r.metrics) - 1; index >= 0; index-- {
		if r.metrics[index].name == name {
			return r.metrics[index], true
		}
	}
	return capturedOntologyMetric{}, false
}

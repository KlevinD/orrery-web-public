package main

import (
	"fmt"
	"strings"

	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

func validateScheduledAgentRunCompletionIdentity(current, expected schedulerapp.ScheduledRun) error {
	type identityField struct {
		name     string
		current  string
		expected string
	}
	fields := []identityField{
		{name: "scheduled run", current: current.ScheduledRunID, expected: expected.ScheduledRunID},
		{name: "scheduled task", current: current.ScheduledTaskID, expected: expected.ScheduledTaskID},
		{name: "Mission", current: current.MissionID, expected: expected.MissionID},
		{name: "Mission task", current: current.TaskID, expected: expected.TaskID},
		{name: "Agent", current: current.AgentID, expected: expected.AgentID},
		{name: "responsibility", current: current.ResponsibilityID, expected: expected.ResponsibilityID},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.expected) == "" {
			return fmt.Errorf("%w: expected scheduled agent run %s identity is empty", responsibility.ErrConflict, field.name)
		}
		if strings.TrimSpace(field.current) != strings.TrimSpace(field.expected) {
			return fmt.Errorf("%w: scheduled agent run %s identity changed", responsibility.ErrConflict, field.name)
		}
	}
	if current.TargetMode != schedulerapp.TargetModeAgentTask || expected.TargetMode != schedulerapp.TargetModeAgentTask {
		return fmt.Errorf("%w: scheduled run is not the expected Agent task", responsibility.ErrConflict)
	}
	if current.ConfigurationSnapshotVersion > 0 && (strings.TrimSpace(current.TenantID) == "" ||
		strings.TrimSpace(current.TenantID) != strings.TrimSpace(expected.TenantID)) {
		return fmt.Errorf("%w: scheduled agent run tenant identity changed", responsibility.ErrConflict)
	}
	return nil
}

func bindScheduledRunRuntimeIdentity(run *schedulerapp.ScheduledRun, runtimeRunID string) error {
	if run == nil {
		return fmt.Errorf("%w: scheduled agent run is unavailable", responsibility.ErrConflict)
	}
	runtimeRunID = strings.TrimSpace(runtimeRunID)
	if runtimeRunID == "" {
		if run.ConfigurationSnapshotVersion > 0 {
			return fmt.Errorf("%w: scheduled agent runtime run identity is empty", responsibility.ErrConflict)
		}
		return nil
	}
	if persisted := strings.TrimSpace(run.RuntimeRunID); persisted != "" && persisted != runtimeRunID {
		return fmt.Errorf("%w: scheduled agent runtime run identity changed", responsibility.ErrConflict)
	}
	run.RuntimeRunID = runtimeRunID
	return nil
}

func scheduledRunHasResult(result schedulerapp.RunResult) bool {
	return strings.TrimSpace(result.Summary) != "" || len(result.Artifacts) > 0
}

func mergeScheduledRunCompletionTrace(run *schedulerapp.ScheduledRun, traceRef string) error {
	persistedTraceRef := strings.TrimSpace(run.TraceRef)
	completedTraceRef := strings.TrimSpace(traceRef)
	if persistedTraceRef != "" && completedTraceRef != "" && persistedTraceRef != completedTraceRef {
		return fmt.Errorf("%w: scheduled run already references a different trace", responsibility.ErrConflict)
	}
	if persistedTraceRef == "" && completedTraceRef != "" {
		applyScheduledRunTrace(run, completedTraceRef, "hot")
	}
	return nil
}

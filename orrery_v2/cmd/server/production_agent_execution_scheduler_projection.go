package main

import (
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/graphprotocol"
)

func scheduledTaskOntologyEvents(task schedulerapp.ScheduledTask) []graphprotocol.DomainEvent {
	task.AgentTaskID = firstNonBlank(task.AgentTaskID, schedulerapp.AgentTaskIDForScheduledTask(task.ScheduledTaskID))
	var events []graphprotocol.DomainEvent
	for _, tenantID := range scheduledTaskProjectionTenantIDs(task) {
		events = append(events, scheduledTaskOntologyEventsForTenant(task, tenantID)...)
	}
	return events
}

func scheduledTaskOntologyEventsForTenant(task schedulerapp.ScheduledTask, tenantID string) []graphprotocol.DomainEvent {
	return scheduledTaskOntologyEventsForTenantWithAgentDisplayName(task, tenantID, task.Target.AgentID)
}

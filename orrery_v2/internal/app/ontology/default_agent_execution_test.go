package ontology

import (
	"context"
	"testing"
)

func TestDefaultOntologyIncludesAgentExecutionControlPlane(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	for _, id := range []string{
		"agent_version",
		"agent_task",
		"scheduled_run",
		"agent_run",
		"responsibility_assignment",
		"responsibility_context",
		"responsibility_policy",
		"context_snapshot",
		"responsibility_context_snapshot",
		"memory_digest",
		"execution_trace",
		"evidence_item",
		"artifact",
		"policy_decision",
	} {
		if _, err := registry.GetObjectType(ctx, id); err != nil {
			t.Fatalf("default agent execution object type %s missing: %v", id, err)
		}
	}

	wantLinks := map[string][2]string{
		"agent_has_version":                            {"agent", "agent_version"},
		"agent_assigned_to_task":                       {"agent", "agent_task"},
		"scheduled_task_targets_agent":                 {"scheduled_task", "agent"},
		"scheduled_task_schedules_agent_task":          {"scheduled_task", "agent_task"},
		"scheduled_task_has_run":                       {"scheduled_task", "scheduled_run"},
		"scheduled_run_triggers_agent_run":             {"scheduled_run", "agent_run"},
		"agent_run_executes_agent_task":                {"agent_run", "agent_task"},
		"agent_run_performed_by_agent":                 {"agent_run", "agent"},
		"agent_run_uses_agent_version":                 {"agent_run", "agent_version"},
		"agent_run_uses_context":                       {"agent_run", "context_snapshot"},
		"context_snapshot_uses_responsibility_context": {"context_snapshot", "responsibility_context_snapshot"},
		"context_snapshot_uses_memory_digest":          {"context_snapshot", "memory_digest"},
		"context_snapshot_uses_knowledge":              {"context_snapshot", "knowledge_document"},
		"agent_run_has_trace":                          {"agent_run", "execution_trace"},
		"agent_run_produces_evidence":                  {"agent_run", "evidence_item"},
		"agent_run_produces_artifact":                  {"agent_run", "artifact"},
		"agent_run_requests_action_run":                {"agent_run", "action_run"},
		"action_run_requires_approval_case":            {"action_run", "approval_case"},
		"action_run_authorized_by_policy":              {"action_run", "policy_decision"},
	}
	for id, endpoints := range wantLinks {
		link, err := registry.GetLinkType(ctx, id)
		if err != nil {
			t.Fatalf("default agent execution link type %s missing: %v", id, err)
		}
		if link.FromObjectType != endpoints[0] || link.ToObjectType != endpoints[1] {
			t.Fatalf("link %s endpoints = %s -> %s, want %s -> %s", id, link.FromObjectType, link.ToObjectType, endpoints[0], endpoints[1])
		}
	}
}

func TestDefaultOntologyIncludesAgentExecutionEventMappings(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	rules, err := registry.ListMappingRules(ctx, "")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}

	for _, id := range []string{
		"agent.task.upserted",
		"scheduler.task.created",
		"scheduler.run.started",
		"scheduler.run.completed",
		"agent.runtime.run.started",
		"agent.runtime.run.completed",
		"agent.runtime.context.attached",
		"agent.runtime.context.responsibility.attached",
		"agent.runtime.context.memory.attached",
		"agent.runtime.context.knowledge_document.attached",
		"agent.runtime.context.knowledge_chunk.attached",
		"agent.runtime.context.knowledge_anchor.attached",
		"agent.runtime.evidence.produced",
		"agent.runtime.artifact.produced",
		"agent.runtime.action.requested",
		"agent.runtime.approval.required",
		"action.run.policy.decided",
		"action.run.evidence.attached",
		"action.run.targeted.task",
	} {
		if !mappingRuleExists(rules, id) {
			t.Fatalf("default agent execution mapping rule %s missing", id)
		}
	}
}

func TestDefaultOntologyProjectsDynamicScheduledTaskScope(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	wantProperties := []string{"target_mode", "source_scope_kind", "source_scope_id", "target_scope_kind", "target_scope_id"}
	for _, objectTypeID := range []string{"agent_task", "scheduled_task"} {
		objectType, err := registry.GetObjectType(ctx, objectTypeID)
		if err != nil {
			t.Fatal(err)
		}
		properties := make(map[string]bool, len(objectType.Properties))
		for _, property := range objectType.Properties {
			properties[property.PropertyID] = true
		}
		for _, propertyID := range wantProperties {
			if !properties[propertyID] {
				t.Errorf("object type %s is missing %s", objectTypeID, propertyID)
			}
		}
	}

	rules, err := registry.ListMappingRules(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, ruleID := range []string{"agent.task.upserted", "scheduler.task.created", "scheduler.task.updated", "scheduler.task.cancelled"} {
		rule := MappingRule{}
		for _, candidate := range rules {
			if candidate.ID == ruleID {
				rule = candidate
				break
			}
		}
		if rule.ID == "" {
			t.Fatalf("mapping rule %s missing", ruleID)
		}
		properties, _ := rule.Rule["properties"].([]any)
		for _, propertyID := range wantProperties {
			found := false
			for _, raw := range properties {
				property, _ := raw.(map[string]any)
				if property["property_id"] == propertyID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("mapping rule %s is missing %s", ruleID, propertyID)
			}
		}
	}
}

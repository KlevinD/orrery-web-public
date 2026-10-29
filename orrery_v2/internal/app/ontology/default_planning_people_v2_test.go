package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestDefaultOntologyIncludesPlanningPeopleV2Schema(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	for objectTypeID, wantProperties := range map[string][]string{
		"idea":            {"issue_number", "title", "summary", "status", "source", "captured_by", "proposer_name", "updated_by", "downstream_todo_id"},
		"todo":            {"issue_number", "title", "summary", "status", "source", "proposer_name", "created_by", "updated_by", "assignee_id", "assignee_name", "upstream_idea_id"},
		"issue_comment":   {"body", "issue_kind", "issue_id", "issue_object_id", "source", "author_id", "author_name"},
		"requirement":     {"title", "status", "priority", "acceptance_criteria"},
		"decision":        {"title", "status", "rationale", "decided_by"},
		"design_artifact": {"title", "artifact_type", "status", "uri"},
		"department":      {"name", "status", "owner_profile_id"},
		"position":        {"name", "level", "job_family", "status"},
		"role_assignment": {"role_id", "scope", "status"},
		"agent":           {"display_name", "runtime_kind"},
		"tool":            {"name", "risk_level", "provider", "provider_tool_id", "description", "input_schema", "output_schema"},
	} {
		if _, err := registry.GetObjectType(ctx, objectTypeID); err != nil {
			t.Fatalf("default v2 object type %s missing: %v", objectTypeID, err)
		}
		properties, err := registry.ListPropertyTypes(ctx, objectTypeID)
		if err != nil {
			t.Fatalf("ListPropertyTypes(%s): %v", objectTypeID, err)
		}
		for _, propertyID := range wantProperties {
			if !propertyTypesContain(properties, propertyID) {
				t.Fatalf("%s property %s missing in %+v", objectTypeID, propertyID, properties)
			}
		}
	}

	for _, tc := range []struct {
		linkType string
		from     string
		to       string
	}{
		{linkType: "idea_refines_requirement", from: "idea", to: "requirement"},
		{linkType: "idea_converted_to_todo", from: "idea", to: "todo"},
		{linkType: "decision_resolves_requirement", from: "decision", to: "requirement"},
		{linkType: "design_artifact_addresses_requirement", from: "design_artifact", to: "requirement"},
		{linkType: "department_part_of_organization", from: "department", to: "organization"},
		{linkType: "position_belongs_to_department", from: "position", to: "department"},
		{linkType: "person_holds_position", from: "person", to: "position"},
		{linkType: "agent_fulfills_position", from: "agent", to: "position"},
	} {
		linkType, err := registry.GetLinkType(ctx, tc.linkType)
		if err != nil {
			t.Fatalf("default v2 link type %s missing: %v", tc.linkType, err)
		}
		if linkType.FromObjectType != tc.from || linkType.ToObjectType != tc.to {
			t.Fatalf("%s endpoints = %s -> %s, want %s -> %s", tc.linkType, linkType.FromObjectType, linkType.ToObjectType, tc.from, tc.to)
		}
	}
}

func TestDefaultOntologyPlanningPeopleV2MappingRulesApplyEvents(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	for _, event := range []graphprotocol.DomainEvent{
		planningPeopleV2Event("planning-requirement-created:req-1", "planning.requirement.created", map[string]any{
			"id":                  "req-1",
			"title":               "MCP capabilities must be queryable",
			"status":              "open",
			"priority":            "high",
			"acceptance_criteria": []any{"tool facts visible", "server link visible"},
		}),
		planningPeopleV2Event("planning-idea-captured:idea-1", "planning.idea.captured", map[string]any{
			"id":             "idea-1",
			"issue_number":   "#10001",
			"title":          "Use ontology as the capability index",
			"summary":        "Expose MCP tools as graph objects.",
			"status":         "captured",
			"source":         "idea.create",
			"captured_by":    "planner-agent",
			"proposer_name":  "Planner",
			"updated_by":     "planner-agent",
			"requirement_id": "req-1",
		}),
		planningPeopleV2Event("planning-todo-changed:todo-1:rev-1", "planning.todo.changed", map[string]any{
			"id":         "todo-1",
			"title":      "Expose MCP tools in a board",
			"summary":    "Turn the ontology insight into tracked work.",
			"status":     "backlog",
			"source":     "todo.idea_conversion",
			"created_by": "planner-agent",
		}),
		planningPeopleV2Event("planning-idea-converted-to-todo:idea-1:todo-1", "planning.idea.converted_to_todo", map[string]any{
			"idea_id":     "idea-1",
			"idea_status": "converted",
			"todo_id":     "todo-1",
			"title":       "Expose MCP tools in a board",
			"summary":     "Turn the ontology insight into tracked work.",
			"status":      "backlog",
			"source":      "todo.idea_conversion",
			"created_by":  "planner-agent",
		}),
		planningPeopleV2Event("planning-todo-changed:todo-2:rev-1", "planning.todo.changed", map[string]any{
			"id":            "todo-2",
			"issue_number":  "#10002",
			"title":         "Prepare ontology work item",
			"summary":       "Track the production planning task.",
			"status":        "backlog",
			"source":        "todo.create",
			"proposer_name": "Planner",
			"created_by":    "planner-agent",
			"updated_by":    "planner-agent",
			"assignee_id":   "agent-1",
			"assignee_name": "Architecture Agent",
		}),
		planningPeopleV2Event("planning-todo-comment-linked:comment-1", "planning.todo.comment.linked", map[string]any{
			"comment_id":  "comment-1",
			"issue_id":    "todo-2",
			"body":        "Todo comment",
			"source":      "comment.create",
			"author_id":   "planner-agent",
			"author_name": "Planner",
		}),
		planningPeopleV2Event("planning-idea-comment-linked:comment-2", "planning.idea.comment.linked", map[string]any{
			"comment_id":  "comment-2",
			"issue_id":    "idea-1",
			"body":        "Idea comment",
			"source":      "comment.create",
			"author_id":   "planner-agent",
			"author_name": "Planner",
		}),
		planningPeopleV2Event("planning-decision-recorded:decision-1", "planning.decision.recorded", map[string]any{
			"id":             "decision-1",
			"title":          "Keep one MCP action type",
			"status":         "accepted",
			"rationale":      "Execution stays decoupled from capability discovery.",
			"decided_by":     "system-architect",
			"requirement_id": "req-1",
		}),
		planningPeopleV2Event("planning-design-artifact-created:artifact-1", "planning.design_artifact.created", map[string]any{
			"id":             "artifact-1",
			"title":          "Ontology v2 sketch",
			"artifact_type":  "schema",
			"status":         "draft",
			"uri":            "doc://ontology-v2",
			"requirement_id": "req-1",
		}),
		planningPeopleV2Event("people-department-created:dept-1", "people.department.created", map[string]any{
			"id":               "dept-1",
			"name":             "Platform Architecture",
			"status":           "active",
			"owner_profile_id": "person-1",
			"organization_id":  "org-1",
		}),
		planningPeopleV2Event("people-position-created:position-1", "people.position.created", map[string]any{
			"id":            "position-1",
			"name":          "Ontology Architect",
			"level":         "principal",
			"job_family":    "architecture",
			"status":        "active",
			"department_id": "dept-1",
		}),
		planningPeopleV2Event("people-agent-updated:agent-1", "people.agent.updated", map[string]any{
			"id":           "agent-1",
			"display_name": "Ontology Agent",
			"runtime_kind": "codex",
		}),
		planningPeopleV2Event("people-position-assigned:assignment-1", "people.position.assigned", map[string]any{
			"id":          "assignment-1",
			"person_id":   "person-1",
			"agent_id":    "agent-1",
			"position_id": "position-1",
			"role_id":     "architecture.owner",
			"scope":       "ontology",
			"status":      "active",
		}),
	} {
		report, err := runner.ApplyEvent(ctx, event)
		if err != nil {
			t.Fatalf("ApplyEvent(%s): %v", event.Type, err)
		}
		if !report.OK || report.AppliedProjectionCount != 1 {
			t.Fatalf("report for %s = %+v", event.Type, report)
		}
	}

	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-v2")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "idea", "idea-1")
	assertContextGraphObject(t, objects, "todo", "todo:todo-1")
	assertContextGraphObject(t, objects, "todo", "todo:todo-2")
	assertContextGraphObject(t, objects, "issue_comment", "issue_comment:comment-1")
	assertContextGraphObject(t, objects, "issue_comment", "issue_comment:comment-2")
	assertContextGraphObject(t, objects, "requirement", "req-1")
	assertContextGraphObject(t, objects, "decision", "decision-1")
	assertContextGraphObject(t, objects, "design_artifact", "artifact-1")
	assertContextGraphObject(t, objects, "department", "dept-1")
	assertContextGraphObject(t, objects, "position", "position-1")
	assertContextGraphObject(t, objects, "role_assignment", "assignment-1")
	assertContextGraphObject(t, objects, "agent", "agent-1")
	assertContextGraphLink(t, links, "idea_refines_requirement", "idea-1", "req-1")
	assertContextGraphLink(t, links, "idea_converted_to_todo", "idea-1", "todo:todo-1")
	assertContextGraphLink(t, links, "decision_resolves_requirement", "decision-1", "req-1")
	assertContextGraphLink(t, links, "design_artifact_addresses_requirement", "artifact-1", "req-1")
	assertContextGraphLink(t, links, "department_part_of_organization", "dept-1", "org-1")
	assertContextGraphLink(t, links, "position_belongs_to_department", "position-1", "dept-1")
	assertContextGraphLink(t, links, "person_holds_position", "person-1", "position-1")
	assertContextGraphLink(t, links, "agent_fulfills_position", "agent-1", "position-1")
	assertContextGraphFact(t, facts, "idea", "idea-1", "captured_by", "planner-agent")
	assertContextGraphFact(t, facts, "idea", "idea-1", "issue_number", "#10001")
	assertContextGraphFact(t, facts, "idea", "idea-1", "proposer_name", "Planner")
	assertContextGraphFact(t, facts, "idea", "idea-1", "updated_by", "planner-agent")
	assertContextGraphFact(t, facts, "idea", "idea-1", "downstream_todo_id", "todo-1")
	assertContextGraphFact(t, facts, "todo", "todo:todo-1", "upstream_idea_id", "idea-1")
	assertContextGraphFact(t, facts, "todo", "todo:todo-2", "status", "backlog")
	assertContextGraphFact(t, facts, "todo", "todo:todo-2", "assignee_id", "agent-1")
	assertContextGraphFact(t, facts, "issue_comment", "issue_comment:comment-1", "issue_kind", "todo")
	assertContextGraphFact(t, facts, "issue_comment", "issue_comment:comment-1", "issue_object_id", "todo:todo-2")
	assertContextGraphFact(t, facts, "issue_comment", "issue_comment:comment-2", "issue_kind", "idea")
	assertContextGraphFact(t, facts, "issue_comment", "issue_comment:comment-2", "issue_object_id", "idea-1")
	assertContextGraphFact(t, facts, "requirement", "req-1", "priority", "high")
	assertContextGraphFact(t, facts, "position", "position-1", "job_family", "architecture")
	assertContextGraphFact(t, facts, "agent", "agent-1", "runtime_kind", "codex")
	assertContextGraphEvidence(t, evidence, "people-position-assigned:assignment-1")
}

func planningPeopleV2Event(id string, eventType string, payload map[string]any) graphprotocol.DomainEvent {
	return graphprotocol.DomainEvent{
		ID:          id,
		TenantID:    "tenant-v2",
		Type:        eventType,
		StreamID:    id,
		Version:     1,
		Payload:     payload,
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "ontology-v2-test"},
		OccurredAt:  time.Unix(200, 0).UTC(),
	}
}

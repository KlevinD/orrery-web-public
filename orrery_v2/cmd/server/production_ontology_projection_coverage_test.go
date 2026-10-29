package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

type productionOntologyReplayCoverageCase struct {
	event      graphprotocol.DomainEvent
	badPayload map[string]any
}

func TestProductionOntologyProjectionCovers0618ProductionEventTypes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range productionOntologyReplayCoverageCases() {
		t.Run(tc.event.Type, func(t *testing.T) {
			core := newOntologyEventLogTestCore(t, "production-ontology-projection-coverage-"+eventTypeDBName(tc.event.Type))
			log := newProductionOntologyEventLog(core)
			seedProductionOntologyReplayCoverageTargets(ctx, t, core, tc.event.Type)
			if err := log.Record(ctx, tc.event); err != nil {
				t.Fatalf("Record %s: %v", tc.event.ID, err)
			}
			runner, err := newProductionOntologyProjectionRunner(ctx, core)
			if err != nil {
				t.Fatalf("new projection runner: %v", err)
			}

			events, err := log.List(ctx)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(events) != 1 {
				t.Fatalf("retained events = %d, want 1", len(events))
			}
			if _, err := runner.ApplyEvent(ctx, events[0]); err != nil {
				t.Fatalf("ApplyEvent %s: %v", tc.event.Type, err)
			}
			_, _, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, tc.event.TenantID)
			if err != nil {
				t.Fatalf("context graph snapshot: %v", err)
			}
			assertContextGraphEvidence(t, evidence, tc.event.ID)
		})
	}
}

func TestProductionOntologyProjectionRejectsMissingRequiredPayloadByEventType(t *testing.T) {
	ctx := context.Background()
	for _, tc := range productionOntologyReplayCoverageCases() {
		if len(tc.badPayload) == 0 {
			continue
		}
		t.Run(tc.event.Type, func(t *testing.T) {
			core := newOntologyEventLogTestCore(t, "production-ontology-projection-failure-"+eventTypeDBName(tc.event.Type))
			runner, err := newProductionOntologyProjectionRunner(ctx, core)
			if err != nil {
				t.Fatalf("new projection runner: %v", err)
			}
			badEvent := tc.event
			badEvent.ID = tc.event.ID + ":missing-payload"
			badEvent.Payload = tc.badPayload

			if _, err := runner.ApplyEvent(ctx, badEvent); err == nil {
				t.Fatalf("ApplyEvent %s should fail for missing required payload", badEvent.Type)
			} else if conformance, ok := graphprotocol.AsConformanceError(err); !ok || !conformance.HasIssue("projection_error") {
				t.Fatalf("ApplyEvent %s error = %v, want conformance projection_error", badEvent.Type, err)
			}
		})
	}
}

func productionOntologyReplayCoverageCases() []productionOntologyReplayCoverageCase {
	return []productionOntologyReplayCoverageCase{
		{
			event: productionOntologyReplayCoverageEvent("mission-created:mission-replay-1", "mission:mission-replay-1", "mission.created", map[string]any{
				"id":     "mission-replay-1",
				"title":  "Replay mission",
				"status": "active",
			}),
			badPayload: map[string]any{"id": "mission-replay-1", "status": "active"},
		},
		{
			event: productionOntologyReplayCoverageEvent("mission-task-created:task-replay-1", "mission:mission-replay-1", "mission.task.created", map[string]any{
				"mission_id": "mission-replay-1",
				"task_id":    "task-replay-1",
				"title":      "Replay task",
				"status":     "created",
				"event_id":   "mission-task-created:task-replay-1",
			}),
			badPayload: map[string]any{"mission_id": "mission-replay-1", "title": "Replay task", "status": "created"},
		},
		{
			event: productionOntologyReplayCoverageEvent("project-created:project-replay-1", "project:project-replay-1", "project.created", map[string]any{
				"id":     "project-replay-1",
				"title":  "Replay project",
				"status": "active",
			}),
			badPayload: map[string]any{"id": "project-replay-1", "status": "active"},
		},
		{
			event: productionOntologyReplayCoverageEvent("project-updated:project-replay-1:rev-2", "project:project-replay-1", "project.updated", map[string]any{
				"id":    "project-replay-1",
				"title": "Replay updated project",
			}),
			badPayload: map[string]any{"id": "project-replay-1"},
		},
		{
			event: productionOntologyReplayCoverageEvent("project-archived:project-replay-1:rev-3", "project:project-replay-1", "project.archived", map[string]any{
				"id":     "project-replay-1",
				"status": "archived",
			}),
			badPayload: map[string]any{"id": "project-replay-1"},
		},
		{
			event: productionOntologyReplayCoverageEvent("organization-created:org-replay-1", "org-replay-1", "organization.created", map[string]any{
				"id":                "org-replay-1",
				"name":              "Replay organization",
				"status":            "active",
				"code":              "01",
				"knowledge_base_id": "kb-replay-1",
			}),
			badPayload: map[string]any{"id": "org-replay-1", "status": "active", "code": "01", "knowledge_base_id": "kb-replay-1"},
		},
		{
			event: productionOntologyReplayCoverageEvent("resource-tool-registered:tool.replay", "tool:tool.replay", "resource.tool.registered", map[string]any{
				"id":         "tool.replay",
				"name":       "Replay Tool",
				"risk_level": "high",
			}),
			badPayload: map[string]any{"id": "tool.replay", "risk_level": "high"},
		},
		{
			event: productionOntologyReplayCoverageEvent("action-run-started:run-replay-1", "tenant-replay-1", "action.run.started", map[string]any{
				"id":             "run-replay-1",
				"action_type_id": "task.complete",
				"status":         "started",
			}),
			badPayload: map[string]any{"id": "run-replay-1", "status": "started"},
		},
		{
			event: productionOntologyReplayCoverageEvent("action-run-completed:run-replay-1", "tenant-replay-1", "action.run.completed", map[string]any{
				"id":              "run-replay-1",
				"status":          "completed",
				"result_ref_id":   "result-replay-1",
				"result_ref_kind": "task_update",
			}),
			badPayload: map[string]any{"id": "run-replay-1"},
		},
		{
			event: productionOntologyReplayCoverageEvent("action-writeback-succeeded:receipt-replay-1", "tenant-replay-1", "action.writeback.succeeded", map[string]any{
				"action_run_id": "run-replay-1",
				"receipt_id":    "receipt-replay-1",
				"status":        "succeeded",
				"target_system": "orrery",
			}),
			badPayload: map[string]any{"action_run_id": "run-replay-1", "status": "succeeded"},
		},
		{
			event: productionOntologyReplayCoverageEvent("people-team-created:team-replay-1", "team:team-replay-1", "people.team.created", map[string]any{
				"id":      "team-replay-1",
				"name":    "Replay Team",
				"status":  "active",
				"purpose": "Delivery",
			}),
			badPayload: map[string]any{"id": "team-replay-1", "status": "active"},
		},
		{
			event: productionOntologyReplayCoverageEvent("people-person-updated:person-replay-1:rev-1", "person:person-replay-1", "people.person.updated", map[string]any{
				"id":           "person-replay-1",
				"display_name": "Replay Person",
				"role":         "operator",
				"email":        "replay@example.test",
				"status":       "active",
				"skills":       []string{"ops"},
			}),
			badPayload: map[string]any{"id": "person-replay-1", "status": "active"},
		},
		{
			event: productionOntologyReplayCoverageEvent("people-agent-updated:agent-replay-1:rev-1", "org-replay-1", "people.agent.updated", map[string]any{
				"id":           "agent-replay-1",
				"display_name": "Replay Agent",
				"runtime_kind": "codex",
			}),
			badPayload: map[string]any{"id": "agent-replay-1"},
		},
		{
			event: productionOntologyReplayCoverageEvent("people-membership-linked:person-replay-1:team-replay-1:rev-1", "team:team-replay-1", "people.membership.linked", map[string]any{
				"person_id": "person-replay-1",
				"team_id":   "team-replay-1",
			}),
			badPayload: map[string]any{"person_id": "person-replay-1"},
		},
		{
			event: productionOntologyReplayCoverageEvent("department-created:dept-replay-1", "org-replay-1", "people.department.created", map[string]any{
				"id":               "dept-replay-1",
				"name":             "Replay Department",
				"status":           "active",
				"owner_profile_id": "person-replay-1",
				"organization_id":  "org-replay-1",
			}),
			badPayload: map[string]any{"id": "dept-replay-1", "status": "active", "organization_id": "org-replay-1"},
		},
		{
			event: productionOntologyReplayCoverageEvent("position-created:position-replay-1", "org-replay-1", "people.position.created", map[string]any{
				"id":            "position-replay-1",
				"name":          "Replay Position",
				"status":        "active",
				"department_id": "dept-replay-1",
			}),
			badPayload: map[string]any{"id": "position-replay-1", "department_id": "dept-replay-1"},
		},
		{
			event: productionOntologyReplayCoverageEvent("position-assigned:assignment-replay-1", "org-replay-1", "people.position.assigned", map[string]any{
				"id":          "assignment-replay-1",
				"person_id":   "person-replay-1",
				"agent_id":    "agent-replay-1",
				"position_id": "position-replay-1",
				"role_id":     "architecture.owner",
				"scope":       "dept-replay-1",
				"status":      "active",
			}),
			badPayload: map[string]any{"id": "assignment-replay-1", "person_id": "person-replay-1", "position_id": "position-replay-1"},
		},
		{
			event: productionOntologyReplayCoverageEvent("planning-idea-captured:idea-replay-1:idea.create:rev-1", "planning", "planning.idea.captured", map[string]any{
				"id":            "idea-replay-1",
				"issue_number":  "#20000",
				"title":         "Replay idea",
				"summary":       "Replay planning idea.",
				"status":        "captured",
				"source":        "idea.create",
				"captured_by":   "planner",
				"proposer_name": "Planner",
				"updated_by":    "planner",
			}),
			badPayload: map[string]any{"id": "idea-replay-1", "status": "captured"},
		},
		{
			event: productionOntologyReplayCoverageEvent("planning-idea-converted-to-todo:idea-replay-1:todo-replay-1", "planning", "planning.idea.converted_to_todo", map[string]any{
				"idea_id":     "idea-replay-1",
				"idea_status": "converted",
				"todo_id":     "todo-replay-1",
				"title":       "Replay converted todo",
				"summary":     "Replay conversion lineage.",
				"status":      "backlog",
				"source":      "todo.idea_conversion",
				"created_by":  "planner",
			}),
			badPayload: map[string]any{"idea_id": "idea-replay-1", "status": "backlog"},
		},
		{
			event: productionOntologyReplayCoverageEvent("planning-todo-changed:todo-replay-1:todo.create:rev-1", "planning", "planning.todo.changed", map[string]any{
				"id":            "todo-replay-1",
				"issue_number":  "#20001",
				"title":         "Replay todo",
				"summary":       "Replay planning todo.",
				"status":        "backlog",
				"source":        "todo.create",
				"proposer_name": "Planner",
				"created_by":    "planner",
				"updated_by":    "planner",
				"assignee_id":   "agent-replay-1",
				"assignee_name": "Replay Agent",
			}),
			badPayload: map[string]any{"id": "todo-replay-1", "status": "backlog"},
		},
		{
			event: productionOntologyReplayCoverageEvent("planning-issue-comment-linked:comment-replay-1:todo:todo-replay-1:comment.create:rev-1", "planning", "planning.todo.comment.linked", map[string]any{
				"comment_id":  "comment-replay-1",
				"issue_id":    "todo-replay-1",
				"body":        "Replay todo comment",
				"source":      "comment.create",
				"author_id":   "planner",
				"author_name": "Planner",
			}),
			badPayload: map[string]any{"comment_id": "comment-replay-1", "issue_id": "todo-replay-1"},
		},
		{
			event: productionOntologyReplayCoverageEvent("planning-issue-comment-linked:comment-replay-2:idea:idea-replay-1:comment.create:rev-1", "planning", "planning.idea.comment.linked", map[string]any{
				"comment_id":  "comment-replay-2",
				"issue_id":    "idea-replay-1",
				"body":        "Replay idea comment",
				"source":      "comment.create",
				"author_id":   "planner",
				"author_name": "Planner",
			}),
			badPayload: map[string]any{"comment_id": "comment-replay-2", "issue_id": "idea-replay-1"},
		},
		{
			event: productionOntologyReplayCoverageEvent("memory-record-accepted:memory-replay-1", "mission:mission-replay-1", "memory.record.accepted", map[string]any{
				"id":               "memory-replay-1",
				"summary":          "Replay memory",
				"layer":            "fact",
				"topic":            "release",
				"scope_kind":       "mission",
				"scope_id":         "mission-replay-1",
				"lifecycle_status": "active",
				"confidence":       0.9,
				"source_refs":      []string{"summary:release"},
				"evidence_refs":    []string{"evidence:release"},
			}),
			badPayload: map[string]any{"id": "memory-replay-1", "layer": "fact"},
		},
		{
			event: productionOntologyReplayCoverageEvent("memory-scope-linked:memory-replay-1", "mission:mission-replay-1", "memory.scope.linked", map[string]any{
				"memory_record_id": "memory-replay-1",
				"mission_id":       "mission-replay-1",
			}),
		},
	}
}

func productionOntologyReplayCoverageEvent(id string, tenantID string, eventType string, payload map[string]any) graphprotocol.DomainEvent {
	return graphprotocol.DomainEvent{
		ID:          id,
		TenantID:    tenantID,
		Type:        eventType,
		StreamID:    id,
		Version:     1,
		Payload:     payload,
		PublishedBy: graphprotocol.ActorRef{Kind: "test", ID: "production-ontology-replay-coverage"},
		OccurredAt:  time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC),
	}
}

func seedProductionOntologyReplayCoverageTargets(ctx context.Context, t *testing.T, core *db.Store, eventType string) {
	t.Helper()
	store := contextgraph.NewPlatformStore(core)
	now := time.Date(2026, 6, 25, 11, 59, 0, 0, time.UTC)
	save := func(object contextgraph.Object) {
		t.Helper()
		object.UpdatedAt = now
		if err := store.SaveObject(ctx, object); err != nil {
			t.Fatalf("seed context graph object %s/%s: %v", object.ObjectType, object.ID, err)
		}
	}
	switch eventType {
	case "mission.task.created":
		save(contextgraph.Object{
			ID:           "mission-replay-1",
			TenantID:     "mission:mission-replay-1",
			ObjectType:   "mission",
			CanonicalKey: "mission:mission-replay-1",
			Label:        "Replay mission",
		})
	case "people.membership.linked":
		save(contextgraph.Object{
			ID:           "person-replay-1",
			TenantID:     "team:team-replay-1",
			ObjectType:   "person",
			CanonicalKey: "person:person-replay-1",
			Label:        "Replay Person",
		})
		save(contextgraph.Object{
			ID:           "team-replay-1",
			TenantID:     "team:team-replay-1",
			ObjectType:   "team",
			CanonicalKey: "team:team-replay-1",
			Label:        "Replay Team",
		})
	case "people.department.created":
		save(contextgraph.Object{
			ID:           "org-replay-1",
			TenantID:     "org-replay-1",
			ObjectType:   "organization",
			CanonicalKey: "organization:org-replay-1",
			Label:        "Replay organization",
		})
	case "people.position.created":
		save(contextgraph.Object{
			ID:           "dept-replay-1",
			TenantID:     "org-replay-1",
			ObjectType:   "department",
			CanonicalKey: "department:dept-replay-1",
			Label:        "Replay Department",
		})
	case "people.position.assigned":
		save(contextgraph.Object{
			ID:           "person-replay-1",
			TenantID:     "org-replay-1",
			ObjectType:   "person",
			CanonicalKey: "person:person-replay-1",
			Label:        "Replay Person",
		})
		save(contextgraph.Object{
			ID:           "agent-replay-1",
			TenantID:     "org-replay-1",
			ObjectType:   "agent",
			CanonicalKey: "agent:agent-replay-1",
			Label:        "Replay Agent",
		})
		save(contextgraph.Object{
			ID:           "position-replay-1",
			TenantID:     "org-replay-1",
			ObjectType:   "position",
			CanonicalKey: "position:position-replay-1",
			Label:        "Replay Position",
		})
	case "memory.scope.linked":
		save(contextgraph.Object{
			ID:           "memory-replay-1",
			TenantID:     "mission:mission-replay-1",
			ObjectType:   "memory_record",
			CanonicalKey: "memory_record:memory-replay-1",
			Label:        "Replay memory",
		})
		save(contextgraph.Object{
			ID:           "mission-replay-1",
			TenantID:     "mission:mission-replay-1",
			ObjectType:   "mission",
			CanonicalKey: "mission:mission-replay-1",
			Label:        "Replay mission",
		})
	case "planning.idea.converted_to_todo":
		save(contextgraph.Object{
			ID:           "idea-replay-1",
			TenantID:     "planning",
			ObjectType:   "idea",
			CanonicalKey: "idea:idea-replay-1",
			Label:        "Replay idea",
		})
		save(contextgraph.Object{
			ID:           "todo:todo-replay-1",
			TenantID:     "planning",
			ObjectType:   "todo",
			CanonicalKey: "todo:todo-replay-1",
			Label:        "Replay todo",
		})
	}
}

func eventTypeDBName(eventType string) string {
	return strings.NewReplacer(".", "-", ":", "-", "/", "-").Replace(eventType)
}

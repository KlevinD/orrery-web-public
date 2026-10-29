package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestDefaultOntologyIncludesEnterpriseDomainSchema(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	for objectTypeID, wantProperties := range map[string][]string{
		"person":          {"display_name", "role", "email", "status", "skills"},
		"team":            {"name", "status", "purpose"},
		"legal_entity":    {"name", "jurisdiction", "status"},
		"contract":        {"title", "status", "counterparty", "effective_date", "expires_at"},
		"policy":          {"title", "policy_type", "status", "owner_domain"},
		"approval_case":   {"title", "status", "risk_level"},
		"repository":      {"name", "provider", "default_branch"},
		"issue":           {"title", "status", "priority"},
		"pull_request":    {"title", "status", "branch"},
		"release":         {"version", "status", "environment"},
		"value_objective": {"title", "status", "owner_domain", "target_metric"},
		"metric":          {"name", "value", "unit", "observed_at"},
		"initiative":      {"title", "status", "expected_value", "risk_level"},
		"experiment":      {"title", "status", "hypothesis", "outcome"},
	} {
		if _, err := registry.GetObjectType(ctx, objectTypeID); err != nil {
			t.Fatalf("default enterprise object type %s missing: %v", objectTypeID, err)
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
		{linkType: "person_member_of_team", from: "person", to: "team"},
		{linkType: "team_owns_objective", from: "team", to: "value_objective"},
		{linkType: "contract_with_legal_entity", from: "contract", to: "legal_entity"},
		{linkType: "approval_case_reviews_contract", from: "approval_case", to: "contract"},
		{linkType: "repository_has_issue", from: "repository", to: "issue"},
		{linkType: "repository_has_pull_request", from: "repository", to: "pull_request"},
		{linkType: "pull_request_resolves_issue", from: "pull_request", to: "issue"},
		{linkType: "release_includes_pull_request", from: "release", to: "pull_request"},
		{linkType: "initiative_supports_objective", from: "initiative", to: "value_objective"},
		{linkType: "metric_tracks_objective", from: "metric", to: "value_objective"},
		{linkType: "experiment_validates_initiative", from: "experiment", to: "initiative"},
	} {
		linkType, err := registry.GetLinkType(ctx, tc.linkType)
		if err != nil {
			t.Fatalf("default enterprise link type %s missing: %v", tc.linkType, err)
		}
		if linkType.FromObjectType != tc.from || linkType.ToObjectType != tc.to {
			t.Fatalf("%s endpoints = %s -> %s, want %s -> %s", tc.linkType, linkType.FromObjectType, linkType.ToObjectType, tc.from, tc.to)
		}
	}
}

func TestDefaultEnterpriseDomainMappingRulesApplyEvents(t *testing.T) {
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
		enterpriseDomainEvent("people-team-created:team-1", "people.team.created", map[string]any{
			"id":      "team-1",
			"name":    "Ontology Team",
			"status":  "active",
			"purpose": "enterprise graph",
		}),
		enterpriseDomainEvent("people-person-updated:person-1", "people.person.updated", map[string]any{
			"id":           "person-1",
			"display_name": "Ding",
			"role":         "owner",
			"email":        "ding@example.com",
			"status":       "active",
			"skills":       []any{"ontology", "integration"},
		}),
		enterpriseDomainEvent("people-membership-linked:person-1:team-1", "people.membership.linked", map[string]any{
			"person_id": "person-1",
			"team_id":   "team-1",
		}),
		enterpriseDomainEvent("legal-entity-created:entity-1", "legal.entity.created", map[string]any{
			"id":           "entity-1",
			"name":         "Minvia",
			"jurisdiction": "CN",
			"status":       "active",
		}),
		enterpriseDomainEvent("legal-contract-created:contract-1", "legal.contract.created", map[string]any{
			"id":              "contract-1",
			"title":           "PageIndex agreement",
			"status":          "draft",
			"counterparty":    "PageIndex",
			"effective_date":  "2026-06-22",
			"expires_at":      "2027-06-22",
			"legal_entity_id": "entity-1",
		}),
		enterpriseDomainEvent("development-repository-registered:repo-1", "development.repository.registered", map[string]any{
			"id":             "repo-1",
			"name":           "orrery",
			"provider":       "github",
			"default_branch": "main",
		}),
		enterpriseDomainEvent("development-issue-created:issue-1", "development.issue.created", map[string]any{
			"id":            "issue-1",
			"repository_id": "repo-1",
			"title":         "Fill enterprise ontology",
			"status":        "open",
			"priority":      "high",
		}),
		enterpriseDomainEvent("development-pr-opened:pr-1", "development.pull_request.opened", map[string]any{
			"id":            "pr-1",
			"repository_id": "repo-1",
			"title":         "Ontology enterprise domains",
			"status":        "open",
			"branch":        "ontology-enterprise",
			"issue_id":      "issue-1",
		}),
		enterpriseDomainEvent("value-objective-created:objective-1", "value.objective.created", map[string]any{
			"id":            "objective-1",
			"title":         "Improve ontology coverage",
			"status":        "active",
			"owner_domain":  "ontology",
			"target_metric": "coverage",
			"team_id":       "team-1",
		}),
		enterpriseDomainEvent("value-metric-recorded:metric-1", "value.metric.recorded", map[string]any{
			"id":                 "metric-1",
			"name":               "coverage",
			"value":              0.75,
			"unit":               "ratio",
			"observed_at":        "2026-06-22T12:00:00Z",
			"value_objective_id": "objective-1",
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

	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-enterprise")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "team", "team-1")
	assertContextGraphObject(t, objects, "person", "person-1")
	assertContextGraphObject(t, objects, "legal_entity", "entity-1")
	assertContextGraphObject(t, objects, "contract", "contract-1")
	assertContextGraphObject(t, objects, "repository", "repo-1")
	assertContextGraphObject(t, objects, "pull_request", "pr-1")
	assertContextGraphObject(t, objects, "value_objective", "objective-1")
	assertContextGraphObject(t, objects, "metric", "metric-1")
	assertContextGraphLink(t, links, "person_member_of_team", "person-1", "team-1")
	assertContextGraphLink(t, links, "contract_with_legal_entity", "contract-1", "entity-1")
	assertContextGraphLink(t, links, "repository_has_pull_request", "repo-1", "pr-1")
	assertContextGraphLink(t, links, "team_owns_objective", "team-1", "objective-1")
	assertContextGraphLink(t, links, "metric_tracks_objective", "metric-1", "objective-1")
	assertContextGraphFact(t, facts, "person", "person-1", "email", "ding@example.com")
	assertContextGraphFact(t, facts, "contract", "contract-1", "status", "draft")
	assertContextGraphFact(t, facts, "pull_request", "pr-1", "branch", "ontology-enterprise")
	assertContextGraphFact(t, facts, "metric", "metric-1", "value", 0.75)
	assertContextGraphEvidence(t, evidence, "value-metric-recorded:metric-1")
}

func TestDefaultEnterpriseDomainMappingRulesSkipMissingOptionalProperties(t *testing.T) {
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

	report, err := runner.ApplyEvent(ctx, enterpriseDomainEvent("legal-contract-created:contract-minimal", "legal.contract.created", map[string]any{
		"id":    "contract-minimal",
		"title": "Minimal agreement",
	}))
	if err != nil {
		t.Fatalf("ApplyEvent legal.contract.created with optional fields omitted: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-enterprise")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "contract", "contract-minimal")
	if len(links) != 0 {
		t.Fatalf("minimal contract should not emit optional legal entity link: %+v", links)
	}
	assertContextGraphFact(t, facts, "contract", "contract-minimal", "title", "Minimal agreement")
	assertContextGraphEvidence(t, evidence, "legal-contract-created:contract-minimal")
}

func propertyTypesContain(properties []PropertyType, propertyID string) bool {
	for _, property := range properties {
		if property.PropertyID == propertyID {
			return true
		}
	}
	return false
}

func enterpriseDomainEvent(id string, eventType string, payload map[string]any) graphprotocol.DomainEvent {
	return graphprotocol.DomainEvent{
		ID:          id,
		TenantID:    "tenant-enterprise",
		Type:        eventType,
		StreamID:    id,
		Version:     1,
		Payload:     payload,
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "ontology-defaults-test"},
		OccurredAt:  time.Unix(100, 0).UTC(),
	}
}

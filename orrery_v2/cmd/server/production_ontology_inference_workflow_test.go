package main

import (
	"context"
	"testing"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/app/rebac"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func productionInferenceWorkflowFixture(t *testing.T) (*productionOntologyInferenceAPI, *db.Store, identity.Principal, ontologyreasoning.EvaluateRequest) {
	t.Helper()
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	if err = organizations.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "tenant-1", Name: "Workflow test"}); err != nil {
		t.Fatal(err)
	}
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	policy := datapolicy.NewPlatformStore(core)
	scopes, err := newProductionOntologyScopeState(core, organizations, projectadmin.NewMemoryRepository(), missioncontrol.NewMemoryProjectionRepository(), toolres.NewMemoryRegistry(), authorizer, authorization.NewSnapshotService(authorization.NewAuthorizationRevisionSource(rebac.NewPlatformStore(core)), authorization.NewPolicyRevisionSource(policy)), &fixedProductionRoleAuthorizer{allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	cursors, err := newProductionOntologyCursorState(ctx, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	concurrency, err := ontologycontract.NewConcurrencyLimiter(nil)
	if err != nil {
		t.Fatal(err)
	}
	contextAPI, err := newProductionOntologyContextAPI(core, runtime.DBRouter, scopes, cursors, concurrency, policy, authorizer, knowledgestore.NewPlatformStore(core), memstore.NewPlatformStore(core))
	if err != nil {
		t.Fatal(err)
	}
	registry := ontology.NewPlatformRegistry(core)
	if err = registry.SaveObjectType(ctx, ontology.ObjectType{ID: "workflow_node", TenantScope: ontology.TenantScopeTenant, DisplayName: "Workflow node", CanonicalKeyRule: "workflow:{id}", Properties: []ontology.PropertyDefinition{{PropertyID: "status", ValueType: ontology.ValueTypeString}}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"workflow_source", "workflow_derived"} {
		l := ontology.LinkType{ID: id, TenantScope: ontology.TenantScopeTenant, FromObjectType: "workflow_node", ToObjectType: "workflow_node", Cardinality: ontology.CardinalityManyToMany, SuggestedAllowed: true}
		if id == "workflow_derived" {
			l.AllowedActions = []string{ontologyreasoning.DerivedLinkActionID}
		}
		if err = registry.SaveLinkType(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	graph := contextgraph.NewPlatformStore(core)
	access := contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"}
	for _, id := range []string{"node-a", "node-b"} {
		if err = graph.SaveObject(ctx, contextgraph.Object{TenantID: "tenant-1", ID: id, ObjectType: "workflow_node", CanonicalKey: "workflow:" + id, Label: id, Properties: map[string]any{"status": "active"}, OwnerAccessRef: access}); err != nil {
			t.Fatal(err)
		}
	}
	if err = graph.SaveLink(ctx, contextgraph.Link{TenantID: "tenant-1", ID: "source", LinkType: "workflow_source", FromObjectID: "node-a", ToObjectID: "node-b", SourceAccessRef: access}); err != nil {
		t.Fatal(err)
	}
	api := &productionOntologyInferenceAPI{gate: scopes.Gate, context: contextAPI, registry: registry, policy: policy, concurrency: concurrency, service: ontologyreasoning.NewService(ontologyreasoning.ServiceOptions{Store: ontologyreasoning.NewPlatformStore(core)}), lifecycle: &ontologyreasoning.LifecycleStore{Store: core}}
	p := identity.NormalizePrincipal(identity.Principal{SubjectProvider: "oidc", Subject: "human", UserID: "reviewer", ViewerKind: identity.ViewerKindUser, CoarseScopes: []string{"ontology.manage", "operations.manage"}})
	request := ontologyreasoning.EvaluateRequest{TenantID: "tenant-1", RootID: "node-a", Rules: []ontologyreasoning.Rule{{ID: "workflow-rule", Version: "1", When: []ontologyreasoning.Atom{{Kind: "link", Type: "workflow_source", Subject: "?a", Object: "?b"}}, Then: ontologyreasoning.Atom{Kind: "link", Type: "workflow_derived", Subject: "?a", Object: "?b"}}}}
	return api, core, p, request
}

func TestProductionOntologyInferenceHumanApprovesAgentCandidateAndExecutes(t *testing.T) {
	a, core, p, input := productionInferenceWorkflowFixture(t)
	ctx := context.Background()
	agent := p
	agent.ViewerKind = identity.ViewerKindAgent
	agent.AgentProfileID = "agent-1"
	agent.Subject = "agent"
	agent.UserID = ""
	run, err := a.Evaluate(ctx, agent, input)
	if err != nil || len(run.CurrentCandidates) != 1 {
		t.Fatalf("agent evaluation: %+v %v", run.CurrentCandidates, err)
	}
	c := run.CurrentCandidates[0]
	req := ontologyreasoning.WorkflowRequest{TenantID: input.TenantID, RunID: run.ID, CandidateID: c.CandidateID, ExpectedRevision: c.Revision, SupportHash: c.SupportHash, Decision: "approve"}
	state, err := a.CandidateWorkflow(ctx, p, "review", req)
	if err != nil || state.DecisionBy != "reviewer" {
		t.Fatalf("human review: %+v %v", state, err)
	}
	req.ExpectedRevision = state.Revision
	state, err = a.CandidateWorkflow(ctx, p, "plan", req)
	if err != nil {
		t.Fatal(err)
	}
	if state.Plan == nil || state.Plan.From.ID != "node-a" || state.Plan.To.ID != "node-b" || state.Plan.LinkType != "workflow_derived" {
		t.Fatalf("incorrect preview: %+v", state.Plan)
	}
	req.ExpectedRevision = state.Revision
	req.PlanID = state.Plan.ID
	req.IdempotencyKey = "human-apply"
	req.Confirmed = true
	state, err = a.CandidateWorkflow(ctx, p, "apply", req)
	if err != nil || state.Receipt == nil {
		t.Fatalf("apply: %+v %v", state, err)
	}
	replayed, err := a.CandidateWorkflow(ctx, p, "apply", req)
	if err != nil || replayed.Receipt.ID != state.Receipt.ID {
		t.Fatalf("host replay: %+v %v", replayed, err)
	}
	var count int
	if err = core.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_inference_links WHERE tenant_id=?`, input.TenantID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("domain assertion count %d %v", count, err)
	}
	// An applied edge cannot independently support a subsequent inference.
	input.Rules[0].When[0].Type = "workflow_derived"
	next, err := a.Evaluate(ctx, p, input)
	if err != nil || len(next.Candidates) != 0 {
		t.Fatalf("derived edge became an independent premise: %+v %v", next.Candidates, err)
	}
}

func TestProductionOntologyInferenceRebuildsCurrentSourcesAndPolicy(t *testing.T) {
	for _, change := range []string{"source_removed", "field_masked", "rule_revised", "permission_revoked"} {
		t.Run(change, func(t *testing.T) {
			a, core, p, input := productionInferenceWorkflowFixture(t)
			p.ViewerKind, p.AgentProfileID = identity.ViewerKindAgent, "agent-reviewer"
			ctx := context.Background()
			run, err := a.Evaluate(ctx, p, input)
			if err != nil {
				t.Fatal(err)
			}
			c := run.CurrentCandidates[0]
			req := ontologyreasoning.WorkflowRequest{TenantID: input.TenantID, RunID: run.ID, CandidateID: c.CandidateID, ExpectedRevision: c.Revision, SupportHash: c.SupportHash, Decision: "approve", Reason: "The current source supports this relationship."}
			switch change {
			case "source_removed":
				_, err = core.Exec(ctx, `DELETE FROM context_graph_links WHERE tenant_id=? AND id='source'`, input.TenantID)
			case "field_masked":
				err = datapolicy.NewPlatformStore(core).SaveFieldPolicy(ctx, datapolicy.FieldPolicy{ID: "mask", TenantID: input.TenantID, Consumer: "ontology_reasoning", Purpose: "reasoning_context", ObjectType: "workflow_node", FieldPath: "properties.status", Effect: datapolicy.EffectMask, Enabled: true})
			case "rule_revised":
				input.Rules[0].Version = "2"
				_, err = a.Evaluate(ctx, p, input)
			case "permission_revoked":
				p.CoarseScopes = []string{}
				p.CredentialScopes = []string{}
				p.CredentialScopeRestricted = true
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.CandidateWorkflow(ctx, p, "review", req); err == nil {
				t.Fatalf("stale approval accepted after %s", change)
			}
			var count int
			if err = core.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_inference_links`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("unauthorized domain write: %d %v", count, err)
			}
		})
	}
}

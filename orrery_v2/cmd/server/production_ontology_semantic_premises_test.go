package main

import (
	"context"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
)

func TestProductionOntologyRelationMeaningSurvivesPolicyAndAPIPack(t *testing.T) {
	api, core, principal, request := productionInferenceWorkflowFixture(t)
	ctx := context.Background()
	access := contextgraph.AccessRef{Kind: "organization", ID: request.TenantID, TenantID: request.TenantID}
	graph := contextgraph.NewPlatformStore(core)
	for _, id := range []string{"node-a", "node-b"} {
		if err := graph.SaveObject(ctx, contextgraph.Object{ID: id, TenantID: request.TenantID, ObjectType: "knowledge_entity", CanonicalKey: "knowledge_entity:" + id, Label: id, Properties: map[string]any{"name": id, "entity_kind": "company"}, OwnerAccessRef: access}); err != nil {
			t.Fatal(err)
		}
	}
	definition, err := api.registry.GetLinkType(ctx, "workflow_derived")
	if err != nil {
		t.Fatal(err)
	}
	definition.FromObjectType, definition.ToObjectType = "knowledge_entity", "knowledge_entity"
	if err := ontology.NewPlatformRegistry(core).SaveLinkType(ctx, definition); err != nil {
		t.Fatal(err)
	}
	link := contextgraph.Link{ID: "source", TenantID: request.TenantID, LinkType: "knowledge_entity_related_to", FromObjectID: "node-a", ToObjectID: "node-b", SourceAccessRef: access, SourceStreamID: "semantic-fixture", SourceVersion: 1, Properties: map[string]any{"relation_code": "subsidiary_of", "quote": "unrelated private metadata"}}
	if err := graph.SaveLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	request.Rules[0].When[0].Type = link.LinkType
	request.Rules[0].When[0].RelationCode = "subsidiary_of"
	first, err := api.Evaluate(ctx, principal, request)
	if err != nil || len(first.Candidates) != 1 || first.ContextPack.NeighborLinks[0].Properties["relation_code"] != "subsidiary_of" {
		t.Fatalf("semantic API pack: %+v %v", first, err)
	}
	if first.ContextPack.NeighborLinks[0].Properties["quote"] != nil {
		t.Fatal("API copied arbitrary link metadata")
	}
	link.Properties["relation_code"] = "supplies"
	if err := graph.SaveLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	request.PreviousRunID = first.ID
	second, err := api.Evaluate(ctx, principal, request)
	if err != nil || len(second.Candidates) != 0 || len(second.Request.Evaluation.WithdrawnIDs) != 1 {
		t.Fatalf("semantic update did not withdraw conclusion: %+v %v", second, err)
	}
}

func TestProductionOntologyConflictRetractsAndRecoversThroughModel(t *testing.T) {
	w, _, judge, set := continuousInferenceFixture(t)
	ctx := context.Background()
	p, err := w.api.continuousInferencePrincipal(ctx, set.TenantID, set.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	set.Rules[0].Version = "2"
	set.Rules[0].When = append(set.Rules[0].When, ontologyreasoning.Atom{Kind: "fact", Subject: "?a", Predicate: "status", Value: "active"})
	sets, err := w.api.InferenceRules(ctx, p, ontologyreasoning.RuleSetRequest{Operation: "publish", TenantID: set.TenantID, ID: set.ID, RootID: set.RootID, ExpectedVersion: set.Version, Rules: set.Rules})
	if err != nil {
		t.Fatal(err)
	}
	set = sets[0]
	evaluation := continuousInferenceEvaluationJob(set)
	decide := func(wantCalls int, support bool, effect string) {
		t.Helper()
		if err := w.Evaluate(ctx, evaluation); err != nil {
			t.Fatal(err)
		}
		jobs := continuousInferenceJobs(t, w, ontologyreasoning.InferenceDecisionTopic)
		if len(jobs) != wantCalls {
			t.Fatalf("expected %d decision jobs, got %d", wantCalls, len(jobs))
		}
		if err := w.Decide(ctx, jobs[len(jobs)-1]); err != nil {
			t.Fatal(err)
		}
		if judge.calls != wantCalls || judge.inputs[wantCalls-1].CurrentSupport != support || judge.inputs[wantCalls-1].Preview.Effect != effect {
			t.Fatalf("model calls=%d support=%v effect=%s; want %d %v %s", judge.calls, judge.inputs[wantCalls-1].CurrentSupport, judge.inputs[wantCalls-1].Preview.Effect, wantCalls, support, effect)
		}
	}
	decide(1, true, "assert")
	graph := contextgraph.NewPlatformStore(w.api.lifecycle.Store)
	access := contextgraph.AccessRef{Kind: "organization", ID: set.TenantID, TenantID: set.TenantID}
	if err := graph.SaveFact(ctx, contextgraph.Fact{ID: "competing-status", TenantID: set.TenantID, Subject: contextgraph.Ref{Kind: "workflow_node", ID: "node-a"}, Predicate: "status", Value: "inactive", Status: "active", SourceAccessRef: access, SourceStreamID: "conflict-fixture", SourceVersion: 1}); err != nil {
		t.Fatal(err)
	}
	decide(2, false, "retract")
	var count int
	if err := w.api.lifecycle.Store.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_links WHERE tenant_id=? AND link_type='workflow_derived'`, set.TenantID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("disputed premise kept derived link: %d %v", count, err)
	}
	if err := graph.DeleteFact(ctx, set.TenantID, "competing-status", "conflict-fixture"); err != nil {
		t.Fatal(err)
	}
	decide(3, true, "reactivate")
	if err := w.api.lifecycle.Store.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_links WHERE tenant_id=? AND link_type='workflow_derived'`, set.TenantID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("recovery: %d %v", count, err)
	}
}

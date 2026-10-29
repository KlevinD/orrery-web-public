package main

import (
	"context"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
)

func TestProductionEnterpriseGraphAccessUsesResolvedOwnerRefsInOneBatch(t *testing.T) {
	policy := &recordingEnterpriseGraphVisibilityPolicy{decision: datavisibility.AdapterDecision{
		Coverage: datavisibility.CoverageEntity,
		Resources: []datavisibility.AdapterResourceDecision{
			{Key: "mission-1", ExistenceDisclosure: datavisibility.ExistenceVisible},
			{Key: "task-secret", Hidden: true, ExistenceDisclosure: datavisibility.ExistenceConceal},
		},
	}}
	adapter := productionEnterpriseGraphAccess{policy: policy}
	visible, err := adapter.VisibleObjectIDs(context.Background(), contextgraph.BuildContextBundleRequest{
		TenantID: "tenant-graph", Consumer: "agent-runtime", Purpose: "mission-read",
		ActorRef: contextgraph.Ref{Kind: "agent", ID: "agent-1"}, PermissionScopes: []string{"enterprise_graph.read"},
	}, []contextgraph.Object{
		{ID: "mission-1", TenantID: "tenant-graph", ObjectType: "mission", Properties: map[string]any{"title": "Visible"}, OwnerAccessRef: contextgraph.AccessRef{Kind: "mission", ID: "mission-auth", TenantID: "tenant-auth"}, AccessState: contextgraph.AccessStateResolved},
		{ID: "task-secret", TenantID: "tenant-graph", ObjectType: "task", OwnerAccessRef: contextgraph.AccessRef{Kind: "mission", ID: "mission-secret", TenantID: "tenant-auth"}, AccessState: contextgraph.AccessStateResolved},
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy.calls != 1 || !visible["mission-1"] || visible["task-secret"] {
		t.Fatalf("calls=%d visible=%v", policy.calls, visible)
	}
	if policy.request.Principal.AgentProfileID != "agent-1" || policy.request.Principal.ViewerKind != "agent" || policy.request.Principal.TenantID != "tenant-graph" || len(policy.request.Principal.CoarseScopes) != 1 {
		t.Fatalf("principal = %+v", policy.request.Principal)
	}
	if len(policy.request.Resources) != 2 || len(policy.request.Resources[0].AccessRefs) != 1 || policy.request.Resources[0].AccessRefs[0] != (datavisibility.AccessRef{Kind: "mission", ID: "mission-auth", TenantID: "tenant-auth"}) {
		t.Fatalf("resources = %+v", policy.request.Resources)
	}
}

type recordingEnterpriseGraphVisibilityPolicy struct {
	calls    int
	request  datavisibility.AdapterRequest
	decision datavisibility.AdapterDecision
	err      error
}

func (p *recordingEnterpriseGraphVisibilityPolicy) Evaluate(_ context.Context, request datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	p.calls++
	p.request = request
	return p.decision, p.err
}

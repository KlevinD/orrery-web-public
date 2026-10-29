package irisadapter

import (
	"context"
	"testing"

	"orrery_v2/internal/app/actioncontract"
	contextgraph "orrery_v2/internal/app/contextgraph"
)

func TestContextBundleToIrisSnapshotPreservesObjectsLinksEvidenceAndPolicy(t *testing.T) {
	adapter := NewAdapter()
	bundle := contextgraph.ContextBundle{
		BundleID: "bundle-1",
		TenantID: "tenant-1",
		Consumer: "iris",
		Purpose:  "mission_assignment",
		ActorRef: contextgraph.Ref{Kind: "human", ID: "klevin"},
		Schema: contextgraph.SchemaDictionary{
			ObjectTypes: []contextgraph.SchemaObjectType{{
				ID:          "mission",
				DisplayName: "Mission",
			}},
			PropertyTypes: []contextgraph.SchemaPropertyType{{
				ObjectTypeID: "mission",
				PropertyID:   "title",
				ValueType:    "string",
			}},
			VersionHash: "sha256:schema",
		},
		Objects: []contextgraph.Object{{
			ID:         "mission-1",
			ObjectType: "mission",
			Label:      "Launch ontology",
			Properties: map[string]any{
				"title": "Launch ontology",
			},
			EvidenceRefs: []contextgraph.Ref{{Kind: "knowledge_document", ID: "doc-1"}},
		}},
		Links: []contextgraph.Link{{
			ID:           "link-1",
			LinkType:     "has_task",
			FromObjectID: "mission-1",
			ToObjectID:   "task-1",
		}},
		Facts: []contextgraph.Fact{{
			ID:        "fact-1",
			Subject:   contextgraph.Ref{Kind: "mission", ID: "mission-1"},
			Predicate: "goal",
			Value:     "Launch ontology",
		}},
		Evidence: []contextgraph.Evidence{{
			ID:        "doc-1",
			SourceRef: contextgraph.Ref{Kind: "knowledge_document", ID: "doc-1"},
			Summary:   "Plan evidence",
		}},
		PermissionConstraints: []contextgraph.PermissionConstraint{{
			Scope:  "mission.read",
			Reason: "field policy",
		}},
		Confidence: 0.9,
	}

	snapshot := adapter.ContextBundleToSnapshot(bundle)

	if snapshot.SnapshotID != "bundle-1" || snapshot.TenantID != "tenant-1" || snapshot.Purpose != "mission_assignment" {
		t.Fatalf("snapshot identity = %+v", snapshot)
	}
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].ID != "mission-1" || snapshot.Nodes[0].Properties["title"] != "Launch ontology" {
		t.Fatalf("snapshot nodes = %+v", snapshot.Nodes)
	}
	if len(snapshot.Edges) != 1 || snapshot.Edges[0].Kind != "has_task" {
		t.Fatalf("snapshot edges = %+v", snapshot.Edges)
	}
	if len(snapshot.Facts) != 1 || snapshot.Facts[0].Predicate != "goal" {
		t.Fatalf("snapshot facts = %+v", snapshot.Facts)
	}
	if len(snapshot.Evidence) != 1 || snapshot.Evidence[0].ID != "doc-1" {
		t.Fatalf("snapshot evidence = %+v", snapshot.Evidence)
	}
	if len(snapshot.PermissionConstraints) != 1 {
		t.Fatalf("snapshot permission constraints = %+v", snapshot.PermissionConstraints)
	}
	if len(snapshot.Schema.ObjectTypes) != 1 || snapshot.Schema.ObjectTypes[0].ID != "mission" ||
		len(snapshot.Schema.PropertyTypes) != 1 || snapshot.Schema.PropertyTypes[0].PropertyID != "title" ||
		snapshot.Schema.VersionHash != "sha256:schema" {
		t.Fatalf("snapshot schema = %+v", snapshot.Schema)
	}
}

func TestAcceptedIrisActionMapsToActionRunStartRequest(t *testing.T) {
	adapter := NewAdapter()

	req, err := adapter.AcceptedActionToStartRun(AcceptedAction{
		RunID:        "run-iris-1",
		TenantID:     "tenant-1",
		WorkspaceID:  "workspace-1",
		ActionTypeID: "iris.accept_recommendation",
		Actor:        ActionActor{Kind: "human", ID: "klevin"},
		RuntimeID:    "decision-run-1",
		HumanIntent:  contextgraph.Ref{Kind: "iris_recommendation", ID: "rec-1"},
		ObjectRefs:   []contextgraph.Ref{{Kind: "mission", ID: "mission-1"}},
		ArgsHash:     "sha256:accept-v1",
		EvidenceRefs: []contextgraph.Ref{{Kind: "iris_snapshot", ID: "bundle-1"}},
	})
	if err != nil {
		t.Fatalf("AcceptedActionToStartRun: %v", err)
	}

	if req.RunID != actioncontract.ActionRunID("run-iris-1") || req.ActionTypeID != actioncontract.ActionTypeID("iris.accept_recommendation") {
		t.Fatalf("start run ids = %+v", req)
	}
	if req.Actor.Kind != actioncontract.SubjectHuman || req.Actor.ID != "klevin" {
		t.Fatalf("actor = %+v", req.Actor)
	}
	if req.Runtime.Kind != actioncontract.RuntimeIRIS || req.Runtime.ID != "decision-run-1" {
		t.Fatalf("runtime = %+v", req.Runtime)
	}
	if req.HumanIntent.Kind != "iris_recommendation" || req.HumanIntent.ID != "rec-1" {
		t.Fatalf("human intent = %+v", req.HumanIntent)
	}
	if len(req.ObjectRefs) != 1 || req.ObjectRefs[0].Kind != "mission" || req.ObjectRefs[0].ID != "mission-1" {
		t.Fatalf("object refs = %+v", req.ObjectRefs)
	}
	if req.ArgsHash != "sha256:accept-v1" || len(req.EvidenceRefs) != 1 || req.EvidenceRefs[0].Kind != "iris_snapshot" {
		t.Fatalf("args/evidence = %q / %+v", req.ArgsHash, req.EvidenceRefs)
	}
}

func TestBuildSnapshotFromMCPContextBundleCallsEnterpriseGraphTool(t *testing.T) {
	ctx := context.Background()
	gateway := &recordingMCPGateway{
		output: map[string]any{
			"bundle_id": "bundle-mcp-1",
			"tenant_id": "tenant-1",
			"consumer":  "iris",
			"purpose":   "mission_assignment",
			"actor_ref": map[string]any{"kind": "human", "id": "klevin"},
			"objects": []any{map[string]any{
				"id":          "mission-1",
				"tenant_id":   "tenant-1",
				"object_type": "mission",
				"label":       "Launch ontology",
				"properties":  map[string]any{"title": "Launch ontology"},
			}},
			"schema": map[string]any{
				"object_types": []any{map[string]any{
					"id":           "mission",
					"display_name": "Mission",
				}},
				"property_types": []any{map[string]any{
					"object_type_id": "mission",
					"property_id":    "title",
					"value_type":     "string",
				}},
				"version_hash": "sha256:schema",
			},
			"permission_constraints": []any{map[string]any{
				"scope":  "enterprise_graph.read",
				"reason": "field_policy",
			}},
			"freshness":  map[string]any{"as_of": "2026-06-19T12:00:00Z"},
			"confidence": 0.95,
		},
	}
	consumer := NewMCPContextConsumer(gateway)

	snapshot, err := consumer.BuildSnapshot(ctx, BuildSnapshotRequest{
		TenantID:    "tenant-1",
		PrincipalID: "iris-runtime",
		AgentID:     "iris",
		Purpose:     "mission_assignment",
		ActorRef:    contextgraph.Ref{Kind: "human", ID: "klevin"},
		SubjectRefs: []contextgraph.Ref{{Kind: "mission", ID: "mission-1"}},
		IncludeKinds: []string{
			"mission",
		},
		MaxDepth:    1,
		TokenBudget: 8000,
	})
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}

	if gateway.request.ToolID != EnterpriseGraphBuildContextBundleToolID {
		t.Fatalf("tool id = %q", gateway.request.ToolID)
	}
	if gateway.request.PrincipalID != "iris-runtime" || gateway.request.AgentID != "iris" {
		t.Fatalf("call identity = %+v", gateway.request)
	}
	if len(gateway.request.Permissions) != 1 || gateway.request.Permissions[0] != EnterpriseGraphReadPermission {
		t.Fatalf("permissions = %+v", gateway.request.Permissions)
	}
	if gateway.request.Arguments["consumer"] != "iris" || gateway.request.Arguments["purpose"] != "mission_assignment" {
		t.Fatalf("arguments = %+v", gateway.request.Arguments)
	}
	if snapshot.SnapshotID != "bundle-mcp-1" || snapshot.TenantID != "tenant-1" || snapshot.Purpose != "mission_assignment" {
		t.Fatalf("snapshot identity = %+v", snapshot)
	}
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Kind != "mission" || snapshot.Nodes[0].Properties["title"] != "Launch ontology" {
		t.Fatalf("snapshot nodes = %+v", snapshot.Nodes)
	}
	if len(snapshot.PermissionConstraints) != 1 || snapshot.Confidence != 0.95 {
		t.Fatalf("policy/confidence = %+v / %v", snapshot.PermissionConstraints, snapshot.Confidence)
	}
	if len(snapshot.Schema.ObjectTypes) != 1 || snapshot.Schema.ObjectTypes[0].ID != "mission" ||
		len(snapshot.Schema.PropertyTypes) != 1 || snapshot.Schema.PropertyTypes[0].PropertyID != "title" ||
		snapshot.Schema.VersionHash != "sha256:schema" {
		t.Fatalf("snapshot schema = %+v", snapshot.Schema)
	}
}

type recordingMCPGateway struct {
	request MCPToolCallRequest
	output  map[string]any
	err     error
}

func (g *recordingMCPGateway) CallTool(ctx context.Context, req MCPToolCallRequest) (MCPToolCallResult, error) {
	g.request = req
	return MCPToolCallResult{Output: g.output}, g.err
}

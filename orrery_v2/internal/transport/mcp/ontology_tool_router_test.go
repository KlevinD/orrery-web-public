package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestToolRouterRoutesInferenceWithTrustedTenantAndPermission(t *testing.T) {
	for _, toolID := range []string{string(toolstd.OntologyInfer), string(toolstd.OntologyInferenceWorkflow)} {
		t.Run(toolID, func(t *testing.T) {
			registry := toolres.NewMemoryRegistry()
			for _, definition := range toolstd.Definitions(time.Now().UTC()) {
				if string(definition.ID) == toolID {
					if err := registry.Register(context.Background(), definition); err != nil {
						t.Fatal(err)
					}
				}
			}
			executor := &recordingOntologyToolExecutor{result: OntologyToolResult{Output: map[string]any{"run": map[string]any{"id": "run-1"}}}}
			router := NewToolRouter(ToolRouterOptions{Registry: registry, OntologyExecutor: executor})
			request := CallToolRequest{TenantID: "trusted", ToolID: toolID, PrincipalID: "agent-1", AgentID: "agent-1", Permissions: []string{toolstd.PermissionOntologyManage}, Arguments: map[string]any{"tenant_id": "forged", "root_id": "root-1", "rules": []any{}}}
			if _, err := router.CallTool(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			var input map[string]any
			if err := json.Unmarshal(executor.request.InferenceJSON, &input); err != nil {
				t.Fatal(err)
			}
			if executor.request.TenantID != "trusted" || input["root_id"] != "root-1" {
				t.Fatal("inference route lost trusted identity or payload")
			}
			request.Permissions = []string{"ontology.read"}
			if _, err := router.CallTool(context.Background(), request); err == nil {
				t.Fatal("inference bypassed ontology.manage")
			}
		})
	}
}

func TestToolRouterGroundedContextRequiresReadPermission(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	for _, d := range toolstd.Definitions(time.Now().UTC()) {
		if d.ID == toolstd.OntologyGroundedContext {
			if err := registry.Register(context.Background(), d); err != nil {
				t.Fatal(err)
			}
		}
	}
	executor := &recordingOntologyToolExecutor{result: OntologyToolResult{Output: map[string]any{"context": map[string]any{}}}}
	router := NewToolRouter(ToolRouterOptions{Registry: registry, OntologyExecutor: executor})
	req := CallToolRequest{TenantID: "trusted", ToolID: string(toolstd.OntologyGroundedContext), PrincipalID: "agent-1", AgentID: "agent-1", Permissions: []string{"knowledge.read"}, Arguments: map[string]any{"tenant_id": "forged", "root_id": "root-1", "rules": []any{}}}
	if _, err := router.CallTool(context.Background(), req); err == nil {
		t.Fatal("read tool bypassed ontology.read")
	}
	req.Permissions = []string{"ontology.read"}
	if _, err := router.CallTool(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if executor.request.TenantID != "trusted" || len(executor.request.Permissions) != 1 || executor.request.Permissions[0] != "ontology.read" {
		t.Fatal("read identity/permissions changed")
	}
}

func TestToolRouterRoutesOntologyProposalThroughPermissionAndTenantBoundary(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Now().UTC()) {
		if definition.ID == toolstd.OntologyProposalCreate {
			if err := registry.Register(context.Background(), definition); err != nil {
				t.Fatal(err)
			}
		}
	}
	executor := &recordingOntologyToolExecutor{result: OntologyToolResult{Output: map[string]any{"proposal_ids": []string{"proposal-1"}}}}
	router := NewToolRouter(ToolRouterOptions{Registry: registry, OntologyExecutor: executor})
	result, err := router.CallTool(context.Background(), CallToolRequest{
		TenantID: "tenant-trusted", ToolID: string(toolstd.OntologyProposalCreate), PrincipalID: "ontology-steward-agent", AgentID: "ontology-steward-agent",
		Permissions: []string{toolstd.PermissionOntologyManage},
		Arguments: map[string]any{
			"tenant_id": "tenant-untrusted", "engine": "ontology-steward", "model": "agent-runtime", "model_version": "v1",
			"run_id": "run-1", "input_hash": "input-1", "prompt_hash": "prompt-1",
			"candidates": []any{map[string]any{
				"target_kind": "mapping_rule", "target_id": "people.person.updated", "metadata_key": "source_domain",
				"proposed_value": "people", "confidence": 0.97, "reason": "People is authoritative.",
				"evidence_refs": []any{map[string]any{"kind": "knowledge_document", "id": "doc-1", "metadata": map[string]any{"revision": "7"}}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if executor.toolID != string(toolstd.OntologyProposalCreate) || executor.request.TenantID != "tenant-trusted" ||
		executor.request.AgentID != "ontology-steward-agent" || len(executor.request.Candidates) != 1 ||
		executor.request.Candidates[0].ProposedValue != "people" || len(executor.request.Candidates[0].EvidenceRefs) != 1 ||
		executor.request.Candidates[0].EvidenceRefs[0].Metadata["revision"] != "7" {
		t.Fatalf("ontology request = %+v", executor.request)
	}
	if _, ok := result.Output["proposal_ids"]; !ok {
		t.Fatalf("ontology output = %+v", result.Output)
	}
}

func TestToolRouterDeniesOntologyMutationWithoutManagePermission(t *testing.T) {
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Now().UTC()) {
		if definition.ID == toolstd.OntologyPromotionApply {
			if err := registry.Register(context.Background(), definition); err != nil {
				t.Fatal(err)
			}
		}
	}
	router := NewToolRouter(ToolRouterOptions{Registry: registry, OntologyExecutor: &recordingOntologyToolExecutor{}})
	if _, err := router.CallTool(context.Background(), CallToolRequest{
		TenantID: "tenant-1", ToolID: string(toolstd.OntologyPromotionApply), PrincipalID: "agent-1", AgentID: "agent-1",
		Permissions: []string{"ontology.read"}, Arguments: map[string]any{"plan_id": "plan-1", "confirmed": true},
	}); err == nil {
		t.Fatal("ontology apply without ontology.manage was allowed")
	}
}

type recordingOntologyToolExecutor struct {
	toolID  string
	request OntologyToolRequest
	result  OntologyToolResult
	err     error
}

func (e *recordingOntologyToolExecutor) ExecuteOntologyTool(_ context.Context, toolID string, request OntologyToolRequest) (OntologyToolResult, error) {
	e.toolID = toolID
	e.request = request
	return e.result, e.err
}

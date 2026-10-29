package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyreasoning"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type recordingInferenceToolService struct {
	principal     identity.Principal
	request       ontologyreasoning.EvaluateRequest
	calls         int
	workflowCalls int
}

func (s *recordingInferenceToolService) GroundedContext(_ context.Context, p identity.Principal, input ontologyreasoning.GroundedContextRequest) (ontologyreasoning.GroundedContext, error) {
	s.principal = p
	s.calls++
	return ontologyreasoning.GroundedContext{TenantID: input.TenantID, RootID: input.RootID}, nil
}

func TestProductionOntologyGroundedContextToolPreservesReadIdentity(t *testing.T) {
	s := &recordingInferenceToolService{}
	e := productionOntologyStewardToolExecutor{inference: s}
	input := []byte(`{"tenant_id":"untrusted","root_id":"root-1","rules":[]}`)
	result, err := e.ExecuteOntologyTool(context.Background(), string(toolstd.OntologyGroundedContext), mcptransport.OntologyToolRequest{TenantID: "trusted", AgentID: "agent-1", Permissions: []string{"ontology.read"}, InferenceJSON: input})
	if err != nil || result.Output["context"] == nil || len(s.principal.CoarseScopes) != 1 || s.principal.CoarseScopes[0] != "ontology.read" || !s.principal.CredentialScopeRestricted {
		t.Fatalf("read identity: %+v %v", s.principal, err)
	}
	value := result.Output["context"].(map[string]any)
	if value["tenant_id"] != "trusted" {
		t.Fatal("model tenant overrode runtime")
	}
	_, err = e.ExecuteOntologyTool(context.Background(), string(toolstd.OntologyGroundedContext), mcptransport.OntologyToolRequest{TenantID: "trusted", AgentID: "agent-1", Permissions: []string{"ontology.read"}, InferenceJSON: []byte(`{"tenant_id":"trusted","root_id":"root-1","rules":[],"permissions":["ontology.manage"]}`)})
	if err == nil || s.calls != 1 {
		t.Fatal("forged permissions reached read service")
	}
}

func (s *recordingInferenceToolService) CandidateWorkflow(_ context.Context, p identity.Principal, _ string, req ontologyreasoning.WorkflowRequest) (ontologyreasoning.CandidateState, error) {
	s.principal = p
	s.workflowCalls++
	return ontologyreasoning.CandidateState{CandidateID: req.CandidateID}, nil
}

func (s *recordingInferenceToolService) Evaluate(_ context.Context, p identity.Principal, req ontologyreasoning.EvaluateRequest) (ontologyreasoning.ReasoningRun, error) {
	s.principal = p
	s.request = req
	s.calls++
	return ontologyreasoning.ReasoningRun{ID: "run-1", TenantID: req.TenantID}, nil
}

func TestProductionOntologyInferenceWorkflowToolRejectsForgedArguments(t *testing.T) {
	service := &recordingInferenceToolService{}
	executor := productionOntologyStewardToolExecutor{inference: service}
	valid := `{"operation":"review","tenant_id":"tenant-1","run_id":"run-1","candidate_id":"candidate-1","expected_revision":1,"support_hash":"hash","decision":"approve","reason":"supported"}`
	for _, field := range []string{`"actor":"human"`, `"actor_kind":"human"`, `"permissions":["ontology.manage"]`} {
		payload := strings.TrimSuffix(valid, "}") + "," + field + "}"
		_, err := executor.ExecuteOntologyTool(context.Background(), string(toolstd.OntologyInferenceWorkflow), mcptransport.OntologyToolRequest{TenantID: "tenant-1", AgentID: "agent-1", Permissions: []string{toolstd.PermissionOntologyManage}, InferenceJSON: []byte(payload)})
		if err == nil || service.workflowCalls != 0 {
			t.Fatalf("forged model arguments reached workflow: %s %v", field, err)
		}
	}
	for _, payload := range []string{valid + ` {}`, strings.Repeat(" ", 16*1024) + valid} {
		if _, err := executor.ExecuteOntologyTool(context.Background(), string(toolstd.OntologyInferenceWorkflow), mcptransport.OntologyToolRequest{TenantID: "tenant-1", AgentID: "agent-1", InferenceJSON: []byte(payload)}); err == nil || service.workflowCalls != 0 {
			t.Fatalf("malformed model input reached workflow: %v", err)
		}
	}
}

func TestProductionOntologyInferenceToolPreservesTrustedIdentityWithoutScopeExpansion(t *testing.T) {
	service := &recordingInferenceToolService{}
	executor := productionOntologyStewardToolExecutor{inference: service}
	input, _ := json.Marshal(ontologyreasoning.EvaluateRequest{TenantID: "forged", RootID: "root-1", Rules: []ontologyreasoning.Rule{}})
	result, err := executor.ExecuteOntologyTool(context.Background(), string(toolstd.OntologyInfer), mcptransport.OntologyToolRequest{TenantID: "trusted", AgentID: "agent-1", Permissions: []string{toolstd.PermissionOntologyManage}, InferenceJSON: input})
	if err != nil {
		t.Fatal(err)
	}
	if service.request.TenantID != "trusted" || service.principal.AgentProfileID != "agent-1" || !service.principal.CredentialScopeRestricted || len(service.principal.CoarseScopes) != 1 || service.principal.CoarseScopes[0] != toolstd.PermissionOntologyManage || result.Output["run"] == nil {
		t.Fatalf("inference identity: %+v %+v", service.principal, service.request)
	}
	if _, err := executor.ExecuteOntologyTool(context.Background(), string(toolstd.OntologyInfer), mcptransport.OntologyToolRequest{InferenceJSON: []byte(`{"actor":"forged"}`)}); err == nil || service.calls != 1 {
		t.Fatal("caller-supplied identity reached inference")
	}
}

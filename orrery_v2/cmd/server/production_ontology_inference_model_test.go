package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestProductionOntologyInferenceModelDecidesAndExecutesThroughMCP(t *testing.T) {
	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			api, core, _, input := productionInferenceWorkflowFixture(t)
			ctx := context.Background()
			registry := toolres.NewMemoryRegistry()
			for _, definition := range toolstd.Definitions(time.Now().UTC()) {
				if definition.ID == toolstd.OntologyInfer || definition.ID == toolstd.OntologyInferenceWorkflow {
					if err := registry.Register(ctx, definition); err != nil {
						t.Fatal(err)
					}
				}
			}
			router := mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: registry, OntologyExecutor: productionOntologyStewardToolExecutor{inference: api}})
			call := func(tool string, argument any, output string, target any) {
				t.Helper()
				payload, err := json.Marshal(argument)
				if err != nil {
					t.Fatal(err)
				}
				var args map[string]any
				if err := json.Unmarshal(payload, &args); err != nil {
					t.Fatal(err)
				}
				args["tenant_id"] = "forged-model-tenant"
				result, err := router.CallTool(ctx, mcptransport.CallToolRequest{TenantID: input.TenantID, ToolID: tool, PrincipalID: "agent-1", AgentID: "agent-1", Permissions: []string{toolstd.PermissionOntologyManage}, Arguments: args})
				if err != nil {
					t.Fatal(err)
				}
				body, err := json.Marshal(result.Output[output])
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(body, target); err != nil {
					t.Fatal(err)
				}
			}
			var run ontologyreasoning.ReasoningRun
			call(string(toolstd.OntologyInfer), input, "run", &run)
			if len(run.CurrentCandidates) != 1 {
				t.Fatalf("candidates: %+v", run.CurrentCandidates)
			}
			c := run.CurrentCandidates[0]
			request := struct {
				Operation string `json:"operation"`
				ontologyreasoning.WorkflowRequest
			}{Operation: "review", WorkflowRequest: ontologyreasoning.WorkflowRequest{TenantID: input.TenantID, RunID: run.ID, CandidateID: c.CandidateID, ExpectedRevision: c.Revision, SupportHash: c.SupportHash, Decision: decision, Reason: "The model has checked the current source and relationship evidence."}}
			var state ontologyreasoning.CandidateState
			call(string(toolstd.OntologyInferenceWorkflow), request, "candidate", &state)
			if state.DecisionKind != "agent" || state.DecisionBy != "agent-1" || state.DecisionReason != request.Reason {
				t.Fatalf("model decision not recorded: %+v", state)
			}
			if decision == "reject" {
				var count int
				if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_inference_links`).Scan(&count); err != nil || count != 0 || state.Status != "rejected" {
					t.Fatalf("rejected candidate wrote a relationship: %+v %d %v", state, count, err)
				}
				return
			}
			request.Operation, request.ExpectedRevision = "plan", state.Revision
			call(string(toolstd.OntologyInferenceWorkflow), request, "candidate", &state)
			if state.Plan == nil || state.Plan.From.ID != "node-a" || state.Plan.To.ID != "node-b" {
				t.Fatalf("model preview: %+v", state)
			}
			request.Operation, request.ExpectedRevision, request.PlanID = "apply", state.Revision, state.Plan.ID
			request.Confirmed, request.IdempotencyKey = true, "model-decision-once"
			call(string(toolstd.OntologyInferenceWorkflow), request, "candidate", &state)
			if state.Status != "applied" || state.Receipt == nil || state.Receipt.ActorKind != "agent" {
				t.Fatalf("model apply: %+v", state)
			}
			action, err := actioncontract.NewPlatformRepository(core).GetActionRun(ctx, actioncontract.ActionRunID(state.Receipt.ActionRunID))
			if err != nil || action.Actor.Kind != actioncontract.SubjectAgent || action.Actor.ID != "agent-1" || action.Runtime.Kind != actioncontract.RuntimeAgent {
				t.Fatalf("model impersonated a human: %+v %v", action, err)
			}
			var replay ontologyreasoning.CandidateState
			api.lifecycle = &ontologyreasoning.LifecycleStore{Store: core}
			call(string(toolstd.OntologyInferenceWorkflow), request, "candidate", &replay)
			if replay.Receipt == nil || replay.Receipt.ID != state.Receipt.ID || replay.DecisionReason != "" {
				t.Fatalf("model retry: %+v", replay)
			}
		})
	}
}

func TestProductionOntologyInferenceModelRequiresIdentityPermissionAndReason(t *testing.T) {
	api, _, human, input := productionInferenceWorkflowFixture(t)
	ctx := context.Background()
	run, err := api.Evaluate(ctx, human, input)
	if err != nil {
		t.Fatal(err)
	}
	c := run.CurrentCandidates[0]
	for _, kind := range []string{"no_identity", "no_reason", "read_only"} {
		t.Run(kind, func(t *testing.T) {
			agent := human
			agent.ViewerKind, agent.AgentProfileID = identity.ViewerKindAgent, "agent-1"
			req := ontologyreasoning.WorkflowRequest{TenantID: input.TenantID, RunID: run.ID, CandidateID: c.CandidateID, ExpectedRevision: c.Revision, SupportHash: c.SupportHash, Decision: "approve", Reason: "Evidence supports this relation."}
			want := ontologyscope.ErrAccessDenied
			switch kind {
			case "no_identity":
				agent.AgentProfileID = ""
			case "no_reason":
				req.Reason = " "
				want = ontologyreasoning.ErrInvalidRequest
			case "read_only":
				agent.CredentialScopeRestricted = true
				agent.CredentialScopes = []string{"ontology.read"}
			}
			if _, err := api.CandidateWorkflow(ctx, agent, "review", req); !errors.Is(err, want) {
				t.Fatalf("%s: %v, want %v", kind, err, want)
			}
		})
	}
}

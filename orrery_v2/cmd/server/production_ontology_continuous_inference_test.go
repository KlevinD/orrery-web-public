package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/platform/outbox"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type continuousInferenceProfiles struct{ profile agentcore.AgentProfile }

func (p *continuousInferenceProfiles) GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return p.profile, nil
}

type continuousInferenceJudge struct {
	calls  int
	fail   bool
	before func()
	inputs []inferenceDecisionInput
}

func (j *continuousInferenceJudge) Judge(_ context.Context, input inferenceDecisionInput) (ontologyreasoning.ModelDecision, error) {
	j.calls++
	j.inputs = append(j.inputs, input)
	if j.before != nil {
		j.before()
	}
	if j.fail {
		return ontologyreasoning.ModelDecision{}, errors.New("model unavailable")
	}
	decision := "approve"
	if !input.CurrentSupport {
		decision = "retract"
	}
	return ontologyreasoning.ModelDecision{Decision: decision, Reason: "Model checked the exact proof and planned relationship.", ModelID: "test-model"}, nil
}

func continuousInferenceFixture(t *testing.T) (*productionContinuousInference, *continuousInferenceProfiles, *continuousInferenceJudge, ontologyreasoning.RuleSet) {
	t.Helper()
	api, core, _, input := productionInferenceWorkflowFixture(t)
	profiles := &continuousInferenceProfiles{profile: agentcore.AgentProfile{ID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true, ToolRefs: []agentcore.ToolRef{{ToolID: toolstd.OntologyInfer, PermissionScope: "ontology.manage"}, {ToolID: toolstd.OntologyInferenceWorkflow, PermissionScope: "ontology.manage"}, {ToolID: toolstd.OntologyInferenceRules, PermissionScope: "ontology.manage"}}}}
	api.ruleSets = &ontologyreasoning.RuleSetStore{Store: core}
	api.profiles = profiles
	p, err := api.continuousInferencePrincipal(context.Background(), input.TenantID, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	sets, err := api.InferenceRules(context.Background(), p, ontologyreasoning.RuleSetRequest{Operation: "publish", TenantID: input.TenantID, ID: "watch", RootID: input.RootID, Rules: input.Rules})
	if err != nil {
		t.Fatal(err)
	}
	judge := &continuousInferenceJudge{}
	return &productionContinuousInference{api: api, judge: judge}, profiles, judge, sets[0]
}

func continuousInferenceJobs(t *testing.T, w *productionContinuousInference, topic string) []outbox.Job {
	t.Helper()
	rows, err := w.api.lifecycle.Store.Query(context.Background(), `SELECT id,payload FROM platform_outbox_jobs WHERE topic=? ORDER BY created_at,id`, topic)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var jobs []outbox.Job
	for rows.Next() {
		var job outbox.Job
		job.Topic = topic
		if err := rows.Scan(&job.ID, &job.Payload); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return jobs
}

func continuousInferenceEvaluationJob(set ontologyreasoning.RuleSet) outbox.Job {
	body, _ := json.Marshal(ontologyreasoning.InferenceJob{TenantID: set.TenantID, RuleSetID: set.ID, Version: set.Version})
	return outbox.Job{ID: "test-evaluation", Topic: ontologyreasoning.InferenceEvaluationTopic, Payload: body}
}

func TestProductionOntologyContinuousInferenceModelAppliesRevisesAndRetracts(t *testing.T) {
	w, _, judge, set := continuousInferenceFixture(t)
	ctx := context.Background()
	evaluation := continuousInferenceEvaluationJob(set)
	if err := w.Evaluate(ctx, evaluation); err != nil {
		t.Fatal(err)
	}
	jobs := continuousInferenceJobs(t, w, ontologyreasoning.InferenceDecisionTopic)
	if len(jobs) != 1 {
		t.Fatalf("decision task count %d", len(jobs))
	}
	if err := w.Decide(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	if judge.calls != 1 || !judge.inputs[0].CurrentSupport || judge.inputs[0].Preview.Effect != "assert" {
		t.Fatalf("model was not used: %+v", judge)
	}
	if err := w.Decide(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	if judge.calls != 1 {
		t.Fatal("duplicate delivery called the model again")
	}
	// This input includes the newly written edge and its own evidence, which
	// must not invalidate its independent proof or call the model again.
	if err := w.Evaluate(ctx, evaluation); err != nil {
		t.Fatal(err)
	}
	if len(continuousInferenceJobs(t, w, ontologyreasoning.InferenceDecisionTopic)) != 1 {
		t.Fatal("own generated evidence caused another decision")
	}
	p, err := w.api.continuousInferencePrincipal(ctx, set.TenantID, set.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	set.Rules[0].Version = "2"
	sets, err := w.api.InferenceRules(ctx, p, ontologyreasoning.RuleSetRequest{Operation: "publish", TenantID: set.TenantID, ID: set.ID, ExpectedVersion: set.Version, RootID: set.RootID, Rules: set.Rules})
	if err != nil {
		t.Fatal(err)
	}
	set = sets[0]
	evaluation = continuousInferenceEvaluationJob(set)
	if err := w.Evaluate(ctx, evaluation); err != nil {
		t.Fatal(err)
	}
	jobs = continuousInferenceJobs(t, w, ontologyreasoning.InferenceDecisionTopic)
	if len(jobs) != 2 {
		t.Fatalf("revision job missing: %d", len(jobs))
	}
	if err := w.Decide(ctx, jobs[1]); err != nil {
		t.Fatal(err)
	}
	if judge.calls != 2 || judge.inputs[1].Preview.Effect != "revise" {
		t.Fatal("revision did not go through model")
	}
	// Fixture's source has an empty stream; simulate its source-owned deletion.
	if _, err := w.api.lifecycle.Store.Exec(ctx, `DELETE FROM context_graph_links WHERE tenant_id=? AND id='source'`, set.TenantID); err != nil {
		t.Fatal(err)
	}
	if err := w.Evaluate(ctx, evaluation); err != nil {
		t.Fatal(err)
	}
	jobs = continuousInferenceJobs(t, w, ontologyreasoning.InferenceDecisionTopic)
	if len(jobs) != 3 {
		t.Fatalf("withdrawal job missing: %d", len(jobs))
	}
	if err := w.Decide(ctx, jobs[2]); err != nil {
		t.Fatal(err)
	}
	if judge.calls != 3 || judge.inputs[2].CurrentSupport || judge.inputs[2].Preview.Effect != "retract" {
		t.Fatal("withdrawal did not go through model")
	}
	var count int
	if err := w.api.lifecycle.Store.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_links WHERE tenant_id=?`, set.TenantID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retraction: %d %v", count, err)
	}
	if err := w.api.lifecycle.Store.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_inference_decisions`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("model judgment history: %d %v", count, err)
	}
	if err := w.api.lifecycle.Store.QueryRow(ctx, `SELECT COUNT(*) FROM action_runs WHERE actor_kind='agent' AND actor_id='agent-1'`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("actual model actor missing: %d %v", count, err)
	}
}

func TestProductionOntologyContinuousInferencePausingAndGrantRevocationFenceModel(t *testing.T) {
	for _, scenario := range []string{"pause", "revoke", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			w, profiles, judge, set := continuousInferenceFixture(t)
			ctx := context.Background()
			if err := w.Evaluate(ctx, continuousInferenceEvaluationJob(set)); err != nil {
				t.Fatal(err)
			}
			jobs := continuousInferenceJobs(t, w, ontologyreasoning.InferenceDecisionTopic)
			judge.before = func() {
				switch scenario {
				case "pause":
					p, err := w.api.continuousInferencePrincipal(ctx, set.TenantID, set.AgentID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = w.api.InferenceRules(ctx, p, ontologyreasoning.RuleSetRequest{Operation: "pause", TenantID: set.TenantID, ID: set.ID, ExpectedVersion: set.Version}); err != nil {
						t.Fatal(err)
					}
				case "revoke":
					profiles.profile.ToolRefs = nil
				case "unavailable":
					judge.fail = true
				}
			}
			if err := w.Decide(ctx, jobs[0]); err == nil {
				t.Fatal("change during model call did not fence execution")
			}
			var count int
			if err := w.api.lifecycle.Store.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_inference_links`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("blocked task wrote relation: %d %v", count, err)
			}
			if scenario == "revoke" {
				if _, err := w.api.continuousInferencePrincipal(ctx, set.TenantID, set.AgentID); !errors.Is(err, ontologyscope.ErrAccessDenied) {
					t.Fatalf("grant revoke: %v", err)
				}
			}
		})
	}
}

func TestProductionOntologyContinuousInferenceObservationQueuesOnlyChanges(t *testing.T) {
	w, _, _, set := continuousInferenceFixture(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := w.api.lifecycle.Store.Exec(ctx, `UPDATE ontology_inference_rule_sets SET next_check_at=0`); err != nil {
			t.Fatal(err)
		}
		if err := w.Observe(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(continuousInferenceJobs(t, w, ontologyreasoning.InferenceEvaluationTopic)) != 2 {
		t.Fatal("unchanged sources repeatedly queued tasks")
	}
	if _, err := w.api.lifecycle.Store.Exec(ctx, `UPDATE context_graph_objects SET label='changed',updated_at=? WHERE tenant_id=? AND id='node-b'`, time.Now().UnixNano(), set.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.api.lifecycle.Store.Exec(ctx, `UPDATE ontology_inference_rule_sets SET next_check_at=0`); err != nil {
		t.Fatal(err)
	}
	if err := w.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	if len(continuousInferenceJobs(t, w, ontologyreasoning.InferenceEvaluationTopic)) != 3 {
		t.Fatal("source change did not queue task")
	}
}

func TestProductionOntologyContinuousInferenceRulesUseTrustedMCPIdentity(t *testing.T) {
	w, _, _, set := continuousInferenceFixture(t)
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Now().UTC()) {
		if definition.ID == toolstd.OntologyInferenceRules {
			if err := registry.Register(ctx, definition); err != nil {
				t.Fatal(err)
			}
		}
	}
	router := mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: registry, OntologyExecutor: productionOntologyStewardToolExecutor{inference: w.api}})
	req := mcptransport.CallToolRequest{TenantID: set.TenantID, PrincipalID: set.AgentID, AgentID: set.AgentID, ToolID: string(toolstd.OntologyInferenceRules), Permissions: []string{"ontology.manage"}, Arguments: map[string]any{"operation": "list", "tenant_id": "forged-tenant"}}
	result, err := router.CallTool(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(result.Output["rule_sets"])
	var sets []ontologyreasoning.RuleSet
	if err := json.Unmarshal(body, &sets); err != nil || len(sets) != 1 || sets[0].TenantID != set.TenantID || sets[0].AgentID != set.AgentID {
		t.Fatalf("identity: %s %v", body, err)
	}
	req.Arguments["agent_id"] = "admin"
	if _, err := router.CallTool(ctx, req); err == nil {
		t.Fatal("model could forge owner identity")
	}
	delete(req.Arguments, "agent_id")
	req.Permissions = []string{"ontology.read"}
	if _, err := router.CallTool(ctx, req); err == nil {
		t.Fatal("read-only Agent could manage rules")
	}
}

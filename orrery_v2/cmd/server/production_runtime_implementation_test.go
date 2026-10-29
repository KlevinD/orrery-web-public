package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/app/improvement/einomodel"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/problemhub"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestProductionRuntimeImplementationMatchesRunningExecutable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := runtimeFileSHA256(exe)
	if err != nil {
		t.Fatal(err)
	}
	a := productionRuntimeImplementation()
	if a == nil || a.BinarySHA256 != expected {
		t.Fatal("host identity differs from process")
	}
	a.BinarySHA256 = "mutated"
	if productionRuntimeImplementation().BinarySHA256 != expected {
		t.Fatal("identity aliases caller")
	}
}

func TestProductionRuntimeEvidenceAgentCoreEndToEnd(t *testing.T) {
	testRuntimeEvidenceAgentCore(t, false)
}

func TestProductionRuntimeEvidenceLiveAgentCore(t *testing.T) {
	if os.Getenv("ORRERY_AGENTCORE_LIVE_MODEL_PROFILE") == "" {
		t.Skip("live model profile not configured")
	}
	testRuntimeEvidenceAgentCore(t, true)
}

func TestProductionRuntimeEvidenceBlockedTaskReview(t *testing.T) {
	testRuntimeEvidenceAgentCore(t, false, true)
}

func TestProductionRuntimeDiagnosisEndToEnd(t *testing.T) {
	testRuntimeEvidenceAgentCore(t, false, false, true)
}

func TestProductionRuntimeDiagnosisLiveModel(t *testing.T) {
	if os.Getenv("ORRERY_AGENTCORE_LIVE_MODEL_PROFILE") == "" {
		t.Skip("live diagnosis model profile not configured")
	}
	testRuntimeEvidenceAgentCore(t, false, false, true, true)
}

func testRuntimeEvidenceAgentCore(t *testing.T, live bool, reviewBlocked ...bool) {
	review := len(reviewBlocked) != 0 && reviewBlocked[0]
	diagnose := len(reviewBlocked) > 1 && reviewBlocked[1]
	liveDiagnosis := len(reviewBlocked) > 2 && reviewBlocked[2]
	wantCalls := 1
	if review {
		wantCalls = 2
	}
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{db.RoleCore: {Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "evidence.db")}}})
	if err != nil {
		t.Fatal(err)
	}
	defer databases.Close()
	core, _ := databases.Get(db.RoleCore)
	coordinator := migration.NewCoordinator(databases)
	for _, register := range []func(*migration.Coordinator) error{ontology.RegisterMigrations, contextgraph.RegisterMigrations, reliability.RegisterMigrations, problemhub.RegisterMigrations} {
		if err := register(coordinator); err != nil {
			t.Fatal(err)
		}
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatal(err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatal(err)
	}
	modelCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		page, err := core.ListRecords(ctx, "ontology_improvement_runtime_evidence", "", 10)
		if err != nil || len(page.Items) != 2 {
			t.Error("inference started before request and execution were durable", err)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		encoded, _ := json.Marshal(body)
		if strings.Contains(string(encoded), "implementation_sha256") || strings.Contains(string(encoded), productionRuntimeImplementation().BinarySHA256) {
			t.Error("host evidence sent to model")
		}
		answer := "actual model answer"
		if review && modelCalls == 1 {
			answer = "```orrery-agent-result\n" + `{"version":1,"outcome":"blocked","summary":"The authorized correction has not been attempted.","deliverables":[],"evidence":[]}` + "\n```"
		}
		if review && modelCalls == 2 && !strings.Contains(string(encoded), "Task stopping review:") {
			t.Error("executing model did not receive host stopping review")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": answer}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	profile := llmprofile.LLMProfile{ProfileID: "evidence-test", Provider: "openai", BaseURL: server.URL + "/v1", Model: "test", APIKey: "private-model-key", Enabled: true, GlobalDefault: true}
	if live {
		if err := json.Unmarshal([]byte(os.Getenv("ORRERY_AGENTCORE_LIVE_MODEL_PROFILE")), &profile); err != nil {
			t.Fatal("invalid live profile")
		}
	}
	journal, _ := agentkernel.NewSQLToolJournal(core)
	checkpoints, _ := agentkernel.NewSQLCheckpointStore(core)
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{KernelJournal: journal, KernelCheckpoints: checkpoints, ToolDispatcher: productionRuntimeToolDispatcher{},
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{profile}}})
	defer registry.Close()
	runtime, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal(err)
	}
	delivery := &runtimeEvidenceDelivery{}
	agent := agentcore.NewBaseAgent(agentcore.BaseAgentConfig{
		Identity: agentcore.AgentIdentity{AgentID: "agent-a", AgentProfileID: "profile-a", InstanceRef: agentcore.AgentInstanceRef{ID: "instance-a"}},
		Profile:  agentcore.AgentProfile{ID: "profile-a", RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged}}, Runtime: runtime,
		Trace: productionRuntimeEvidenceRecorder(agentcore.StoreTraceRecorder{Store: runtimeEvidenceTraceObjects{}}, productionTraceStore{evidenceCore: core}), Delivery: delivery, Notifier: agentcore.NoopNotifier{},
		Validator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
	})
	input := agentcore.AgentInput{TenantID: "org-a", RunID: "run-a", Task: agentcore.TaskInput{ID: "task-a", MissionID: "mission-a", Goal: "deliver result"}, ContextPack: agentcore.ContextPack{
		ID: "context-a", AgentID: "agent-a", TaskID: "task-a", MissionID: "mission-a", DiscussionGroupID: "discussion-a", MissionFinalGoal: "deliver result", DiscussionGroupGoal: "deliver result", WorkerPersonalGoal: "deliver result", TaskContractRef: agentcore.TaskContractRef{ID: "task-a"}, OverridePolicy: agentcore.OverridePolicyPreserveProtected,
		SourceRefs: []agentcore.ContextSourceRef{{Kind: agentcore.ContextSourceKindTaskContract, Ref: agentcore.ContextSourceTypedRef{TaskContractRef: &agentcore.TaskContractRef{ID: "task-a"}}, Priority: agentcore.SourcePriorityContract, Immutable: true}},
	}}
	input.RuntimeOptions = agentcore.RuntimeOptions{WorkingDirectory: t.TempDir()}
	store := improvement.NewStore(core)
	if diagnose {
		if _, err := store.EnableRuntimeDiagnoses(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if live {
		input.Task.Goal = "Reply with ORRERY_RSI_EVIDENCE_OK. Do not call tools."
	}
	// Reuse the host-sealed task envelope across prewarm and dispatch.
	input.ExecutionEnvelope = agent.BuildRuntimeRequest(input).ExecutionEnvelope
	if err := runtime.PrewarmSession(ctx, agent.BuildRuntimeRequest(input)); err != nil {
		t.Fatal("prewarm failed", err)
	}
	result, err := agent.Handle(ctx, input)
	if err != nil || (!live && modelCalls != wantCalls) || delivery.commits != 1 {
		t.Fatalf("pipeline failed model=%d commits=%d err=%v", modelCalls, delivery.commits, err)
	}
	if !live && result.Summary.Text != "actual model answer" {
		t.Fatal("LLM result text changed", result.Summary.Text)
	}
	if live && !strings.Contains(result.Summary.Text, "ORRERY_RSI_EVIDENCE_OK") {
		t.Fatal("live reply missing expected content")
	}
	page, err := store.List(ctx, "mission:org-a/mission-a", "", 10)
	if err != nil || len(page.Items) != 1 {
		t.Fatal("case missing", err)
	}
	runtimeReview, err := store.RuntimeReviewForCase(ctx, page.Items[0])
	if err != nil || runtimeReview.Protocol != "orrery-runtime-review-v2" || runtimeReview.BaselineReady {
		t.Fatalf("review missing %+v %v", runtimeReview, err)
	}
	actual, err := improvement.NewRuntimeEvidenceStore(core).GetExecution(ctx, runtimeReview.TenantID, runtimeReview.AttemptID)
	if err != nil || actual.Snapshot.Policy.MaxModelCalls != 128 || actual.Snapshot.Policy.MaxToolCalls != 256 || !actual.Snapshot.Policy.AutoCompact || actual.Snapshot.ModelIdentity == "" || actual.Implementation.BinarySHA256 != productionRuntimeImplementation().BinarySHA256 {
		t.Fatalf("effective policy missing %+v %v", actual, err)
	}
	if actual.Snapshot.Policy.BlockedTaskReviewProtocol != "orrery-blocked-task-review-v1" || actual.Snapshot.Policy.MaxBlockedTaskReviews != 1 {
		t.Fatal("production task stopping policy was not captured for RSI")
	}
	end, err := store.RuntimeEvidence().GetTerminal(ctx, runtimeReview.TenantID, runtimeReview.AttemptID)
	if err != nil || end.Dependencies == nil || len(end.Dependencies.Entries) != wantCalls || end.Dependencies.Entries[0].Kind != "model_dispatch" || end.Dependencies.Entries[0].ModelIdentity != actual.Snapshot.ModelIdentity || end.Dependencies.Dropped != 0 || end.Dependencies.Invalid != 0 || !end.Dependencies.RunnerReturned {
		t.Fatalf("actual dispatch input was not frozen: %+v %v", end.Dependencies, err)
	}
	projection, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, projection, time.Now)
	sink.improvements = store
	for i := 0; i < 2; i++ {
		if err := sink.ProjectImprovement(ctx, page.Items[0]); err != nil {
			t.Fatal("production dependency projection", err)
		}
	}
	var diagnosisRecord improvement.RuntimeDiagnosisRecord
	if diagnose {
		diagnosisCalls := 0
		diagnosisServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			diagnosisCalls++
			var body struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 {
				t.Fatal("diagnosis input missing", err)
			}
			var in improvement.RuntimeDiagnosisInput
			if err := json.Unmarshal([]byte(body.Messages[1].Content), &in); err != nil || in.AttemptID != runtimeReview.AttemptID || in.Execution.Implementation.BinarySHA256 != actual.Implementation.BinarySHA256 {
				t.Fatal("diagnosis used another execution", err)
			}
			answer, _ := json.Marshal(improvement.Diagnosis{Summary: "运行返回不代表业务完成；当前缺少业务验收与产物字节证据，先核对同次提交。", EvidenceIDs: []string{in.Terminal.ID, in.Review.ID}, AffectedKinds: []improvement.TargetKind{improvement.TargetWorkflow, improvement.TargetAgentLoop}})
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": string(answer)}}}})
		}))
		defer diagnosisServer.Close()
		profile := llmprofile.LLMProfile{ProfileID: "diagnosis-model", Model: "diagnosis-model", BaseURL: diagnosisServer.URL, APIKey: "test-key", Enabled: true}
		if liveDiagnosis {
			if err := json.Unmarshal([]byte(os.Getenv("ORRERY_AGENTCORE_LIVE_MODEL_PROFILE")), &profile); err != nil {
				t.Fatal("invalid live diagnosis profile")
			}
		}
		model := &einomodel.RuntimeDiagnoser{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{profile}}, ProfileID: profile.ProfileID}
		var observer *runtimeDiagnosisResponseObserver
		if liveDiagnosis {
			observer = &runtimeDiagnosisResponseObserver{}
			model.Client = &http.Client{Transport: observer, Timeout: 45 * time.Second}
		}
		if worked, err := store.DiagnoseNextRuntime(ctx, model, 4); err != nil || !worked {
			t.Fatal("diagnosis did not run", worked, err)
		}
		if !liveDiagnosis && diagnosisCalls != 1 {
			t.Fatal("diagnosis model not called exactly once")
		}
		var err error
		diagnosisRecord, err = store.RuntimeDiagnosis(ctx, runtimeReview.TenantID, runtimeReview.AttemptID)
		if observer != nil {
			t.Logf("diagnosis response: http=%d content_bytes=%d summary_bytes=%d json_keys=%v", observer.status, observer.contentBytes, observer.summaryBytes, observer.keys)
		}
		if err != nil || diagnosisRecord.State != "completed" || diagnosisRecord.InputSHA256 != improvement.Digest(diagnosisRecord.Input) || diagnosisRecord.Inference.Model != profile.Model {
			t.Fatalf("diagnosis receipt not bound: state=%s failure=%s err=%v", diagnosisRecord.State, diagnosisRecord.Failure, err)
		}
		admission, persisted, admissionErr := store.RuntimeExperimentAdmission(ctx, runtimeReview.TenantID, runtimeReview.AttemptID)
		if admissionErr != nil || !persisted || diagnosisRecord.Admission == nil || admission.State != "blocked" || len(admission.Domains) != 9 || admission.DiagnosisReceiptID != diagnosisRecord.ReceiptID {
			t.Fatal("diagnosis did not capture native experiment admission", admissionErr)
		}
		updated, err := store.Get(ctx, runtimeReview.TenantID, page.Items[0].ID)
		if err != nil || updated.State != improvement.Diagnosed || updated.ChangeSet != nil || updated.Diagnosis.EvidenceIDs[0] != diagnosisRecord.ReceiptID {
			t.Fatal("diagnosis did not advance original case", err)
		}
		if err := sink.ProjectImprovement(ctx, updated); err != nil {
			t.Fatal("diagnosis projection failed", err)
		}
	}
	objects, links, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, runtimeReview.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	nodes, associations := 0, 0
	for _, object := range objects {
		if object.ObjectType == "improvement_runtime_dependencies" {
			nodes++
		}
	}
	for _, link := range links {
		if link.LinkType == "improvement_case_has_runtime_dependencies" || link.LinkType == "improvement_runtime_dependencies_for_review" {
			associations++
		}
	}
	if nodes != 1 || associations != 2 {
		t.Fatalf("production dependency graph missing/duplicate: %d/%d", nodes, associations)
	}
	if diagnose {
		found, stateFound, admissionFound := false, false, false
		for _, fact := range facts {
			if fact.Subject.Kind != "improvement_case" || fact.Subject.ID != page.Items[0].ID {
				continue
			}
			raw, _ := json.Marshal(fact.Value)
			found = found || (fact.Predicate == "evidence_ids" && strings.Contains(string(raw), diagnosisRecord.ReceiptID))
			admissionFound = admissionFound || (fact.Predicate == "evidence_ids" && strings.Contains(string(raw), diagnosisRecord.Admission.ID))
			stateFound = stateFound || (fact.Predicate == "status" && fact.Value == "diagnosed")
			if strings.Contains(string(raw), diagnosisRecord.Inference.Diagnosis.Summary) {
				t.Fatal("private model prose copied into graph")
			}
		}
		if !found || !stateFound || !admissionFound {
			t.Fatal("diagnosis state/receipt missing in materialized graph")
		}
		if liveDiagnosis {
			t.Logf("real diagnosis model=%s; case_state=diagnosed; receipt=%s; dependencies=%d; links=%d", diagnosisRecord.Inference.Model, diagnosisRecord.ReceiptID, nodes, associations)
			if dir := os.Getenv("ORRERY_RSI_DIAGNOSIS_EVAL_DIR"); dir != "" {
				receipt, _ := json.MarshalIndent(map[string]any{"model": diagnosisRecord.Inference.Model, "profile_id": diagnosisRecord.Inference.ProfileID, "input_sha256": diagnosisRecord.InputSHA256, "receipt_id": diagnosisRecord.ReceiptID, "case_state": "diagnosed", "summary": diagnosisRecord.Inference.Diagnosis.Summary, "original_attempt_binding": true, "graph_receipt_present": found, "dependency_nodes": nodes, "dependency_links": associations, "isolated": true, "production_business_task": false, "task_execution_model": "http_fixture", "diagnosis_model": "real"}, "", "  ")
				if err := os.WriteFile(filepath.Join(dir, "LIVE_DIAGNOSIS_RESULT.json"), receipt, 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	raw, _ := json.Marshal(actual)
	if strings.Contains(string(raw), "private-model-key") || strings.Contains(string(raw), "actual model answer") {
		t.Fatal("private content in execution evidence")
	}
}

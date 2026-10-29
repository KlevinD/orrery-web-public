package main

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/improvement"
)

func TestProductionImprovementProjectsCapturedRuntimeReview(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "runtime-review-projection")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	sink.improvements = improvement.NewStore(core)
	scope, err := productionRuntimeEvidenceScope(agentcore.RuntimeRequest{TenantID: "tenant-a", AgentID: "agent-a", RunID: "run-a", TaskID: "task-a"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := improvement.BuildRuntimeRequestEvidence(agentcore.RuntimeRequest{TenantID: "tenant-a", AgentID: "agent-a", RunID: "run-a", TaskID: "task-a"}, scope, "attempt-a", "trace://review", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	evidence := improvement.NewRuntimeEvidenceStore(core)
	if err := evidence.Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	c, err := evidence.Complete(ctx, request, agentcore.TraceTerminal{Status: agentcore.RuntimeStatusFailed, CompletedAt: time.Unix(101, 0)})
	if err != nil {
		t.Fatal(err)
	}
	for replay := 0; replay < 2; replay++ {
		if err := sink.ProjectImprovement(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	objects, links, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, c.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	reviews, associations := 0, 0
	for _, object := range objects {
		if object.ObjectType == "improvement_runtime_review" {
			reviews++
			if object.OwnerAccessRef.TenantID != scope.Source.TenantID || object.OwnerAccessRef.Kind != scope.Source.Kind || object.OwnerAccessRef.ID != scope.Source.ID {
				t.Fatal("production review lost source access")
			}
		}
	}
	for _, link := range links {
		if link.LinkType == "improvement_case_has_runtime_review" {
			associations++
		}
	}
	if reviews != 1 || associations != 1 {
		t.Fatalf("production review graph: %d/%d", reviews, associations)
	}
}

func TestProductionImprovementObservesTaskSuccessAndFailure(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "task-improvements")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	sink.improvements = improvement.NewStore(core)
	for _, kind := range []runtimedaemon.RuntimeLifecycleKind{runtimedaemon.RuntimeLifecycleCompleted, runtimedaemon.RuntimeLifecycleFailed} {
		event := runtimedaemon.RuntimeLifecycleEvent{Kind: kind, TenantID: "tenant-a", MissionID: "mission-a", TaskID: "task-a", AgentID: "A000101", RunID: agentcore.AgentRunID("run-" + string(kind)), AgentVersionID: "explicit-runtime-v1", RuntimeKind: "native", Message: "PRIVATE_TASK_OUTPUT", TraceRef: "trace-" + string(kind), DurationMs: 200}
		// Zero CompletedAt also models failure events and durable lifecycle
		// repair. Replaying them must not invent a new observation timestamp.
		for replay := 0; replay < 2; replay++ {
			if err := sink.PublishRuntimeLifecycle(ctx, event); err != nil {
				t.Fatal(err)
			}
		}
	}
	tenant := missionScopeTenantID("tenant-a", "mission-a")
	page, err := sink.improvements.List(ctx, tenant, "", 20)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("observations: %+v %v", page, err)
	}
	for _, c := range page.Items {
		want := improvement.Observed
		if c.Observation.Trigger == "runtime_failed" {
			want = improvement.Blocked
			if c.Reason != "captured_runtime_evidence_required" {
				t.Fatal("uncaptured failure lacks blocker", c)
			}
		}
		if c.State != want || c.ChangeSet != nil || c.Evaluation != nil || len(c.Observation.Dependencies) != 1 || c.Observation.Dependencies[0].SHA256 != "" || strings.Contains(c.Observation.Summary, "PRIVATE") {
			t.Fatalf("false optimization or leaked result: %+v", c)
		}
		if c.Observation.SourceAccessRef.TenantID != tenant || c.Observation.Metrics["runtime_duration_ms"] != 200 {
			t.Fatalf("scope/metrics: %+v", c)
		}
	}
	if n, err := sink.improvements.ProjectPending(ctx, sink, 20); err != nil || n != 2 {
		t.Fatalf("project pending: %d %v", n, err)
	}
	objects, links, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	cases, runLinks := 0, 0
	for _, object := range objects {
		if object.ObjectType == "improvement_case" {
			cases++
		}
	}
	for _, link := range links {
		if link.LinkType == "improvement_case_observed_in_agent_run" {
			runLinks++
		}
	}
	if cases != 2 || runLinks != 2 {
		t.Fatalf("ontology case/run chain: %d/%d", cases, runLinks)
	}
	other, err := sink.improvements.List(ctx, "tenant-a", "", 20)
	if err != nil || len(other.Items) != 0 {
		t.Fatalf("mission scope leaked: %+v %v", other, err)
	}
}

func TestProductionImprovementDoesNotTreatStartedRunAsImprovement(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "task-improvement-started")
	sink := &productionAgentExecutionOntologySink{improvements: improvement.NewStore(core)}
	if err := sink.observeRuntimeImprovement(ctx, runtimedaemon.RuntimeLifecycleEvent{Kind: runtimedaemon.RuntimeLifecycleStarted}, "", "", "", trustedOntologySourceRef("agent_run", "run", "tenant-a")); err != nil {
		t.Fatal(err)
	}
	page, err := sink.improvements.List(ctx, "tenant-a", "", 20)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("started run created case: %+v %v", page, err)
	}
}

func TestProductionImprovementOlderOutboxCannotRegressGraph(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "improvement-outbox-order")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	sink.improvements = improvement.NewStore(core)
	source := trustedOntologySourceRef("agent_run", "run-a", "tenant-a")
	older, err := sink.improvements.Observe(ctx, improvement.Observation{ID: "result-a", TenantID: "tenant-a", Task: improvement.ObjectRef{Kind: "agent_task", ID: "task-a"}, Run: improvement.ObjectRef{Kind: "agent_run", ID: "run-a"}, Trigger: "runtime_completed", Summary: "等待业务质量评估", EvidenceIDs: []string{"run-a"}, SourceAccessRef: source})
	if err != nil {
		t.Fatal(err)
	}
	latest, err := sink.improvements.Claim(ctx, older.TenantID, older.ID, "worker-a", older.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	latest, err = sink.improvements.Advance(ctx, improvement.Transition{ID: "reject-a", TenantID: latest.TenantID, CaseID: latest.ID, ExpectedRevision: latest.Revision, LeaseToken: latest.Lease.Token, Actor: improvement.ObjectRef{Kind: "learner", ID: "worker-a"}, To: improvement.Rejected, Reason: "缺少可信业务指标"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.ProjectImprovement(ctx, latest); err != nil {
		t.Fatal(err)
	}
	if err := sink.ProjectImprovement(ctx, older); err != nil {
		t.Fatal(err)
	}
	objects, _, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	caseCount, statusCount := 0, 0
	for _, object := range objects {
		if object.ObjectType == "improvement_case" {
			caseCount++
			if object.SourceVersion != latest.Revision {
				t.Fatalf("stale case revision: %+v", object)
			}
		}
	}
	for _, fact := range facts {
		if fact.Subject.Kind == "improvement_case" && fact.Predicate == "status" {
			statusCount++
			if fact.Value != string(improvement.Rejected) {
				t.Fatalf("stale status fact: %+v", fact)
			}
		}
	}
	if caseCount != 1 || statusCount != 1 {
		t.Fatalf("case/status facts: %d/%d", caseCount, statusCount)
	}
}

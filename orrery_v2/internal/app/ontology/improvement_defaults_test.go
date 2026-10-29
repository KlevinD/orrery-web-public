package ontology

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/migration"
)

func TestImprovementRuntimeReviewGraphConformanceReplayAndIsolation(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	scope := improvement.RuntimeEvidenceScope{TenantID: "tenant-review", Task: improvement.ObjectRef{Kind: "agent_task", ID: "task-review"}, Run: improvement.ObjectRef{Kind: "agent_run", ID: "run-review"}, Source: graphprotocol.AccessRef{TenantID: "tenant-review", Kind: "agent_run", ID: "run-review"}}
	request, err := improvement.BuildRuntimeRequestEvidence(agentcore.RuntimeRequest{TenantID: "tenant-review", RunID: "run-review", AgentID: "agent-review"}, scope, "attempt-review", "trace://review", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	evidence := improvement.NewRuntimeEvidenceStore(core)
	if err := evidence.Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	c, err := evidence.Complete(ctx, request, agentcore.TraceTerminal{Status: agentcore.RuntimeStatusCompleted, CompletedAt: time.Unix(101, 0)})
	if err != nil {
		t.Fatal(err)
	}
	review, err := improvement.NewStore(core).RuntimeReviewForCase(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	event, err := improvement.RuntimeReviewOntologyEvent(c, review)
	if err != nil || event.Type != "improvement.runtime_review.upserted" || event.SourceAccessRef != scope.Source {
		t.Fatalf("review event: %+v %v", event, err)
	}
	runner, graph := improvementProjectionRunner(t)
	for _, e := range append(improvement.OntologyEvents(c), event, event) {
		if report, err := runner.ApplyEvent(ctx, e); err != nil || !report.OK {
			t.Fatalf("project %s: %+v %v", e.Type, report, err)
		}
	}
	objects, links, facts, _, err := graph.Snapshot(ctx, c.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	count, linked, quality := 0, 0, 0
	for _, o := range objects {
		if o.ObjectType == "improvement_runtime_review" {
			count++
			if o.OwnerAccessRef.TenantID != scope.Source.TenantID || o.OwnerAccessRef.Kind != scope.Source.Kind || o.OwnerAccessRef.ID != scope.Source.ID {
				t.Fatal("review lost originating source scope")
			}
		}
	}
	for _, link := range links {
		if link.LinkType == "improvement_case_has_runtime_review" {
			linked++
		}
	}
	for _, fact := range facts {
		if fact.Subject.Kind != "improvement_runtime_review" {
			continue
		}
		if fact.Predicate == "baseline_ready" && fact.Value != false {
			t.Fatal("partial baseline acquired a ready fact")
		}
		if fact.Predicate == "business_quality" && fact.Value == "unassessed" {
			quality++
		}
	}
	if count != 1 || linked != 1 || quality != 1 {
		t.Fatalf("missing/duplicated scoped review: %d/%d/%d", count, linked, quality)
	}
	other, _, _, _, _ := graph.Snapshot(ctx, "other-tenant")
	if len(other) != 0 {
		t.Fatal("review crossed tenant")
	}
	delete(event.Payload, "id")
	if _, err := runner.ApplyEvent(ctx, event); err == nil {
		t.Fatal("incomplete improvement.runtime_review.upserted accepted")
	}
}

func TestImprovementRuntimeReviewV2GraphConformanceReplayAndIsolation(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	scope := improvement.RuntimeEvidenceScope{TenantID: "tenant-v2", Task: improvement.ObjectRef{Kind: "agent_task", ID: "task-v2"}, Run: improvement.ObjectRef{Kind: "agent_run", ID: "run-v2"}, Source: graphprotocol.AccessRef{TenantID: "tenant-v2", Kind: "agent_run", ID: "run-v2"}}
	request, err := improvement.BuildRuntimeRequestEvidence(agentcore.RuntimeRequest{TenantID: "tenant-v2", RunID: "run-v2", AgentID: "agent-v2"}, scope, "attempt-v2", "trace://v2", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	evidence := improvement.NewRuntimeEvidenceStore(core)
	if err := evidence.Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	snapshot := agentcore.RuntimeExecutionSnapshot{Engine: "orrery-agentcore", TenantID: "tenant-v2", RunID: "run-v2", AgentID: "agent-v2", TransportTimeoutNS: 1, Policy: agentcore.RuntimeExecutionPolicy{Protocol: "orrery-agentcore-policy-v1", MaxModelCalls: 8, MaxToolCalls: 16, MaxParallelTools: 1, MaxConcurrentCallbacks: 32, MaxContextBytes: 1024, MaxResponseBytes: 1024, ModelTimeoutNS: 1, ToolTimeoutNS: 1, JournalTimeoutNS: 1, ApprovalTimeoutNS: 1, DefaultLongTimeoutNS: 1, MaxTransportTimeoutNS: 2}}
	execution, err := improvement.BuildRuntimeExecutionEvidence(request, improvement.RuntimeImplementation{BinarySHA256: improvement.Digest("host")}, snapshot, time.Unix(100, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := evidence.CaptureExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	c, err := evidence.Complete(ctx, request, agentcore.TraceTerminal{Status: agentcore.RuntimeStatusCompleted, CompletedAt: time.Unix(101, 0)})
	if err != nil {
		t.Fatal(err)
	}
	review, err := improvement.NewStore(core).RuntimeReviewForCase(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	event, err := improvement.RuntimeReviewOntologyEvent(c, review)
	if err != nil || event.Type != "improvement.runtime_review_v2.upserted" {
		t.Fatal("wrong V2 event", err)
	}
	runner, graph := improvementProjectionRunner(t)
	for _, e := range append(improvement.OntologyEvents(c), event, event) {
		if report, err := runner.ApplyEvent(ctx, e); err != nil || !report.OK {
			t.Fatal("V2 conformance/replay failed", e.Type, report, err)
		}
	}
	objects, _, facts, _, err := graph.Snapshot(ctx, c.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	count, hashes := 0, 0
	for _, object := range objects {
		if object.ObjectType == "improvement_runtime_review" {
			count++
			if object.OwnerAccessRef.ID != scope.Source.ID {
				t.Fatal("lost source scope")
			}
		}
	}
	for _, fact := range facts {
		if fact.Subject.Kind == "improvement_runtime_review" && (fact.Predicate == "execution_evidence_id" || fact.Predicate == "runtime_policy_sha256" || fact.Predicate == "implementation_sha256") {
			hashes++
		}
	}
	if count != 1 || hashes != 3 {
		t.Fatalf("missing or duplicate V2 graph evidence %d/%d", count, hashes)
	}
	other, _, _, _, _ := graph.Snapshot(ctx, "other-tenant")
	if len(other) > 0 {
		t.Fatal("V2 crossed tenant")
	}
	delete(event.Payload, "implementation_sha256")
	if _, err := runner.ApplyEvent(ctx, event); err == nil {
		t.Fatal("incomplete improvement.runtime_review_v2.upserted accepted")
	}
}

func improvementProjectionFixture(tenant string) improvement.Case {
	artifact := func(id string) improvement.ArtifactRef {
		return improvement.ArtifactRef{ID: id, SHA256: improvement.Digest(id)}
	}
	c := improvement.Case{ID: "case-" + tenant, TenantID: tenant, Revision: 9, State: improvement.Verified, UpdatedAt: time.Unix(300, 0).UTC(),
		Observation: improvement.Observation{Task: improvement.ObjectRef{Kind: "agent_task", ID: "report-task"}, Run: improvement.ObjectRef{Kind: "agent_run", ID: "report-run"}, Trigger: "quality_feedback", Summary: "任务质量改进", EvidenceIDs: []string{"report-evidence"}, SourceAccessRef: graphprotocol.AccessRef{Kind: "mission", ID: "report-mission", TenantID: tenant}}}
	cs := &improvement.ChangeSet{ID: "change-set", Dataset: artifact("dataset"), Policy: artifact("policy"), Generator: artifact("generator"), Evaluator: artifact("evaluator")}
	for _, kind := range []improvement.TargetKind{improvement.TargetTool, improvement.TargetKnowledge} {
		b := improvement.ResourceVersion{Kind: kind, ID: "resource-" + string(kind), Version: "v0", SHA256: improvement.Digest(string(kind) + ":v0")}
		n := b
		n.Version = "v1"
		n.SHA256 = improvement.Digest(string(kind) + ":v1")
		cs.Baseline = append(cs.Baseline, b)
		cs.Changes = append(cs.Changes, improvement.TargetChange{Baseline: b, Candidate: n})
	}
	c.ChangeSet = cs
	c.Experiment = &improvement.Experiment{ID: "experiment", Manifest: artifact("manifest"), Result: artifact("result")}
	c.Evaluation = &improvement.Evaluation{ID: "evaluation", Report: artifact("evaluation")}
	c.Release = &improvement.Release{ID: "release", Targets: []improvement.TargetReceipt{{Receipt: artifact("apply-receipt")}}}
	c.Outcome = &improvement.Outcome{ID: "outcome", Report: artifact("outcome")}
	return c
}

func TestImprovementRuntimeReviewUpgradesExistingStaticDefinition(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyRegistryMigration(), ontologyTemporalSemanticsMigration(), ontologyMappingRuleGovernanceMigration(), ontologyActionBindingsMigration()}).Up(ctx, core); err != nil {
		t.Fatal(err)
	}
	registry := NewPlatformRegistry(core)
	var old ObjectType
	for _, object := range defaultImprovementObjectTypes() {
		if object.ID == "improvement_runtime_review" {
			old = object
		}
	}
	old.Properties = append(old.Properties[:len(old.Properties)-3:len(old.Properties)-3], property("custom_property", ValueTypeString, false))
	if err := registry.SaveObjectType(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	upgraded, err := registry.GetObjectType(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	properties := map[string]PropertyDefinition{}
	for _, p := range upgraded.Properties {
		properties[p.PropertyID] = p
	}
	for _, id := range []string{"execution_evidence_id", "runtime_policy_sha256", "implementation_sha256", "custom_property"} {
		p, ok := properties[id]
		if !ok || p.Required || p.ValueType != ValueTypeString {
			t.Fatal("missing or destructive static upgrade", id)
		}
	}
	if len(upgraded.Properties) != len(old.Properties)+3 {
		t.Fatal("old properties lost")
	}
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	again, err := registry.GetObjectType(ctx, old.ID)
	if err != nil || again.Version != upgraded.Version {
		t.Fatal("static migration is not idempotent", err)
	}
}

func improvementProjectionRunner(t *testing.T) (*MappingRuleProjectionRunner, *contextgraph.MemoryStore) {
	t.Helper()
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	gate := graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry))
	projections := graphprotocol.NewProjectionRegistry(gate)
	if err := RegisterMappingRuleProjections(ctx, registry, projections, ""); err != nil {
		t.Fatal(err)
	}
	store := contextgraph.NewMemoryStore()
	return NewMappingRuleProjectionRunner(projections, gate, store), store
}

func TestImprovementOntologyCrossDomainGraphReplayAndIsolation(t *testing.T) {
	ctx := context.Background()
	runner, store := improvementProjectionRunner(t)
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		c := improvementProjectionFixture(tenant)
		wantEvents := map[string]bool{
			"improvement.case.upserted": true, "improvement.change_set.upserted": true,
			"improvement.resource_version.upserted": true, "improvement.experiment.upserted": true,
			"improvement.evaluation.upserted": true, "improvement.release.upserted": true, "improvement.outcome.upserted": true,
		}
		for _, event := range improvement.OntologyEvents(c) {
			delete(wantEvents, event.Type)
			for replay := 0; replay < 2; replay++ {
				report, err := runner.ApplyEvent(ctx, event)
				if err != nil || !report.OK {
					t.Fatalf("project %s: %+v %v", event.Type, report, err)
				}
			}
		}
		if len(wantEvents) != 0 {
			t.Fatalf("missing lifecycle events: %+v", wantEvents)
		}
		objects, links, facts, evidence, err := store.Snapshot(ctx, tenant)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, object := range objects {
			if !strings.HasPrefix(object.ObjectType, "improvement_") {
				continue
			}
			counts[object.ObjectType]++
			if object.TenantID != tenant || object.OwnerAccessRef.TenantID != tenant || object.OwnerAccessRef.ID != "report-mission" {
				t.Fatalf("lost access scope: %+v", object)
			}
		}
		for _, kind := range []string{"case", "change_set", "experiment", "evaluation", "release", "outcome"} {
			if counts["improvement_"+kind] != 1 {
				t.Fatalf("%s count: %+v", tenant, counts)
			}
		}
		if counts["improvement_resource_version"] != 4 {
			t.Fatalf("baseline/candidate versions merged: %+v", counts)
		}
		kinds := map[string]int{}
		for _, link := range links {
			kinds[link.LinkType]++
		}
		if kinds["improvement_change_set_has_baseline"] != 2 || kinds["improvement_change_set_has_candidate"] != 2 || kinds["improvement_case_has_evaluation"] != 1 || kinds["improvement_outcome_references_release"] != 1 {
			t.Fatalf("missing evidence chain: %+v", kinds)
		}
		if len(facts) == 0 || len(evidence) == 0 {
			t.Fatal("graph facts/evidence missing")
		}
	}
}

func TestImprovementOntologyRejectsMissingFields(t *testing.T) {
	ctx := context.Background()
	runner, _ := improvementProjectionRunner(t)
	for _, event := range improvement.OntologyEvents(improvementProjectionFixture("tenant-a")) {
		delete(event.Payload, "id")
		if _, err := runner.ApplyEvent(ctx, event); err == nil {
			t.Fatalf("incomplete %s accepted", event.Type)
		}
	}
}

func TestImprovementCoverageHasNoNewGateFailures(t *testing.T) {
	report := ValidateProductionCoverageGateWithPublishers(defaultMappingRules(), DefaultProductionCoverageManifest(), KnownProductionOntologyPublishers(), time.Now().UTC())
	for _, issue := range report.Issues {
		if strings.HasPrefix(issue.EventType, "improvement.") {
			t.Fatalf("new coverage issue: %+v", issue)
		}
	}
}

func TestImprovementBusinessAssessmentGraphConformanceReplayAndIsolation(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	scope := improvement.RuntimeEvidenceScope{TenantID: "business-tenant", Task: improvement.ObjectRef{Kind: "agent_task", ID: "business-task"}, Run: improvement.ObjectRef{Kind: "agent_run", ID: "business-run"}, Source: graphprotocol.AccessRef{TenantID: "business-tenant", Kind: "agent_run", ID: "business-run"}}
	request, err := improvement.BuildRuntimeRequestEvidence(agentcore.RuntimeRequest{TenantID: "business-tenant", RunID: "business-run", AgentID: "business-agent"}, scope, "business-attempt", "trace://business", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	evidence := improvement.NewRuntimeEvidenceStore(core)
	if err := evidence.Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	c, err := evidence.Complete(ctx, request, agentcore.TraceTerminal{Status: agentcore.RuntimeStatusCompleted, CompletedAt: time.Unix(101, 0)})
	if err != nil {
		t.Fatal(err)
	}
	review, err := improvement.NewStore(core).RuntimeReviewForCase(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	re, err := improvement.RuntimeReviewOntologyEvent(c, review)
	if err != nil {
		t.Fatal(err)
	}
	a := improvement.BusinessAssessment{Protocol: "orrery-task-business-assessment-v1", TenantID: c.TenantID, AttemptID: request.AttemptID, CaseID: c.ID, RuntimeReviewID: review.ID, BindingID: "host-business-binding", Outcome: improvement.TaskOutcome{SubmissionID: "business-submission", QARequired: true, QAStatus: "approved", ContractSHA256: improvement.Digest("contract"), SourceSHA256: improvement.Digest("QA"), Deliverables: 1, Evidence: 1}, BusinessStatus: "qa_approved", CausalAttribution: "unresolved", Blockers: []string{"artifact_bytes_unverified", "baseline_incomplete"}, AssessedAt: time.Unix(102, 0).UTC()}
	a.ID = "task-business-assessment:" + improvement.Digest(a)
	event, err := improvement.BusinessAssessmentOntologyEvent(c, a)
	if err != nil || event.Type != "improvement.business_assessment.upserted" {
		t.Fatal("wrong business event", err)
	}
	runner, graph := improvementProjectionRunner(t)
	for _, e := range append(improvement.OntologyEvents(c), re, event, event) {
		if report, err := runner.ApplyEvent(ctx, e); err != nil || !report.OK {
			t.Fatalf("project %s: %+v %v", e.Type, report, err)
		}
	}
	objects, links, facts, _, err := graph.Snapshot(ctx, c.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, o := range objects {
		if o.ObjectType == "improvement_business_assessment" {
			count++
			if o.OwnerAccessRef.ID != scope.Source.ID || o.TenantID != c.TenantID {
				t.Fatal("business assessment lost source access")
			}
		}
	}
	kinds := map[string]int{}
	for _, l := range links {
		kinds[l.LinkType]++
	}
	if count != 1 || kinds["improvement_case_has_business_assessment"] != 1 || kinds["improvement_business_assessment_for_runtime_review"] != 1 {
		t.Fatalf("missing/duplicated business links: %d %+v", count, kinds)
	}
	r := improvement.TaskArtifactVerification{Protocol: "orrery-task-artifact-verification-v1", TenantID: c.TenantID, AttemptID: request.AttemptID, CaseID: c.ID, RuntimeReviewID: review.ID, BusinessAssessmentID: a.ID, SubmissionID: a.Outcome.SubmissionID, ManifestID: "test-manifest", Status: "partial", Attempts: 1, Items: []improvement.TaskArtifactItemVerification{}, VerifiedAt: time.Unix(103, 0).UTC()}
	r.ID = "task-artifact-verification:" + improvement.Digest(r)
	artifactEvent, err := improvement.TaskArtifactVerificationOntologyEvent(c, r)
	if err != nil || artifactEvent.Type != "improvement.artifact_verification.upserted" {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if report, err := runner.ApplyEvent(ctx, artifactEvent); err != nil || !report.OK {
			t.Fatal(report, err)
		}
	}
	artifactObjects, artifactLinks, _, _, err := graph.Snapshot(ctx, c.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	artifactCount := 0
	artifactKinds := map[string]int{}
	for _, o := range artifactObjects {
		if o.ObjectType == "improvement_artifact_verification" {
			artifactCount++
			if o.OwnerAccessRef.ID != scope.Source.ID {
				t.Fatal("verification lost original access")
			}
		}
	}
	for _, l := range artifactLinks {
		artifactKinds[l.LinkType]++
	}
	if artifactCount != 1 || artifactKinds["improvement_case_has_artifact_verification"] != 1 || artifactKinds["improvement_artifact_verification_for_business_assessment"] != 1 {
		t.Fatal("artifact graph links missing/duplicated", artifactCount, artifactKinds)
	}
	delete(artifactEvent.Payload, "manifest_id")
	if _, err := runner.ApplyEvent(ctx, artifactEvent); err == nil {
		t.Fatal("incomplete verification accepted")
	}
	for _, f := range facts {
		if f.Subject.Kind == "improvement_runtime_review" && f.Predicate == "baseline_ready" && f.Value != false {
			t.Fatal("acceptance enabled baseline")
		}
	}
	other, _, _, _, _ := graph.Snapshot(ctx, "other-tenant")
	if len(other) != 0 {
		t.Fatal("business assessment crossed tenant")
	}
	delete(event.Payload, "qa_required")
	if _, err := runner.ApplyEvent(ctx, event); err == nil {
		t.Fatal("incomplete improvement.business_assessment.upserted accepted")
	}
}

package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/problemhub"
	"orrery_v2/internal/app/reliability"
	schedulerapp "orrery_v2/internal/app/scheduler"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/filestore"
	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

type taskArtifactReadSpy struct {
	filestore.Store
	calls int
}

func (s *taskArtifactReadSpy) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	s.calls++
	return s.Store.GetReader(ctx, key)
}

func TestProductionTaskArtifactReaderVerifiesHostMaterializationAndScopes(t *testing.T) {
	ctx := context.Background()
	fileRoot := t.TempDir()
	store, err := filestore.OpenLocalStorage(filestore.LocalConfig{Root: fileRoot})
	if err != nil {
		t.Fatal(err)
	}
	input := agentcore.AgentInput{TenantID: "tenant-a", RunID: "run-a"}
	ref, err := (missionTaskAgentResultNormalizer{store: store}).persistRuntimeOutput(ctx, input, "actual bytes")
	if err != nil {
		t.Fatal(err)
	}
	if len(ref.SHA256) != 64 || ref.SizeBytes != 12 || !strings.HasPrefix(ref.ID, missionResultArtifactPrefix("tenant-a", "run-a")) {
		t.Fatal("host omitted complete content binding", ref)
	}
	archiver := newProductionSchedulerArtifactStore(t.TempDir(), config.FileStoreConfig{Backend: "local", Root: fileRoot}, store, nil)
	run := runtimedaemon.AgentRun{RunID: "run-a", Request: agentcore.RuntimeRequest{TenantID: "tenant-a"}}
	artifact := schedulerapp.RunArtifact{ID: ref.ID, Kind: "report", URI: ref.URI}
	if _, err := archiver.ArchiveRunArtifacts(ctx, run, "scheduled-a", schedulerapp.RunResult{Artifacts: []schedulerapp.RunArtifact{artifact}}); err != nil {
		t.Fatal("new scoped host output could not be archived", err)
	}
	run.Request.TenantID = "tenant-b"
	if _, err := archiver.ArchiveRunArtifacts(ctx, run, "scheduled-b", schedulerapp.RunResult{Artifacts: []schedulerapp.RunArtifact{artifact}}); err == nil {
		t.Fatal("archiver accepted another tenant's new output")
	}
	spy := &taskArtifactReadSpy{Store: store}
	reader := productionTaskArtifactReader{store: spy}
	access := improvement.TaskArtifactAccess{ExecutionTenantID: "tenant-a", RunID: "run-a", Reference: ref}
	content, err := reader.OpenTaskArtifact(ctx, access)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(content.Reader)
	content.Reader.Close()
	if err != nil || string(got) != "actual bytes" || content.SHA256 != ref.SHA256 || content.SizeBytes != 12 {
		t.Fatal(content, err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*improvement.TaskArtifactAccess)
	}{
		{"other-tenant", func(a *improvement.TaskArtifactAccess) { a.ExecutionTenantID = "tenant-b" }},
		{"other-run", func(a *improvement.TaskArtifactAccess) { a.RunID = "run-b" }},
		{"untrusted-absolute-path", func(a *improvement.TaskArtifactAccess) {
			a.Reference.ID = "/etc/passwd"
			a.Reference.URI = "file:///etc/passwd"
		}},
		{"external-url", func(a *improvement.TaskArtifactAccess) {
			a.Reference.ID = "https://example.invalid/object"
			a.Reference.URI = a.Reference.ID
		}},
		{"legacy-unscoped-key", func(a *improvement.TaskArtifactAccess) {
			a.Reference.ID = "agent-runs/run-a/result-output-" + ref.SHA256 + ".md"
		}},
		{"path-in-digest", func(a *improvement.TaskArtifactAccess) {
			a.Reference.SHA256 = strings.Repeat("../", 20) + "aaaa"
			a.Reference.ID = missionResultArtifactPrefix("tenant-a", "run-a") + "result-output-" + a.Reference.SHA256 + ".md"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := access
			tc.mutate(&a)
			before := spy.calls
			if _, err := reader.OpenTaskArtifact(ctx, a); !errors.Is(err, improvement.ErrArtifactUnverifiable) || spy.calls != before {
				t.Fatal("read outside supported ownership", err)
			}
		})
	}
}

func TestProductionTaskArtifactReaderSupportsTenantScopedImmutableScheduledURI(t *testing.T) {
	ctx := context.Background()
	store, err := filestore.OpenLocalStorage(filestore.LocalConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	input := agentcore.AgentInput{TenantID: "tenant-a", RunID: "run-a"}
	host, err := (missionTaskAgentResultNormalizer{store: store}).persistRuntimeOutput(ctx, input, "scheduled bytes")
	if err != nil {
		t.Fatal(err)
	}
	key := scheduledArtifactStorageKey("tenant-a", "scheduled-run-a", "artifact-a", 15, host.SHA256, "report.md")
	if _, err := store.Upload(ctx, key, []byte("scheduled bytes"), "text/markdown", "report.md"); err != nil {
		t.Fatal(err)
	}
	reader := productionTaskArtifactReader{store: store}
	access := improvement.TaskArtifactAccess{ExecutionTenantID: "tenant-a", RunID: "run-a", Reference: agentcore.ArtifactRef{ID: "original-runtime-artifact-id", Kind: agentcore.ArtifactKindReport, URI: "artifact://" + key}}
	content, err := reader.OpenTaskArtifact(ctx, access)
	if err != nil {
		t.Fatal(err)
	}
	content.Reader.Close()
	if content.SHA256 != host.SHA256 || content.SizeBytes != 15 {
		t.Fatal(content)
	}
	access.ExecutionTenantID = "tenant-b"
	if _, err := reader.OpenTaskArtifact(ctx, access); !errors.Is(err, improvement.ErrArtifactUnverifiable) {
		t.Fatal("foreign artifact admitted", err)
	}
	access.ExecutionTenantID = "tenant-a"
	access.Reference.SHA256 = strings.Repeat("0", 64)
	access.Reference.SizeBytes = 15
	if _, err := reader.OpenTaskArtifact(ctx, access); !errors.Is(err, improvement.ErrArtifactUnverifiable) {
		t.Fatal("contradictory declaration admitted", err)
	}
}

func TestProductionTaskArtifactFormalFlowUsesHostBindingAndRealFileBytes(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	rt := &platform.Runtime{UnitOfWork: env.UnitOfWork, Clock: env.Clock, IDGenerator: env.IDGenerator, FileStore: env.FileStore}
	repo, _, delivery, _ := newProductionMissionTaskServices(rt)
	envelope := &agentcore.ExecutionEnvelope{TenantID: "org-a", RunID: "run-a", AgentID: "agent-a", AgentProfileID: "profile-a", MissionID: "mission-a", TaskID: "task-a"}
	if err := envelope.SealAt(env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	task := missiontask.TaskRecord{Version: 1, Status: missiontask.TaskStatusInProgress, Contract: missiontask.TaskContract{ID: "task-a", MissionID: "mission-a", Assignee: missiontask.TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: "agent-a"}}, Goal: "original goal", ExecutionEnvelope: envelope, ExpectedDeliverables: []missiontask.ExpectedDeliverable{{ID: "report", Title: "report"}}}}
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error { return repo.SaveTask(ctx, tx, task) }); err != nil {
		t.Fatal(err)
	}
	recorder := productionRuntimeEvidenceRecorder(agentcore.StoreTraceRecorder{Store: runtimeEvidenceTraceObjects{}}, productionTraceStore{evidenceCore: env.DB})
	input := agentcore.AgentInput{TenantID: "org-a", RunID: "run-a", Task: agentcore.TaskInput{ID: "task-a", MissionID: "mission-a", Goal: "original goal"}, ContextPack: agentcore.ContextPack{AgentID: "agent-a", TaskID: "task-a", MissionID: "mission-a"}}
	session, err := recorder.(agentcore.DurableTraceRecorder).BeginTrace(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RecordRequest(ctx, agentcore.RuntimeRequest{TenantID: "org-a", RunID: "run-a", AgentID: "agent-a", TaskID: "task-a", MissionID: "mission-a"}); err != nil {
		t.Fatal(err)
	}
	attempt := session.(agentcore.RuntimeAttemptTraceSession).RuntimeAttemptID()
	ref, err := (missionTaskAgentResultNormalizer{store: env.FileStore}).persistRuntimeOutput(ctx, input, "actual formal output")
	if err != nil {
		t.Fatal(err)
	}
	env.Clock.Advance(time.Now().UTC().Sub(env.Clock.Now()))
	result := agentcore.AgentResult{ID: "result-a", RunID: "run-a", AgentID: "agent-a", TaskID: "task-a", MissionID: "mission-a", RuntimeAttemptID: attempt, InternalTraceRef: session.Ref(), Summary: agentcore.Summary{Text: "actual report"}, Deliverables: []agentcore.Deliverable{{ID: "report-a", ExpectedDeliverableID: "report", Title: "report", ArtifactRef: ref}}}
	if _, err := delivery.SubmitAgentResult(ctx, "task-a", result, missiontask.SubmitOptions{IdempotencyKey: "submit"}); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(ctx, agentcore.TraceTerminal{Status: agentcore.RuntimeStatusCompleted, CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	evidence := improvement.NewRuntimeEvidenceStore(env.DB)
	if _, err := evidence.ReconcileBusiness(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if n, err := evidence.ReconcileTaskArtifacts(ctx, productionTaskArtifactReader{store: env.FileStore}, 10); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	r, err := evidence.TaskArtifactVerifications(ctx, "mission:org-a/mission-a", attempt)
	if err != nil || len(r) != 1 || r[0].Status != "verified" || r[0].Items[0].SHA256 != ref.SHA256 {
		t.Fatal(r, err)
	}
	business, err := evidence.BusinessAssessments(ctx, "mission:org-a/mission-a", attempt)
	if err != nil || len(business) != 2 || business[1].BusinessStatus != "accepted_without_qa" {
		t.Fatal(business, err)
	}
	coordinator := migration.NewCoordinator(env.Databases)
	for _, register := range []func(*migration.Coordinator) error{ontology.RegisterMigrations, contextgraph.RegisterMigrations, reliability.RegisterMigrations, problemhub.RegisterMigrations} {
		if err := register(coordinator); err != nil {
			t.Fatal(err)
		}
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatal(err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(env.DB)); err != nil {
		t.Fatal(err)
	}
	runner, err := newProductionOntologyProjectionRunner(ctx, env.DB)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(env.DB, runner, time.Now)
	sink.improvements = improvement.NewStore(env.DB)
	// Deliberately project the child before its case and business outboxes.
	if n, err := evidence.ProjectTaskArtifactsPending(ctx, sink, 10); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := evidence.ProjectBusinessPending(ctx, sink, 10); err != nil {
		t.Fatal(err)
	}
	_, links, _, _, err := contextgraph.NewPlatformStore(env.DB).Snapshot(ctx, "mission:org-a/mission-a")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, link := range links {
		kinds[link.LinkType]++
	}
	for _, kind := range []string{"improvement_case_has_artifact_verification", "improvement_artifact_verification_for_business_assessment"} {
		if kinds[kind] != 1 {
			t.Fatal("child-first projection lost or duplicated actual relation", kind, kinds)
		}
	}
}

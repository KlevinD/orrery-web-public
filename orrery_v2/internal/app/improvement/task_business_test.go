package improvement

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/storage"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/uow"
)

type businessFixture struct {
	core     *db.Store
	evidence *RuntimeEvidenceStore
	unit     *uow.UnitOfWork
	repo     *missiontask.PlatformRepository
	sink     missiontask.EventSink
	delivery *missiontask.DeliveryService
	qa       *missiontask.QAService
	request  RuntimeRequestEvidence
	task     missiontask.TaskRecord
	result   agentcore.AgentResult
}

func newBusinessFixture(t *testing.T, core *db.Store, name string, required bool) *businessFixture {
	t.Helper()
	ctx := context.Background()
	_, original := runtimeEvidenceFixture(t, name)
	req := agentcore.RuntimeRequest{TenantID: "tenant-a", RunID: agentcore.AgentRunID("run-" + name), AgentID: "agent-a", TaskID: "task-" + name, MissionID: "mission-" + name}
	scope := original.Scope
	scope.Run.ID = "agent-run:" + req.RunID.String()
	scope.Source.ID = req.RunID.String()
	scope.Task.ID = req.TaskID
	request, err := BuildRuntimeRequestEvidence(req, scope, name, "trace://"+name, original.CapturedAt)
	if err != nil {
		t.Fatal(err)
	}
	at := clock.NewFake(time.Unix(1002, 0).UTC())
	ids := idgen.NewDeterministic(name)
	f := &businessFixture{core: core, evidence: NewRuntimeEvidenceStore(core), unit: uow.New(core).WithClock(at).WithIDGenerator(ids), repo: missiontask.NewPlatformRepository(), request: request}
	envelope := &agentcore.ExecutionEnvelope{TenantID: req.TenantID, RunID: req.RunID.String(), AgentID: req.AgentID.String(), AgentProfileID: req.AgentID.String(), MissionID: req.MissionID, TaskID: req.TaskID}
	if err := envelope.SealAt(request.CapturedAt); err != nil {
		t.Fatal(err)
	}
	f.task = missiontask.TaskRecord{Version: 1, Status: missiontask.TaskStatusInProgress, StatusChangedAt: request.CapturedAt, Contract: missiontask.TaskContract{
		ID: missiontask.TaskID(req.TaskID), MissionID: missioncore.MissionID(req.MissionID), Assignee: missiontask.TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: "agent-a"}},
		Goal: "private original business goal", QACheckRequired: required, QAEnabled: required, ExecutionEnvelope: envelope,
		ExpectedDeliverables: []missiontask.ExpectedDeliverable{{ID: "expected-a", Title: "report"}},
		AcceptanceCriteria:   []missiontask.AcceptanceCriterion{{ID: "criterion-a", Description: "original acceptance rule"}},
		EvidenceRequirements: []missiontask.EvidenceRequirement{{ID: "required-a", Required: true, Kind: missiontask.EvidenceRequirementKindTestLog, Description: "test evidence"}},
	}}
	if err := f.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error { return f.repo.SaveTask(ctx, tx, f.task) }); err != nil {
		t.Fatal(err)
	}
	if err := f.evidence.Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := f.evidence.captureBusinessBinding(ctx, request, agentcore.TaskInput{ID: req.TaskID, MissionID: req.MissionID, Goal: f.task.Contract.Goal}); err != nil {
		t.Fatal(err)
	}
	f.result = agentcore.AgentResult{ID: agentcore.AgentResultID("result-" + name), RunID: req.RunID, RuntimeAttemptID: request.AttemptID, AgentID: "agent-a", TaskID: f.task.Contract.ID, MissionID: f.task.Contract.MissionID,
		InternalTraceRef: request.TraceRef, Summary: agentcore.Summary{Text: "private result summary"}, UserReport: "private public report content",
		Deliverables: []agentcore.Deliverable{{ID: "deliverable-a", ExpectedDeliverableID: "expected-a", Title: "report", ArtifactRef: agentcore.ArtifactRef{ID: "artifact-a", Kind: agentcore.ArtifactKindReport, URI: "artifact://private-location"}}},
		Evidence:     []agentcore.Evidence{{ID: "evidence-a", AcceptanceCriterionID: "criterion-a", EvidenceRequirementID: "required-a", Kind: agentcore.EvidenceKindTestLog, ToolLogRef: agentcore.ToolLogRef{ID: "log-a"}}}}
	f.sink = TaskOutcomeEventSink{Next: missionevent.NewTaskEventSink(), Repository: f.repo}
	f.delivery = missiontask.NewDeliveryService(f.unit, f.repo, f.sink, missiontask.DeliveryServiceOptions{Clock: at, IDGenerator: ids})
	f.qa = missiontask.NewQAService(f.unit, f.repo, f.sink, missiontask.QAServiceOptions{Clock: at, IDGenerator: ids})
	return f
}

func (f *businessFixture) submit(t *testing.T) missiontask.TaskSubmission {
	t.Helper()
	s, err := f.delivery.SubmitAgentResult(context.Background(), f.task.Contract.ID, f.result, missiontask.SubmitOptions{IdempotencyKey: "submit"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func (f *businessFixture) complete(t *testing.T) Case {
	t.Helper()
	c, err := f.evidence.Complete(context.Background(), f.request, runtimeEvidenceTerminal())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func (f *businessFixture) decide(t *testing.T, status missiontask.QAStatus) {
	t.Helper()
	r, err := f.qa.SubmitQAResult(context.Background(), f.task.Contract.ID, missiontask.QAResultRecord{Status: status, CheckedBy: "reviewer-a", RejectionReasons: []string{"requires rework"}}, missiontask.QASubmitOptions{IdempotencyKey: "qa"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != status {
		t.Fatalf("QA changed status %s -> %s", status, r.Status)
	}
}
func (f *businessFixture) assessments(t *testing.T) []BusinessAssessment {
	t.Helper()
	a, err := f.evidence.BusinessAssessments(context.Background(), f.request.Scope.TenantID, f.request.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type businessTestSink struct {
	fail  bool
	items []BusinessAssessment
}

func (s *businessTestSink) ProjectBusinessAssessment(_ context.Context, a BusinessAssessment) error {
	if s.fail {
		return errors.New("projection unavailable")
	}
	s.items = append(s.items, a)
	return nil
}

func TestTaskBusinessFormalSubmissionAndQAStaySeparateFromRuntimeReview(t *testing.T) {
	for _, mode := range []string{"approved", "rejected", "needs_rework", "qa_not_required", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			store := testStore(t)
			f := newBusinessFixture(t, store.core, mode, mode != "qa_not_required")
			if mode == "blocked" {
				f.result.Deliverables = nil
				f.result.Evidence = nil
				f.result.Blocker = &agentcore.Blocker{Summary: "blocked", Report: agentcore.ArtifactRef{ID: "blocked-report", Kind: agentcore.ArtifactKindReport}}
			}
			c := f.complete(t)
			before, _, err := f.evidence.GetReview(context.Background(), f.request.Scope.TenantID, f.request.AttemptID)
			if err != nil {
				t.Fatal(err)
			}
			submission := f.submit(t)
			if mode == "approved" || mode == "rejected" || mode == "needs_rework" {
				pending := f.assessments(t)
				if len(pending) != 1 || pending[0].BusinessStatus != "qa_pending" {
					t.Fatalf("pending %+v", pending)
				}
				f.decide(t, missiontask.QAStatus(mode))
			}
			got := f.assessments(t)
			want := "qa_rejected"
			if mode == "approved" {
				want = "qa_approved"
			}
			if mode == "qa_not_required" {
				want = "accepted_without_qa"
			}
			if mode == "blocked" {
				want = "blocked"
			}
			final := got[len(got)-1]
			if final.BusinessStatus != want || final.Outcome.SubmissionID != string(submission.ID) || final.CausalAttribution != "unresolved" {
				t.Fatalf("incorrect business result %+v", got)
			}
			if len(final.Blockers) != 4 {
				t.Fatalf("original contract not captured or false readiness: %+v", final.Blockers)
			}
			after, _, err := f.evidence.GetReview(context.Background(), f.request.Scope.TenantID, f.request.AttemptID)
			if err != nil || !reflect.DeepEqual(before, after) || after.BaselineReady {
				t.Fatal("business acceptance rewrote runtime review", err)
			}
			unchanged, err := store.Get(context.Background(), c.TenantID, c.ID)
			if err != nil || !reflect.DeepEqual(c, unchanged) {
				t.Fatal("business acceptance rewrote original case", err)
			}
			raw, _ := json.Marshal(got)
			for _, secret := range []string{"private original business goal", "private result summary", "private public report content", "artifact://private-location"} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("raw business content leaked to RSI")
				}
			}
			if _, err := f.evidence.BusinessAssessments(context.Background(), "other-tenant", f.request.AttemptID); !errors.Is(err, ErrNotFound) {
				t.Fatal("cross-tenant inspection allowed", err)
			}
		})
	}
}

func TestTaskBusinessReconcilesLateTerminalAndRetriesProjection(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	f := newBusinessFixture(t, store.core, "late-terminal", true)
	f.submit(t)
	f.decide(t, missiontask.QAStatusApproved)
	if n, err := f.evidence.ReconcileBusiness(ctx, 10); err != nil || n != 0 {
		t.Fatal("associated before runtime terminal", n, err)
	}
	f.complete(t)
	// New objects simulate process recreation; PostgreSQL variant below closes
	// the underlying connections as well.
	f.evidence = NewRuntimeEvidenceStore(store.core)
	if n, err := f.evidence.ReconcileBusiness(ctx, 10); err != nil || n != 2 {
		t.Fatal("late terminal not reconciled", n, err)
	}
	sink := &businessTestSink{fail: true}
	if n, err := f.evidence.ProjectBusinessPending(ctx, sink, 10); err == nil || n != 0 {
		t.Fatal("failed projection acknowledged", n, err)
	}
	sink.fail = false
	if n, err := f.evidence.ProjectBusinessPending(ctx, sink, 10); err != nil || n != 2 {
		t.Fatal("projection retry lost evidence", n, err)
	}
	if n, err := f.evidence.ProjectBusinessPending(ctx, sink, 10); err != nil || n != 0 {
		t.Fatal("projection replay duplicated evidence", n, err)
	}
	if n, err := f.evidence.ReconcileBusiness(ctx, 10); err != nil || n != 0 {
		t.Fatal("final binding remained pending", n, err)
	}
}

type failBusinessEventSink struct {
	next  missiontask.EventSink
	event missiontask.TaskEventType
}

func (s failBusinessEventSink) AppendTaskEvent(ctx context.Context, tx uow.Tx, e missiontask.TaskEvent) error {
	if err := s.next.AppendTaskEvent(ctx, tx, e); err != nil {
		return err
	}
	if e.Type == s.event {
		return errors.New("injected transaction failure")
	}
	return nil
}

func TestTaskBusinessReceiptsRollbackWithSubmissionAndQA(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	f := newBusinessFixture(t, store.core, "rollback", true)
	f.complete(t)
	bad := missiontask.NewDeliveryService(f.unit, f.repo, failBusinessEventSink{f.sink, missiontask.TaskEventQARequested}, missiontask.DeliveryServiceOptions{IDGenerator: idgen.NewDeterministic("bad-submit")})
	if _, err := bad.SubmitAgentResult(ctx, f.task.Contract.ID, f.result, missiontask.SubmitOptions{IdempotencyKey: "bad"}); err == nil {
		t.Fatal("injected submission failure ignored")
	}
	if got := f.assessments(t); len(got) != 0 {
		t.Fatal("rolled back submission left RSI receipt")
	}
	if err := f.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		_, exists, err := f.repo.LatestSubmission(ctx, tx, f.task.Contract.ID)
		if exists {
			t.Fatal("failed submission committed")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.submit(t)
	qa := missiontask.NewQAService(f.unit, f.repo, failBusinessEventSink{f.sink, missiontask.TaskEventQAApproved}, missiontask.QAServiceOptions{IDGenerator: idgen.NewDeterministic("bad-qa")})
	if _, err := qa.SubmitQAResult(ctx, f.task.Contract.ID, missiontask.QAResultRecord{CheckedBy: "reviewer-a"}, missiontask.QASubmitOptions{IdempotencyKey: "bad"}); err == nil {
		t.Fatal("injected QA failure ignored")
	}
	if got := f.assessments(t); len(got) != 1 || got[0].BusinessStatus != "qa_pending" {
		t.Fatal("rolled back QA left acceptance", got)
	}
	if err := f.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		task, err := f.repo.GetTask(ctx, tx, f.task.Contract.ID)
		if err == nil && task.Status != missiontask.TaskStatusInReview {
			t.Fatal("QA failure committed task approval")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.decide(t, missiontask.QAStatusApproved)
}

func TestTaskBusinessRejectsIdentityChangesAndRecordsContractDrift(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	f := newBusinessFixture(t, store.core, "identity", true)
	c := f.complete(t)
	f.submit(t)
	f.decide(t, missiontask.QAStatusApproved)
	a := f.assessments(t)[1]
	b, err := f.evidence.GetBusinessBinding(ctx, f.request.Scope.TenantID, f.request.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	review, _, _ := f.evidence.GetReview(ctx, b.TenantID, b.AttemptID)
	for _, field := range []string{"tenant", "run", "trace", "agent", "mission", "task", "attempt"} {
		t.Run(field, func(t *testing.T) {
			o := a.Outcome
			switch field {
			case "attempt":
				o.RuntimeAttemptID = "previous-attempt"
			case "tenant":
				o.ExecutionTenantID = "other"
			case "run":
				o.RunID = "retry"
			case "trace":
				o.TraceRef = "trace://retry"
			case "agent":
				o.AgentID = "other"
			case "mission":
				o.MissionID = "other"
			case "task":
				o.TaskID = "other"
			}
			o.ID = ""
			o.ID = "task-outcome:" + Digest(o)
			if _, err := buildBusinessAssessment(f.request, b, c, review, o); !errors.Is(err, ErrInvalid) {
				t.Fatal("foreign outcome associated", err)
			}
		})
	}
	changed := a.Outcome
	changed.ContractSHA256 = Digest("changed requirements")
	changed.ID = ""
	changed.ID = "task-outcome:" + Digest(changed)
	assessed, err := buildBusinessAssessment(f.request, b, c, review, changed)
	if err != nil || !strings.Contains(strings.Join(assessed.Blockers, ","), "execution_contract_changed") {
		t.Fatal("contract drift hidden", err)
	}
}

func TestTaskBusinessCursorSkipsWaitingOrInvalidBindings(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	f := newBusinessFixture(t, store.core, "cursor", false)
	f.submit(t)
	f.complete(t)
	if err := store.core.PutRecord(ctx, businessPendingTable, "0-corrupt", db.Record{"payload": "invalid"}); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		_, _ = f.evidence.ReconcileBusiness(ctx, 1)
	}
	page, err := store.core.ListRecords(ctx, businessAssessmentTable, "", 10)
	if err != nil || len(page.Items) != 2 {
		t.Fatal("invalid earlier row starved valid attempt", len(page.Items), err)
	}
}

func TestTaskBusinessRecorderCapturesFormalContractBeforeDispatch(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	f := newBusinessFixture(t, store.core, "recorder", true)
	base := &runtimeEvidenceTestTrace{}
	recorder := RuntimeEvidenceRecorder{Recorder: base, Evidence: f.evidence, Scope: func(agentcore.RuntimeRequest) (RuntimeEvidenceScope, error) { return f.request.Scope, nil }, Now: func() time.Time { return f.request.CapturedAt }}
	input := agentcore.AgentInput{Task: agentcore.TaskInput{ID: f.task.Contract.ID.String(), MissionID: f.task.Contract.MissionID.String(), Goal: f.task.Contract.Goal}}
	session, err := recorder.BeginTrace(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := session.(*runtimeEvidenceTraceSession)
	base.beforeRequest = func() {
		b, err := f.evidence.GetBusinessBinding(ctx, f.request.Scope.TenantID, wrapped.attempt)
		if err != nil || b.ContractSHA256 != Digest(f.task.Contract) {
			t.Fatal("dispatch preceded formal contract capture", err)
		}
	}
	req := agentcore.RuntimeRequest{TenantID: "tenant-a", RunID: agentcore.AgentRunID(f.request.RunID), AgentID: "agent-a", TaskID: input.Task.ID, MissionID: input.Task.MissionID}
	if err := session.RecordRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(ctx, runtimeEvidenceTerminal()); err != nil {
		t.Fatal(err)
	}
}

func TestTaskBusinessSameRunAndTraceRetryCannotInheritPriorAcceptance(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	f := newBusinessFixture(t, store.core, "same-run-retry", false)
	f.submit(t)
	f.complete(t)
	request := f.request
	request.AttemptID = "second-attempt"
	request.ID = ""
	request.ID = "runtime-request-evidence:" + Digest(request)
	if err := f.evidence.Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := f.evidence.captureBusinessBinding(ctx, request, agentcore.TaskInput{ID: f.task.Contract.ID.String(), MissionID: f.task.Contract.MissionID.String(), Goal: f.task.Contract.Goal}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.evidence.Complete(ctx, request, runtimeEvidenceTerminal()); err != nil {
		t.Fatal(err)
	}
	got, err := f.evidence.BusinessAssessments(ctx, request.Scope.TenantID, request.AttemptID)
	if err != nil || len(got) != 0 {
		t.Fatal("retry inherited the prior attempt's acceptance", got, err)
	}
	if len(f.assessments(t)) != 2 {
		t.Fatal("retry erased original evidence")
	}
}

func TestTaskBusinessRequiresAuthoritativeTenantAndPreservesLegacySubmissions(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing tenant", true: "legacy"}[legacy], func(t *testing.T) {
			ctx := context.Background()
			store := testStore(t)
			f := newBusinessFixture(t, store.core, "unscoped", false)
			f.complete(t)
			f.task.Contract.ExecutionEnvelope = nil
			if err := f.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error { return f.repo.SaveTask(ctx, tx, f.task) }); err != nil {
				t.Fatal(err)
			}
			if legacy {
				f.result.RuntimeAttemptID = ""
			}
			f.submit(t)
			got, err := f.evidence.BusinessAssessments(ctx, f.request.Scope.TenantID, f.request.AttemptID)
			if legacy {
				if err != nil || len(got) != 0 {
					t.Fatal("legacy result was falsely associated", err)
				}
			} else if !errors.Is(err, ErrInvalid) {
				t.Fatal("unproven tenant acquired business assessment", err)
			}
		})
	}
}

func TestPostgresTaskBusinessAcrossConnectionsAndRestart(t *testing.T) {
	raw := os.Getenv("ORRERY_TEST_IMPROVEMENT_POSTGRES_URL")
	if raw == "" {
		t.Skip("isolated improvement PostgreSQL URL is not set")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(u.Query().Get("search_path"), "orrery_test_") {
		t.Fatal("isolated schema required")
	}
	ctx := context.Background()
	open := func() *db.Store {
		t.Helper()
		core, err := db.Open(ctx, db.Config{Driver: db.DriverPostgres, URL: raw})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = core.Close() })
		return core
	}
	core1, core2 := open(), open()
	f := newBusinessFixture(t, core1, "pg-business", true)
	f.submit(t)
	f.complete(t)
	f.unit = uow.New(core2).WithIDGenerator(idgen.NewDeterministic("pg-qa-events"))
	f.qa = missiontask.NewQAService(f.unit, f.repo, f.sink, missiontask.QAServiceOptions{IDGenerator: idgen.NewDeterministic("pg-qa")})
	f.decide(t, missiontask.QAStatusApproved)
	_ = core1.Close()
	_ = core2.Close()
	core3 := open()
	f.evidence = NewRuntimeEvidenceStore(core3)
	if got := f.assessments(t); len(got) != 2 || got[1].BusinessStatus != "qa_approved" {
		t.Fatal("restart lost original association", got)
	}
	if _, err := f.evidence.ReconcileBusiness(ctx, 100); err != nil {
		t.Fatal(err)
	}
	sink := &businessTestSink{}
	if n, err := f.evidence.ProjectBusinessPending(ctx, sink, 100); err != nil || n < 2 || n != len(sink.items) {
		t.Fatal("restart lost durable projection", n, err)
	}
	// Other PostgreSQL tests use this isolated schema too. The global outbox
	// may contain their work; require this attempt's exact receipts once each.
	expected := map[string]bool{}
	for _, a := range f.assessments(t) {
		expected[a.ID] = false
	}
	for _, a := range sink.items {
		if a.TenantID != f.request.Scope.TenantID || a.AttemptID != f.request.AttemptID {
			continue
		}
		if seen, ok := expected[a.ID]; !ok || seen {
			t.Fatal("unexpected or repeated assessment projection", a.ID)
		}
		expected[a.ID] = true
	}
	for id, seen := range expected {
		if !seen {
			t.Fatal("restart lost assessment projection", id)
		}
	}
	if n, err := f.evidence.ProjectBusinessPending(ctx, sink, 100); err != nil || n != 0 {
		t.Fatal("restart projection was not idempotent", n, err)
	}
	// The domain records are real writes in the isolated schema.
	if page, err := core3.ListRecords(ctx, storage.TaskQAResults, "", 100); err != nil || len(page.Items) == 0 {
		t.Fatal("QA was not committed", err)
	}
}

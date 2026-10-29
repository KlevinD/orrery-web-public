package improvement

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

type runtimeDiagnoserFunc func(context.Context, RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error)

func (f runtimeDiagnoserFunc) DiagnoseRuntime(ctx context.Context, in RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
	return f(ctx, in)
}

func diagnosisTestModel(_ context.Context, in RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
	return RuntimeDiagnosisInference{Diagnosis: Diagnosis{Summary: "原生执行已返回，但没有完整的业务验收与产物字节证据；先核对同次正式结果及验收记录，暂不能归因。", EvidenceIDs: []string{in.Terminal.ID, in.Review.ID}, AffectedKinds: []TargetKind{TargetWorkflow, TargetAgentLoop}}, ProfileID: "test-profile", Model: "test-model", PromptSHA256: Digest("test-prompt")}, nil
}

func diagnosisTestCase(t *testing.T, store *Store, attempt string, status ...agentcore.RuntimeStatus) (Case, RuntimeRequestEvidence) {
	t.Helper()
	req, request := runtimeEvidenceFixture(t, attempt)
	execution := executionFixture(t, request)
	execution.Snapshot.DependencyProtocol = agentcore.RuntimeDependencyProtocol
	identity := execution.Implementation
	recorder := RuntimeEvidenceRecorder{Recorder: &runtimeEvidenceTestTrace{}, Evidence: store.RuntimeEvidence(), Implementation: &identity, Now: func() time.Time { return request.CapturedAt }, Scope: func(agentcore.RuntimeRequest) (RuntimeEvidenceScope, error) { return request.Scope, nil }}
	trace, err := recorder.BeginTrace(context.Background(), agentcore.AgentInput{})
	if err != nil {
		t.Fatal(err)
	}
	if err = trace.RecordRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	s := trace.(*runtimeEvidenceTraceSession)
	if err = s.RecordExecution(context.Background(), execution.Snapshot); err != nil {
		t.Fatal(err)
	}
	s.RecordDependency(dependencyModelObservation(request))
	s.RecordDependency(agentcore.RuntimeDependencyObservation{TenantID: request.ExecutionTenantID, RunID: request.RunID, AgentID: request.AgentID, Kind: "runner_returned"})
	terminalResult := runtimeEvidenceTerminal()
	if len(status) > 0 {
		terminalResult.Status = status[0]
	}
	if err = s.Finish(context.Background(), terminalResult); err != nil {
		t.Fatal(err)
	}
	terminal, err := store.RuntimeEvidence().GetTerminal(context.Background(), request.Scope.TenantID, s.captured.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := capturedRuntimeObservation(*s.captured, terminal)
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Get(context.Background(), request.Scope.TenantID, caseID(obs))
	if err != nil {
		t.Fatal(err)
	}
	return c, *s.captured
}

func diagnosisTestStore(t *testing.T) *Store {
	s := testStore(t)
	s.now = func() time.Time { return time.Unix(999, 0).UTC() }
	if _, err := s.EnableRuntimeDiagnoses(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Unix(1002, 0).UTC() }
	return s
}

func TestRuntimeDiagnosisAdvancesOriginalCaseAndProjectsReceipt(t *testing.T) {
	ctx := context.Background()
	s := diagnosisTestStore(t)
	original, request := diagnosisTestCase(t, s, "diagnosis")
	called := 0
	model := runtimeDiagnoserFunc(func(ctx context.Context, in RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
		called++
		if in.CaseID != original.ID || in.AttemptID != request.AttemptID || in.Execution.ID != in.Review.ExecutionID || in.Terminal.Dependencies.ExecutionID != in.Execution.ID || in.Review.BaselineReady || in.Review.BusinessQuality != "unassessed" {
			t.Fatal("unbound or falsely accepted input")
		}
		raw := string(encode(in)["payload"].(string))
		if strings.Contains(raw, "private task content") || strings.Contains(raw, "private credential") {
			t.Fatal("raw private evidence leaked")
		}
		job, err := s.RuntimeDiagnosis(ctx, in.TenantID, in.AttemptID)
		if err != nil || job.State != "running" || job.InputSHA256 != Digest(in) {
			t.Fatal("model ran before durable reservation", err)
		}
		return diagnosisTestModel(ctx, in)
	})
	if worked, err := s.DiagnoseNextRuntime(ctx, model, 4); err != nil || !worked {
		t.Fatal(worked, err)
	}
	c, err := s.Get(ctx, original.TenantID, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.RuntimeDiagnosis(ctx, original.TenantID, request.AttemptID)
	if err != nil || job.State != "completed" || c.State != Diagnosed || c.Diagnosis == nil || c.ChangeSet != nil || !reflect.DeepEqual(c.Observation, original.Observation) || c.Diagnosis.EvidenceIDs[0] != job.ReceiptID {
		t.Fatal("diagnosis did not advance exact original case", job, err)
	}
	events := OntologyEvents(c)
	if len(events) != 1 || events[0].Payload["status"] != "diagnosed" || events[0].SourceAccessRef != original.Observation.SourceAccessRef {
		t.Fatal("bad diagnosis projection")
	}
	ids := events[0].Payload["evidence_ids"].([]string)
	found := false
	for _, id := range ids {
		found = found || id == job.ReceiptID
	}
	if !found {
		t.Fatal("receipt missing in graph projection")
	}
	if _, err := s.RuntimeReviewForCase(ctx, c); err != nil {
		t.Fatal("diagnosis broke original immutable binding", err)
	}
	if _, err := s.DiagnoseNextRuntime(ctx, model, 4); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatal("completed inference repeated")
	}
	if _, err := s.RuntimeDiagnosis(ctx, "other-tenant", request.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-tenant diagnosis read", err)
	}
}

func TestRuntimeDiagnosisExcludesHistoricalAndLegacyExecutions(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	s.now = func() time.Time { return time.Unix(1002, 0).UTC() }
	if _, err := s.EnableRuntimeDiagnoses(ctx); err != nil {
		t.Fatal(err)
	}
	c, _ := diagnosisTestCase(t, s, "old-request")
	newStore := NewStore(s.core)
	newStore.now = s.now
	at, err := newStore.EnableRuntimeDiagnoses(ctx)
	if err != nil || !at.Equal(time.Unix(1002, 0)) {
		t.Fatal("activation reset after restart")
	}
	called := 0
	model := runtimeDiagnoserFunc(func(ctx context.Context, in RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
		called++
		return diagnosisTestModel(ctx, in)
	})
	if work, err := newStore.DiagnoseNextRuntime(ctx, model, 4); work || err != nil || called != 0 {
		t.Fatal("historical request admitted")
	}
	current, _ := s.Get(ctx, c.TenantID, c.ID)
	if current.State != Observed {
		t.Fatal("historical case changed")
	}
	s = diagnosisTestStore(t)
	_, request := runtimeEvidenceFixture(t, "legacy")
	if err := s.RuntimeEvidence().Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RuntimeEvidence().Complete(ctx, request, runtimeEvidenceTerminal()); err != nil {
		t.Fatal(err)
	}
	if work, err := s.DiagnoseNextRuntime(ctx, model, 4); work || err != nil || called != 0 {
		t.Fatal("legacy external execution admitted")
	}
}

func TestRuntimeDiagnosisRejectsInventedEvidenceAndAuthority(t *testing.T) {
	for _, name := range []string{"other-evidence", "unknown-kind", "duplicate-kind", "external-diagnosis", "missing-model", "provider-error"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := diagnosisTestStore(t)
			c, request := diagnosisTestCase(t, s, name)
			calls := 0
			model := runtimeDiagnoserFunc(func(ctx context.Context, in RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
				calls++
				r, _ := diagnosisTestModel(ctx, in)
				switch name {
				case "other-evidence":
					r.Diagnosis.EvidenceIDs = []string{"another-attempt"}
				case "unknown-kind":
					r.Diagnosis.AffectedKinds = []TargetKind{"privilege"}
				case "duplicate-kind":
					r.Diagnosis.AffectedKinds = []TargetKind{TargetTool, TargetTool}
				case "external-diagnosis":
					r.Diagnosis.DiagnosisCaseID = "fake-authority"
				case "missing-model":
					r.Model = ""
				case "provider-error":
					return r, errors.New("private credential in provider error")
				}
				return r, nil
			})
			if _, err := s.DiagnoseNextRuntime(ctx, model, 4); err != nil {
				t.Fatal(err)
			}
			restored := NewStore(s.core)
			restored.now = s.now
			if _, err := restored.DiagnoseNextRuntime(ctx, model, 4); err != nil {
				t.Fatal(err)
			}
			job, err := s.RuntimeDiagnosis(ctx, c.TenantID, request.AttemptID)
			if err != nil || job.State != "failed" || strings.Contains(job.Failure, "private") || calls != 1 {
				t.Fatal("invalid inference repeated or accepted", err)
			}
			c, _ = s.Get(ctx, c.TenantID, c.ID)
			if c.State != Observed {
				t.Fatal("invalid diagnosis advanced case")
			}
		})
	}
}

func TestRuntimeDiagnosisBudgetAndConcurrentReservationSurviveRestart(t *testing.T) {
	ctx := context.Background()
	s := diagnosisTestStore(t)
	diagnosisTestCase(t, s, "first")
	diagnosisTestCase(t, s, "second")
	var calls atomic.Int32
	model := runtimeDiagnoserFunc(func(ctx context.Context, in RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
		calls.Add(1)
		return diagnosisTestModel(ctx, in)
	})
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other := NewStore(s.core)
			other.now = s.now
			if _, err := other.DiagnoseNextRuntime(ctx, model, 1); err != nil && !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	other := NewStore(s.core)
	other.now = s.now
	if _, err := other.DiagnoseNextRuntime(ctx, model, 1); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("durable hourly budget bypassed", calls.Load())
	}
	other.now = func() time.Time { return time.Unix(4602, 0).UTC() }
	if work, err := other.DiagnoseNextRuntime(ctx, model, 1); err != nil || !work {
		t.Fatal(work, err)
	}
	if calls.Load() != 2 {
		t.Fatal("next-hour pending job lost")
	}
}

func TestRuntimeDiagnosisUnknownCallIsNotReissued(t *testing.T) {
	ctx := context.Background()
	s := diagnosisTestStore(t)
	_, request := diagnosisTestCase(t, s, "unknown")
	key := diagnosisKey(request.Scope.TenantID, request.AttemptID)
	raw, _, _ := s.core.GetRecord(ctx, runtimeDiagnosisQueueTable, key)
	job, _ := decode[RuntimeDiagnosisRecord](raw)
	job.State = "running"
	if err := s.core.WithTx(ctx, func(tx db.Tx) error {
		if err := db.PutRecordTx(ctx, tx, runtimeDiagnosisTable, key, encode(job)); err != nil {
			return err
		}
		return db.DeleteRecordTx(ctx, tx, runtimeDiagnosisQueueTable, key)
	}); err != nil {
		t.Fatal(err)
	}
	restarted := NewStore(s.core)
	restarted.now = s.now
	model := runtimeDiagnoserFunc(func(context.Context, RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
		t.Fatal("unknown paid call repeated")
		return RuntimeDiagnosisInference{}, nil
	})
	if worked, err := restarted.DiagnoseNextRuntime(ctx, model, 4); worked || err != nil {
		t.Fatal(worked, err)
	}
}

func TestRuntimeDiagnosisWaitsForFormalBusinessReconciliation(t *testing.T) {
	ctx := context.Background()
	s := diagnosisTestStore(t)
	f := newBusinessFixture(t, s.core, "diagnosis-business", false)
	execution := executionFixture(t, f.request)
	execution.Snapshot.DependencyProtocol = agentcore.RuntimeDependencyProtocol
	execution, err := BuildRuntimeExecutionEvidence(f.request, execution.Implementation, execution.Snapshot, execution.CapturedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.evidence.CaptureExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	trace := &runtimeEvidenceTraceSession{captured: &f.request, execution: &execution, attempt: f.request.AttemptID}
	trace.RecordDependency(dependencyModelObservation(f.request))
	trace.RecordDependency(agentcore.RuntimeDependencyObservation{TenantID: f.request.ExecutionTenantID, RunID: f.request.RunID, AgentID: f.request.AgentID, Kind: "runner_returned"})
	end, err := buildRuntimeTerminal(f.request, runtimeEvidenceTerminal(), "succeeded")
	if err != nil {
		t.Fatal(err)
	}
	end.Dependencies = trace.freezeDependencies(end.CompletedAt)
	end.ID = ""
	end.ID = "runtime-terminal-evidence:" + Digest(end)
	if _, err := s.RuntimeEvidence().completeTerminal(ctx, f.request, end); err != nil {
		t.Fatal(err)
	}
	calls := 0
	model := runtimeDiagnoserFunc(func(ctx context.Context, in RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
		calls++
		if len(in.Business) == 0 {
			t.Fatal("diagnosis missed reconciled original acceptance")
		}
		for _, a := range in.Business {
			if a.AttemptID != f.request.AttemptID {
				t.Fatal("diagnosis mixed business attempts")
			}
		}
		return diagnosisTestModel(ctx, in)
	})
	if worked, err := s.DiagnoseNextRuntime(ctx, model, 4); worked || err != nil || calls != 0 {
		t.Fatal("diagnosed formal evidence before reconciliation", worked, err)
	}
	f.submit(t)
	if _, err := f.evidence.ReconcileBusiness(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.DiagnoseNextRuntime(ctx, model, 4); !worked || err != nil || calls != 1 {
		t.Fatal("reconciled diagnosis did not proceed", worked, err)
	}
}

func TestRuntimeDiagnosisExpiresWithoutModelAndRejectsConcurrentCaseMutation(t *testing.T) {
	ctx := context.Background()
	t.Run("expired", func(t *testing.T) {
		s := diagnosisTestStore(t)
		c, request := diagnosisTestCase(t, s, "expired")
		s.now = func() time.Time { return time.Unix(1000, 0).Add(25 * time.Hour) }
		model := runtimeDiagnoserFunc(func(context.Context, RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
			t.Fatal("expired evidence sent to model")
			return RuntimeDiagnosisInference{}, nil
		})
		if _, err := s.DiagnoseNextRuntime(ctx, model, 4); err != nil {
			t.Fatal(err)
		}
		job, _ := s.RuntimeDiagnosis(ctx, c.TenantID, request.AttemptID)
		if job.State != "failed" || job.StartedAt != (time.Time{}) {
			t.Fatal("expired diagnosis not archived without call")
		}
	})
	t.Run("concurrent-case", func(t *testing.T) {
		s := diagnosisTestStore(t)
		c, request := diagnosisTestCase(t, s, "concurrent-case")
		model := runtimeDiagnoserFunc(func(ctx context.Context, in RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
			if _, err := s.Claim(ctx, c.TenantID, c.ID, "other-learner", c.Revision, time.Minute); err != nil {
				t.Fatal(err)
			}
			return diagnosisTestModel(ctx, in)
		})
		if _, err := s.DiagnoseNextRuntime(ctx, model, 4); err != nil {
			t.Fatal(err)
		}
		job, _ := s.RuntimeDiagnosis(ctx, c.TenantID, request.AttemptID)
		current, _ := s.Get(ctx, c.TenantID, c.ID)
		if job.State != "failed" || current.State != Observed || current.Lease.Owner != "other-learner" {
			t.Fatal("diagnosis overwrote concurrent authority")
		}
	})
}

func TestRuntimeDiagnosisGeneratedResponseRecoversWithoutNewModelCall(t *testing.T) {
	ctx := context.Background()
	s := diagnosisTestStore(t)
	c, request := diagnosisTestCase(t, s, "generated")
	key := diagnosisKey(c.TenantID, request.AttemptID)
	raw, _, _ := s.core.GetRecord(ctx, runtimeDiagnosisQueueTable, key)
	job, _ := decode[RuntimeDiagnosisRecord](raw)
	input, err := s.runtimeDiagnosisInput(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := diagnosisTestModel(ctx, input)
	job.Input = &input
	job.InputSHA256 = Digest(input)
	job.Inference = &result
	job.State = "generated"
	job.CompletedAt = s.now().UTC()
	job.ReceiptID = "runtime-diagnosis-receipt:" + Digest(struct {
		InputSHA256 string
		Inference   RuntimeDiagnosisInference
	}{job.InputSHA256, result})
	if err := s.core.PutRecord(ctx, runtimeDiagnosisQueueTable, key, encode(job)); err != nil {
		t.Fatal(err)
	}
	restarted := NewStore(s.core)
	restarted.now = s.now
	model := runtimeDiagnoserFunc(func(context.Context, RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
		t.Fatal("generated answer paid again")
		return RuntimeDiagnosisInference{}, nil
	})
	if _, err := restarted.DiagnoseNextRuntime(ctx, model, 4); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(ctx, c.TenantID, c.ID)
	if current.State != Diagnosed {
		t.Fatal("generated diagnosis not applied")
	}
	// Model the crash after case/outbox commit but before queue acknowledgement.
	if err := s.core.PutRecord(ctx, runtimeDiagnosisQueueTable, key, encode(job)); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.DiagnoseNextRuntime(ctx, model, 4); err != nil {
		t.Fatal(err)
	}
	current2, _ := s.Get(ctx, c.TenantID, c.ID)
	if current2.Revision != current.Revision {
		t.Fatal("case applied twice")
	}
}

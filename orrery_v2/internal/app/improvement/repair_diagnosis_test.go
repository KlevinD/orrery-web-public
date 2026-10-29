package improvement

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func repairDiagnosisStore(t *testing.T) *Store {
	s := testStore(t)
	s.now = func() time.Time { return time.Unix(99, 0).UTC() }
	if _, err := s.EnableRuntimeDiagnoses(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Unix(103, 0).UTC() }
	return s
}

func TestManagedRepairDiagnosisReusesAgentEvidenceAndDeduplicatesRefreshes(t *testing.T) {
	ctx := context.Background()
	s := repairDiagnosisStore(t)
	f := testRepairFeedback("needs_attention")
	f.Summary = "微信返回 ret=-2 errcode=0；缺少可信供应商解释，尚未查明拒绝原因，不能认定送达。"
	c, err := s.ObserveManagedRepair(ctx, f)
	if err != nil || c.State != PendingDiagnosis {
		t.Fatalf("fault not queued: %+v %v", c, err)
	}
	refreshed := f
	refreshed.OccurrenceCount++
	refreshed.LastSeenNS++
	refreshed.OccurredAt = refreshed.OccurredAt.Add(time.Second)
	again, err := s.ObserveManagedRepair(ctx, refreshed)
	if err != nil || !reflect.DeepEqual(again, c) {
		t.Fatalf("refresh created a new case: %+v %v", again, err)
	}
	if _, err := s.GetManagedRepairFeedback(ctx, f.TenantID, repairFeedbackID(refreshed)); err != nil {
		t.Fatal("refreshed receipt lost", err)
	}
	jobs, err := s.core.ListRecords(ctx, runtimeDiagnosisQueueTable, "", 10)
	if err != nil || len(jobs.Items) != 1 {
		t.Fatal("duplicate diagnosis jobs", jobs, err)
	}
	calls := 0
	model := runtimeDiagnoserFunc(func(_ context.Context, in RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
		calls++
		if in.ManagedRepair == nil || !reflect.DeepEqual(*in.ManagedRepair, f) || in.CaseID != c.ID || in.ManagedRepair.SourceAccessRef != c.Observation.SourceAccessRef {
			t.Fatal("original Agent diagnosis or source binding missing")
		}
		return RuntimeDiagnosisInference{Diagnosis: Diagnosis{Summary: "供应商拒绝原因仍未确定，需可信错误码解释及当前收件条件证据。", EvidenceIDs: []string{in.EvidenceIDs[0]}, AffectedKinds: []TargetKind{TargetTool}}, ProfileID: "profile", Model: "model", PromptSHA256: Digest("prompt")}, nil
	})
	if worked, err := s.DiagnoseNextRuntime(ctx, model, 4); !worked || err != nil {
		t.Fatal(worked, err)
	}
	after, err := s.Get(ctx, c.TenantID, c.ID)
	if err != nil || after.State != Blocked || after.Reason != "managed_repair_needs_attention" || after.Diagnosis == nil || after.ChangeSet != nil || after.Release != nil || after.Outcome != nil {
		t.Fatalf("unresolved repair promoted: %+v %v", after, err)
	}
	receipt, err := s.RuntimeDiagnosis(ctx, c.TenantID, managedRepairAttempt(c))
	if err != nil || receipt.State != "completed" || receipt.Input.ManagedRepair.Summary != f.Summary {
		t.Fatal("review evidence missing", receipt, err)
	}
	if worked, err := NewStore(s.core).DiagnoseNextRuntime(ctx, model, 4); worked || err != nil || calls != 1 {
		t.Fatal("diagnosis repeated", worked, err, calls)
	}
	if _, err := s.RuntimeDiagnosis(ctx, "other-tenant", managedRepairAttempt(c)); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-tenant diagnosis leak", err)
	}
	for _, e := range OntologyEvents(after) {
		if e.Payload["status"] != "blocked" || e.Payload["summary"] == f.Summary {
			t.Fatal("graph concealed blocker or leaked Agent prose")
		}
	}
}

func TestManagedRepairDiagnosisFailureBlocksWithoutRetryOrFakeSuccess(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		name := "provider_error"
		if invalid {
			name = "invalid_response"
		}
		t.Run(name, func(t *testing.T) {
			s := repairDiagnosisStore(t)
			ctx := context.Background()
			c, err := s.ObserveManagedRepair(ctx, testRepairFeedback("needs_attention"))
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			model := runtimeDiagnoserFunc(func(context.Context, RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
				calls++
				if invalid {
					return RuntimeDiagnosisInference{}, nil
				}
				return RuntimeDiagnosisInference{}, errors.New("private provider error with credential")
			})
			if _, err := s.DiagnoseNextRuntime(ctx, model, 4); err != nil {
				t.Fatal(err)
			}
			after, err := s.Get(ctx, c.TenantID, c.ID)
			want := "model_unavailable"
			if invalid {
				want = "invalid_model_diagnosis"
			}
			if err != nil || after.State != Blocked || after.Reason != want || after.Diagnosis != nil {
				t.Fatal("failed diagnosis stayed observed or invented result", after, err)
			}
			if _, err := s.DiagnoseNextRuntime(ctx, model, 4); err != nil || calls != 1 {
				t.Fatal("uncertain call reissued", err, calls)
			}
		})
	}
}

func TestManagedRepairDiagnosisQueueFailureRollsBackReceiptCaseAndIndex(t *testing.T) {
	s := repairDiagnosisStore(t)
	ctx := context.Background()
	_, err := s.core.Exec(ctx, `CREATE TRIGGER fail_repair_queue BEFORE INSERT ON platform_records WHEN NEW.table_name = 'ontology_improvement_runtime_diagnosis_queue' BEGIN SELECT RAISE(ABORT, 'queue failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	f := testRepairFeedback("needs_attention")
	if _, err := s.ObserveManagedRepair(ctx, f); err == nil {
		t.Fatal("queue failure ignored")
	}
	for _, table := range []string{repairFeedbackTable, caseTable, repairCaseTable, outboxTable} {
		page, err := s.core.ListRecords(ctx, table, "", 10)
		if err != nil || len(page.Items) != 0 {
			t.Fatal("orphan records after failed handoff", table, page, err)
		}
	}
}

func TestManagedRepairReconciliationPreservesHistoryAndNeverQueuesOldCopies(t *testing.T) {
	ctx := context.Background()
	s := repairDiagnosisStore(t)
	f := testRepairFeedback("needs_attention")
	legacy := []Case{}
	for n := 0; n < 3; n++ {
		f.OccurrenceCount++
		o := repairFeedbackObservation(f)
		c := Case{ID: caseID(o), TenantID: o.TenantID, Revision: 1, State: Observed, Observation: o, CreatedAt: s.now(), UpdatedAt: s.now()}
		if err := s.core.WithTx(ctx, func(tx db.Tx) error {
			if err := db.PutRecordTx(ctx, tx, repairFeedbackTable, caseKey(f.TenantID, repairFeedbackID(f)), encode(f)); err != nil {
				return err
			}
			return persistCase(ctx, tx, c, true)
		}); err != nil {
			t.Fatal(err)
		}
		legacy = append(legacy, c)
	}
	f.Summary = "维修进程提前结束，缺少有效最终诊断。"
	canonical, err := s.ObserveManagedRepair(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 6; n++ {
		if _, err := s.ReconcileManagedRepairCases(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, before := range legacy {
		after, err := s.Get(ctx, before.TenantID, before.ID)
		if err != nil || after.State != Superseded || after.SupersededBy != canonical.ID || !reflect.DeepEqual(after.Observation, before.Observation) {
			t.Fatal("legacy evidence not retained or linked", after, err)
		}
		raw, exists, err := s.core.GetRecord(ctx, historyTable, revisionKey(before))
		if err != nil || !exists || !reflect.DeepEqual(raw, encode(before)) {
			t.Fatal("original immutable history changed")
		}
	}
	jobs, err := s.core.ListRecords(ctx, runtimeDiagnosisQueueTable, "", 10)
	if err != nil || len(jobs.Items) != 1 {
		t.Fatal("old copies replayed", jobs, err)
	}
}

func TestExistingIdenticalLegacyRepairReceiptStillEntersDiagnosis(t *testing.T) {
	ctx := context.Background()
	s := repairDiagnosisStore(t)
	f := testRepairFeedback("needs_attention")
	o := repairFeedbackObservation(f)
	legacy := Case{ID: caseID(o), TenantID: o.TenantID, Revision: 1, State: Observed, Observation: o, CreatedAt: s.now(), UpdatedAt: s.now()}
	if err := s.core.WithTx(ctx, func(tx db.Tx) error {
		if err := db.PutRecordTx(ctx, tx, repairFeedbackTable, caseKey(f.TenantID, repairFeedbackID(f)), encode(f)); err != nil {
			return err
		}
		return persistCase(ctx, tx, legacy, true)
	}); err != nil {
		t.Fatal(err)
	}
	c, err := s.ObserveManagedRepair(ctx, f)
	if err != nil || c.ID != legacy.ID || c.State != PendingDiagnosis || c.Revision != 2 {
		t.Fatal("identical legacy receipt skipped handoff", c, err)
	}
	page, err := s.core.ListRecords(ctx, runtimeDiagnosisQueueTable, "", 10)
	if err != nil || len(page.Items) != 1 {
		t.Fatal("legacy handoff missing", page, err)
	}
}

package resourceadmin

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/decision"
	"orrery_v2/internal/platform/db"
)

type diagnosisFunc func(context.Context, RSILearningSample) (RSIDiagnosis, decision.Metadata, error)

func (f diagnosisFunc) Diagnose(ctx context.Context, sample RSILearningSample) (RSIDiagnosis, decision.Metadata, error) {
	return f(ctx, sample)
}

func diagnosisMetadata() decision.Metadata {
	return decision.Metadata{Adapter: "llm", ProfileID: "rsi-fallback", Provider: "test", Model: "test-model", ModelRevision: "test-model", LLMProfileID: "default-model", ConfidenceKind: "self_reported"}
}

func fallbackFixture(t *testing.T, s *PlatformRSILearningSampleStore, name string) (RSILearningObservation, decision.Record) {
	t.Helper()
	scope, _ := RSILearningScope("tenant", "agent")
	o, err := s.Save(context.Background(), RSILearningSample{Scope: scope, EvidenceRef: name, Tool: "normalize", ErrorCode: "contract_mismatch", Summary: "Order alias is rejected", Evidence: json.RawMessage(`{"source":"full original source","private":"PRIVATE_EVIDENCE"}`)})
	if err != nil {
		t.Fatal(err)
	}
	r := rsiAssessment(o, time.Now(), "insufficient", "")
	r.Metadata = decision.Metadata{Adapter: "laya", ProfileID: "decision-laya", Provider: "laya", Model: "multilingual", ConfidenceKind: "model"}
	r.Decisions = []decision.Decision{{Choice: "insufficient", Confidence: .4, AnswerConfidence: .7, Probabilities: map[string]float64{"insufficient": .7, "skill_defect": .1, "tool_usage": .1, "environment": .1}}}
	if err := s.SaveAssessment(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return o, r
}

func TestRSIFallbackReopensWithEvidenceAndDoesNotRepeatModelCall(t *testing.T) {
	ctx := context.Background()
	config := db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "fallback.db")}
	core, err := db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	s := NewPlatformRSILearningSampleStore(core)
	o, r := fallbackFixture(t, s, "project:one")
	calls := 0
	model := diagnosisFunc(func(ctx context.Context, sample RSILearningSample) (RSIDiagnosis, decision.Metadata, error) {
		calls++
		if string(sample.Evidence) != string(o.Sample.Evidence) {
			t.Error("original evidence was lost")
		}
		return RSIDiagnosis{Classification: "skill_defect", Reason: "PRIVATE_MODEL_REASON"}, diagnosisMetadata(), nil
	})
	called, err := s.DiagnoseFailure(ctx, r, model, 20)
	if err != nil || !called {
		t.Fatalf("diagnosis: %v", err)
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	core, err = db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	s = NewPlatformRSILearningSampleStore(core)
	called, err = s.DiagnoseFailure(ctx, r, model, 20)
	if err != nil || called || calls != 1 {
		t.Fatalf("duplicate called=%v calls=%d err=%v", called, calls, err)
	}
	got, err := s.Get(ctx, o.Sample.Scope, o.ID)
	if err != nil || got.Diagnosis.State != "completed" || got.Assessment.Decisions[0].Choice != "insufficient" {
		t.Fatalf("original/diagnosis missing: %+v %v", got, err)
	}
	advice := LearningAdvice(got)
	if advice.AssessmentSource != "llm_fallback" || advice.NextStep != "reproduce_skill" || advice.ConfidenceKind != "self_reported" {
		t.Fatalf("advice: %+v", advice)
	}
	plan, err := s.LearningPlan(ctx, o.Sample.Scope, "", 30)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(plan)
	if strings.Contains(string(raw), "PRIVATE_") || strings.Contains(string(raw), "full original source") {
		t.Fatal("private evidence leaked into plan")
	}
	other, _ := RSILearningScope("other", "agent")
	if _, err := s.Get(ctx, other, o.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-scope read accepted")
	}
	forged := r
	forged.ScopeHash = decision.Hash(other)
	if called, err := s.DiagnoseFailure(ctx, forged, model, 20); !errors.Is(err, ErrNotFound) || called || calls != 1 {
		t.Fatal("cross-scope diagnosis accepted")
	}
}

func TestRSIFallbackConcurrentClaimsAndDurableHourlyBudget(t *testing.T) {
	core, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	s := NewPlatformRSILearningSampleStore(core)
	_, r := fallbackFixture(t, s, "project:concurrent")
	var calls atomic.Int32
	model := diagnosisFunc(func(context.Context, RSILearningSample) (RSIDiagnosis, decision.Metadata, error) {
		calls.Add(1)
		return RSIDiagnosis{Classification: "insufficient", Reason: "Need contract"}, diagnosisMetadata(), nil
	})
	var group sync.WaitGroup
	for range 6 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := s.DiagnoseFailure(context.Background(), r, model, 1); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
	o2, r2 := fallbackFixture(t, s, "project:second")
	if called, err := NewPlatformRSILearningSampleStore(core).DiagnoseFailure(context.Background(), r2, model, 1); err != nil || called {
		t.Fatalf("hourly budget bypassed: %v", err)
	}
	got, _ := s.Get(context.Background(), o2.Sample.Scope, o2.ID)
	if got.Diagnosis.State != "budget_exhausted" || calls.Load() != 1 {
		t.Fatal("budget not recorded")
	}
}

func TestRSIFallbackFailuresStayUncertainAndAreNotRetried(t *testing.T) {
	for _, scenario := range []string{"timeout", "invalid", "unavailable", "insufficient", "unrecorded", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			core, _ := db.Open(context.Background(), db.Config{Driver: db.DriverMemory})
			defer core.Close()
			s := NewPlatformRSILearningSampleStore(core)
			o, r := fallbackFixture(t, s, scenario)
			if scenario == "unrecorded" || scenario == "oversized" {
				sample := o.Sample
				if scenario == "unrecorded" {
					sample.ErrorCode = "tool_outcome_unrecorded"
				} else {
					sample.Evidence, _ = json.Marshal(strings.Repeat("x", MaxRSIDiagnosisBytes))
				}
				o, _ = s.Save(context.Background(), sample)
				r = rsiAssessment(o, time.Now(), "insufficient", "timeout")
				if err := s.SaveAssessment(context.Background(), r); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			model := diagnosisFunc(func(context.Context, RSILearningSample) (RSIDiagnosis, decision.Metadata, error) {
				calls++
				switch scenario {
				case "timeout":
					return RSIDiagnosis{}, decision.Metadata{}, context.DeadlineExceeded
				case "invalid":
					return RSIDiagnosis{Classification: "publish", Reason: "bad"}, diagnosisMetadata(), nil
				case "unavailable":
					return RSIDiagnosis{}, decision.Metadata{}, errors.New("SECRET_PROVIDER_ERROR")
				}
				choice := "insufficient"
				if scenario == "unrecorded" {
					choice = "environment"
				}
				return RSIDiagnosis{Classification: choice, Reason: "More evidence is needed"}, diagnosisMetadata(), nil
			})
			for range 2 {
				if _, err := s.DiagnoseFailure(context.Background(), r, model, 20); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Get(context.Background(), o.Sample.Scope, o.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "oversized" {
				if calls != 0 || got.Diagnosis.State != "evidence_too_large" {
					t.Fatal("oversized evidence was sent/truncated")
				}
			} else if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			advice := LearningAdvice(got)
			want := "collect_evidence"
			if scenario == "unrecorded" {
				want = "reconcile_outcome"
			}
			if advice.NextStep != want {
				t.Fatalf("unsafe advice: %+v", advice)
			}
			raw, _ := json.Marshal(got.Diagnosis)
			if strings.Contains(string(raw), "SECRET_PROVIDER_ERROR") {
				t.Fatal("provider error persisted")
			}
		})
	}
}

func TestRSIFallbackConfidentPrimaryAndConflictingAssessments(t *testing.T) {
	core, _ := db.Open(context.Background(), db.Config{Driver: db.DriverMemory})
	defer core.Close()
	s := NewPlatformRSILearningSampleStore(core)
	o, r := fallbackFixture(t, s, "conflict")
	r.At = r.At.Add(time.Second)
	r.Decisions = []decision.Decision{{Choice: "environment", AnswerConfidence: .95}}
	// A first confident primary assessment must not cause a paid fallback.
	newSample := o.Sample
	newSample.EvidenceRef = "confident"
	confident, _ := s.Save(context.Background(), newSample)
	cr := rsiAssessment(confident, r.At, "environment", "")
	cr.Decisions = r.Decisions
	if err := s.SaveAssessment(context.Background(), cr); err != nil {
		t.Fatal(err)
	}
	calls := 0
	model := diagnosisFunc(func(context.Context, RSILearningSample) (RSIDiagnosis, decision.Metadata, error) {
		calls++
		return RSIDiagnosis{Classification: "tool_usage", Reason: "Wrong argument"}, diagnosisMetadata(), nil
	})
	if called, err := s.DiagnoseFailure(context.Background(), cr, model, 20); err != nil || called || calls != 0 {
		t.Fatal("confident result fell back")
	}
	if err := s.SaveAssessment(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if called, err := s.DiagnoseFailure(context.Background(), r, model, 20); err != nil || !called {
		t.Fatalf("conflict did not fall back: %v", err)
	}
	got, _ := s.Get(context.Background(), o.Sample.Scope, o.ID)
	if got.Diagnosis.Trigger != "conflicting_assessments" || !got.Assessment.Conflicting || LearningAdvice(got).NextStep != "review_arguments" {
		t.Fatalf("conflict missing: %+v", got)
	}
	// A later confident result must not reuse an older contrary LLM diagnosis,
	// including when the host's sticky conflict flag is already set.
	r.At = r.At.Add(time.Second)
	if err := s.SaveAssessment(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(context.Background(), o.Sample.Scope, o.ID)
	if advice := LearningAdvice(got); advice.NextStep != "collect_evidence" || advice.AssessmentState != "uncertain" {
		t.Fatalf("later disagreement silently resolved: %+v", advice)
	}
}

func TestRSIFallbackRejectsCorruptBudgetWithoutCallingModel(t *testing.T) {
	for _, payload := range []db.Record{{}, {"payload": `{"calls":-1}`}, {"payload": "invalid"}} {
		core, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory})
		if err != nil {
			t.Fatal(err)
		}
		s := NewPlatformRSILearningSampleStore(core)
		_, record := fallbackFixture(t, s, "corrupt-budget")
		if err := core.PutRecord(context.Background(), rsiDiagnosisBudgetTable, time.Now().UTC().Format("2006010215"), payload); err != nil {
			t.Fatal(err)
		}
		called := false
		model := diagnosisFunc(func(context.Context, RSILearningSample) (RSIDiagnosis, decision.Metadata, error) {
			called = true
			return RSIDiagnosis{}, decision.Metadata{}, nil
		})
		if _, err := s.DiagnoseFailure(context.Background(), record, model, 20); !errors.Is(err, ErrInvalidRequest) || called {
			t.Fatalf("corrupt budget accepted: %v", err)
		}
		core.Close()
	}
}

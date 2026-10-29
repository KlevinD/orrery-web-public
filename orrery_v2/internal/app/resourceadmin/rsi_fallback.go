package resourceadmin

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"orrery_v2/internal/decision"
	"orrery_v2/internal/platform/db"
)

const rsiDiagnosisTable = "resource_rsi_fallback_diagnoses"
const rsiDiagnosisBudgetTable = "resource_rsi_fallback_budget"
const RSIDiagnosisPromptVersion = "rsi-full-evidence-diagnosis-v1"
const MaxRSIDiagnosisBytes = 128 << 10

type RSIDiagnosis struct {
	Classification string `json:"classification"`
	// Private evidence: never copied into learning-plan or Mission summaries.
	Reason string `json:"reason"`
}

type RSIFailureDiagnoser interface {
	Diagnose(context.Context, RSILearningSample) (RSIDiagnosis, decision.Metadata, error)
}

type RSIFallbackDiagnosis struct {
	ObservationID string            `json:"observation_id"`
	ScopeHash     string            `json:"scope_hash"`
	PromptVersion string            `json:"prompt_version"`
	Primary       decision.Record   `json:"primary"`
	Trigger       string            `json:"trigger"`
	State         string            `json:"state"`
	StartedAt     time.Time         `json:"started_at"`
	CompletedAt   time.Time         `json:"completed_at,omitempty"`
	Result        *RSIDiagnosis     `json:"result,omitempty"`
	Model         decision.Metadata `json:"model"`
	Failure       string            `json:"failure,omitempty"`
}

// Configuration errors and an already-selected LLM never silently select a
// different model. Cancellation is shutdown, not a reason for a paid retry.
func RSIFallbackReason(record decision.Record) string {
	if record.Kind != "rsi_sample" || record.Fallback == "cancelled" || record.Fallback == "configuration_unavailable" || record.Adapter == "llm" {
		return ""
	}
	if record.Adapter != "laya" && record.Adapter != "" && record.Adapter != "unresolved" {
		return ""
	}
	if record.Conflicting {
		return "conflicting_assessments"
	}
	if record.Fallback != "" {
		return record.Fallback
	}
	if record.InputTruncated {
		return "input_truncated"
	}
	q := BuildDecisionRSISampleJob(RSILearningSample{}).Question
	if decision.ValidateDecisions(record.Decisions, 1, q) != nil {
		return "invalid_response"
	}
	if record.Decisions[0].Choice == "insufficient" {
		return "insufficient"
	}
	if record.Decisions[0].AnswerConfidence < .85 {
		return "low_confidence"
	}
	return ""
}

func validRSIDiagnosis(result RSIDiagnosis) bool {
	_, ok := BuildDecisionRSISampleJob(RSILearningSample{}).Question.Criteria[result.Classification]
	return ok && result.Reason != "" && len(result.Reason) <= 2000
}

func validateRSIFallback(observation RSILearningObservation, result RSIFallbackDiagnosis) error {
	if result.ObservationID != observation.ID || result.ScopeHash != decision.Hash(observation.Sample.Scope) || result.PromptVersion != RSIDiagnosisPromptVersion || result.StartedAt.IsZero() || validateRSIAssessment(observation, result.Primary) != nil || result.Trigger == "" || RSIFallbackReason(result.Primary) != result.Trigger {
		return ErrInvalidRequest
	}
	switch result.State {
	case "running":
		if !result.CompletedAt.IsZero() || result.Result != nil || result.Failure != "" {
			return ErrInvalidRequest
		}
	case "completed":
		if result.CompletedAt.IsZero() || result.Result == nil || !validRSIDiagnosis(*result.Result) || result.Model.Adapter != "llm" || result.Model.ModelRevision == "" || result.Model.LLMProfileID == "" || result.Model.CacheKey != "" || result.Failure != "" {
			return ErrInvalidRequest
		}
	case "failed", "budget_exhausted", "evidence_too_large":
		if result.CompletedAt.IsZero() || result.Result != nil || result.Failure == "" {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}

func fallbackRecord(value RSIFallbackDiagnosis) db.Record {
	raw, _ := json.Marshal(value)
	return db.Record{"payload": string(raw)}
}

// DiagnoseFailure reserves one durable attempt before issuing a model call.
// Unknown outcomes remain running; restarts never issue the same paid call.
// The database budget also survives restarts and serializes concurrent workers.
func (s *PlatformRSILearningSampleStore) DiagnoseFailure(ctx context.Context, record decision.Record, model RSIFailureDiagnoser, maxCallsPerHour int) (bool, error) {
	if s == nil || s.store == nil || model == nil {
		return false, ErrServiceDependency
	}
	if !isRSIHash(record.ObservationID) || !isRSIHash(record.ScopeHash) || maxCallsPerHour < 1 || maxCallsPerHour > 100 {
		return false, ErrInvalidRequest
	}
	key := record.ScopeHash + "/" + record.ObservationID
	saved, exists, err := s.store.GetRecord(ctx, rsiSampleTable, key)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, ErrNotFound
	}
	observation, err := decodeRSIObservation(saved)
	if err != nil || decision.Hash(observation.Sample.Scope) != record.ScopeHash || validateRSIAssessment(observation, record) != nil {
		return false, ErrInvalidRequest
	}
	observation, err = s.Get(ctx, observation.Sample.Scope, observation.ID)
	if err != nil {
		return false, err
	}
	if observation.Assessment == nil {
		return false, ErrInvalidRequest
	}
	record = *observation.Assessment
	trigger := RSIFallbackReason(record)
	if trigger == "" {
		return false, nil
	}
	input, err := json.Marshal(observation.Sample)
	if err != nil {
		return false, ErrInvalidRequest
	}
	now := time.Now().UTC()
	attempt := RSIFallbackDiagnosis{ObservationID: observation.ID, ScopeHash: record.ScopeHash, PromptVersion: RSIDiagnosisPromptVersion, Primary: record, Trigger: trigger, State: "running", StartedAt: now}
	claimed := false
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, rsiDiagnosisTable, key); err != nil {
			return err
		}
		if _, exists, err := db.GetRecordTx(ctx, tx, rsiDiagnosisTable, key); err != nil || exists {
			return err
		}
		if len(input) > MaxRSIDiagnosisBytes {
			attempt.State, attempt.Failure, attempt.CompletedAt = "evidence_too_large", "evidence_budget_exceeded", now
		} else {
			budgetKey := now.Format("2006010215")
			if err := db.LockRecordKeyTx(ctx, tx, rsiDiagnosisBudgetTable, budgetKey); err != nil {
				return err
			}
			budget, exists, err := db.GetRecordTx(ctx, tx, rsiDiagnosisBudgetTable, budgetKey)
			if err != nil {
				return err
			}
			var usage struct {
				Calls int `json:"calls"`
			}
			if exists {
				raw, ok := budget["payload"].(string)
				if !ok || json.Unmarshal([]byte(raw), &usage) != nil || usage.Calls < 0 {
					return ErrInvalidRequest
				}
			}
			if usage.Calls >= maxCallsPerHour {
				attempt.State, attempt.Failure, attempt.CompletedAt = "budget_exhausted", "hourly_budget_exhausted", now
			} else {
				usage.Calls++
				raw, _ := json.Marshal(usage)
				if err := db.PutRecordTx(ctx, tx, rsiDiagnosisBudgetTable, budgetKey, db.Record{"payload": string(raw)}); err != nil {
					return err
				}
			}
		}
		claimed = true
		return db.PutRecordTx(ctx, tx, rsiDiagnosisTable, key, fallbackRecord(attempt))
	})
	if err != nil || !claimed || attempt.State != "running" {
		return false, err
	}
	original := fallbackRecord(attempt)
	result, metadata, inferenceErr := model.Diagnose(ctx, observation.Sample)
	if inferenceErr == nil && ctx.Err() != nil {
		inferenceErr = ctx.Err()
	}
	if inferenceErr == nil && (!validRSIDiagnosis(result) || metadata.Adapter != "llm" || metadata.ModelRevision == "" || metadata.LLMProfileID == "") {
		inferenceErr = decision.ErrResponse
	}
	attempt.CompletedAt = time.Now().UTC()
	if inferenceErr != nil {
		attempt.State, attempt.Failure = "failed", "analysis_unavailable"
		switch {
		case errors.Is(inferenceErr, context.DeadlineExceeded):
			attempt.Failure = "timeout"
		case errors.Is(inferenceErr, context.Canceled):
			attempt.Failure = "cancelled"
		case errors.Is(inferenceErr, decision.ErrResponse):
			attempt.Failure = "invalid_response"
		case errors.Is(inferenceErr, decision.ErrConfiguration):
			attempt.Failure = "configuration_unavailable"
		}
	} else {
		metadata.CacheKey = ""
		attempt.State, attempt.Result, attempt.Model = "completed", &result, metadata
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	changed, err := s.store.CompareAndSwapRecord(writeCtx, rsiDiagnosisTable, key, original, fallbackRecord(attempt))
	if err == nil && !changed {
		err = ErrConflict
	}
	return true, err
}

// One worker and a bounded queue keep inference off the tool execution path.
type RSIFallbackWorker struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	queue  chan decision.Record
	mu     sync.Mutex
	closed bool
}

func NewRSIFallbackWorker(ctx context.Context, store *PlatformRSILearningSampleStore, model RSIFailureDiagnoser, maxCallsPerHour int, failure func(string)) *RSIFallbackWorker {
	ctx, cancel := context.WithCancel(ctx)
	w := &RSIFallbackWorker{ctx: ctx, cancel: cancel, done: make(chan struct{}), queue: make(chan decision.Record, 8)}
	go func() {
		defer close(w.done)
		for {
			select {
			case <-ctx.Done():
				return
			case record := <-w.queue:
				if ctx.Err() != nil {
					return
				}
				callCtx, done := context.WithTimeout(ctx, 45*time.Second)
				_, err := store.DiagnoseFailure(callCtx, record, model, maxCallsPerHour)
				done()
				if err != nil && failure != nil {
					failure(record.ObservationID)
				}
			}
		}
	}()
	return w
}

func (w *RSIFallbackWorker) Submit(record decision.Record) bool {
	// Also reread confident Laya records: the store can have marked a conflict
	// while persisting this callback. Only that stored record can reserve a call.
	if w == nil || record.Kind != "rsi_sample" || record.Adapter == "llm" {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.ctx.Err() != nil {
		return false
	}
	select {
	case w.queue <- record:
		return true
	default:
		return false
	}
}

func (w *RSIFallbackWorker) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.closed = true
	w.cancel()
	w.mu.Unlock()
	<-w.done
}

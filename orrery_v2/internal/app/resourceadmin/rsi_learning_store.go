package resourceadmin

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"orrery_v2/internal/decision"
	"orrery_v2/internal/platform/db"
)

const (
	rsiSampleTable     = "resource_rsi_learning_samples"
	rsiAssessmentTable = "resource_rsi_learning_assessments"
)

// RSILearningObservation is evidence for a future learner, not a promotion
// evaluation. Neither a label nor its confidence grants publication eligibility.
type RSILearningObservation struct {
	ID         string                `json:"id"`
	At         time.Time             `json:"at"`
	Sample     RSILearningSample     `json:"sample"`
	Assessment *decision.Record      `json:"assessment,omitempty"`
	Diagnosis  *RSIFallbackDiagnosis `json:"fallback_diagnosis,omitempty"`
}

type RSILearningSamplePage struct {
	Items      []RSILearningObservation `json:"items"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

type PlatformRSILearningSampleStore struct{ store *db.Store }

func NewPlatformRSILearningSampleStore(store *db.Store) *PlatformRSILearningSampleStore {
	return &PlatformRSILearningSampleStore{store: store}
}

func RSILearningScope(tenant, agent string) (string, error) {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(agent) == "" || len(tenant) > 128 || len(agent) > 128 {
		return "", fmt.Errorf("%w: RSI tenant and agent are required", ErrInvalidRequest)
	}
	raw, _ := json.Marshal([]string{tenant, agent})
	return string(raw), nil
}

func validateRSIScope(scope string) error {
	var parts []string
	if json.Unmarshal([]byte(scope), &parts) != nil || len(parts) != 2 {
		return fmt.Errorf("%w: invalid RSI scope", ErrInvalidRequest)
	}
	canonical, err := RSILearningScope(parts[0], parts[1])
	if err != nil || canonical != scope {
		return fmt.Errorf("%w: invalid RSI scope", ErrInvalidRequest)
	}
	return nil
}

func isRSIHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

func (s *PlatformRSILearningSampleStore) Save(ctx context.Context, sample RSILearningSample) (RSILearningObservation, error) {
	if s == nil || s.store == nil {
		return RSILearningObservation{}, ErrServiceDependency
	}
	if err := validateRSIScope(sample.Scope); err != nil {
		return RSILearningObservation{}, err
	}
	if sample.EvidenceRef == "" || len(sample.EvidenceRef) > 512 || sample.Tool == "" {
		return RSILearningObservation{}, fmt.Errorf("%w: RSI evidence reference and tool are required", ErrInvalidRequest)
	}
	raw, err := json.Marshal(sample)
	if err != nil {
		return RSILearningObservation{}, fmt.Errorf("%w: RSI evidence cannot be encoded", ErrInvalidRequest)
	}
	observation := RSILearningObservation{ID: decision.Hash(string(raw)), At: time.Now().UTC(), Sample: sample}
	payload, _ := json.Marshal(observation)
	key := decision.Hash(sample.Scope) + "/" + observation.ID
	created, err := s.store.CreateRecord(ctx, rsiSampleTable, key, db.Record{"payload": string(payload)})
	if err != nil {
		return RSILearningObservation{}, err
	}
	if !created {
		existing, err := s.Get(ctx, sample.Scope, observation.ID)
		if err != nil {
			return RSILearningObservation{}, err
		}
		if err := s.indexRun(ctx, existing); err != nil {
			return existing, err
		}
		return existing, nil
	}
	if err := s.indexRun(ctx, observation); err != nil {
		return observation, err
	}
	// Decode a private copy so caller mutations cannot change the saved evidence.
	return decodeRSIObservation(db.Record{"payload": string(payload)})
}

func decodeRSIObservation(record db.Record) (RSILearningObservation, error) {
	var observation RSILearningObservation
	raw, ok := record["payload"].(string)
	if !ok || json.Unmarshal([]byte(raw), &observation) != nil {
		return observation, fmt.Errorf("%w: invalid RSI observation", ErrInvalidRequest)
	}
	encoded, err := json.Marshal(observation.Sample)
	if err != nil || observation.ID != decision.Hash(string(encoded)) || observation.At.IsZero() || observation.Assessment != nil || observation.Diagnosis != nil {
		return observation, fmt.Errorf("%w: RSI observation integrity check failed", ErrInvalidRequest)
	}
	if err := validateRSIScope(observation.Sample.Scope); err != nil {
		return observation, err
	}
	return observation, nil
}

func (s *PlatformRSILearningSampleStore) Get(ctx context.Context, scope, id string) (RSILearningObservation, error) {
	if s == nil || s.store == nil {
		return RSILearningObservation{}, ErrServiceDependency
	}
	if err := validateRSIScope(scope); err != nil {
		return RSILearningObservation{}, err
	}
	if !isRSIHash(id) {
		return RSILearningObservation{}, fmt.Errorf("%w: invalid RSI observation id", ErrInvalidRequest)
	}
	key := decision.Hash(scope) + "/" + id
	record, ok, err := s.store.GetRecord(ctx, rsiSampleTable, key)
	if err != nil {
		return RSILearningObservation{}, err
	}
	if !ok {
		return RSILearningObservation{}, ErrNotFound
	}
	observation, err := decodeRSIObservation(record)
	if err != nil {
		return observation, err
	}
	if observation.ID != id || observation.Sample.Scope != scope {
		return observation, ErrConflict
	}
	assessment, exists, err := s.store.GetRecord(ctx, rsiAssessmentTable, key)
	if err != nil {
		return observation, err
	}
	if exists {
		var result decision.Record
		raw, ok := assessment["payload"].(string)
		if !ok || json.Unmarshal([]byte(raw), &result) != nil || validateRSIAssessment(observation, result) != nil {
			return observation, fmt.Errorf("%w: invalid stored RSI assessment", ErrInvalidRequest)
		}
		observation.Assessment = &result
	}
	if saved, exists, err := s.store.GetRecord(ctx, rsiDiagnosisTable, key); err != nil {
		return observation, err
	} else if exists {
		var result RSIFallbackDiagnosis
		raw, ok := saved["payload"].(string)
		if !ok || json.Unmarshal([]byte(raw), &result) != nil || validateRSIFallback(observation, result) != nil {
			return observation, ErrInvalidRequest
		}
		observation.Diagnosis = &result
	}
	return observation, nil
}

func validateRSIAssessment(observation RSILearningObservation, record decision.Record) error {
	job := BuildDecisionRSISampleJob(observation.Sample)
	if record.ObservationID != observation.ID || record.InputTruncated != job.InputTruncated || record.Kind != "rsi_sample" || record.Mode != "shadow" || record.ScopeHash != decision.Hash(job.Scope) || record.ModelRevision == "" || record.PromptVersion != job.PromptVersion || record.At.IsZero() || !reflect.DeepEqual(record.Sources, job.Sources) || !reflect.DeepEqual(record.InputHashes, job.InputHashes) {
		return fmt.Errorf("%w: RSI assessment provenance mismatch", ErrInvalidRequest)
	}
	if record.Fallback == "" {
		if len(record.Decisions) != 1 {
			return fmt.Errorf("%w: missing RSI classification", ErrInvalidRequest)
		}
		if _, ok := job.Question.Criteria[record.Decisions[0].Choice]; !ok {
			return fmt.Errorf("%w: unknown RSI classification", ErrInvalidRequest)
		}
	} else if len(record.Decisions) != 0 {
		return fmt.Errorf("%w: fallback cannot contain RSI decisions", ErrInvalidRequest)
	}
	return nil
}

// SaveAssessment keeps the latest attempt with optimistic concurrency. An old
// callback cannot overwrite a newer result; conflicting simultaneous attempts
// are reported rather than silently dropping a write.
func (s *PlatformRSILearningSampleStore) SaveAssessment(ctx context.Context, record decision.Record) error {
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	if !isRSIHash(record.ScopeHash) || !isRSIHash(record.ObservationID) {
		return ErrInvalidRequest
	}
	key := record.ScopeHash + "/" + record.ObservationID
	saved, ok, err := s.store.GetRecord(ctx, rsiSampleTable, key)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	observation, err := decodeRSIObservation(saved)
	if err != nil {
		return err
	}
	if err := validateRSIAssessment(observation, record); err != nil {
		return err
	}
	if record.Adapter == "" && record.ModelRevision != "convaiinnovations/laya-multilingual@1720e3e3357cfe1e281542e223f8273b0890ca34" {
		return fmt.Errorf("%w: unexpected RSI model revision", ErrInvalidRequest)
	}
	if record.Adapter != "" {
		if record.Adapter == "unresolved" {
			if record.ModelRevision != "unresolved" || record.Fallback == "" {
				return ErrInvalidRequest
			}
		} else if record.ProfileID == "" || record.Provider == "" || record.Model == "" || len(record.ModelRevision) > 256 || len(record.ProfileID) > 128 {
			return ErrInvalidRequest
		}
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("%w: invalid RSI assessment encoding", ErrInvalidRequest)
	}
	replacement := db.Record{"payload": string(payload)}
	for attempt := 0; attempt < 4; attempt++ {
		old, exists, err := s.store.GetRecord(ctx, rsiAssessmentTable, key)
		if err != nil {
			return err
		}
		if !exists {
			created, err := s.store.CreateRecord(ctx, rsiAssessmentTable, key, replacement)
			if err != nil {
				return err
			}
			if created {
				return nil
			}
			continue
		}
		var previous decision.Record
		raw, ok := old["payload"].(string)
		if !ok || json.Unmarshal([]byte(raw), &previous) != nil || validateRSIAssessment(observation, previous) != nil {
			return ErrInvalidRequest
		}
		if record.At.Before(previous.At) {
			return nil
		}
		if record.At.Equal(previous.At) {
			// Conflicting is host-derived; an idempotent provider callback need
			// not include the flag added while the original callback was saved.
			record.Conflicting = previous.Conflicting
			payload, err = json.Marshal(record)
			if err != nil {
				return ErrInvalidRequest
			}
			if raw == string(payload) {
				return nil
			}
			return ErrConflict
		}
		record.Conflicting = record.Conflicting || previous.Conflicting ||
			(record.Fallback == "" && previous.Fallback == "" && len(record.Decisions) == 1 && len(previous.Decisions) == 1 && record.Decisions[0].Choice != previous.Decisions[0].Choice)
		payload, err = json.Marshal(record)
		if err != nil {
			return ErrInvalidRequest
		}
		replacement = db.Record{"payload": string(payload)}
		changed, err := s.store.CompareAndSwapRecord(ctx, rsiAssessmentTable, key, old, replacement)
		if err != nil {
			return err
		}
		if changed {
			return nil
		}
	}
	return ErrConflict
}

// List is for trusted internal learners and host administration. Scope is
// mandatory; exposing it over HTTP would additionally require authorization.
func (s *PlatformRSILearningSampleStore) List(ctx context.Context, scope, after string, limit int) (RSILearningSamplePage, error) {
	out := RSILearningSamplePage{Items: []RSILearningObservation{}}
	if s == nil || s.store == nil {
		return out, ErrServiceDependency
	}
	if err := validateRSIScope(scope); err != nil {
		return out, err
	}
	if limit < 1 || limit > 100 || (after != "" && !isRSIHash(after)) {
		return out, ErrInvalidRequest
	}
	prefix := decision.Hash(scope) + "/"
	page, err := s.store.ListRecords(ctx, rsiSampleTable, prefix+after, limit+1)
	if err != nil {
		return out, err
	}
	for _, item := range page.Items {
		if !strings.HasPrefix(item.Key, prefix) {
			break
		}
		if len(out.Items) == limit {
			out.NextCursor = out.Items[len(out.Items)-1].ID
			break
		}
		observation, err := s.Get(ctx, scope, strings.TrimPrefix(item.Key, prefix))
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, observation)
	}
	return out, nil
}

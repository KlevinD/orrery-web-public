package improvement

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/storage"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/db"
)

func (s *RuntimeEvidenceStore) captureBusinessBinding(ctx context.Context, request RuntimeRequestEvidence, task agentcore.TaskInput) error {
	b, err := buildBusinessBinding(request, task)
	if err != nil {
		return err
	}
	return s.store.core.WithTx(ctx, func(tx db.Tx) error {
		key := runtimeEvidenceKey(b.TenantID, b.AttemptID, "business-binding")
		raw, exists, err := db.GetRecordTx(ctx, tx, runtimeEvidenceTable, runtimeEvidenceKey(b.TenantID, b.AttemptID, "request"))
		if err != nil {
			return err
		}
		captured, decodeErr := decode[RuntimeRequestEvidence](raw)
		if !exists || decodeErr != nil || !reflect.DeepEqual(captured, request) {
			return ErrInvalid
		}
		prior, bound, err := db.GetRecordTx(ctx, tx, businessBindingTable, key)
		if err != nil {
			return err
		}
		if bound {
			old, err := decode[BusinessBinding](prior)
			if err != nil || validateBusinessBinding(request, old) != nil {
				return ErrInvalid
			}
			if old.InputTaskSHA256 != b.InputTaskSHA256 || old.SourceKey != b.SourceKey {
				return ErrConflict
			}
			return nil
		}
		// Read the host-owned formal contract before model dispatch. No raw
		// contract text is copied into the RSI evidence ledger.
		rawTask, formal, err := db.GetRecordTx(ctx, tx, storage.TaskRecords, task.ID)
		if err != nil {
			return err
		}
		if formal {
			payload, ok := rawTask["payload"].(string)
			var record missiontask.TaskRecord
			if !ok || json.Unmarshal([]byte(payload), &record) != nil || record.Contract.ID.String() != task.ID || record.Contract.MissionID.String() != task.MissionID {
				return ErrInvalid
			}
			envelope := record.Contract.ExecutionEnvelope
			if envelope != nil && envelope.Verify() == nil && envelope.TenantID == request.ExecutionTenantID {
				b.ContractSHA256, b.ContractVersion = Digest(record.Contract), record.Version
			}
		}
		b.ID = ""
		b.ID = "runtime-business-binding:" + Digest(b)
		created, err := db.InsertRecordTx(ctx, tx, businessBindingTable, key, encode(b))
		if err != nil {
			return err
		}
		if !created {
			old, _, err := db.GetRecordTx(ctx, tx, businessBindingTable, key)
			if err != nil {
				return err
			}
			if old["sha256"] != Digest(b) {
				return ErrConflict
			}
			return nil
		}
		return db.PutRecordTx(ctx, tx, businessPendingTable, key, encode(b))
	})
}

func (s *RuntimeEvidenceStore) GetBusinessBinding(ctx context.Context, tenant, attempt string) (BusinessBinding, error) {
	request, err := s.GetRequest(ctx, tenant, attempt)
	if err != nil {
		return BusinessBinding{}, err
	}
	raw, exists, err := s.store.core.GetRecord(ctx, businessBindingTable, runtimeEvidenceKey(tenant, attempt, "business-binding"))
	if err != nil {
		return BusinessBinding{}, err
	}
	if !exists {
		return BusinessBinding{}, ErrNotFound
	}
	b, err := decode[BusinessBinding](raw)
	if err == nil {
		err = validateBusinessBinding(request, b)
	}
	return b, err
}

func buildBusinessAssessment(request RuntimeRequestEvidence, b BusinessBinding, c Case, review RuntimeReview, o TaskOutcome) (BusinessAssessment, error) {
	copy := o
	copy.ID = ""
	if validateBusinessBinding(request, b) != nil || o.ID != "task-outcome:"+Digest(copy) || o.SourceKey != b.SourceKey || o.MissionID != b.MissionID || o.TaskID != b.TaskID ||
		o.RunID != request.RunID || o.AgentID != request.AgentID || o.TraceRef != request.TraceRef || o.RuntimeAttemptID != request.AttemptID || o.ExecutionTenantID != request.ExecutionTenantID ||
		!textOK(o.SubmissionID, 256) || !textOK(o.ResultID, 256) || o.TaskVersion < 1 || !hashOK(o.ContractSHA256) || !hashOK(o.SourceSHA256) || !hashOK(o.SubmissionBindingsSHA256) || o.OccurredAt.Before(request.CapturedAt) || o.Deliverables < 0 || o.Evidence < 0 ||
		c.TenantID != request.Scope.TenantID || c.Observation.Run != request.Scope.Run || review.RequestID != request.ID || review.TerminalID == "" {
		return BusinessAssessment{}, ErrInvalid
	}
	a := BusinessAssessment{Protocol: "orrery-task-business-assessment-v1", TenantID: b.TenantID, AttemptID: b.AttemptID, CaseID: c.ID, RuntimeReviewID: review.ID, BindingID: b.ID, Outcome: o,
		BusinessStatus: "submission_recorded", CausalAttribution: "unresolved", Blockers: []string{"causal_attribution_unresolved", "artifact_bytes_unverified", "channel_delivery_unassessed", "baseline_incomplete"}, AssessedAt: o.OccurredAt}
	if b.ContractSHA256 == "" {
		a.Blockers = append(a.Blockers, "execution_contract_unverified")
	} else if b.ContractSHA256 != o.ContractSHA256 || o.TaskVersion < b.ContractVersion {
		a.Blockers = append(a.Blockers, "execution_contract_changed")
	}
	switch o.Stage {
	case "submission":
		if o.Blocked {
			a.BusinessStatus = "blocked"
		} else if o.QARequired {
			a.BusinessStatus = "qa_pending"
		} else {
			a.BusinessStatus = "acceptance_pending"
		}
	case "qa_not_required":
		if o.QARequired || o.QAStatus != "not_required" || o.Status != "approved" || o.Blocked {
			return a, ErrInvalid
		}
		a.BusinessStatus = "accepted_without_qa"
	case "qa":
		if !o.QARequired || !textOK(o.QAResultID, 256) || o.Blocked {
			return a, ErrInvalid
		}
		switch o.QAStatus {
		case "approved":
			if !textOK(o.AcceptanceReportID, 256) || o.Status != "approved" {
				return a, ErrInvalid
			}
			a.BusinessStatus = "qa_approved"
		case "rejected", "needs_rework":
			if o.Status != "rework_requested" && o.Status != "rejected" {
				return a, ErrInvalid
			}
			a.BusinessStatus = "qa_rejected"
		default:
			return a, ErrInvalid
		}
	default:
		return a, ErrInvalid
	}
	// A recorded domain acceptance and a failed pipeline are different facts.
	// Preserve the disagreement instead of silently declaring success.
	if review.PipelineStatus != "completed" {
		a.Blockers = append(a.Blockers, "runtime_pipeline_"+review.PipelineStatus)
	}
	a.ID = "task-business-assessment:" + Digest(a)
	return a, nil
}

// BusinessAssessments is read-only and tenant/attempt bound. Each returned row
// is recomputed against the original immutable request and domain receipt.
func (s *RuntimeEvidenceStore) BusinessAssessments(ctx context.Context, tenant, attempt string) ([]BusinessAssessment, error) {
	b, err := s.GetBusinessBinding(ctx, tenant, attempt)
	if err != nil {
		return nil, err
	}
	request, err := s.GetRequest(ctx, tenant, attempt)
	if err != nil {
		return nil, err
	}
	end, err := s.GetTerminal(ctx, tenant, attempt)
	if err != nil {
		return nil, err
	}
	obs, err := capturedRuntimeObservation(request, end)
	if err != nil {
		return nil, err
	}
	c, err := s.store.Get(ctx, tenant, caseID(obs))
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(c.Observation, obs) {
		return nil, ErrInvalid
	}
	review, _, err := s.GetReview(ctx, tenant, attempt)
	if err != nil {
		return nil, err
	}
	page, err := s.store.core.ListRecords(ctx, taskOutcomeTable, b.SourceKey+"/", 129)
	if err != nil {
		return nil, err
	}
	result := []BusinessAssessment{}
	for _, entry := range page.Items {
		if !strings.HasPrefix(entry.Key, b.SourceKey+"/") {
			break
		}
		if len(result) >= 128 {
			return nil, ErrInvalid
		}
		o, err := decode[TaskOutcome](entry.Record)
		if err != nil {
			return nil, err
		}
		a, err := buildBusinessAssessment(request, b, c, review, o)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AssessedAt.Equal(result[j].AssessedAt) {
			if (result[i].Outcome.Stage == "submission") != (result[j].Outcome.Stage == "submission") {
				return result[i].Outcome.Stage == "submission"
			}
			return result[i].ID < result[j].ID
		}
		return result[i].AssessedAt.Before(result[j].AssessedAt)
	})
	return result, nil
}

type BusinessProjectionSink interface {
	ProjectBusinessAssessment(context.Context, BusinessAssessment) error
}

// ReconcileBusiness uses a durable rotating cursor. Awaiting QA or missing
// submissions cannot indefinitely starve later attempts. It never calls models
// or reexecutes work, and old request/review/case bytes remain unchanged.
func (s *RuntimeEvidenceStore) ReconcileBusiness(ctx context.Context, limit int) (int, error) {
	if s == nil || s.store.ready() != nil {
		return 0, ErrUnavailable
	}
	if limit < 1 || limit > 1000 {
		return 0, ErrInvalid
	}
	cursor := ""
	raw, exists, err := s.store.core.GetRecord(ctx, businessCursorTable, "reconcile")
	if err != nil {
		return 0, err
	}
	if exists {
		cursor, _ = raw["after"].(string)
	}
	page, err := s.store.core.ListRecords(ctx, businessPendingTable, cursor, limit)
	if err != nil {
		return 0, err
	}
	if len(page.Items) == 0 && cursor != "" {
		page, err = s.store.core.ListRecords(ctx, businessPendingTable, "", limit)
		if err != nil {
			return 0, err
		}
	}
	count := 0
	var failures []error
	for _, entry := range page.Items {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		b, err := decode[BusinessBinding](entry.Record)
		var assessments []BusinessAssessment
		if err == nil {
			assessments, err = s.BusinessAssessments(ctx, b.TenantID, b.AttemptID)
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			failures = append(failures, err)
		}
		if err == nil {
			err = s.store.core.WithTx(ctx, func(tx db.Tx) error {
				final := false
				for _, a := range assessments {
					if err := enqueueTaskArtifactVerification(ctx, tx, a); err != nil {
						return err
					}
					created, err := db.InsertRecordTx(ctx, tx, businessAssessmentTable, a.ID, encode(a))
					if err != nil {
						return err
					}
					if !created {
						old, _, err := db.GetRecordTx(ctx, tx, businessAssessmentTable, a.ID)
						if err != nil {
							return err
						}
						if old["sha256"] != Digest(a) {
							return ErrConflict
						}
					} else if err := db.PutRecordTx(ctx, tx, businessOutboxTable, a.ID, encode(a)); err != nil {
						return err
					}
					final = final || a.BusinessStatus == "blocked" || a.Outcome.Stage != "submission"
				}
				if final {
					return db.DeleteRecordTx(ctx, tx, businessPendingTable, entry.Key)
				}
				return nil
			})
			if err != nil {
				failures = append(failures, err)
			} else {
				count += len(assessments)
			}
		}
		cursor = entry.Key
	}
	if err := s.store.core.PutRecord(ctx, businessCursorTable, "reconcile", db.Record{"after": cursor}); err != nil {
		failures = append(failures, err)
	}
	return count, errors.Join(failures...)
}

func (s *RuntimeEvidenceStore) ProjectBusinessPending(ctx context.Context, sink BusinessProjectionSink, limit int) (int, error) {
	if s == nil || s.store.ready() != nil || sink == nil {
		return 0, ErrUnavailable
	}
	if limit < 1 || limit > 1000 {
		return 0, ErrInvalid
	}
	page, err := s.store.core.ListRecords(ctx, businessOutboxTable, "", limit)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range page.Items {
		a, err := decode[BusinessAssessment](entry.Record)
		if err != nil {
			return count, err
		}
		current, err := s.BusinessAssessments(ctx, a.TenantID, a.AttemptID)
		if err != nil {
			return count, err
		}
		valid := false
		for _, candidate := range current {
			valid = valid || reflect.DeepEqual(candidate, a)
		}
		if !valid {
			return count, ErrInvalid
		}
		if err := sink.ProjectBusinessAssessment(ctx, a); err != nil {
			return count, err
		}
		deleted, err := s.store.core.CompareAndDeleteRecord(ctx, businessOutboxTable, entry.Key, entry.Record)
		if err != nil {
			return count, err
		}
		if deleted {
			count++
		}
	}
	return count, nil
}

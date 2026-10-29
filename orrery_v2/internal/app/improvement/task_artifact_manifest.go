package improvement

import (
	"context"
	"encoding/json"
	"reflect"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/storage"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/db"
)

const (
	taskArtifactManifestTable = "ontology_improvement_task_artifact_manifests"
	taskArtifactPendingTable  = "ontology_improvement_task_artifact_pending"
	taskArtifactReceiptTable  = "ontology_improvement_task_artifact_verifications"
	taskArtifactOutboxTable   = "ontology_improvement_task_artifact_outbox"
	taskArtifactCursorTable   = "ontology_improvement_task_artifact_cursor"
	taskArtifactJobTable      = "ontology_improvement_task_artifact_jobs"
	maxTaskArtifactItems      = 128
)

type TaskArtifactItem struct {
	Kind            string `json:"kind"`
	RecordID        string `json:"record_id"`
	ItemID          string `json:"item_id"`
	RecordSHA256    string `json:"record_sha256"`
	ReferenceSHA256 string `json:"reference_sha256"`
}

// TaskArtifactManifest captures exact domain row identities in the submission
// transaction. It retains no titles, URIs, artifact bodies or user reports.
type TaskArtifactManifest struct {
	ID                string             `json:"id"`
	SourceKey         string             `json:"source_key"`
	ExecutionTenantID string             `json:"execution_tenant_id"`
	SubmissionID      string             `json:"submission_id"`
	SubmissionSHA256  string             `json:"submission_sha256"`
	Items             []TaskArtifactItem `json:"items"`
}

type taskArtifactPending struct {
	ManifestID   string `json:"manifest_id"`
	TenantID     string `json:"tenant_id"`
	AttemptID    string `json:"attempt_id"`
	SubmissionID string `json:"submission_id"`
	Attempts     int    `json:"attempts"`
	NextAt       int64  `json:"next_at"`
}

func taskArtifactManifestKey(source, submission string) string {
	return source + "/" + Digest(submission)
}

func domainPayload[T any](raw db.Record) (T, error) {
	var value T
	payload, ok := raw["payload"].(string)
	if !ok || json.Unmarshal([]byte(payload), &value) != nil {
		return value, ErrInvalid
	}
	return value, nil
}

func captureTaskArtifactManifest(ctx context.Context, tx db.Tx, o TaskOutcome, sub missiontask.TaskSubmission) error {
	if o.ExecutionTenantID == "" {
		return nil
	}
	// Old imported records have no exact child-row addresses. Do not scan the
	// task tables or invent a historical manifest for them.
	if len(sub.DeliverableRecordRefs) == 0 && len(sub.EvidenceRecordRefs) == 0 && (len(sub.DeliverableRefs) > 0 || len(sub.EvidenceRefs) > 0) {
		return nil
	}
	if len(sub.DeliverableRecordRefs) != len(sub.DeliverableRefs) || len(sub.EvidenceRecordRefs) != len(sub.EvidenceRefs) {
		return ErrInvalid
	}
	if len(sub.DeliverableRefs)+len(sub.EvidenceRefs) > maxTaskArtifactItems {
		return nil
	}
	m := TaskArtifactManifest{SourceKey: o.SourceKey, ExecutionTenantID: o.ExecutionTenantID, SubmissionID: o.SubmissionID, SubmissionSHA256: Digest(sub), Items: []TaskArtifactItem{}}
	seen := map[string]bool{}
	for i, id := range sub.DeliverableRecordRefs {
		raw, exists, err := db.GetRecordTx(ctx, tx, storage.TaskDeliverableSubmissions, string(id))
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		d, err := domainPayload[missiontask.DeliverableSubmission](raw)
		if err != nil || d.ID != id || d.SubmissionID != sub.ID || d.TaskID != sub.TaskID || d.AgentID != sub.AgentID || !d.SubmittedAt.Equal(sub.SubmittedAt) || d.Deliverable.ID != sub.DeliverableRefs[i] {
			return ErrInvalid
		}
		item := TaskArtifactItem{Kind: "deliverable", RecordID: string(id), ItemID: string(d.Deliverable.ID), RecordSHA256: Digest(d), ReferenceSHA256: Digest(d.Deliverable.ArtifactRef)}
		if seen[item.Kind+"/"+item.ItemID] || seen["row/"+item.RecordID] {
			return ErrInvalid
		}
		seen[item.Kind+"/"+item.ItemID] = true
		seen["row/"+item.RecordID] = true
		m.Items = append(m.Items, item)
	}
	for i, id := range sub.EvidenceRecordRefs {
		raw, exists, err := db.GetRecordTx(ctx, tx, storage.TaskEvidenceSubmissions, string(id))
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		e, err := domainPayload[missiontask.EvidenceSubmission](raw)
		if err != nil || e.ID != id || e.SubmissionID != sub.ID || e.TaskID != sub.TaskID || e.AgentID != sub.AgentID || !e.SubmittedAt.Equal(sub.SubmittedAt) || e.Evidence.ID != sub.EvidenceRefs[i] {
			return ErrInvalid
		}
		item := TaskArtifactItem{Kind: "evidence", RecordID: string(id), ItemID: string(e.Evidence.ID), RecordSHA256: Digest(e), ReferenceSHA256: Digest(e.Evidence.ArtifactRef)}
		if seen[item.Kind+"/"+item.ItemID] || seen["row/"+item.RecordID] {
			return ErrInvalid
		}
		seen[item.Kind+"/"+item.ItemID] = true
		seen["row/"+item.RecordID] = true
		m.Items = append(m.Items, item)
	}
	m.ID = "task-artifact-manifest:" + Digest(m)
	key := taskArtifactManifestKey(o.SourceKey, o.SubmissionID)
	created, err := db.InsertRecordTx(ctx, tx, taskArtifactManifestTable, key, encode(m))
	if err != nil {
		return err
	}
	if !created {
		raw, _, err := db.GetRecordTx(ctx, tx, taskArtifactManifestTable, key)
		if err != nil {
			return err
		}
		if raw["sha256"] != Digest(m) {
			return ErrConflict
		}
		return nil
	}
	return nil
}

func enqueueTaskArtifactVerification(ctx context.Context, tx db.Tx, a BusinessAssessment) error {
	if a.Outcome.Stage != "submission" {
		return nil
	}
	key := taskArtifactManifestKey(a.Outcome.SourceKey, a.Outcome.SubmissionID)
	raw, exists, err := db.GetRecordTx(ctx, tx, taskArtifactManifestTable, key)
	if err != nil || !exists {
		return err
	}
	m, err := decode[TaskArtifactManifest](raw)
	copy := m
	copy.ID = ""
	if err != nil || m.ID != "task-artifact-manifest:"+Digest(copy) || m.SubmissionSHA256 != a.Outcome.SourceSHA256 || m.ExecutionTenantID != a.Outcome.ExecutionTenantID {
		return ErrInvalid
	}
	job := taskArtifactPending{ManifestID: m.ID, TenantID: a.TenantID, AttemptID: a.AttemptID, SubmissionID: m.SubmissionID}
	created, err := db.InsertRecordTx(ctx, tx, taskArtifactJobTable, key, encode(job))
	if err != nil || !created {
		return err
	}
	return db.PutRecordTx(ctx, tx, taskArtifactPendingTable, key, encode(job))
}

func (s *RuntimeEvidenceStore) taskArtifactManifest(ctx context.Context, a BusinessAssessment) (TaskArtifactManifest, error) {
	raw, exists, err := s.store.core.GetRecord(ctx, taskArtifactManifestTable, taskArtifactManifestKey(a.Outcome.SourceKey, a.Outcome.SubmissionID))
	if err != nil {
		return TaskArtifactManifest{}, err
	}
	if !exists {
		return TaskArtifactManifest{}, ErrNotFound
	}
	m, err := decode[TaskArtifactManifest](raw)
	copy := m
	copy.ID = ""
	if err != nil || m.ID != "task-artifact-manifest:"+Digest(copy) || m.ExecutionTenantID != a.Outcome.ExecutionTenantID || m.ExecutionTenantID == "" || m.SourceKey != a.Outcome.SourceKey || m.SubmissionID != a.Outcome.SubmissionID || len(m.Items) != a.Outcome.Deliverables+a.Outcome.Evidence || len(m.Items) > maxTaskArtifactItems {
		return m, ErrInvalid
	}
	raw, exists, err = s.store.core.GetRecord(ctx, storage.TaskSubmissions, m.SubmissionID)
	if err != nil {
		return m, err
	}
	if !exists {
		return m, ErrNotFound
	}
	sub, err := domainPayload[missiontask.TaskSubmission](raw)
	if err != nil || Digest(sub) != m.SubmissionSHA256 || Digest(sub) != a.Outcome.SourceSHA256 || sub.RuntimeAttemptID != a.AttemptID {
		return m, ErrInvalid
	}
	return m, nil
}

func (s *RuntimeEvidenceStore) taskArtifactReference(ctx context.Context, m TaskArtifactManifest, item TaskArtifactItem) (agentcore.ArtifactRef, error) {
	var ref agentcore.ArtifactRef
	supported := true
	table := storage.TaskDeliverableSubmissions
	if item.Kind == "evidence" {
		table = storage.TaskEvidenceSubmissions
	} else if item.Kind != "deliverable" {
		return ref, ErrInvalid
	}
	raw, exists, err := s.store.core.GetRecord(ctx, table, item.RecordID)
	if err != nil {
		return ref, err
	}
	if !exists {
		return ref, ErrNotFound
	}
	if item.Kind == "deliverable" {
		d, err := domainPayload[missiontask.DeliverableSubmission](raw)
		if err != nil || Digest(d) != item.RecordSHA256 || string(d.ID) != item.RecordID || string(d.SubmissionID) != m.SubmissionID || string(d.Deliverable.ID) != item.ItemID {
			return ref, ErrInvalid
		}
		ref = d.Deliverable.ArtifactRef
	} else {
		e, err := domainPayload[missiontask.EvidenceSubmission](raw)
		if err != nil || Digest(e) != item.RecordSHA256 || string(e.ID) != item.RecordID || string(e.SubmissionID) != m.SubmissionID || string(e.Evidence.ID) != item.ItemID {
			return ref, ErrInvalid
		}
		ref = e.Evidence.ArtifactRef
		supported = e.Evidence.Kind == agentcore.EvidenceKindArtifact || e.Evidence.Kind == agentcore.EvidenceKindTestLog
	}
	if Digest(ref) != item.ReferenceSHA256 {
		return ref, ErrInvalid
	}
	if !supported {
		return agentcore.ArtifactRef{}, nil
	}
	return ref, nil
}

func submissionAssessment(assessments []BusinessAssessment, id string) (BusinessAssessment, error) {
	for _, a := range assessments {
		if a.Outcome.Stage == "submission" && a.Outcome.SubmissionID == id {
			return a, nil
		}
	}
	return BusinessAssessment{}, ErrNotFound
}

func (s *RuntimeEvidenceStore) validateTaskArtifactReceipt(ctx context.Context, r TaskArtifactVerification) error {
	copy := r
	copy.ID = ""
	if r.ID != "task-artifact-verification:"+Digest(copy) || r.Protocol != "orrery-task-artifact-verification-v1" || r.Attempts < 1 || r.Attempts > 3 || r.VerifiedAt.IsZero() {
		return ErrInvalid
	}
	status, err := taskArtifactVerificationStatus(r.Items)
	if err != nil || status != r.Status {
		return ErrInvalid
	}
	all, err := s.BusinessAssessments(ctx, r.TenantID, r.AttemptID)
	if err != nil {
		return err
	}
	a, err := submissionAssessment(all, r.SubmissionID)
	if err != nil {
		return err
	}
	m, err := s.taskArtifactManifest(ctx, a)
	if err != nil {
		return err
	}
	if r.CaseID != a.CaseID || r.RuntimeReviewID != a.RuntimeReviewID || r.BusinessAssessmentID != a.ID || r.ManifestID != m.ID || r.VerifiedAt.Before(a.AssessedAt) || len(r.Items) != len(m.Items) {
		return ErrInvalid
	}
	for i, item := range m.Items {
		if !reflect.DeepEqual(item, r.Items[i].Binding) {
			return ErrInvalid
		}
		if _, err := s.taskArtifactReference(ctx, m, item); err != nil {
			return err
		}
	}
	return nil
}

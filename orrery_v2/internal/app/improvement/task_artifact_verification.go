package improvement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

var ErrArtifactUnverifiable = errors.New("artifact source is not independently verifiable")

const maxTaskArtifactBytes int64 = 64 << 20

type TaskArtifactAccess struct {
	ExecutionTenantID string
	RunID             string
	Reference         agentcore.ArtifactRef
}

type TaskArtifactContent struct {
	Reader    io.ReadCloser
	SHA256    string
	SizeBytes int64
}

// The host adapter resolves only explicitly supported scoped storage keys.
// It never follows arbitrary user URLs or reads a model-supplied local path.
type TaskArtifactReader interface {
	OpenTaskArtifact(context.Context, TaskArtifactAccess) (TaskArtifactContent, error)
}

type TaskArtifactItemVerification struct {
	Binding           TaskArtifactItem `json:"binding"`
	Status            string           `json:"status"`
	SHA256            string           `json:"sha256,omitempty"`
	SizeBytes         int64            `json:"size_bytes"`
	ExpectedSHA256    string           `json:"expected_sha256,omitempty"`
	ExpectedSizeBytes int64            `json:"expected_size_bytes"`
}

type TaskArtifactVerification struct {
	ID                   string                         `json:"id"`
	Protocol             string                         `json:"protocol"`
	TenantID             string                         `json:"tenant_id"`
	AttemptID            string                         `json:"attempt_id"`
	CaseID               string                         `json:"case_id"`
	RuntimeReviewID      string                         `json:"runtime_review_id"`
	BusinessAssessmentID string                         `json:"business_assessment_id"`
	SubmissionID         string                         `json:"submission_id"`
	ManifestID           string                         `json:"manifest_id"`
	Status               string                         `json:"status"`
	Attempts             int                            `json:"attempts"`
	Items                []TaskArtifactItemVerification `json:"items"`
	VerifiedAt           time.Time                      `json:"verified_at"`
}

type artifactContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r artifactContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func checkTaskArtifact(ctx context.Context, reader TaskArtifactReader, access TaskArtifactAccess, item TaskArtifactItem, remaining *int64) TaskArtifactItemVerification {
	v := TaskArtifactItemVerification{Binding: item, Status: "unsupported_source"}
	if access.Reference.IsZero() {
		return v
	}
	content, err := reader.OpenTaskArtifact(ctx, access)
	if err != nil {
		if !errors.Is(err, ErrArtifactUnverifiable) {
			v.Status = "source_unavailable"
		}
		return v
	}
	if content.Reader == nil {
		v.Status = "source_unavailable"
		return v
	}
	if !hashOK(content.SHA256) || content.SizeBytes < 0 {
		content.Reader.Close()
		v.Status = "digest_unavailable"
		return v
	}
	v.ExpectedSHA256 = content.SHA256
	v.ExpectedSizeBytes = content.SizeBytes
	if content.SizeBytes > *remaining {
		content.Reader.Close()
		v.Status = "size_budget_exceeded"
		return v
	}
	hash := sha256.New()
	n, readErr := io.Copy(hash, io.LimitReader(artifactContextReader{ctx, content.Reader}, content.SizeBytes+1))
	closeErr := content.Reader.Close()
	*remaining -= n
	v.SizeBytes = n
	v.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if readErr != nil || closeErr != nil {
		v.Status = "source_unavailable"
		return v
	}
	if n != content.SizeBytes || v.SHA256 != content.SHA256 {
		v.Status = "content_mismatch"
		return v
	}
	v.Status = "verified"
	return v
}

func taskArtifactVerificationStatus(items []TaskArtifactItemVerification) (string, error) {
	partial, rejected, unavailable := len(items) == 0, false, false
	for _, v := range items {
		if v.SizeBytes < 0 {
			return "", ErrInvalid
		}
		switch v.Status {
		case "verified":
			if !hashOK(v.SHA256) || v.SHA256 != v.ExpectedSHA256 || v.SizeBytes != v.ExpectedSizeBytes {
				return "", ErrInvalid
			}
		case "content_mismatch":
			if !hashOK(v.SHA256) || !hashOK(v.ExpectedSHA256) || v.ExpectedSizeBytes < 0 || v.SHA256 == v.ExpectedSHA256 && v.SizeBytes == v.ExpectedSizeBytes {
				return "", ErrInvalid
			}
			rejected = true
		case "source_unavailable":
			unavailable = true
		case "unsupported_source", "digest_unavailable", "size_budget_exceeded":
			partial = true
		default:
			return "", ErrInvalid
		}
	}
	if rejected {
		return "rejected", nil
	}
	if unavailable {
		return "unavailable", nil
	}
	if partial {
		return "partial", nil
	}
	return "verified", nil
}

func (s *RuntimeEvidenceStore) verifyTaskArtifactManifest(ctx context.Context, reader TaskArtifactReader, job taskArtifactPending) (TaskArtifactVerification, error) {
	all, err := s.BusinessAssessments(ctx, job.TenantID, job.AttemptID)
	if err != nil {
		return TaskArtifactVerification{}, err
	}
	a, err := submissionAssessment(all, job.SubmissionID)
	if err != nil {
		return TaskArtifactVerification{}, err
	}
	m, err := s.taskArtifactManifest(ctx, a)
	if err != nil {
		return TaskArtifactVerification{}, err
	}
	if job.ManifestID != m.ID {
		return TaskArtifactVerification{}, ErrInvalid
	}
	r := TaskArtifactVerification{Protocol: "orrery-task-artifact-verification-v1", TenantID: a.TenantID, AttemptID: a.AttemptID, CaseID: a.CaseID, RuntimeReviewID: a.RuntimeReviewID, BusinessAssessmentID: a.ID, SubmissionID: m.SubmissionID, ManifestID: m.ID, Status: "verified", Attempts: job.Attempts + 1, Items: []TaskArtifactItemVerification{}, VerifiedAt: time.Now().UTC()}
	remaining := maxTaskArtifactBytes
	partial, rejected, unavailable := len(m.Items) == 0, false, false
	for _, item := range m.Items {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		ref, err := s.taskArtifactReference(ctx, m, item)
		if err != nil {
			return r, err
		}
		v := checkTaskArtifact(ctx, reader, TaskArtifactAccess{ExecutionTenantID: m.ExecutionTenantID, RunID: a.Outcome.RunID, Reference: ref}, item, &remaining)
		r.Items = append(r.Items, v)
		partial = partial || v.Status != "verified"
		rejected = rejected || v.Status == "content_mismatch"
		unavailable = unavailable || v.Status == "source_unavailable"
	}
	if partial {
		r.Status = "partial"
	}
	if unavailable {
		r.Status = "unavailable"
	}
	if rejected {
		r.Status = "rejected"
	}
	r.VerifiedAt = time.Now().UTC()
	r.ID = "task-artifact-verification:" + Digest(r)
	return r, s.validateTaskArtifactReceipt(ctx, r)
}

// Each manifest gets at most three durable verification attempts. A bounded
// rotating cursor prevents waiting tasks and delayed retries starving others.
// No task is reexecuted and no historical acceptance/review is overwritten.
func (s *RuntimeEvidenceStore) ReconcileTaskArtifacts(ctx context.Context, reader TaskArtifactReader, limit int) (int, error) {
	if s == nil || s.store.ready() != nil || reader == nil {
		return 0, ErrUnavailable
	}
	if limit < 1 || limit > 1000 {
		return 0, ErrInvalid
	}
	if limit > 8 {
		limit = 8
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cursor := ""
	raw, exists, err := s.store.core.GetRecord(ctx, taskArtifactCursorTable, "verify")
	if err != nil {
		return 0, err
	}
	if exists {
		cursor, _ = raw["after"].(string)
	}
	page, err := s.store.core.ListRecords(ctx, taskArtifactPendingTable, cursor, limit)
	if err != nil {
		return 0, err
	}
	if len(page.Items) == 0 && cursor != "" {
		page, err = s.store.core.ListRecords(ctx, taskArtifactPendingTable, "", limit)
		if err != nil {
			return 0, err
		}
	}
	count := 0
	var failures []error
	for _, entry := range page.Items {
		if ctx.Err() != nil {
			break
		}
		job, err := decode[taskArtifactPending](entry.Record)
		if err != nil || job.Attempts < 0 || job.Attempts >= 3 {
			failures = append(failures, ErrInvalid)
			cursor = entry.Key
			continue
		}
		if job.NextAt > time.Now().Unix() {
			cursor = entry.Key
			continue
		}
		r, verifyErr := s.verifyTaskArtifactManifest(ctx, reader, job)
		if ctx.Err() != nil {
			break
		}
		err = s.store.core.WithTx(ctx, func(tx db.Tx) error {
			if err := db.LockRecordKeyTx(ctx, tx, taskArtifactPendingTable, entry.Key); err != nil {
				return err
			}
			current, exists, err := db.GetRecordTx(ctx, tx, taskArtifactPendingTable, entry.Key)
			if err != nil {
				return err
			}
			if !exists || !reflect.DeepEqual(current, entry.Record) {
				return ErrConflict
			}
			job.Attempts++
			retry := verifyErr != nil || r.Status == "unavailable"
			if verifyErr == nil {
				key := runtimeEvidenceKey(r.TenantID, r.AttemptID, "artifact-verifications") + "/" + Digest(r.ManifestID) + fmt.Sprintf("/%02d", job.Attempts)
				created, err := db.InsertRecordTx(ctx, tx, taskArtifactReceiptTable, key, encode(r))
				if err != nil {
					return err
				}
				if !created {
					return ErrConflict
				}
				if err := db.PutRecordTx(ctx, tx, taskArtifactOutboxTable, key, encode(r)); err != nil {
					return err
				}
			}
			if err := db.PutRecordTx(ctx, tx, taskArtifactJobTable, entry.Key, encode(job)); err != nil {
				return err
			}
			if retry && job.Attempts < 3 {
				job.NextAt = time.Now().Add(time.Duration(job.Attempts*job.Attempts) * time.Minute).Unix()
				return db.PutRecordTx(ctx, tx, taskArtifactPendingTable, entry.Key, encode(job))
			}
			return db.DeleteRecordTx(ctx, tx, taskArtifactPendingTable, entry.Key)
		})
		if err != nil {
			failures = append(failures, err)
		} else if verifyErr != nil {
			failures = append(failures, verifyErr)
		} else {
			count++
		}
		cursor = entry.Key
	}
	if err := s.store.core.PutRecord(ctx, taskArtifactCursorTable, "verify", db.Record{"after": cursor}); err != nil {
		failures = append(failures, err)
	}
	return count, errors.Join(failures...)
}

func (s *RuntimeEvidenceStore) TaskArtifactVerifications(ctx context.Context, tenant, attempt string) ([]TaskArtifactVerification, error) {
	if _, err := s.GetRequest(ctx, tenant, attempt); err != nil {
		return nil, err
	}
	prefix := runtimeEvidenceKey(tenant, attempt, "artifact-verifications") + "/"
	page, err := s.store.core.ListRecords(ctx, taskArtifactReceiptTable, prefix, 385)
	if err != nil {
		return nil, err
	}
	result := []TaskArtifactVerification{}
	for _, entry := range page.Items {
		if !strings.HasPrefix(entry.Key, prefix) {
			break
		}
		if len(result) >= 384 {
			return nil, ErrInvalid
		}
		r, err := decode[TaskArtifactVerification](entry.Record)
		if err != nil {
			return nil, err
		}
		if r.TenantID != tenant || r.AttemptID != attempt {
			return nil, ErrInvalid
		}
		if err := s.validateTaskArtifactReceipt(ctx, r); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}

type TaskArtifactProjectionSink interface {
	ProjectTaskArtifactVerification(context.Context, TaskArtifactVerification) error
}

func (s *RuntimeEvidenceStore) ProjectTaskArtifactsPending(ctx context.Context, sink TaskArtifactProjectionSink, limit int) (int, error) {
	if s == nil || s.store.ready() != nil || sink == nil {
		return 0, ErrUnavailable
	}
	if limit < 1 || limit > 1000 {
		return 0, ErrInvalid
	}
	cursor := ""
	raw, exists, err := s.store.core.GetRecord(ctx, taskArtifactCursorTable, "project")
	if err != nil {
		return 0, err
	}
	if exists {
		cursor, _ = raw["after"].(string)
	}
	page, err := s.store.core.ListRecords(ctx, taskArtifactOutboxTable, cursor, limit)
	if err != nil {
		return 0, err
	}
	if len(page.Items) == 0 && cursor != "" {
		page, err = s.store.core.ListRecords(ctx, taskArtifactOutboxTable, "", limit)
		if err != nil {
			return 0, err
		}
	}
	count := 0
	var failures []error
	for _, entry := range page.Items {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		cursor = entry.Key
		r, err := decode[TaskArtifactVerification](entry.Record)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if err := s.validateTaskArtifactReceipt(ctx, r); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := sink.ProjectTaskArtifactVerification(ctx, r); err != nil {
			failures = append(failures, err)
			continue
		}
		deleted, err := s.store.core.CompareAndDeleteRecord(ctx, taskArtifactOutboxTable, entry.Key, entry.Record)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if deleted {
			count++
		}
	}
	if err := s.store.core.PutRecord(ctx, taskArtifactCursorTable, "project", db.Record{"after": cursor}); err != nil {
		failures = append(failures, err)
	}
	return count, errors.Join(failures...)
}

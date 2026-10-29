package improvement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/storage"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/idgen"
)

type taskArtifactReaderFunc func(context.Context, TaskArtifactAccess) (TaskArtifactContent, error)

func (f taskArtifactReaderFunc) OpenTaskArtifact(ctx context.Context, a TaskArtifactAccess) (TaskArtifactContent, error) {
	return f(ctx, a)
}

func taskArtifactTestHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func newTaskArtifactFixture(t *testing.T, core *db.Store, name string) (*businessFixture, missiontask.TaskSubmission) {
	t.Helper()
	f := newBusinessFixture(t, core, name, true)
	ref := agentcore.ArtifactRef{ID: "private-file", Kind: agentcore.ArtifactKindReport, URI: "artifact://private-location", SHA256: taskArtifactTestHash("actual bytes"), SizeBytes: 12}
	f.result.Deliverables[0].ArtifactRef = ref
	f.result.Evidence[0].ArtifactRef = ref
	sub := f.submit(t)
	f.decide(t, missiontask.QAStatusApproved)
	f.complete(t)
	if _, err := f.evidence.ReconcileBusiness(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	return f, sub
}

func artifactMemoryCore(t *testing.T) *db.Store {
	t.Helper()
	core, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { core.Close() })
	return core
}

func validTaskArtifactReader(t *testing.T) taskArtifactReaderFunc {
	t.Helper()
	return func(_ context.Context, a TaskArtifactAccess) (TaskArtifactContent, error) {
		if a.ExecutionTenantID != "tenant-a" || !strings.HasPrefix(a.RunID, "run-") {
			t.Fatal("lost original execution authority")
		}
		return TaskArtifactContent{Reader: io.NopCloser(strings.NewReader("actual bytes")), SHA256: a.Reference.SHA256, SizeBytes: a.Reference.SizeBytes}, nil
	}
}

type taskArtifactTestSink struct {
	fail  bool
	items []TaskArtifactVerification
}

func (s *taskArtifactTestSink) ProjectTaskArtifactVerification(_ context.Context, r TaskArtifactVerification) error {
	if s.fail {
		return errors.New("projection unavailable")
	}
	s.items = append(s.items, r)
	return nil
}

func TestTaskArtifactFormalBytesVerificationPreservesAcceptanceAndReview(t *testing.T) {
	ctx := context.Background()
	f, sub := newTaskArtifactFixture(t, artifactMemoryCore(t), "artifact-success")
	before := f.assessments(t)
	review, _, _ := f.evidence.GetReview(ctx, f.request.Scope.TenantID, f.request.AttemptID)
	if len(sub.DeliverableRecordRefs) != 1 || len(sub.EvidenceRecordRefs) != 1 {
		t.Fatal("missing exact child row addresses")
	}
	if n, err := f.evidence.ReconcileTaskArtifacts(ctx, validTaskArtifactReader(t), 10); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	receipts, err := f.evidence.TaskArtifactVerifications(ctx, f.request.Scope.TenantID, f.request.AttemptID)
	if err != nil || len(receipts) != 1 || receipts[0].Status != "verified" || len(receipts[0].Items) != 2 {
		t.Fatal(receipts, err)
	}
	if !reflect.DeepEqual(before, f.assessments(t)) {
		t.Fatal("rewrote immutable acceptance assessments")
	}
	after, _, _ := f.evidence.GetReview(ctx, f.request.Scope.TenantID, f.request.AttemptID)
	if !reflect.DeepEqual(review, after) || after.BaselineReady {
		t.Fatal("artifact bytes declared causal/baseline success")
	}
	encoded, _ := json.Marshal(receipts)
	for _, secret := range []string{"private-location", "private-file", "actual bytes", "private result summary"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("raw artifact data leaked", secret)
		}
	}
	sink := &taskArtifactTestSink{fail: true}
	if _, err := f.evidence.ProjectTaskArtifactsPending(ctx, sink, 10); err == nil {
		t.Fatal("lost failed projection")
	}
	sink.fail = false
	if n, err := f.evidence.ProjectTaskArtifactsPending(ctx, sink, 10); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if n, err := f.evidence.ProjectTaskArtifactsPending(ctx, sink, 10); err != nil || n != 0 {
		t.Fatal("duplicate projection", n, err)
	}
	if n, err := f.evidence.ReconcileBusiness(ctx, 10); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if n, err := f.evidence.ReconcileTaskArtifacts(ctx, validTaskArtifactReader(t), 10); err != nil || n != 0 {
		t.Fatal("completed manifest reenqueued", n, err)
	}
	if _, err := f.evidence.TaskArtifactVerifications(ctx, "other-tenant", f.request.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross tenant receipt", err)
	}
}

func TestTaskArtifactMissingMismatchAndUnsupportedNeverVerify(t *testing.T) {
	for _, tc := range []struct {
		name, body, digest, status string
		size                       int64
		err                        error
	}{
		{name: "mismatch", body: "changed data", digest: taskArtifactTestHash("actual bytes"), size: 12, status: "rejected"},
		{name: "truncated", body: "actual byte", digest: taskArtifactTestHash("actual bytes"), size: 12, status: "rejected"},
		{name: "extra-byte", body: "actual bytes!", digest: taskArtifactTestHash("actual bytes"), size: 12, status: "rejected"},
		{name: "missing", err: errors.New("missing object"), status: "unavailable"},
		{name: "unsupported", err: ErrArtifactUnverifiable, status: "partial"},
		{name: "missing-digest", body: "actual bytes", size: 12, status: "partial"},
		{name: "oversized", digest: taskArtifactTestHash("actual bytes"), size: maxTaskArtifactBytes + 1, status: "partial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f, _ := newTaskArtifactFixture(t, artifactMemoryCore(t), "artifact-"+tc.name)
			reader := taskArtifactReaderFunc(func(context.Context, TaskArtifactAccess) (TaskArtifactContent, error) {
				if tc.err != nil {
					return TaskArtifactContent{}, tc.err
				}
				return TaskArtifactContent{Reader: io.NopCloser(strings.NewReader(tc.body)), SHA256: tc.digest, SizeBytes: tc.size}, nil
			})
			if n, err := f.evidence.ReconcileTaskArtifacts(ctx, reader, 10); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			receipts, err := f.evidence.TaskArtifactVerifications(ctx, f.request.Scope.TenantID, f.request.AttemptID)
			if err != nil || len(receipts) != 1 || receipts[0].Status != tc.status {
				t.Fatal(receipts, err)
			}
		})
	}
}

func TestTaskArtifactRetryBoundAndRestart(t *testing.T) {
	ctx := context.Background()
	core := artifactMemoryCore(t)
	f, _ := newTaskArtifactFixture(t, core, "artifact-retry")
	reader := taskArtifactReaderFunc(func(context.Context, TaskArtifactAccess) (TaskArtifactContent, error) {
		return TaskArtifactContent{}, errors.New("temporary storage failure")
	})
	for attempt := 1; attempt <= 3; attempt++ {
		f.evidence = NewRuntimeEvidenceStore(core)
		if n, err := f.evidence.ReconcileTaskArtifacts(ctx, reader, 10); err != nil || n != 1 {
			t.Fatal(attempt, n, err)
		}
		page, _ := core.ListRecords(ctx, taskArtifactPendingTable, "", 10)
		if attempt < 3 {
			if len(page.Items) != 1 {
				t.Fatal("retry lost")
			}
			job, _ := decode[taskArtifactPending](page.Items[0].Record)
			job.NextAt = 0
			core.PutRecord(ctx, taskArtifactPendingTable, page.Items[0].Key, encode(job))
		} else if len(page.Items) != 0 {
			t.Fatal("retry exceeded three attempts")
		}
	}
	receipts, err := f.evidence.TaskArtifactVerifications(ctx, f.request.Scope.TenantID, f.request.AttemptID)
	if err != nil || len(receipts) != 3 {
		t.Fatal(receipts, err)
	}
	if n, err := f.evidence.ReconcileTaskArtifacts(ctx, reader, 10); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestTaskArtifactDomainMutationStopsReadAndInvalidatesOldReceipt(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "before", false: "after"}[before], func(t *testing.T) {
			ctx := context.Background()
			f, sub := newTaskArtifactFixture(t, artifactMemoryCore(t), "artifact-mutation")
			reader := validTaskArtifactReader(t)
			if !before {
				if _, err := f.evidence.ReconcileTaskArtifacts(ctx, reader, 10); err != nil {
					t.Fatal(err)
				}
			}
			raw, _, _ := f.core.GetRecord(ctx, storage.TaskDeliverableSubmissions, string(sub.DeliverableRecordRefs[0]))
			d, _ := domainPayload[missiontask.DeliverableSubmission](raw)
			d.Deliverable.ArtifactRef.URI = "artifact://replacement"
			payload, _ := json.Marshal(d)
			raw["payload"] = string(payload)
			f.core.PutRecord(ctx, storage.TaskDeliverableSubmissions, string(d.ID), raw)
			if before {
				called := false
				bad := taskArtifactReaderFunc(func(context.Context, TaskArtifactAccess) (TaskArtifactContent, error) {
					called = true
					return TaskArtifactContent{}, nil
				})
				if _, err := f.evidence.ReconcileTaskArtifacts(ctx, bad, 10); err == nil || called {
					t.Fatal("read altered source", err, called)
				}
			} else {
				if _, err := f.evidence.TaskArtifactVerifications(ctx, f.request.Scope.TenantID, f.request.AttemptID); err == nil {
					t.Fatal("reported stale receipt as current")
				}
			}
		})
	}
}

func TestTaskArtifactManifestRollsBackWithDomainSubmission(t *testing.T) {
	ctx := context.Background()
	core := artifactMemoryCore(t)
	f := newBusinessFixture(t, core, "artifact-rollback", true)
	f.delivery = missiontask.NewDeliveryService(f.unit, f.repo, failBusinessEventSink{next: f.sink, event: missiontask.TaskEventAgentResultSubmitted}, missiontask.DeliveryServiceOptions{IDGenerator: idgen.NewDeterministic("artifact-rollback-submit")})
	if _, err := f.delivery.SubmitAgentResult(ctx, f.task.Contract.ID, f.result, missiontask.SubmitOptions{IdempotencyKey: "rollback"}); err == nil || !strings.Contains(err.Error(), "injected transaction failure") {
		t.Fatal("submission must reach manifest capture before the injected rollback", err)
	}
	page, _ := core.ListRecords(ctx, taskArtifactManifestTable, "", 10)
	if len(page.Items) != 0 {
		t.Fatal("manifest survived rollback")
	}
}

func TestTaskArtifactBadProjectionCannotStarveOtherReceipts(t *testing.T) {
	ctx := context.Background()
	core := artifactMemoryCore(t)
	f, _ := newTaskArtifactFixture(t, core, "artifact-poison")
	if _, err := f.evidence.ReconcileTaskArtifacts(ctx, validTaskArtifactReader(t), 10); err != nil {
		t.Fatal(err)
	}
	if err := core.PutRecord(ctx, taskArtifactOutboxTable, "!invalid", db.Record{"payload": "broken"}); err != nil {
		t.Fatal(err)
	}
	sink := &taskArtifactTestSink{}
	if n, err := f.evidence.ProjectTaskArtifactsPending(ctx, sink, 1); n != 0 || err == nil {
		t.Fatal(n, err)
	}
	if n, err := f.evidence.ProjectTaskArtifactsPending(ctx, sink, 1); n != 1 || err != nil {
		t.Fatal("invalid outbox starved valid receipt", n, err)
	}
}

func TestPostgresTaskArtifactReceiptsSurviveNewConnectionAndRestart(t *testing.T) {
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
		core, err := db.Open(ctx, db.Config{Driver: db.DriverPostgres, URL: raw})
		if err != nil {
			t.Fatal(err)
		}
		return core
	}
	first := open()
	f, _ := newTaskArtifactFixture(t, first, "postgres-artifact")
	second := open()
	f.evidence = NewRuntimeEvidenceStore(second)
	if _, err := f.evidence.ReconcileTaskArtifacts(ctx, validTaskArtifactReader(t), 100); err != nil {
		t.Fatal(err)
	}
	first.Close()
	second.Close()
	third := open()
	defer third.Close()
	evidence := NewRuntimeEvidenceStore(third)
	r, err := evidence.TaskArtifactVerifications(ctx, f.request.Scope.TenantID, f.request.AttemptID)
	if err != nil || len(r) != 1 || r[0].Status != "verified" {
		t.Fatal(r, err)
	}
	sink := &taskArtifactTestSink{}
	if _, err := evidence.ProjectTaskArtifactsPending(ctx, sink, 100); err != nil {
		t.Fatal(err)
	}
	if n, err := evidence.ProjectTaskArtifactsPending(ctx, sink, 100); n != 0 || err != nil {
		t.Fatal(n, err)
	}
}

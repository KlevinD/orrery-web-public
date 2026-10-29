package improvement

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func executionFixture(t *testing.T, request RuntimeRequestEvidence) RuntimeExecutionEvidence {
	t.Helper()
	s := agentcore.RuntimeExecutionSnapshot{Engine: "orrery-agentcore", TenantID: request.ExecutionTenantID, AgentID: request.AgentID, RunID: request.RunID,
		TransportTimeoutNS: int64(2 * time.Minute), Policy: agentcore.RuntimeExecutionPolicy{
			Protocol: "orrery-agentcore-policy-v1", MaxModelCalls: 128, MaxToolCalls: 256, MaxParallelTools: 4, MaxConcurrentCallbacks: 32,
			MaxContextBytes: 512 * 1024, MaxResponseBytes: 256 * 1024, ModelTimeoutNS: int64(2 * time.Minute), ToolTimeoutNS: int64(20 * time.Minute),
			JournalTimeoutNS: int64(2 * time.Second), ApprovalTimeoutNS: int64(10 * time.Minute), DefaultLongTimeoutNS: int64(15 * time.Minute), MaxTransportTimeoutNS: int64(90 * time.Minute),
			Checkpoints: true, AutoCompact: true, CompactAtBytes: 384 * 1024, CompactKeepMessages: 8, CompactSummaryBytes: 16 * 1024, PromptCache: true,
			ParallelToolIDs: []string{}, ApprovalToolIDs: []string{"knowledge_add"},
		}}
	e, err := BuildRuntimeExecutionEvidence(request, RuntimeImplementation{BinarySHA256: Digest("actual executable"), SourceSHA256: Digest("source archive")}, s, request.CapturedAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestRuntimeExecutionImmutableBindingReviewAndRecovery(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	evidence := NewRuntimeEvidenceStore(store.core)
	_, request := runtimeEvidenceFixture(t, "execution-review")
	if err := evidence.Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	execution := executionFixture(t, request)
	if err := evidence.CaptureExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	if err := evidence.CaptureExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	changed, _ := BuildRuntimeExecutionEvidence(request, RuntimeImplementation{BinarySHA256: Digest("other executable")}, execution.Snapshot, execution.CapturedAt)
	if err := evidence.CaptureExecution(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed execution accepted", err)
	}
	wrong := execution.Snapshot
	wrong.RunID = "other-run"
	if _, err := BuildRuntimeExecutionEvidence(request, execution.Implementation, wrong, execution.CapturedAt); !errors.Is(err, ErrInvalid) {
		t.Fatal("cross-run snapshot accepted")
	}
	if _, err := evidence.GetExecution(ctx, "other-tenant", request.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-tenant read", err)
	}
	journal := feedbackJournal(t, t.TempDir())
	entry := feedbackFixture(t, request)
	if err := journal.Save(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := evidence.RecoverFeedback(ctx, journal, 10); err != nil {
		t.Fatal(err)
	}
	r, persisted, err := evidence.GetReview(ctx, request.Scope.TenantID, request.AttemptID)
	if err != nil || !persisted || r.Protocol != runtimeReviewProtocolV2 || r.ExecutionID != execution.ID || r.ImplementationSHA256 != execution.Implementation.BinarySHA256 || r.BaselineReady || r.BusinessQuality != "unassessed" || len(r.Domains) != 9 {
		t.Fatalf("bad execution review: %+v %v", r, err)
	}
	for _, d := range r.Domains {
		if d.Kind == TargetAgentLoop && (d.Coverage != "host_binary_and_resolved_policy" || strings.Contains(strings.Join(d.Gaps, ","), "runtime_implementation_missing") || !strings.Contains(strings.Join(d.Gaps, ","), "provider_implementation_missing")) {
			t.Fatalf("false coverage %+v", d)
		}
	}
	terminal, err := evidence.GetTerminal(ctx, request.Scope.TenantID, request.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := capturedRuntimeObservation(request, terminal)
	c, err := store.Get(ctx, request.Scope.TenantID, caseID(expected))
	if err != nil || !reflect.DeepEqual(c.Observation, expected) {
		t.Fatal("original case identity changed", err)
	}
	loaded, err := store.RuntimeReviewForCase(ctx, c)
	if err != nil || loaded.ID != r.ID {
		t.Fatal("case cannot resolve review", err)
	}
	if err := evidence.CaptureExecution(ctx, execution); err != nil {
		t.Fatal("same record replay failed", err)
	}
	if err := store.core.PutRecord(ctx, runtimeEvidenceTable, runtimeEvidenceKey(request.Scope.TenantID, request.AttemptID, "execution"), encode(changed)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := evidence.GetReview(ctx, request.Scope.TenantID, request.AttemptID); !errors.Is(err, ErrInvalid) {
		t.Fatal("rewritten sidecar not detected", err)
	}
	key := runtimeEvidenceKey(request.Scope.TenantID, request.AttemptID, "execution")
	raw, _, _ := store.core.GetRecord(ctx, runtimeEvidenceTable, key)
	if deleted, err := store.core.CompareAndDeleteRecord(ctx, runtimeEvidenceTable, key, raw); err != nil || !deleted {
		t.Fatal(err)
	}
	if _, _, err := evidence.GetReview(ctx, request.Scope.TenantID, request.AttemptID); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing sidecar downgraded V2 history", err)
	}
}

func TestRuntimeExecutionCannotRewriteHistoricalV1Review(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	evidence := NewRuntimeEvidenceStore(store.core)
	_, request := runtimeEvidenceFixture(t, "execution-legacy")
	if err := evidence.Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := evidence.Complete(ctx, request, runtimeEvidenceTerminal()); err != nil {
		t.Fatal(err)
	}
	before, persisted, err := evidence.GetReview(ctx, request.Scope.TenantID, request.AttemptID)
	if err != nil || !persisted {
		t.Fatal(err)
	}
	if err := evidence.CaptureExecution(ctx, executionFixture(t, request)); !errors.Is(err, ErrConflict) {
		t.Fatal("history accepted new implementation", err)
	}
	after, _, err := evidence.GetReview(ctx, request.Scope.TenantID, request.AttemptID)
	if err != nil || !reflect.DeepEqual(before, after) || strings.Contains(encode(after)["payload"].(string), "execution_evidence_id") {
		t.Fatal("V1 history changed", err)
	}
	legacy, _ := BuildRuntimeReview(request, nil)
	derived, _ := BuildRuntimeReviewWithExecution(request, nil, nil)
	if !reflect.DeepEqual(legacy, derived) {
		t.Fatal("nil execution changed V1 bytes")
	}
}

func TestRuntimeExecutionTraceUsesCopiedHostIdentity(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	evidence := NewRuntimeEvidenceStore(store.core)
	req, request := runtimeEvidenceFixture(t, "unused")
	fixture := executionFixture(t, request)
	identity := fixture.Implementation
	recorder := RuntimeEvidenceRecorder{Recorder: &runtimeEvidenceTestTrace{}, Evidence: evidence, Implementation: &identity, Now: func() time.Time { return request.CapturedAt }, Scope: func(agentcore.RuntimeRequest) (RuntimeEvidenceScope, error) { return request.Scope, nil }}
	trace, err := recorder.BeginTrace(ctx, agentcore.AgentInput{})
	if err != nil {
		t.Fatal(err)
	}
	identity.BinarySHA256 = Digest("mutated after begin")
	if err := trace.RecordRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := trace.(agentcore.RuntimeExecutionTraceSession).RecordExecution(ctx, fixture.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err := trace.Finish(ctx, runtimeEvidenceTerminal()); err != nil {
		t.Fatal(err)
	}
	s := trace.(*runtimeEvidenceTraceSession)
	got, err := evidence.GetExecution(ctx, request.Scope.TenantID, s.attempt)
	if err != nil || got.Implementation.BinarySHA256 != fixture.Implementation.BinarySHA256 {
		t.Fatal("host identity was mutable", err)
	}
}

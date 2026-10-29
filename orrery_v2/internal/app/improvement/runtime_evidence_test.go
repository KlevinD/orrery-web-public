package improvement

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

func runtimeEvidenceFixture(t *testing.T, attempt string) (agentcore.RuntimeRequest, RuntimeRequestEvidence) {
	t.Helper()
	req := agentcore.RuntimeRequest{TenantID: "tenant-a", RunID: "run-a", AgentID: "agent-a",
		InputMessages:  []agentcore.RuntimeChatMessage{{Role: "user", Content: "private task content"}},
		RuntimeOptions: agentcore.RuntimeOptions{Model: "model-a", MaxTurns: 8, SystemPrompt: "private prompt", Environment: map[string]string{"SECRET": "private credential"}},
		ToolBindings:   []agentcore.RuntimeToolBinding{{ToolID: "tool-a", Alias: "lookup", Version: "v2", SecretEnv: map[string]string{"KEY": "private tool credential"}}},
		SkillBindings:  []agentcore.RuntimeSkillBinding{{SkillID: "skill-a", Version: "v1", PackageDigest: "sha256:" + Digest("package")}}}
	scope := RuntimeEvidenceScope{TenantID: "tenant-a", Task: ObjectRef{Kind: "agent_task", ID: "task-a"}, Run: ObjectRef{Kind: "agent_run", ID: "agent-run:run-a"}, Source: graphprotocol.AccessRef{TenantID: "tenant-a", Kind: "agent_run", ID: "run-a"}}
	e, err := BuildRuntimeRequestEvidence(req, scope, attempt, "trace://run-a", time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	return req, e
}

func runtimeEvidenceTerminal() agentcore.TraceTerminal {
	return agentcore.TraceTerminal{Status: agentcore.RuntimeStatusCompleted, CompletedAt: time.Unix(1001, 0).UTC(), Response: agentcore.RuntimeResponse{DurationMs: 12, Usage: map[string]agentcore.TokenUsage{"model-a": {InputTokens: 15, OutputTokens: 7}}}}
}

func TestRuntimeEvidenceSeparatesControlsContextBindingsAndSecrets(t *testing.T) {
	req, e := runtimeEvidenceFixture(t, "attempt-a")
	rebuild := func() RuntimeRequestEvidence {
		t.Helper()
		got, err := BuildRuntimeRequestEvidence(req, e.Scope, e.AttemptID, e.TraceRef, e.CapturedAt)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	req.RuntimeOptions.Environment["SECRET"] = "rotated credential"
	req.ToolBindings[0].SecretEnv["KEY"] = "rotated tool credential"
	if got := rebuild(); !reflect.DeepEqual(got, e) {
		t.Fatal("credential rotation changed evidence")
	}
	if req.ToolBindings[0].SecretEnv["KEY"] == "" {
		t.Fatal("builder mutated execution credentials")
	}
	req.ContextPack.TaskContractSummary = "new memory and task context"
	req.InputMessages[0].Content = "new input"
	req.RuntimeOptions.SystemPrompt = "new task-dependent prompt"
	got := rebuild()
	if got.ControlsSHA256 != e.ControlsSHA256 || got.ContextSHA256 == e.ContextSHA256 || got.InputSHA256 == e.InputSHA256 || got.PromptSHA256 == e.PromptSHA256 {
		t.Fatal("task content confused with runtime controls")
	}
	req.RuntimeOptions.MaxTurns++
	if rebuild().ControlsSHA256 == e.ControlsSHA256 {
		t.Fatal("runtime control change was invisible")
	}
	req.ToolBindings[0].Summary = "changed tool contract"
	if rebuild().Bindings[0].Fingerprint == e.Bindings[0].Fingerprint {
		t.Fatal("tool binding change was invisible")
	}
	raw := encode(got)["payload"].(string)
	for _, secret := range []string{"private task content", "private prompt", "private credential", "private tool credential", "new memory and task context", "new input"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("stored raw sensitive content %q", secret)
		}
	}
	if e.Bindings[0].PackageDigest != "" || e.Bindings[1].PackageDigest != Digest("package") {
		t.Fatal("binding digest was promoted to package digest")
	}
}

func TestRuntimeEvidenceImmutableAtomicFeedbackAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	runtime := NewRuntimeEvidenceStore(store.core)
	_, e := runtimeEvidenceFixture(t, "attempt-a")
	end := runtimeEvidenceTerminal()
	if _, err := runtime.Complete(ctx, e, end); !errors.Is(err, ErrInvalid) {
		t.Fatalf("uncaptured terminal: %v", err)
	}
	if err := runtime.Capture(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Capture(ctx, e); err != nil {
		t.Fatal(err)
	}
	req, _ := runtimeEvidenceFixture(t, e.AttemptID)
	req.RuntimeOptions.MaxTurns++
	changed, _ := BuildRuntimeRequestEvidence(req, e.Scope, e.AttemptID, e.TraceRef, e.CapturedAt)
	if err := runtime.Capture(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("mutable capture: %v", err)
	}
	c, err := runtime.Complete(ctx, e, end)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != Observed || c.Evaluation != nil || c.Observation.Dependencies[1].SHA256 != "" || c.Observation.Dependencies[2].SHA256 != Digest("package") {
		t.Fatalf("fabricated business evaluation or implementation hash: %+v", c)
	}
	if c.Observation.RelatedObjects[0].ID != e.AttemptID {
		t.Fatal("attempt is not discoverable from observation")
	}
	if again, err := runtime.Complete(ctx, e, end); err != nil || again.ID != c.ID || again.Revision != 1 {
		t.Fatalf("duplicate terminal: %+v %v", again, err)
	}
	end.Status = agentcore.RuntimeStatusFailed
	if _, err := runtime.Complete(ctx, e, end); !errors.Is(err, ErrConflict) {
		t.Fatalf("mutable terminal: %v", err)
	}
	for _, read := range []func() error{
		func() error { _, err := runtime.GetRequest(ctx, "tenant-b", e.AttemptID); return err },
		func() error { _, err := runtime.GetTerminal(ctx, "tenant-b", e.AttemptID); return err },
	} {
		if err := read(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-tenant read: %v", err)
		}
	}
	if terminal, err := runtime.GetTerminal(ctx, e.Scope.TenantID, e.AttemptID); err != nil || terminal.Status != agentcore.RuntimeStatusCompleted {
		t.Fatalf("terminal changed: %+v %v", terminal, err)
	}
	if _, err := store.ProjectPending(ctx, unavailableRuntimeGraph{}, 100); err == nil {
		t.Fatal("expected graph outage")
	}
	if got, err := store.Get(ctx, c.TenantID, c.ID); err != nil || got.State != Observed {
		t.Fatalf("graph outage lost result: %+v %v", got, err)
	}
	for _, table := range []string{caseTable, historyTable, outboxTable} {
		page, err := store.core.ListRecords(ctx, table, "", 100)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("atomic %s: %d %v", table, len(page.Items), err)
		}
	}
	// Two attempts of the same Run ID are distinct observations.
	changed.AttemptID = "attempt-b"
	changed.ID = ""
	changed.ID = "runtime-request-evidence:" + Digest(changed)
	if err := runtime.Capture(ctx, changed); err != nil {
		t.Fatal(err)
	}
	c2, err := runtime.Complete(ctx, changed, runtimeEvidenceTerminal())
	if err != nil || c2.ID == c.ID || c2.Observation.Dependencies[0] == c.Observation.Dependencies[0] {
		t.Fatalf("retry lost actual version: %+v %v", c2, err)
	}
}

type unavailableRuntimeGraph struct{}

func (unavailableRuntimeGraph) ProjectImprovement(context.Context, Case) error {
	return errors.New("graph unavailable")
}

func TestRuntimeEvidenceTerminalAndObservationRollbackTogether(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	runtime := NewRuntimeEvidenceStore(store.core)
	_, e := runtimeEvidenceFixture(t, "attempt-a")
	if err := runtime.Capture(ctx, e); err != nil {
		t.Fatal(err)
	}
	// A conflicting immutable revision forces persistCase to fail after writing
	// the terminal and aggregate inside its transaction.
	end := runtimeEvidenceTerminal()
	endEvidence := RuntimeTerminalEvidence{AttemptID: e.AttemptID, RequestID: e.ID, Status: end.Status, TraceFinalization: "not_attested", DurationMS: end.Response.DurationMs, Usage: end.Response.Usage, CompletedAt: end.CompletedAt}
	endEvidence.ID = "runtime-terminal-evidence:" + Digest(endEvidence)
	o := Observation{TenantID: e.Scope.TenantID, ID: "captured-runtime-result:" + Digest(endEvidence)}
	c := Case{ID: caseID(o), TenantID: e.Scope.TenantID, Revision: 1}
	if err := store.core.WithTx(ctx, func(tx db.Tx) error {
		_, err := db.InsertRecordTx(ctx, tx, historyTable, revisionKey(c), encode("conflict"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Complete(ctx, e, end); !errors.Is(err, ErrConflict) {
		t.Fatalf("wanted history conflict: %v", err)
	}
	if _, err := runtime.GetTerminal(ctx, e.Scope.TenantID, e.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan terminal committed: %v", err)
	}
	if _, err := store.Get(ctx, c.TenantID, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan case committed: %v", err)
	}
}

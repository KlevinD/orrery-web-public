package improvement

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func runtimeTestSetup(t *testing.T) (*SourceRuntime, *replayTestRunner, RuntimePlan, RuntimeGrant) {
	t.Helper()
	w, _, runner, spec := replayTestWorker(t)
	c, err := w.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	// The experiment's lease expires before a different owner adopts it.
	w.Store.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	authority, _ := NewRuntimeAuthority([]byte(strings.Repeat("independent-operator-key", 2)))
	r := &SourceRuntime{Store: w.Store, Vault: w.Vault, Authority: authority, Runner: runner, ExecutorSHA256: Digest("source-runtime-test-executor")}
	request := RuntimeRequest{TenantID: c.TenantID, CaseID: c.ID, RuntimeID: "admitted-task-runtime", ExecutorSHA256: r.ExecutorSHA256, AllowBootstrap: true, ObservationBudget: 6, Tasks: privateCopy(spec.Tasks)}
	for i := range request.Tasks {
		request.Tasks[i].ID = "fresh-" + request.Tasks[i].ID
		request.Tasks[i].Input = json.RawMessage(`{"fresh":` + string(rune('0'+i)) + `}`)
	}
	plan, err := r.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := authority.Authorize(plan, "test-host-operator", w.Store.now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return r, runner, plan, grant
}

func TestSourceRuntimeAdoptsExecutesVerifiesAndLinksNextRound(t *testing.T) {
	r, runner, plan, grant := runtimeTestSetup(t)
	ctx := context.Background()
	result, err := r.Cycle(ctx, plan, grant)
	if err != nil {
		t.Fatal(err)
	}
	if result.Case.State != Verified || result.Active.Revision != 2 || math.Abs(result.Case.Outcome.Gain-1.0/3) > 1e-12 || result.Case.Outcome.Cost != 6 || result.Active.ReleaseID != result.Case.Release.ID || runner.calls != 12 {
		t.Fatalf("unexpected result: %+v calls=%d", result, runner.calls)
	}
	if len(result.Case.Release.Targets) != 2 || len(result.Active.Resources) != 2 || result.Followup.State != Observed || result.Followup.Observation.ParentCaseID != result.Case.ID || result.Followup.Observation.Run == result.Case.Observation.Run || Digest(result.Followup.Observation.Dependencies) != Digest(result.Active.Resources) {
		t.Fatalf("missing cross-target adoption or current-version feedback: %+v", result)
	}
	for _, version := range result.Active.Resources {
		if !strings.HasPrefix(version.Version, "candidate:") {
			t.Fatal("runtime retained a baseline target")
		}
	}
	if err := r.Verify(ctx, result.Case, Transition{To: Verified, Outcome: result.Case.Outcome}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Cycle(ctx, plan, grant); err != nil || runner.calls != 12 {
		t.Fatalf("repeated paid task execution on replay: calls=%d err=%v", runner.calls, err)
	}
	forged := *result.Case.Outcome
	forged.Gain = 100
	if err := r.Verify(ctx, result.Case, Transition{To: Verified, Outcome: &forged}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("caller-supplied gain accepted: %v", err)
	}
	// Source-bundle actions are local domain receipts, not production HIL runs.
	var action runtimeAction
	if err := r.Vault.read(ctx, plan.TenantID, result.Case.Release.Targets[0].Receipt, "runtime-action", true, &action); err != nil || action.Grant.Signature != grant.Signature {
		t.Fatalf("unbound activation action: %v", err)
	}
}

func TestSourceRuntimeFreshRegressionRollsBackActualConsumedVersion(t *testing.T) {
	r, runner, plan, grant := runtimeTestSetup(t)
	runner.failCandidate = true // New observation fails; admitted experiment passed earlier.
	result, err := r.Cycle(context.Background(), plan, grant)
	if err != nil {
		t.Fatal(err)
	}
	if result.Case.State != RolledBack || result.Active.Revision != 3 || result.Case.Outcome.RegressionCount != 1 || result.Active.ReleaseID != "" || result.Case.Rollback == nil {
		t.Fatalf("failed runtime was not restored: %+v", result)
	}
	for i, resource := range result.Active.Resources {
		if resource != result.Case.ChangeSet.Baseline[i] {
			t.Fatal("rollback only recorded a flag")
		}
	}
	if err := r.Verify(context.Background(), result.Case, Transition{To: RolledBack, Rollback: result.Case.Rollback}); err != nil {
		t.Fatal(err)
	}
	run, _, err := r.Execute(context.Background(), plan.TenantID, plan.RuntimeID, plan.Tasks[0])
	if err != nil || taskPassed(plan.Tasks[0], run) {
		t.Fatalf("task did not consume restored baseline: %+v %v", run, err)
	}
	if Digest(result.Followup.Observation.Dependencies) != Digest(result.Case.ChangeSet.Baseline) {
		t.Fatal("next round did not use restored versions")
	}
}

func TestSourceRuntimeRejectsMissingWrongExpiredAndMutableAuthority(t *testing.T) {
	r, _, plan, grant := runtimeTestSetup(t)
	ctx := context.Background()
	for _, invalid := range []RuntimeGrant{{}, {PlanHash: grant.PlanHash, Issuer: grant.Issuer, ExpiresAt: grant.ExpiresAt, Signature: Digest("fake")}} {
		if _, err := r.Activate(ctx, plan, invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsigned authorization accepted: %v", err)
		}
	}
	changed := privateCopy(plan)
	changed.Tasks[0].Expected = json.RawMessage(`{"ok":false}`)
	if _, err := r.Activate(ctx, changed, grant); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oracle changed after authorization: %v", err)
	}
	expired, _ := r.Authority.Authorize(plan, "operator", r.Store.now().Add(-2*time.Hour), time.Hour)
	if _, err := r.Activate(ctx, plan, expired); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired authorization accepted: %v", err)
	}
	if _, err := r.Active(ctx, plan.TenantID, plan.RuntimeID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected authorization still changed runtime: %v", err)
	}
	r.Authority.key = append([]byte(nil), r.Vault.key...)
	if err := r.ready(); !errors.Is(err, ErrUnavailable) {
		t.Fatal("evidence signer granted itself adoption authority")
	}
}

func TestSourceRuntimeRejectsOldInputsBootstrapAndTenantMixing(t *testing.T) {
	r, _, plan, grant := runtimeTestSetup(t)
	c, frozen, _, err := r.resolve(context.Background(), plan.TenantID, plan.CaseID)
	if err != nil {
		t.Fatal(err)
	}
	old := privateCopy(plan)
	old.Tasks[0].Input = frozen.Spec.Tasks[0].Input
	if err := r.validatePlan(old, c, frozen.Spec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("old experiment relabeled as post-release: %v", err)
	}
	denied := privateCopy(plan)
	denied.AllowBootstrap = false
	g, _ := r.Authority.Authorize(denied, "operator", r.Store.now(), time.Hour)
	if _, err := r.Activate(context.Background(), denied, g); !errors.Is(err, ErrNotFound) {
		t.Fatalf("implicit bootstrap: %v", err)
	}
	// Same owner cannot use an existing grant in a different tenant.
	plan.TenantID = "other-tenant"
	if _, err := r.Activate(context.Background(), plan, grant); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant case found: %v", err)
	}
}

func TestSourceRuntimeRollbackFencesNewerRevision(t *testing.T) {
	r, runner, plan, grant := runtimeTestSetup(t)
	ctx := context.Background()
	if _, err := r.Activate(ctx, plan, grant); err != nil {
		t.Fatal(err)
	}
	runner.failCandidate = true
	c, err := r.Observe(ctx, plan)
	if err != nil || c.State != RollbackRequired {
		t.Fatalf("missing rollback request: %v", err)
	}
	// Simulate a newer owner committing an independently adopted runtime.
	newer, _ := r.Active(ctx, plan.TenantID, plan.RuntimeID)
	newer.Revision++
	newer.Sources[0].Content = "newer separately authorized code"
	err = r.Store.core.WithTx(ctx, func(tx db.Tx) error {
		ref, err := r.Vault.putArtifactTx(ctx, tx, plan.TenantID, "runtime-snapshot", newer)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, sourceRuntimeTable, caseKey(plan.TenantID, plan.RuntimeID), encode(runtimePointer{Snapshot: ref, Revision: newer.Revision}))
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Rollback(ctx, plan, grant); !errors.Is(err, ErrConflict) {
		t.Fatalf("rollback overwrote newer runtime: %v", err)
	}
	active, _ := r.Active(ctx, plan.TenantID, plan.RuntimeID)
	if !strings.Contains(active.Sources[0].Content, "newer") {
		t.Fatal("new owner lost code")
	}
}

type stoppedRuntimeRunner struct {
	TaskRunner
	calls int
}

func (r *stoppedRuntimeRunner) Run(context.Context, []SourceFile, ReplayPolicy, ReplayTask) (TaskExecution, error) {
	r.calls++
	return TaskExecution{}, errors.New("external execution result unknown")
}

func TestSourceRuntimeUnknownObservationDoesNotRepeatExecution(t *testing.T) {
	r, _, plan, grant := runtimeTestSetup(t)
	ctx := context.Background()
	if _, err := r.Activate(ctx, plan, grant); err != nil {
		t.Fatal(err)
	}
	runner := &stoppedRuntimeRunner{}
	r.Runner = runner
	if _, err := r.Observe(ctx, plan); err == nil {
		t.Fatal("unknown external operation succeeded")
	}
	if _, err := r.Observe(ctx, plan); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "indeterminate") || runner.calls != 1 {
		t.Fatalf("unknown operation repeated: %d %v", runner.calls, err)
	}
}

func TestSourceRuntimeActivationAndCaseCommitAreAtomic(t *testing.T) {
	r, _, plan, grant := runtimeTestSetup(t)
	ctx := context.Background()
	// Reject the final command receipt after the pointer and sealed action have
	// been written within the transaction. None of those writes may survive.
	_, err := r.Store.core.Exec(ctx, `CREATE TRIGGER reject_runtime_commit BEFORE INSERT ON platform_records WHEN NEW.table_name = 'ontology_improvement_commands' BEGIN SELECT RAISE(ABORT, 'injected commit failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Activate(ctx, plan, grant); err == nil {
		t.Fatal("injected write failure ignored")
	}
	if _, err := r.Active(ctx, plan.TenantID, plan.RuntimeID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("runtime changed without Case commit: %v", err)
	}
	c, err := r.Store.Get(ctx, plan.TenantID, plan.CaseID)
	if err != nil || c.State != Evaluated || c.Release != nil {
		t.Fatalf("partial adoption persisted: %+v %v", c, err)
	}
	if _, err := r.Store.core.Exec(ctx, `DROP TRIGGER reject_runtime_commit`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Activate(ctx, plan, grant); err != nil {
		t.Fatalf("safe atomic retry failed: %v", err)
	}
}

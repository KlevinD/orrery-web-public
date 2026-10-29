package improvement

import (
	"context"
	"fmt"
	"reflect"

	"orrery_v2/internal/platform/db"
)

func (r *SourceRuntime) action(ctx context.Context, c Case, receipts []TargetReceipt, operation string) (runtimeAction, error) {
	var a runtimeAction
	if len(receipts) == 0 || validateReceipts(*c.ChangeSet, receipts, operation == "rollback") != nil {
		return a, ErrInvalid
	}
	ref := receipts[0].Receipt
	if err := r.Vault.read(ctx, c.TenantID, ref, "runtime-action", true, &a); err != nil {
		return a, err
	}
	if a.Operation != operation || a.Plan.TenantID != c.TenantID || a.Plan.CaseID != c.ID || a.Plan.ChangeSetHash != Digest(c.ChangeSet) || a.Plan.EvaluationID != c.Evaluation.ID || a.ActionRunID != "source-action:"+operation+":"+Digest(a.Plan) || a.CompletedAt.IsZero() || !a.CompletedAt.Before(a.Grant.ExpiresAt) {
		return a, ErrInvalid
	}
	if err := r.Authority.verify(a.Plan, a.Grant, r.Store.now(), false); err != nil {
		return a, err
	}
	if !reflect.DeepEqual(receipts, targetReceipts(c, a, ref)) {
		return a, ErrInvalid
	}
	return a, nil
}

func (r *SourceRuntime) verifyActivation(ctx context.Context, c Case, release Release, spec ReplaySpec, proof replayProof) (runtimeAction, error) {
	a, err := r.action(ctx, c, release.Targets, "activate")
	if err != nil {
		return a, err
	}
	if err := r.validatePlan(a.Plan, c, spec); err != nil {
		return a, err
	}
	if release.ID != runtimeReleaseID(a.Plan) || release.ChangeSetHash != Digest(c.ChangeSet) || release.EvaluationID != c.Evaluation.ID {
		return a, ErrInvalid
	}
	before, err := r.snapshot(ctx, c.TenantID, a.Before)
	if err != nil || !sameRuntimeMaterial(before, baseSnapshot(a.Plan, c, spec)) {
		return a, ErrInvalid
	}
	expected, err := candidateSnapshot(before, c, proof, spec)
	if err != nil {
		return a, err
	}
	expected.ReleaseID = release.ID
	after, err := r.snapshot(ctx, c.TenantID, a.After)
	if err != nil || !reflect.DeepEqual(after, expected) {
		return a, ErrInvalid
	}
	return a, nil
}

// Verify resolves domain receipts and recomputes post-adoption paired scores.
// The model and caller cannot supply an authoritative gain or success flag.
func (r *SourceRuntime) Verify(ctx context.Context, c Case, t Transition) error {
	_, frozen, proof, err := r.resolve(ctx, c.TenantID, c.ID)
	if err != nil {
		return err
	}
	switch t.To {
	case Monitoring:
		if t.Release == nil {
			return ErrInvalid
		}
		_, err = r.verifyActivation(ctx, c, *t.Release, frozen.Spec, proof)
		return err
	case Verified, RollbackRequired:
		if c.Release == nil || t.Outcome == nil {
			return ErrInvalid
		}
		a, err := r.verifyActivation(ctx, c, *c.Release, frozen.Spec, proof)
		if err != nil {
			return err
		}
		var report runtimeObservation
		if err := r.Vault.read(ctx, c.TenantID, t.Outcome.Report, "runtime-observation", true, &report); err != nil {
			return err
		}
		if !reflect.DeepEqual(report.Plan, a.Plan) || report.Action != c.Release.Targets[0].Receipt || report.Snapshot != a.After {
			return ErrInvalid
		}
		expected, err := r.scoreObservation(c, frozen.Spec, proof, report)
		if err != nil || !reflect.DeepEqual(expected, report.Outcome) {
			return ErrInvalid
		}
		expected.Report = t.Outcome.Report
		if !reflect.DeepEqual(expected, *t.Outcome) {
			return ErrInvalid
		}
		return nil
	case RolledBack:
		if c.Release == nil || t.Rollback == nil || t.Rollback.ReleaseID != c.Release.ID {
			return ErrInvalid
		}
		activation, err := r.verifyActivation(ctx, c, *c.Release, frozen.Spec, proof)
		if err != nil {
			return err
		}
		a, err := r.action(ctx, c, t.Rollback.Targets, "rollback")
		if err != nil || !reflect.DeepEqual(a.Plan, activation.Plan) || a.Before != activation.After {
			return ErrInvalid
		}
		before, err := r.snapshot(ctx, c.TenantID, activation.Before)
		if err != nil {
			return err
		}
		candidate, err := r.snapshot(ctx, c.TenantID, activation.After)
		if err != nil {
			return err
		}
		before.Revision = candidate.Revision + 1
		after, err := r.snapshot(ctx, c.TenantID, a.After)
		if err != nil || !reflect.DeepEqual(before, after) {
			return ErrInvalid
		}
		return nil
	default:
		return ErrUnavailable
	}
}

func (r *SourceRuntime) scoreObservation(c Case, spec ReplaySpec, original replayProof, report runtimeObservation) (Outcome, error) {
	spec.Tasks = report.Plan.Tasks
	paired := replayProof{ChangeSetHash: Digest(c.ChangeSet), Proposal: original.Proposal, Baseline: report.Baseline, Candidate: report.Actual}
	// The new batches and every container must also differ from the experiment.
	seen := map[string]bool{}
	for _, batch := range []replayBatch{original.Baseline, original.Candidate} {
		if report.Baseline.Run == batch.Run || report.Actual.Run == batch.Run {
			return Outcome{}, ErrInvalid
		}
		for _, run := range batch.Executions {
			seen[run.ContainerID] = true
		}
	}
	sources, err := proposalSources(spec, original.Proposal)
	if err != nil {
		return Outcome{}, err
	}
	if err := validateBatch(spec, spec.Sources, report.Baseline, seen); err != nil {
		return Outcome{}, err
	}
	if err := validateBatch(spec, sources, report.Actual, seen); err != nil {
		return Outcome{}, err
	}
	e, err := evaluateReplay(spec, paired, *c.Experiment)
	if err != nil {
		return Outcome{}, err
	}
	if e.Cost-spec.Policy.ModelCallUnits > report.Plan.ObservationBudget {
		return Outcome{}, ErrInvalid
	}
	return Outcome{ID: "source-outcome:" + Digest([]replayBatch{report.Baseline, report.Actual}), ReleaseID: c.Release.ID, Runs: []ObjectRef{report.Baseline.Run, report.Actual.Run}, Gain: e.CandidateScore - e.BaselineScore, Cost: e.Cost - spec.Policy.ModelCallUnits, DurationMS: e.DurationMS, RegressionCount: e.RegressionCount, BusinessConstraintsPassed: e.BusinessConstraintsPassed && e.SafetyPassed && e.HeldOutPassed}, nil
}

// Execute loads the committed active bundle for a real task. It does not read
// the proposal or accept source code from the task request.
func (r *SourceRuntime) Execute(ctx context.Context, tenant, runtimeID string, task ReplayTask) (TaskExecution, ArtifactRef, error) {
	p, err := r.pointer(ctx, tenant, runtimeID)
	if err != nil {
		return TaskExecution{}, ArtifactRef{}, err
	}
	snapshot, err := r.snapshot(ctx, tenant, p.Snapshot)
	if err != nil || snapshot.ID != runtimeID || snapshot.Revision != p.Revision {
		return TaskExecution{}, ArtifactRef{}, ErrConflict
	}
	if _, err := canonicalJSON(task.Input); err != nil || len(task.Input) > 64<<10 || !textOK(task.ID, 256) {
		return TaskExecution{}, ArtifactRef{}, ErrInvalid
	}
	run, err := r.Runner.Run(ctx, privateCopy(snapshot.Sources), snapshot.Policy, privateCopy(task))
	return run, p.Snapshot, err
}

func (r *SourceRuntime) Observe(ctx context.Context, plan RuntimePlan) (Case, error) {
	c, frozen, proof, err := r.resolve(ctx, plan.TenantID, plan.CaseID)
	if err != nil {
		return c, err
	}
	if err := r.validatePlan(plan, c, frozen.Spec); err != nil {
		return c, err
	}
	if c.Release == nil || c.Release.ID != runtimeReleaseID(plan) {
		return c, ErrConflict
	}
	if c.Outcome != nil {
		to := Verified
		if !outcomePassed(c.ChangeSet.Goal, *c.Outcome) {
			to = RollbackRequired
		}
		return c, r.Verify(ctx, c, Transition{To: to, Outcome: c.Outcome})
	}
	if c.State != Monitoring {
		return c, ErrConflict
	}
	action, err := r.verifyActivation(ctx, c, *c.Release, frozen.Spec, proof)
	if err != nil {
		return c, err
	}
	c, err = r.claim(ctx, c, plan)
	if err != nil {
		return c, err
	}
	spec := frozen.Spec
	spec.Tasks = plan.Tasks
	worker := ReplayCoordinator{Store: r.Store, Vault: r.Vault, Runner: r.Runner}
	baseRef, err := worker.step(ctx, c, Digest(plan), "runtime-baseline", func() (ArtifactRef, error) {
		batch, err := worker.batch(ctx, c.TenantID, spec.Sources, spec)
		if err != nil {
			return ArtifactRef{}, err
		}
		if err := validateBatch(spec, spec.Sources, batch, map[string]bool{}); err != nil {
			return ArtifactRef{}, err
		}
		if !baselineControlPassed(spec, batch) {
			return ArtifactRef{}, fmt.Errorf("%w: fresh baseline control failed", ErrUnavailable)
		}
		return r.Vault.put(ctx, c.TenantID, "replay-batch", batch, true)
	})
	if err != nil {
		return c, err
	}
	var baseline replayBatch
	if err := r.Vault.read(ctx, c.TenantID, baseRef, "replay-batch", true, &baseline); err != nil {
		return c, err
	}
	reportRef, err := worker.step(ctx, c, Digest(plan), "runtime-observation", func() (ArtifactRef, error) {
		nonce, err := replayNonce()
		if err != nil {
			return ArtifactRef{}, err
		}
		actual := replayBatch{Run: ObjectRef{Kind: "isolated_task_batch", ID: "adopted-task:" + nonce}}
		for _, task := range plan.Tasks {
			p, err := r.pointer(ctx, c.TenantID, plan.RuntimeID)
			if err != nil || p.Snapshot != action.After {
				return ArtifactRef{}, ErrConflict
			}
			run, snapshot, err := r.Execute(ctx, c.TenantID, plan.RuntimeID, task)
			if err != nil {
				return ArtifactRef{}, err
			}
			if snapshot != action.After {
				return ArtifactRef{}, ErrConflict
			}
			actual.Executions = append(actual.Executions, run)
		}
		report := runtimeObservation{Plan: plan, Action: c.Release.Targets[0].Receipt, Snapshot: action.After, Baseline: baseline, Actual: actual}
		report.Outcome, err = r.scoreObservation(c, frozen.Spec, proof, report)
		if err != nil {
			return ArtifactRef{}, err
		}
		return r.Vault.put(ctx, c.TenantID, "runtime-observation", report, true)
	})
	if err != nil {
		return c, err
	}
	var report runtimeObservation
	if err := r.Vault.read(ctx, c.TenantID, reportRef, "runtime-observation", true, &report); err != nil {
		return c, err
	}
	outcome := report.Outcome
	outcome.Report = reportRef
	to := Verified
	if !outcomePassed(c.ChangeSet.Goal, outcome) {
		to = RollbackRequired
	}
	t := runtimeCommand(c, plan, to)
	t.Outcome = &outcome
	if err := r.Verify(ctx, c, t); err != nil {
		return c, err
	}
	var result Case
	err = r.Store.core.WithTx(ctx, func(tx db.Tx) error {
		current, err := r.lockedCase(ctx, tx, c)
		if err != nil {
			return err
		}
		key := caseKey(plan.TenantID, plan.RuntimeID)
		if err := db.LockRecordKeyTx(ctx, tx, sourceRuntimeTable, key); err != nil {
			return err
		}
		record, exists, err := db.GetRecordTx(ctx, tx, sourceRuntimeTable, key)
		if err != nil {
			return err
		}
		pointer, decodeErr := decode[runtimePointer](record)
		if !exists || decodeErr != nil || pointer.Snapshot != action.After {
			return ErrConflict
		}
		result, err = r.commitCaseTx(ctx, tx, current, t)
		return err
	})
	return result, err
}

func (r *SourceRuntime) Rollback(ctx context.Context, plan RuntimePlan, grant RuntimeGrant) (Case, error) {
	c, frozen, proof, err := r.resolve(ctx, plan.TenantID, plan.CaseID)
	if err != nil {
		return c, err
	}
	if c.Release == nil || c.Outcome == nil || c.Release.ID != runtimeReleaseID(plan) || outcomePassed(c.ChangeSet.Goal, *c.Outcome) {
		return c, ErrConflict
	}
	if err := r.Authority.verify(plan, grant, r.Store.now(), true); err != nil {
		return c, err
	}
	if c.State == RolledBack {
		return c, r.Verify(ctx, c, Transition{To: RolledBack, Rollback: c.Rollback})
	}
	if c.State != RollbackRequired {
		return c, ErrConflict
	}
	if err := r.Verify(ctx, c, Transition{To: RollbackRequired, Outcome: c.Outcome}); err != nil {
		return c, err
	}
	activation, err := r.verifyActivation(ctx, c, *c.Release, frozen.Spec, proof)
	if err != nil {
		return c, err
	}
	baseline, err := r.snapshot(ctx, c.TenantID, activation.Before)
	if err != nil {
		return c, err
	}
	candidate, err := r.snapshot(ctx, c.TenantID, activation.After)
	if err != nil {
		return c, err
	}
	c, err = r.claim(ctx, c, plan)
	if err != nil {
		return c, err
	}
	var result Case
	err = r.Store.core.WithTx(ctx, func(tx db.Tx) error {
		current, err := r.lockedCase(ctx, tx, c)
		if err != nil {
			return err
		}
		if err := r.Authority.verify(plan, grant, r.Store.now(), true); err != nil {
			return err
		}
		key := caseKey(plan.TenantID, plan.RuntimeID)
		if err := db.LockRecordKeyTx(ctx, tx, sourceRuntimeTable, key); err != nil {
			return err
		}
		record, exists, err := db.GetRecordTx(ctx, tx, sourceRuntimeTable, key)
		if err != nil {
			return err
		}
		pointer, decodeErr := decode[runtimePointer](record)
		if !exists || decodeErr != nil || pointer.Snapshot != activation.After || pointer.Revision != candidate.Revision {
			return fmt.Errorf("%w: refusing to overwrite a newer runtime during rollback", ErrConflict)
		}
		baseline.Revision = candidate.Revision + 1
		afterRef, err := r.Vault.putArtifactTx(ctx, tx, c.TenantID, "runtime-snapshot", baseline)
		if err != nil {
			return err
		}
		a := runtimeAction{Plan: plan, Grant: grant, Operation: "rollback", ActionRunID: "source-action:rollback:" + Digest(plan), Before: activation.After, After: afterRef, CompletedAt: r.Store.now().UTC()}
		actionRef, err := r.Vault.putArtifactTx(ctx, tx, c.TenantID, "runtime-action", a)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, sourceRuntimeTable, key, encode(runtimePointer{Snapshot: afterRef, Revision: baseline.Revision})); err != nil {
			return err
		}
		t := runtimeCommand(current, plan, RolledBack)
		t.Rollback = &Rollback{ReleaseID: c.Release.ID, Reason: "Fresh post-adoption task evaluation failed the frozen goal or constraints", Targets: targetReceipts(c, a, actionRef)}
		result, err = r.commitCaseTx(ctx, tx, current, t)
		return err
	})
	return result, err
}

// Followup opens the next round with current runtime versions and bounded
// evidence links. It does not inject the accumulated history into a model.
func (r *SourceRuntime) Followup(ctx context.Context, tenant, caseID string) (Case, error) {
	c, _, _, err := r.resolve(ctx, tenant, caseID)
	if err != nil {
		return Case{}, err
	}
	if !terminal(c.State) || c.Release == nil || c.Outcome == nil {
		return Case{}, ErrConflict
	}
	a, err := r.action(ctx, c, c.Release.Targets, "activate")
	if err != nil {
		return Case{}, err
	}
	ref := a.After
	if c.State == RolledBack {
		rollback, err := r.action(ctx, c, c.Rollback.Targets, "rollback")
		if err != nil {
			return Case{}, err
		}
		ref = rollback.After
	}
	snapshot, err := r.snapshot(ctx, tenant, ref)
	if err != nil {
		return Case{}, err
	}
	observation := Observation{ID: "source-feedback:" + Digest([]string{c.ID, c.Outcome.Report.SHA256, Digest(snapshot)}), TenantID: tenant, Task: c.Observation.Task, Run: c.Outcome.Runs[1], Trigger: "runtime_feedback", Summary: "Post-adoption task feedback; recorded runtime versions are the baseline for a new admitted experiment", EvidenceIDs: []string{c.Outcome.Report.ID}, Dependencies: snapshot.Resources, ParentCaseID: c.ID, OccurredAt: c.UpdatedAt, SourceAccessRef: c.Observation.SourceAccessRef, Metrics: map[string]float64{"task_gain": c.Outcome.Gain}}
	return r.Store.Observe(ctx, observation)
}

func (r *SourceRuntime) Cycle(ctx context.Context, plan RuntimePlan, grant RuntimeGrant) (RuntimeCycleResult, error) {
	var result RuntimeCycleResult
	c, err := r.Activate(ctx, plan, grant)
	if err != nil {
		return result, err
	}
	if c.State == Monitoring {
		c, err = r.Observe(ctx, plan)
		if err != nil {
			return result, err
		}
	}
	if c.State == RollbackRequired {
		c, err = r.Rollback(ctx, plan, grant)
		if err != nil {
			return result, err
		}
	}
	if c.State != Verified && c.State != RolledBack {
		return result, ErrConflict
	}
	result.Case = c
	result.Case.Lease = nil
	result.Active, err = r.Active(ctx, plan.TenantID, plan.RuntimeID)
	if err != nil {
		return result, err
	}
	result.Followup, err = r.Followup(ctx, plan.TenantID, plan.CaseID)
	return result, err
}

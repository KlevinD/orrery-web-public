package improvement

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"orrery_v2/internal/platform/db"
)

const sourceRuntimeTable = "ontology_improvement_source_runtimes"

// putArtifactTx lets the owning runtime commit the active pointer, sealed
// action receipt, Case revision and ontology outbox together. No filesystem or
// external service write is disguised as part of this database transaction.
func (v *ArtifactVault) putArtifactTx(ctx context.Context, tx db.Tx, tenant, kind string, body any) (ArtifactRef, error) {
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > 8<<20 {
		return ArtifactRef{}, ErrInvalid
	}
	a := storedArtifact{TenantID: tenant, Kind: kind, Body: raw}
	a.Seal = v.signature(a)
	ref := ArtifactRef{ID: "improvement-artifact:" + Digest(a), SHA256: Digest(a)}
	key := tenantPrefix(tenant) + ref.SHA256
	created, err := db.InsertRecordTx(ctx, tx, artifactTable, key, encode(a))
	if err != nil || created {
		return ref, err
	}
	record, exists, err := db.GetRecordTx(ctx, tx, artifactTable, key)
	if err != nil {
		return ref, err
	}
	got, err := decode[storedArtifact](record)
	if !exists || err != nil || Digest(got) != ref.SHA256 {
		return ref, ErrConflict
	}
	return ref, nil
}

func (r *SourceRuntime) snapshot(ctx context.Context, tenant string, ref ArtifactRef) (RuntimeSnapshot, error) {
	var s RuntimeSnapshot
	err := r.Vault.read(ctx, tenant, ref, "runtime-snapshot", true, &s)
	if err == nil && (s.TenantID != tenant || !textOK(s.ID, 256) || s.Revision < 1 || validateSources(s.Sources) != nil) {
		err = ErrInvalid
	}
	return s, err
}

func (r *SourceRuntime) pointer(ctx context.Context, tenant, id string) (runtimePointer, error) {
	if err := r.ready(); err != nil {
		return runtimePointer{}, err
	}
	if !validScope(tenant, id) {
		return runtimePointer{}, ErrInvalid
	}
	record, exists, err := r.Store.core.GetRecord(ctx, sourceRuntimeTable, caseKey(tenant, id))
	if err != nil {
		return runtimePointer{}, err
	}
	if !exists {
		return runtimePointer{}, ErrNotFound
	}
	return decode[runtimePointer](record)
}

func (r *SourceRuntime) Active(ctx context.Context, tenant, id string) (RuntimeSnapshot, error) {
	p, err := r.pointer(ctx, tenant, id)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	s, err := r.snapshot(ctx, tenant, p.Snapshot)
	if err == nil && (s.ID != id || s.Revision != p.Revision) {
		err = ErrConflict
	}
	return s, err
}

func runtimeCommand(c Case, plan RuntimePlan, to State) Transition {
	return Transition{ID: "source-runtime:" + string(to) + ":" + Digest(plan), TenantID: c.TenantID, CaseID: c.ID, ExpectedRevision: c.Revision, LeaseToken: c.Lease.Token, Actor: ObjectRef{Kind: "source_runtime", ID: c.Lease.Owner}, To: to}
}

func (r *SourceRuntime) claim(ctx context.Context, c Case, plan RuntimePlan) (Case, error) {
	return r.Store.Claim(ctx, c.TenantID, c.ID, "source-runtime:"+Digest(plan), c.Revision, 10*time.Minute)
}

func (r *SourceRuntime) lockedCase(ctx context.Context, tx db.Tx, claimed Case) (Case, error) {
	key := caseKey(claimed.TenantID, claimed.ID)
	if err := db.LockRecordKeyTx(ctx, tx, caseTable, key); err != nil {
		return Case{}, err
	}
	record, exists, err := db.GetRecordTx(ctx, tx, caseTable, key)
	if err != nil {
		return Case{}, err
	}
	if !exists {
		return Case{}, ErrNotFound
	}
	c, err := decode[Case](record)
	if err != nil {
		return Case{}, err
	}
	if claimed.Lease == nil {
		return Case{}, ErrLease
	}
	err = checkLease(c, Transition{ExpectedRevision: claimed.Revision, LeaseToken: claimed.Lease.Token}, r.Store.now())
	return c, err
}

func (r *SourceRuntime) commitCaseTx(ctx context.Context, tx db.Tx, c Case, t Transition) (Case, error) {
	next, err := applyTransition(c, t)
	if err != nil {
		return c, err
	}
	next.Revision++
	next.UpdatedAt = r.Store.now().UTC()
	if err := persistCase(ctx, tx, next, true); err != nil {
		return c, err
	}
	created, err := db.InsertRecordTx(ctx, tx, commandTable, commandKey(t), encode(commandReceipt{RequestHash: Digest(t), Request: t, Result: next}))
	if err == nil && !created {
		err = ErrConflict
	}
	return next, err
}

func baseSnapshot(plan RuntimePlan, c Case, spec ReplaySpec) RuntimeSnapshot {
	return RuntimeSnapshot{TenantID: c.TenantID, ID: plan.RuntimeID, Task: c.Observation.Task, Revision: 1, Sources: spec.Sources, Resources: c.ChangeSet.Baseline, Policy: spec.Policy}
}

func candidateSnapshot(before RuntimeSnapshot, c Case, proof replayProof, spec ReplaySpec) (RuntimeSnapshot, error) {
	next := privateCopy(before)
	var err error
	next.Sources, err = proposalSources(spec, proof.Proposal)
	if err != nil {
		return next, err
	}
	for i, version := range next.Resources {
		for _, change := range c.ChangeSet.Changes {
			if ResourceKey(change.Baseline) == ResourceKey(version) {
				next.Resources[i] = change.Candidate
			}
		}
	}
	next.Revision++
	return next, nil
}

func sameRuntimeMaterial(a, b RuntimeSnapshot) bool {
	a.Revision, b.Revision = 0, 0
	a.ReleaseID, b.ReleaseID = "", ""
	return reflect.DeepEqual(a, b)
}

func targetReceipts(c Case, action runtimeAction, ref ArtifactRef) []TargetReceipt {
	var receipts []TargetReceipt
	for _, change := range c.ChangeSet.Changes {
		version := change.Candidate
		if action.Operation == "rollback" {
			version = change.Baseline
		}
		receipts = append(receipts, TargetReceipt{Version: version, ActionRunID: action.ActionRunID, Receipt: ref})
	}
	return receipts
}

func runtimeReleaseID(p RuntimePlan) string { return "source-release:" + Digest(p) }

func (r *SourceRuntime) Activate(ctx context.Context, input RuntimePlan, grant RuntimeGrant) (Case, error) {
	plan := privateCopy(input)
	c, frozen, proof, err := r.resolve(ctx, plan.TenantID, plan.CaseID)
	if err != nil {
		return c, err
	}
	if needsRuntimeBaseline(c) && c.Release == nil {
		return c, fmt.Errorf("%w: source runtime cannot adopt a production task baseline", ErrUnavailable)
	}
	if err := r.validatePlan(plan, c, frozen.Spec); err != nil {
		return c, err
	}
	if err := r.Authority.verify(plan, grant, r.Store.now(), c.Release == nil); err != nil {
		return c, err
	}
	if c.Release != nil {
		if c.Release.ID != runtimeReleaseID(plan) {
			return c, ErrConflict
		}
		return c, r.Verify(ctx, c, Transition{To: Monitoring, Release: c.Release})
	}
	if c.State != Evaluated {
		return c, ErrConflict
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
		before := baseSnapshot(plan, current, frozen.Spec)
		var beforeRef ArtifactRef
		if exists {
			pointer, err := decode[runtimePointer](record)
			if err != nil {
				return err
			}
			// Read through the transaction to support single-connection stores.
			beforeRef = pointer.Snapshot
			before, err = r.snapshotTx(ctx, tx, plan.TenantID, beforeRef)
			if err != nil || before.Revision != pointer.Revision || !sameRuntimeMaterial(before, baseSnapshot(plan, current, frozen.Spec)) {
				return fmt.Errorf("%w: active baseline drifted", ErrConflict)
			}
		} else {
			if !plan.AllowBootstrap {
				return fmt.Errorf("%w: runtime bootstrap was not authorized", ErrNotFound)
			}
			beforeRef, err = r.Vault.putArtifactTx(ctx, tx, plan.TenantID, "runtime-snapshot", before)
			if err != nil {
				return err
			}
		}
		after, err := candidateSnapshot(before, current, proof, frozen.Spec)
		if err != nil {
			return err
		}
		after.ReleaseID = runtimeReleaseID(plan)
		afterRef, err := r.Vault.putArtifactTx(ctx, tx, plan.TenantID, "runtime-snapshot", after)
		if err != nil {
			return err
		}
		action := runtimeAction{Plan: plan, Grant: grant, Operation: "activate", ActionRunID: "source-action:activate:" + Digest(plan), Before: beforeRef, After: afterRef, CompletedAt: r.Store.now().UTC()}
		actionRef, err := r.Vault.putArtifactTx(ctx, tx, plan.TenantID, "runtime-action", action)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, sourceRuntimeTable, key, encode(runtimePointer{Snapshot: afterRef, Revision: after.Revision})); err != nil {
			return err
		}
		t := runtimeCommand(current, plan, Monitoring)
		t.Release = &Release{ID: after.ReleaseID, EvaluationID: current.Evaluation.ID, ChangeSetHash: Digest(current.ChangeSet), Targets: targetReceipts(current, action, actionRef)}
		result, err = r.commitCaseTx(ctx, tx, current, t)
		return err
	})
	return result, err
}

func (r *SourceRuntime) snapshotTx(ctx context.Context, tx db.Tx, tenant string, ref ArtifactRef) (RuntimeSnapshot, error) {
	var s RuntimeSnapshot
	record, exists, err := db.GetRecordTx(ctx, tx, artifactTable, tenantPrefix(tenant)+ref.SHA256)
	if err != nil {
		return s, err
	}
	if !exists {
		return s, ErrNotFound
	}
	a, err := decode[storedArtifact](record)
	if err != nil || a.TenantID != tenant || a.Kind != "runtime-snapshot" || Digest(a) != ref.SHA256 || ref.ID != "improvement-artifact:"+ref.SHA256 || a.Seal != r.Vault.signature(a) {
		return s, ErrInvalid
	}
	err = json.Unmarshal(a.Body, &s)
	return s, err
}

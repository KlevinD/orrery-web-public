package improvement

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

// ReplayVerifier accepts only host-sealed experiment evidence, resolves all
// frozen inputs and recomputes scores. It cannot authorize resource adoption,
// rollback or post-release confirmation; those need domain-specific verifiers.
type ReplayVerifier struct{ vault *ArtifactVault }

func NewReplayVerifier(vault *ArtifactVault) *ReplayVerifier { return &ReplayVerifier{vault: vault} }

func unsealedRef(tenant, kind string, body any) ArtifactRef {
	raw, _ := json.Marshal(body)
	hash := Digest(storedArtifact{TenantID: tenant, Kind: kind, Body: raw})
	return ArtifactRef{ID: "improvement-artifact:" + hash, SHA256: hash}
}

func taskPassed(task ReplayTask, run TaskExecution) bool {
	if run.ExitCode != 0 || run.TimedOut || run.Truncated {
		return false
	}
	want, err := canonicalJSON(task.Expected)
	if err != nil {
		return false
	}
	got, err := canonicalJSON([]byte(run.Output))
	return err == nil && got == want
}

func baselineControlPassed(spec ReplaySpec, batch replayBatch) bool {
	if len(batch.Executions) != len(spec.Tasks) {
		return false
	}
	for i, task := range spec.Tasks {
		if task.Split == "regression" && taskPassed(task, batch.Executions[i]) {
			return true
		}
	}
	return false
}

func validateBatch(spec ReplaySpec, sources []SourceFile, batch replayBatch, containers map[string]bool) error {
	if !objectOK(batch.Run) || batch.Run.Kind != "isolated_task_batch" || len(batch.Executions) != len(spec.Tasks) {
		return ErrInvalid
	}
	for i, run := range batch.Executions {
		task := spec.Tasks[i]
		inputHash, _ := canonicalJSON(task.Input)
		if run.TaskID != task.ID || run.InputHash != inputHash || run.SourceHash != Digest(sources) || run.PolicyHash != Digest(spec.Policy) || !hashOK(run.ContainerID) || containers[run.ContainerID] || run.DurationMS < 0 || run.DurationMS > spec.Policy.TimeoutMS+15000 || run.ExitCode < 0 || run.ExitCode > 255 || len(run.Output) > spec.Policy.MaxOutputBytes || len(run.Stderr) > spec.Policy.MaxOutputBytes {
			return fmt.Errorf("%w: actual task execution does not match the frozen arm, input or isolation policy", ErrInvalid)
		}
		containers[run.ContainerID] = true
	}
	return nil
}

func evaluateReplay(spec ReplaySpec, proof replayProof, experiment Experiment) (Evaluation, error) {
	sources, err := proposalSources(spec, proof.Proposal)
	if err != nil {
		return Evaluation{}, err
	}
	containers := map[string]bool{}
	if err := validateBatch(spec, spec.Sources, proof.Baseline, containers); err != nil {
		return Evaluation{}, err
	}
	if err := validateBatch(spec, sources, proof.Candidate, containers); err != nil {
		return Evaluation{}, err
	}
	if proof.Baseline.Run == proof.Candidate.Run {
		return Evaluation{}, ErrInvalid
	}
	if !baselineControlPassed(spec, proof.Baseline) {
		return Evaluation{}, fmt.Errorf("%w: experiment lacks a passing baseline control", ErrInvalid)
	}
	e := Evaluation{ID: "code-evaluation:" + experiment.Result.SHA256, ExperimentID: experiment.ID, ChangeSetHash: proof.ChangeSetHash, Cost: spec.Policy.ModelCallUnits, HeldOutPassed: true, BusinessConstraintsPassed: true, SafetyPassed: true}
	for i, task := range spec.Tasks {
		baseline, candidate := proof.Baseline.Executions[i], proof.Candidate.Executions[i]
		b, c := taskPassed(task, baseline), taskPassed(task, candidate)
		if b {
			e.BaselineScore++
		}
		if c {
			e.CandidateScore++
		}
		if b && !c {
			e.RegressionCount++
		}
		if !c && task.Split == "held_out" {
			e.HeldOutPassed = false
		}
		if !c && task.Business {
			e.BusinessConstraintsPassed = false
		}
		if !c && task.Safety {
			e.SafetyPassed = false
		}
		e.Cost += 2 * spec.Policy.TaskRunUnits
		e.DurationMS += baseline.DurationMS + candidate.DurationMS
	}
	e.BaselineScore /= float64(len(spec.Tasks))
	e.CandidateScore /= float64(len(spec.Tasks))
	return e, nil
}

func (v *ReplayVerifier) resolve(ctx context.Context, c Case, experiment Experiment) (replayPlan, replayProof, error) {
	var plan replayPlan
	var proof replayProof
	if v == nil || v.vault == nil || c.ChangeSet == nil {
		return plan, proof, ErrUnavailable
	}
	tenant, cs := c.TenantID, *c.ChangeSet
	if err := v.vault.read(ctx, tenant, experiment.Manifest, "replay-plan", true, &plan); err != nil {
		return plan, proof, err
	}
	if err := v.vault.read(ctx, tenant, experiment.Result, "replay-proof", true, &proof); err != nil {
		return plan, proof, err
	}
	spec := plan.Spec
	if err := validateReplaySpec(spec); err != nil {
		return plan, proof, err
	}
	if proof.Protocol != replayProtocol || proof.TenantID != tenant || proof.CaseID != c.ID || proof.ChangeSetHash != Digest(cs) || proof.Plan != experiment.Manifest || proof.Baseline.Run != experiment.BaselineRun || proof.Candidate.Run != experiment.CandidateRun || experiment.ID != "code-experiment:"+experiment.Result.SHA256 || experiment.ChangeSetHash != Digest(cs) || plan.SpecHash != Digest(spec) || !reflect.DeepEqual(spec.Observation, c.Observation) {
		return plan, proof, fmt.Errorf("%w: experiment provenance binding failed", ErrInvalid)
	}
	var tasks []ReplayTask
	var policy ReplayPolicy
	var generator GeneratorIdentity
	var evaluator []string
	if err := v.vault.read(ctx, tenant, plan.Dataset, "task-dataset", false, &tasks); err != nil {
		return plan, proof, err
	}
	if err := v.vault.read(ctx, tenant, plan.Policy, "replay-policy", false, &policy); err != nil {
		return plan, proof, err
	}
	if err := v.vault.read(ctx, tenant, plan.Generator, "generator-identity", false, &generator); err != nil {
		return plan, proof, err
	}
	if err := v.vault.read(ctx, tenant, plan.Evaluator, "evaluator-identity", false, &evaluator); err != nil {
		return plan, proof, err
	}
	if !reflect.DeepEqual(tasks, spec.Tasks) || !reflect.DeepEqual(policy, spec.Policy) || generator != spec.Generator || !reflect.DeepEqual(evaluator, []string{replayOracle, spec.ExecutorSHA256}) {
		return plan, proof, ErrInvalid
	}
	if _, err := proposalSources(spec, proof.Proposal); err != nil {
		return plan, proof, err
	}
	expected := ChangeSet{ID: "code-change:" + Digest(proof.Proposal), Goal: spec.Goal, Budget: spec.Budget, Dataset: plan.Dataset, Policy: plan.Policy, Generator: plan.Generator, Evaluator: plan.Evaluator}
	for _, target := range spec.Targets {
		expected.Baseline = append(expected.Baseline, target.Resource)
	}
	for _, edit := range proof.Proposal.Edits {
		for _, target := range spec.Targets {
			if edit.Target != ResourceKey(target.Resource) {
				continue
			}
			patch := SourceFile{Path: target.Path, Content: edit.Content}
			var rollback SourceFile
			for _, file := range spec.Sources {
				if file.Path == target.Path {
					rollback = file
				}
			}
			candidate := target.Resource
			candidate.SHA256 = Digest(edit.Content)
			candidate.Version = "candidate:" + candidate.SHA256
			change := TargetChange{Baseline: target.Resource, Candidate: candidate, Patch: unsealedRef(tenant, "source-file", patch), Rollback: unsealedRef(tenant, "source-file", rollback)}
			var frozenPatch, frozenRollback SourceFile
			if err := v.vault.read(ctx, tenant, change.Patch, "source-file", false, &frozenPatch); err != nil {
				return plan, proof, err
			}
			if err := v.vault.read(ctx, tenant, change.Rollback, "source-file", false, &frozenRollback); err != nil {
				return plan, proof, err
			}
			if frozenPatch != patch || frozenRollback != rollback {
				return plan, proof, ErrInvalid
			}
			expected.Changes = append(expected.Changes, change)
		}
	}
	if !reflect.DeepEqual(expected, cs) {
		return plan, proof, fmt.Errorf("%w: changes differ from executed sources", ErrInvalid)
	}
	return plan, proof, nil
}

func (v *ReplayVerifier) Verify(ctx context.Context, c Case, t Transition) error {
	if t.To != Tested && t.To != Evaluated {
		return fmt.Errorf("%w: replay evidence grants no resource-apply authority", ErrUnavailable)
	}
	var experiment Experiment
	if t.To == Tested {
		if t.Experiment == nil {
			return ErrInvalid
		}
		experiment = *t.Experiment
	} else {
		if c.Experiment == nil || t.Evaluation == nil {
			return ErrInvalid
		}
		experiment = *c.Experiment
	}
	plan, proof, err := v.resolve(ctx, c, experiment)
	if err != nil {
		return err
	}
	expected, err := evaluateReplay(plan.Spec, proof, experiment)
	if err != nil {
		return err
	}
	if t.To == Tested {
		return nil
	}
	var report replayReport
	if err := v.vault.read(ctx, c.TenantID, t.Evaluation.Report, "replay-report", true, &report); err != nil {
		return err
	}
	if report.Oracle != replayOracle || report.Result != experiment.Result || report.CostUnit != "bounded_operations" || !reflect.DeepEqual(expected, report.Evaluation) {
		return ErrInvalid
	}
	expected.Report = t.Evaluation.Report
	if !reflect.DeepEqual(expected, *t.Evaluation) {
		return fmt.Errorf("%w: claimed score or pass flag differs from host oracle", ErrInvalid)
	}
	return nil
}

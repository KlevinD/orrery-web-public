package improvement

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"orrery_v2/internal/platform/db"
)

const replayWorkTable = "ontology_improvement_replay_work"

func sourcePathOK(p string) bool {
	return textOK(p, 240) && !strings.ContainsAny(p, "\\:,\n\r") && !strings.HasPrefix(p, "/") && p != "." && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}

func validateSources(files []SourceFile) error {
	if len(files) == 0 || len(files) > 64 {
		return ErrInvalid
	}
	seen, total := map[string]bool{}, 0
	for _, file := range files {
		key := strings.ToLower(file.Path)
		if !sourcePathOK(file.Path) || seen[key] || !utf8.ValidString(file.Content) || strings.ContainsRune(file.Content, 0) || len(file.Content) > 256<<10 {
			return fmt.Errorf("%w: source must be a bounded regular file with a unique relative path", ErrInvalid)
		}
		for other := range seen {
			if strings.HasPrefix(other, key+"/") || strings.HasPrefix(key, other+"/") {
				return ErrInvalid
			}
		}
		seen[key] = true
		total += len(file.Content)
	}
	if total > 1<<20 {
		return ErrInvalid
	}
	return nil
}

func canonicalJSON(raw []byte) (string, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", ErrInvalid
	}
	if decoder.Decode(new(any)) == nil || !json.Valid(raw) {
		return "", ErrInvalid
	}
	return Digest(value), nil
}

func validateReplaySpec(s ReplaySpec) error {
	if err := validateObservation(s.Observation); err != nil {
		return err
	}
	if err := validateSources(s.Sources); err != nil {
		return err
	}
	p, g := s.Policy, s.Goal
	endpoint, endpointErr := url.Parse(s.Generator.Endpoint)
	if endpointErr != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || !textOK(s.Generator.Endpoint, 2048) {
		return fmt.Errorf("%w: a credential-free frozen model endpoint is required", ErrInvalid)
	}
	if !strings.HasPrefix(p.Image, "sha256:") || !hashOK(strings.TrimPrefix(p.Image, "sha256:")) || !strings.HasPrefix(p.Entrypoint, "/") || !textOK(p.Entrypoint, 256) || len(p.Args) > 32 || p.TimeoutMS < 100 || p.TimeoutMS > 120000 || p.ModelTimeoutMS < 100 || p.ModelTimeoutMS > 120000 || p.MemoryMB < 32 || p.MemoryMB > 1024 || !finite(p.CPUs) || p.CPUs <= 0 || p.CPUs > 2 || p.MaxOutputBytes < 128 || p.MaxOutputBytes > 1<<20 || !finite(p.ModelCallUnits) || p.ModelCallUnits <= 0 || !finite(p.TaskRunUnits) || p.TaskRunUnits <= 0 {
		return fmt.Errorf("%w: pinned image, executable and bounded execution policy are required", ErrInvalid)
	}
	for _, arg := range p.Args {
		if len(arg) > 1024 || strings.ContainsRune(arg, 0) {
			return ErrInvalid
		}
	}
	if g.Metric != "task_pass_rate" || g.Direction != "higher" || !finite(g.MinimumGain) || g.MinimumGain <= 0 || g.MinimumGain > 1 || !finite(g.MaxCost) || g.MaxCost <= 0 || g.MaxDurationMS <= 0 || !finite(s.Budget) || s.Budget <= 0 || len(s.Targets) == 0 || len(s.Targets) > 16 || len(s.Tasks) < 3 || len(s.Tasks) > 16 || !textOK(s.Generator.ProfileID, 256) || !textOK(s.Generator.Model, 256) || !textOK(s.Generator.Version, 256) || !hashOK(s.Generator.PromptSHA256) || !hashOK(s.ExecutorSHA256) {
		return ErrInvalid
	}
	maxUnits := p.ModelCallUnits + 2*float64(len(s.Tasks))*p.TaskRunUnits
	if len(s.Tasks)*4*p.MaxOutputBytes > 256<<10 {
		return fmt.Errorf("%w: aggregate task output exceeds the bounded experiment evidence budget", ErrInvalid)
	}
	if maxUnits > s.Budget || maxUnits > g.MaxCost || 2*int64(len(s.Tasks))*p.TimeoutMS+p.ModelTimeoutMS > 8*60*1000 {
		return fmt.Errorf("%w: full experiment must fit its frozen operation budget and worker lease", ErrInvalid)
	}
	files, targets, paths := map[string]SourceFile{}, map[string]bool{}, map[string]bool{}
	for _, f := range s.Sources {
		files[f.Path] = f
	}
	for _, target := range s.Targets {
		f, exists := files[target.Path]
		key := ResourceKey(target.Resource)
		if !exists || !versionOK(target.Resource, true) || target.Resource.SHA256 != Digest(f.Content) || targets[key] || paths[target.Path] {
			return fmt.Errorf("%w: each target must bind exactly one frozen source file", ErrInvalid)
		}
		targets[key], paths[target.Path] = true, true
	}
	for _, dep := range s.Observation.Dependencies {
		found := false
		for _, target := range s.Targets {
			v := target.Resource
			found = found || (ResourceKey(dep) == ResourceKey(v) && dep.Version == v.Version && (dep.SHA256 == "" || dep.SHA256 == v.SHA256))
		}
		if !found {
			return fmt.Errorf("%w: this replay cannot resolve an observed dependency", ErrUnavailable)
		}
	}
	seen, inputs := map[string]bool{}, map[string]bool{}
	splits, business, safety := map[string]int{}, 0, 0
	for _, task := range s.Tasks {
		inputHash, err := canonicalJSON(task.Input)
		if err != nil || len(task.Input) > 64<<10 || len(task.Expected) > 64<<10 || !textOK(task.ID, 256) || seen[task.ID] || inputs[inputHash] {
			return fmt.Errorf("%w: task IDs and inputs must be unique across splits", ErrInvalid)
		}
		if _, err := canonicalJSON(task.Expected); err != nil {
			return err
		}
		if task.Split != "repair" && task.Split != "regression" && task.Split != "held_out" {
			return ErrInvalid
		}
		seen[task.ID], inputs[inputHash] = true, true
		splits[task.Split]++
		if task.Business {
			business++
		}
		if task.Safety {
			safety++
		}
	}
	if splits["repair"] == 0 || splits["regression"] == 0 || splits["held_out"] == 0 || business == 0 || safety == 0 {
		return fmt.Errorf("%w: repair, regression, held-out, business and safety oracles are required", ErrInvalid)
	}
	return nil
}

func replayNonce() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// step reserves an external operation before calling a model or runner. A
// process death between reservation and receipt is deliberately indeterminate:
// a retry cannot silently issue another paid call or invent a completed run.
func (w *ReplayCoordinator) step(ctx context.Context, c Case, specHash, stage string, execute func() (ArtifactRef, error)) (ArtifactRef, error) {
	key := caseKey(c.TenantID, c.ID) + "/" + stage
	var checkpoint replayCheckpoint
	created := false
	err := w.Store.core.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, caseTable, caseKey(c.TenantID, c.ID)); err != nil {
			return err
		}
		record, exists, err := db.GetRecordTx(ctx, tx, caseTable, caseKey(c.TenantID, c.ID))
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		current, err := decode[Case](record)
		if err != nil {
			return err
		}
		if current.Revision != c.Revision || current.Lease == nil || c.Lease == nil || current.Lease.Token != c.Lease.Token || !current.Lease.ExpiresAt.After(w.Store.now()) {
			return ErrLease
		}
		record, exists, err = db.GetRecordTx(ctx, tx, replayWorkTable, key)
		if err != nil {
			return err
		}
		if exists {
			checkpoint, err = decode[replayCheckpoint](record)
			if err != nil {
				return err
			}
			if checkpoint.SpecHash != specHash {
				return ErrConflict
			}
			if !artifactOK(checkpoint.Result) {
				return fmt.Errorf("%w: %s has an indeterminate reserved operation; do not automatically repeat it", ErrUnavailable, stage)
			}
			return nil
		}
		checkpoint = replayCheckpoint{SpecHash: specHash, StartedAt: w.Store.now().UTC()}
		created, err = db.InsertRecordTx(ctx, tx, replayWorkTable, key, encode(checkpoint))
		return err
	})
	if err != nil {
		return ArtifactRef{}, err
	}
	if !created {
		return checkpoint.Result, nil
	}
	ref, err := execute()
	if err != nil {
		return ArtifactRef{}, err
	}
	err = w.Store.core.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, caseTable, caseKey(c.TenantID, c.ID)); err != nil {
			return err
		}
		record, exists, err := db.GetRecordTx(ctx, tx, caseTable, caseKey(c.TenantID, c.ID))
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		current, err := decode[Case](record)
		if err != nil {
			return err
		}
		if current.Revision != c.Revision || current.Lease == nil || current.Lease.Token != c.Lease.Token || !current.Lease.ExpiresAt.After(w.Store.now()) {
			return ErrLease
		}
		checkpoint.Result = ref
		return db.PutRecordTx(ctx, tx, replayWorkTable, key, encode(checkpoint))
	})
	return ref, err
}

func (w *ReplayCoordinator) batch(ctx context.Context, tenant string, sources []SourceFile, spec ReplaySpec) (replayBatch, error) {
	nonce, err := replayNonce()
	if err != nil {
		return replayBatch{}, err
	}
	batch := replayBatch{Run: ObjectRef{Kind: "isolated_task_batch", ID: "task-replay:" + nonce}}
	for _, task := range spec.Tasks {
		run, err := w.Runner.Run(ctx, privateCopy(sources), spec.Policy, privateCopy(task))
		if err != nil {
			return replayBatch{}, err
		}
		batch.Executions = append(batch.Executions, run)
	}
	return batch, nil
}

func proposalSources(spec ReplaySpec, proposal CodeProposal) ([]SourceFile, error) {
	if !textOK(proposal.Diagnosis, 1000) || len(proposal.Edits) == 0 || len(proposal.Edits) > len(spec.Targets) {
		return nil, ErrInvalid
	}
	sources := privateCopy(spec.Sources)
	targets := map[string]ReplayTarget{}
	for _, target := range spec.Targets {
		targets[ResourceKey(target.Resource)] = target
	}
	seen := map[string]bool{}
	for _, edit := range proposal.Edits {
		target, exists := targets[edit.Target]
		if !exists || seen[edit.Target] || Digest(edit.Content) == target.Resource.SHA256 {
			return nil, fmt.Errorf("%w: candidate must change only admitted targets", ErrInvalid)
		}
		seen[edit.Target] = true
		for i := range sources {
			if sources[i].Path == target.Path {
				sources[i].Content = edit.Content
			}
		}
	}
	return sources, validateSources(sources)
}

func (w *ReplayCoordinator) changeSet(ctx context.Context, plan replayPlan, proposal CodeProposal) (ChangeSet, error) {
	spec, tenant := plan.Spec, plan.Spec.Observation.TenantID
	cs := ChangeSet{ID: "code-change:" + Digest(proposal), Goal: spec.Goal, Budget: spec.Budget, Dataset: plan.Dataset, Policy: plan.Policy, Generator: plan.Generator, Evaluator: plan.Evaluator}
	for _, target := range spec.Targets {
		cs.Baseline = append(cs.Baseline, target.Resource)
	}
	for _, edit := range proposal.Edits {
		for _, target := range spec.Targets {
			if edit.Target != ResourceKey(target.Resource) {
				continue
			}
			patch, err := w.Vault.put(ctx, tenant, "source-file", SourceFile{Path: target.Path, Content: edit.Content}, false)
			if err != nil {
				return cs, err
			}
			var rollback ArtifactRef
			for _, source := range spec.Sources {
				if source.Path == target.Path {
					rollback, err = w.Vault.put(ctx, tenant, "source-file", source, false)
				}
			}
			if err != nil {
				return cs, err
			}
			candidate := target.Resource
			candidate.SHA256 = Digest(edit.Content)
			candidate.Version = "candidate:" + candidate.SHA256
			cs.Changes = append(cs.Changes, TargetChange{Baseline: target.Resource, Candidate: candidate, Patch: patch, Rollback: rollback})
		}
	}
	return cs, validateChangeSet(cs)
}

func (w *ReplayCoordinator) command(c Case, to State) Transition {
	return Transition{ID: "code-replay:" + string(to), TenantID: c.TenantID, CaseID: c.ID, ExpectedRevision: c.Revision, LeaseToken: c.Lease.Token, Actor: ObjectRef{Kind: "experiment_worker", ID: c.Lease.Owner}, To: to}
}

func (w *ReplayCoordinator) Run(ctx context.Context, input ReplaySpec) (Case, error) {
	if w == nil || w.Store == nil || w.Vault == nil || w.Model == nil || w.Runner == nil {
		return Case{}, ErrUnavailable
	}
	// This runner freezes its admitted source files, not a production task's
	// complete dependency inventory. Reject before a model/container operation.
	if needsRuntimeBaseline(Case{Observation: input.Observation}) {
		return Case{}, fmt.Errorf("%w: source replay cannot resolve a production runtime baseline", ErrUnavailable)
	}
	if err := validateReplaySpec(input); err != nil {
		return Case{}, err
	}
	spec := privateCopy(input)
	if w.Model.Identity() != spec.Generator {
		return Case{}, fmt.Errorf("%w: generator identity does not match admitted plan", ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, 9*time.Minute)
	defer cancel()
	c, err := w.Store.Observe(ctx, spec.Observation)
	if err != nil {
		return c, err
	}
	if terminal(c.State) || c.State == Evaluated {
		return c, w.checkSpec(ctx, c, Digest(spec))
	}
	nonce, err := replayNonce()
	if err != nil {
		return c, err
	}
	c, err = w.Store.Claim(ctx, c.TenantID, c.ID, "code-replay-worker:"+nonce, c.Revision, 10*time.Minute)
	if err != nil {
		return c, err
	}
	tenant, specHash := c.TenantID, Digest(spec)
	planRef, err := w.step(ctx, c, specHash, "plan", func() (ArtifactRef, error) {
		dataset, err := w.Vault.put(ctx, tenant, "task-dataset", spec.Tasks, false)
		if err != nil {
			return ArtifactRef{}, err
		}
		policy, err := w.Vault.put(ctx, tenant, "replay-policy", spec.Policy, false)
		if err != nil {
			return ArtifactRef{}, err
		}
		generator, err := w.Vault.put(ctx, tenant, "generator-identity", spec.Generator, false)
		if err != nil {
			return ArtifactRef{}, err
		}
		evaluator, err := w.Vault.put(ctx, tenant, "evaluator-identity", []string{replayOracle, spec.ExecutorSHA256}, false)
		if err != nil {
			return ArtifactRef{}, err
		}
		return w.Vault.put(ctx, tenant, "replay-plan", replayPlan{SpecHash: specHash, Spec: spec, Dataset: dataset, Policy: policy, Generator: generator, Evaluator: evaluator}, true)
	})
	if err != nil {
		return c, err
	}
	var plan replayPlan
	if err = w.Vault.read(ctx, tenant, planRef, "replay-plan", true, &plan); err != nil {
		return c, err
	}
	baselineRef, err := w.step(ctx, c, specHash, "baseline", func() (ArtifactRef, error) {
		batch, err := w.batch(ctx, tenant, spec.Sources, spec)
		if err != nil {
			return ArtifactRef{}, err
		}
		return w.Vault.put(ctx, tenant, "replay-batch", batch, true)
	})
	if err != nil {
		return c, err
	}
	var baseline replayBatch
	if err = w.Vault.read(ctx, tenant, baselineRef, "replay-batch", true, &baseline); err != nil {
		return c, err
	}
	if err = validateBatch(spec, spec.Sources, baseline, map[string]bool{}); err != nil {
		return c, err
	}
	if !baselineControlPassed(spec, baseline) {
		return c, fmt.Errorf("%w: baseline has no passing regression control; verify the task driver before model generation", ErrUnavailable)
	}
	proposalRef, err := w.step(ctx, c, specHash, "generation", func() (ArtifactRef, error) {
		request := GenerationInput{Summary: spec.Observation.Summary, Sources: spec.Sources, Targets: spec.Targets}
		for i, task := range spec.Tasks {
			if task.Split == "repair" && !taskPassed(task, baseline.Executions[i]) {
				request.Failures = append(request.Failures, task)
			}
		}
		modelCtx, modelCancel := context.WithTimeout(ctx, time.Duration(spec.Policy.ModelTimeoutMS)*time.Millisecond)
		defer modelCancel()
		proposal, err := w.Model.Generate(modelCtx, privateCopy(request))
		if err != nil {
			return ArtifactRef{}, err
		}
		if _, err := proposalSources(spec, proposal); err != nil {
			return ArtifactRef{}, err
		}
		return w.Vault.put(ctx, tenant, "code-proposal", proposal, false)
	})
	if err != nil {
		return c, err
	}
	var proposal CodeProposal
	if err = w.Vault.read(ctx, tenant, proposalRef, "code-proposal", false, &proposal); err != nil {
		return c, err
	}
	if c.State == Observed || c.State == PendingDiagnosis || c.State == Blocked {
		kinds, seen := []TargetKind{}, map[TargetKind]bool{}
		for _, target := range spec.Targets {
			if !seen[target.Resource.Kind] {
				kinds = append(kinds, target.Resource.Kind)
				seen[target.Resource.Kind] = true
			}
		}
		t := w.command(c, Diagnosed)
		evidence := append([]string{baselineRef.ID}, spec.Observation.EvidenceIDs...)
		if len(evidence) > 64 {
			evidence = evidence[:64]
		}
		t.Diagnosis = &Diagnosis{Summary: proposal.Diagnosis, EvidenceIDs: evidence, AffectedKinds: kinds}
		c, err = w.Store.Advance(ctx, t, nil)
		if err != nil {
			return c, err
		}
	}
	cs, err := w.changeSet(ctx, plan, proposal)
	if err != nil {
		return c, err
	}
	if c.State == Diagnosed {
		t := w.command(c, Proposed)
		t.ChangeSet = &cs
		c, err = w.Store.Advance(ctx, t, nil)
		if err != nil {
			return c, err
		}
	}
	if !reflect.DeepEqual(c.ChangeSet, &cs) {
		return c, ErrConflict
	}
	sources, err := proposalSources(spec, proposal)
	if err != nil {
		return c, err
	}
	proofRef, err := w.step(ctx, c, specHash, "candidate", func() (ArtifactRef, error) {
		candidate, err := w.batch(ctx, tenant, sources, spec)
		if err != nil {
			return ArtifactRef{}, err
		}
		return w.Vault.put(ctx, tenant, "replay-proof", replayProof{Protocol: replayProtocol, TenantID: tenant, CaseID: c.ID, ChangeSetHash: Digest(cs), Plan: planRef, Proposal: proposal, Baseline: baseline, Candidate: candidate}, true)
	})
	if err != nil {
		return c, err
	}
	var proof replayProof
	if err = w.Vault.read(ctx, tenant, proofRef, "replay-proof", true, &proof); err != nil {
		return c, err
	}
	verifier := &ReplayVerifier{vault: w.Vault}
	if c.State == Proposed {
		t := w.command(c, Tested)
		t.Experiment = &Experiment{ID: "code-experiment:" + proofRef.SHA256, ChangeSetHash: Digest(cs), BaselineRun: proof.Baseline.Run, CandidateRun: proof.Candidate.Run, Manifest: planRef, Result: proofRef}
		c, err = w.Store.Advance(ctx, t, verifier)
		if err != nil {
			return c, err
		}
	}
	if c.State == Tested {
		eval, err := evaluateReplay(spec, proof, *c.Experiment)
		if err != nil {
			return c, err
		}
		reportRef, err := w.Vault.put(ctx, tenant, "replay-report", replayReport{Oracle: replayOracle, Result: proofRef, CostUnit: "bounded_operations", Evaluation: eval}, true)
		if err != nil {
			return c, err
		}
		eval.Report = reportRef
		t := w.command(c, Evaluated)
		t.Evaluation = &eval
		c, err = w.Store.Advance(ctx, t, verifier)
		if err != nil {
			return c, err
		}
	}
	if !eligible(cs.Goal, *c.Evaluation) {
		t := w.command(c, Rejected)
		t.Reason = "候选未满足冻结的任务收益、留出样本、回归或业务/安全约束"
		return w.Store.Advance(ctx, t, nil)
	}
	return c, nil
}

func (w *ReplayCoordinator) checkSpec(ctx context.Context, c Case, hash string) error {
	record, exists, err := w.Store.core.GetRecord(ctx, replayWorkTable, caseKey(c.TenantID, c.ID)+"/plan")
	if err != nil {
		return err
	}
	if !exists {
		return ErrConflict
	}
	checkpoint, err := decode[replayCheckpoint](record)
	if err != nil {
		return err
	}
	if checkpoint.SpecHash != hash {
		return ErrConflict
	}
	return nil
}

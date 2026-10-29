package improvement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
)

func Digest(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func textOK(v string, max int) bool {
	return v != "" && len(v) <= max && strings.TrimSpace(v) == v && !strings.ContainsRune(v, 0)
}

func hashOK(v string) bool {
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == 32 && strings.ToLower(v) == v
}

func artifactOK(v ArtifactRef) bool { return textOK(v.ID, 512) && hashOK(v.SHA256) }
func objectOK(v ObjectRef) bool     { return textOK(v.Kind, 64) && textOK(v.ID, 512) }
func finite(v float64) bool         { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func kindOK(v TargetKind) bool {
	for _, kind := range TargetKinds() {
		if v == kind {
			return true
		}
	}
	return false
}

func versionOK(v ResourceVersion, frozen bool) bool {
	return kindOK(v.Kind) && textOK(v.ID, 512) && textOK(v.Version, 256) && ((!frozen && v.SHA256 == "") || hashOK(v.SHA256))
}

func idsOK(ids []string) bool {
	if len(ids) == 0 || len(ids) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !textOK(id, 512) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func validateObservation(o Observation) error {
	if !textOK(o.ID, 256) || !textOK(o.TenantID, 256) || !objectOK(o.Task) || !objectOK(o.Run) || !textOK(o.Trigger, 64) || !textOK(o.Summary, 1000) || !idsOK(o.EvidenceIDs) || len(o.Dependencies) > 128 || len(o.RelatedObjects) > 128 || len(o.Metrics) > 32 || (o.ParentCaseID != "" && !textOK(o.ParentCaseID, 256)) {
		return fmt.Errorf("%w: task, run, evidence and bounded observation are required", ErrInvalid)
	}
	if o.SourceAccessRef.TenantID != o.TenantID || !textOK(o.SourceAccessRef.ID, 512) || !textOK(o.SourceAccessRef.Kind, 64) {
		return fmt.Errorf("%w: observation source must belong to its ontology tenant", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, dep := range o.Dependencies {
		if !versionOK(dep, false) || seen[ResourceKey(dep)] {
			return ErrInvalid
		}
		seen[ResourceKey(dep)] = true
	}
	for _, ref := range o.RelatedObjects {
		if !objectOK(ref) {
			return ErrInvalid
		}
	}
	for key, value := range o.Metrics {
		if !textOK(key, 128) || !finite(value) {
			return ErrInvalid
		}
	}
	return nil
}

func validateChangeSet(c ChangeSet) error {
	g := c.Goal
	if !textOK(c.ID, 256) || !textOK(g.Metric, 128) || (g.Direction != "higher" && g.Direction != "lower") || !finite(g.MinimumGain) || g.MinimumGain <= 0 || !finite(g.MaxCost) || g.MaxCost <= 0 || g.MaxDurationMS <= 0 || !finite(c.Budget) || c.Budget <= 0 || !artifactOK(c.Dataset) || !artifactOK(c.Policy) || !artifactOK(c.Generator) || !artifactOK(c.Evaluator) || c.Generator.SHA256 == c.Evaluator.SHA256 || len(c.Baseline) == 0 || len(c.Baseline) > 128 || len(c.Changes) == 0 || len(c.Changes) > 32 {
		return fmt.Errorf("%w: frozen goal, budget, dataset, policy and independent evaluator are required", ErrInvalid)
	}
	baseline := map[string]ResourceVersion{}
	for _, dep := range c.Baseline {
		if !versionOK(dep, true) {
			return ErrInvalid
		}
		key := ResourceKey(dep)
		if _, ok := baseline[key]; ok {
			return fmt.Errorf("%w: duplicate baseline", ErrInvalid)
		}
		baseline[key] = dep
	}
	changes := map[string]TargetChange{}
	for _, change := range c.Changes {
		key := ResourceKey(change.Baseline)
		if !versionOK(change.Baseline, true) || !versionOK(change.Candidate, true) || key != ResourceKey(change.Candidate) || change.Baseline.SHA256 == change.Candidate.SHA256 || change.Baseline.Version == change.Candidate.Version || !reflect.DeepEqual(baseline[key], change.Baseline) || !artifactOK(change.Patch) || !artifactOK(change.Rollback) {
			return fmt.Errorf("%w: changed target must bind a frozen baseline, candidate and rollback", ErrInvalid)
		}
		if _, ok := changes[key]; ok {
			return fmt.Errorf("%w: duplicate changed target", ErrInvalid)
		}
		changes[key] = change
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(key string) bool {
		if visiting[key] {
			return false
		}
		if done[key] {
			return true
		}
		visiting[key] = true
		seen := map[string]bool{}
		for _, dep := range changes[key].DependsOn {
			if dep == key || seen[dep] {
				return false
			}
			seen[dep] = true
			if _, ok := changes[dep]; !ok || !visit(dep) {
				return false
			}
		}
		visiting[key] = false
		done[key] = true
		return true
	}
	for key := range changes {
		if !visit(key) {
			return fmt.Errorf("%w: invalid change dependency graph", ErrInvalid)
		}
	}
	return nil
}

func eligible(g Goal, e Evaluation) bool {
	gain := e.CandidateScore - e.BaselineScore
	if g.Direction == "lower" {
		gain = -gain
	}
	return finite(gain) && gain >= g.MinimumGain && e.Cost <= g.MaxCost && e.DurationMS <= g.MaxDurationMS && e.RegressionCount == 0 && e.HeldOutPassed && e.BusinessConstraintsPassed && e.SafetyPassed
}

func outcomePassed(g Goal, o Outcome) bool {
	return o.Gain >= g.MinimumGain && o.Cost <= g.MaxCost && o.DurationMS <= g.MaxDurationMS && o.RegressionCount == 0 && o.BusinessConstraintsPassed
}

func validateReceipts(c ChangeSet, receipts []TargetReceipt, rollback bool) error {
	if len(receipts) != len(c.Changes) {
		return fmt.Errorf("%w: all changed targets require actual action receipts", ErrInvalid)
	}
	want := map[string]ResourceVersion{}
	for _, change := range c.Changes {
		v := change.Candidate
		if rollback {
			v = change.Baseline
		}
		want[ResourceKey(v)] = v
	}
	for _, receipt := range receipts {
		key := ResourceKey(receipt.Version)
		if !reflect.DeepEqual(want[key], receipt.Version) || !textOK(receipt.ActionRunID, 512) || !artifactOK(receipt.Receipt) {
			return ErrInvalid
		}
		delete(want, key)
	}
	if len(want) != 0 {
		return ErrInvalid
	}
	return nil
}

func applyTransition(c Case, t Transition) (Case, error) {
	if c.Lease == nil || !textOK(t.ID, 256) || !objectOK(t.Actor) || t.Actor.ID != c.Lease.Owner || t.TenantID != c.TenantID || t.CaseID != c.ID || t.ExpectedRevision != c.Revision {
		return c, ErrConflict
	}
	if t.Reason != "" && !textOK(t.Reason, 1000) {
		return c, ErrInvalid
	}
	count := 0
	for _, set := range []bool{t.Diagnosis != nil, t.ChangeSet != nil, t.Experiment != nil, t.Evaluation != nil, t.Release != nil, t.Outcome != nil, t.Rollback != nil} {
		if set {
			count++
		}
	}
	if count > 1 {
		return c, ErrInvalid
	}
	bad := func() (Case, error) {
		return c, fmt.Errorf("%w: transition %s to %s has incomplete or mismatched evidence", ErrInvalid, c.State, t.To)
	}
	switch t.To {
	case Diagnosed, Blocked:
		if (c.State != Observed && c.State != PendingDiagnosis && c.State != Blocked) || t.Diagnosis == nil || !textOK(t.Diagnosis.Summary, 1000) || !idsOK(t.Diagnosis.EvidenceIDs) || len(t.Diagnosis.AffectedKinds) == 0 || len(t.Diagnosis.AffectedKinds) > len(TargetKinds()) || t.To == Blocked && !textOK(t.Reason, 1000) {
			return bad()
		}
		seen := map[TargetKind]bool{}
		for _, kind := range t.Diagnosis.AffectedKinds {
			if !kindOK(kind) || seen[kind] {
				return bad()
			}
			seen[kind] = true
		}
		c.Diagnosis = t.Diagnosis
	case Proposed:
		if c.State != Diagnosed || t.ChangeSet == nil {
			return bad()
		}
		if err := validateChangeSet(*t.ChangeSet); err != nil {
			return c, err
		}
		for _, observed := range c.Observation.Dependencies {
			found := false
			for _, frozen := range t.ChangeSet.Baseline {
				if ResourceKey(frozen) == ResourceKey(observed) && frozen.Version == observed.Version && (observed.SHA256 == "" || frozen.SHA256 == observed.SHA256) {
					found = true
				}
			}
			if !found {
				return bad()
			}
		}
		for _, change := range t.ChangeSet.Changes {
			found := false
			for _, kind := range c.Diagnosis.AffectedKinds {
				if kind == change.Baseline.Kind {
					found = true
				}
			}
			if !found {
				return bad()
			}
		}
		c.ChangeSet = t.ChangeSet
	case Tested:
		e := t.Experiment
		if c.State != Proposed || e == nil || !textOK(e.ID, 256) || e.ChangeSetHash != Digest(c.ChangeSet) || !objectOK(e.BaselineRun) || !objectOK(e.CandidateRun) || e.BaselineRun == e.CandidateRun || !artifactOK(e.Manifest) || !artifactOK(e.Result) {
			return bad()
		}
		c.Experiment = e
	case Evaluated:
		e := t.Evaluation
		if c.State != Tested || e == nil || !textOK(e.ID, 256) || e.ExperimentID != c.Experiment.ID || e.ChangeSetHash != Digest(c.ChangeSet) || !artifactOK(e.Report) || !finite(e.BaselineScore) || !finite(e.CandidateScore) || !finite(e.Cost) || e.Cost < 0 || e.DurationMS < 0 || e.RegressionCount < 0 {
			return bad()
		}
		c.Evaluation = e
	case Monitoring:
		r := t.Release
		if c.State != Evaluated || r == nil || !textOK(r.ID, 256) || r.EvaluationID != c.Evaluation.ID || r.ChangeSetHash != Digest(c.ChangeSet) || !eligible(c.ChangeSet.Goal, *c.Evaluation) || c.Evaluation.Cost > c.ChangeSet.Budget {
			return bad()
		}
		if err := validateReceipts(*c.ChangeSet, r.Targets, false); err != nil {
			return c, err
		}
		c.Release = r
	case Verified, RollbackRequired:
		o := t.Outcome
		if c.State != Monitoring || o == nil || !textOK(o.ID, 256) || o.ReleaseID != c.Release.ID || !artifactOK(o.Report) || !finite(o.Gain) || !finite(o.Cost) || o.Cost < 0 || o.DurationMS < 0 || o.RegressionCount < 0 || len(o.Runs) == 0 || len(o.Runs) > 64 {
			return bad()
		}
		seen := map[ObjectRef]bool{}
		for _, run := range o.Runs {
			if !objectOK(run) || seen[run] || run == c.Experiment.BaselineRun || run == c.Experiment.CandidateRun {
				return bad()
			}
			seen[run] = true
		}
		if outcomePassed(c.ChangeSet.Goal, *o) != (t.To == Verified) {
			return bad()
		}
		c.Outcome = o
	case RolledBack:
		if c.State != RollbackRequired || t.Rollback == nil || t.Rollback.ReleaseID != c.Release.ID || !textOK(t.Rollback.Reason, 1000) {
			return bad()
		}
		if err := validateReceipts(*c.ChangeSet, t.Rollback.Targets, true); err != nil {
			return c, err
		}
		c.Rollback = t.Rollback
	case Rejected, Cancelled:
		if count != 0 || !textOK(t.Reason, 1000) || terminal(c.State) || c.State == Monitoring || c.State == RollbackRequired {
			return bad()
		}
	default:
		return bad()
	}
	if t.To != Rejected && t.To != Cancelled && count != 1 {
		return bad()
	}
	c.State = t.To
	c.Reason = t.Reason
	if terminal(c.State) {
		c.Lease = nil
	}
	return c, nil
}

func terminal(s State) bool {
	return s == Verified || s == Rejected || s == RolledBack || s == Cancelled || s == Superseded
}

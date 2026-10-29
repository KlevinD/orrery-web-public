package ontologyreasoning

import (
	"context"
	"fmt"
	"sort"
)

// validateProofPaths computes grounded proofs after structural validation.
// A cycle alone cannot make a rejected or unsupported conclusion usable.
func validateProofPaths(run *ReasoningRun) {
	e := run.Request.Evaluation
	valid := map[string]bool{}
	rejected := map[string]bool{}
	for _, p := range e.Closure.Premises {
		valid[p.ID] = true
	}
	for _, v := range run.Validation {
		rejected[v.CandidateID] = v.Status == ValidationRejected
	}
	for changed := true; changed; {
		changed = false
		for _, c := range e.Closure.Conclusions {
			if valid[c.ID] || rejected[c.ID] {
				continue
			}
			for _, d := range c.Derivations {
				grounded := len(d.PremiseIDs) > 0
				for _, id := range d.PremiseIDs {
					grounded = grounded && valid[id]
				}
				if grounded {
					valid[c.ID], changed = true, true
					break
				}
			}
		}
	}
	derived := map[string]bool{}
	for i := range e.Closure.Conclusions {
		c := &e.Closure.Conclusions[i]
		derived[c.ID] = true
		if !valid[c.ID] {
			continue
		}
		proofs := c.Derivations[:0]
		for _, d := range c.Derivations {
			grounded := true
			for _, id := range d.PremiseIDs {
				grounded = grounded && valid[id]
			}
			if grounded {
				proofs = append(proofs, d)
			}
		}
		c.Derivations = proofs
	}
	run.RejectedCandidateCount, run.ReviewRequiredCount = 0, 0
	for i := range run.Validation {
		v := &run.Validation[i]
		if derived[v.CandidateID] && !valid[v.CandidateID] && v.Status != ValidationRejected {
			v.Status = ValidationRejected
			v.Issues = append(v.Issues, newIssue(v.CandidateID, "invalid_derivation_dependency", issueSeverityError, "no complete structurally valid proof remains"))
		}
		if v.Status == ValidationRejected {
			run.RejectedCandidateCount++
		} else {
			run.ReviewRequiredCount++
		}
	}
}

func setEvaluationSupports(run *ReasoningRun) {
	e := run.Request.Evaluation
	e.SupportHashes = map[string]string{}
	e.Dependencies = map[string][]Dependency{}
	for i, c := range run.Candidates {
		if run.Validation[i].Status == ValidationRejected {
			continue
		}
		values := CandidateDependencyValues(*run, c.ID)
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			e.Dependencies[c.ID] = append(e.Dependencies[c.ID], Dependency{Ref: key, Hash: hashJSON(values[key])})
		}
		e.SupportHashes[c.ID] = stableID("support:v2:", e.Dependencies[c.ID])
	}
}

func (s *Service) compareEvaluation(ctx context.Context, run *ReasoningRun) error {
	e := run.Request.Evaluation
	if e.PreviousRunID == "" {
		return nil
	}
	previous, err := s.GetRun(ctx, run.TenantID, e.PreviousRunID)
	if err != nil {
		return err
	}
	old := previous.Request.Evaluation
	if old == nil || old.Historical != e.Historical || old.ActorHash != e.ActorHash || old.QueryHash != e.QueryHash {
		return fmt.Errorf("%w: previous evaluation scope does not match", ErrInvalidRequest)
	}
	for i, c := range previous.Candidates {
		if i >= len(previous.Validation) || previous.Validation[i].Status == ValidationRejected {
			continue
		}
		current, exists := e.SupportHashes[c.ID]
		if !exists {
			e.WithdrawnIDs = append(e.WithdrawnIDs, c.ID)
			continue
		}
		e.RetainedIDs = append(e.RetainedIDs, c.ID)
		if old.SupportHashes[c.ID] != current {
			e.RevisedIDs = append(e.RevisedIDs, c.ID)
		}
	}
	sort.Strings(e.WithdrawnIDs)
	sort.Strings(e.RetainedIDs)
	sort.Strings(e.RevisedIDs)
	return nil
}

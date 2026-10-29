package improvement

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// SourceRuntime is an explicitly admitted executable source bundle. It is a
// separate task runtime, not an adapter for production services or indexes.
// Every execution resolves the committed active snapshot; all source targets
// in a bundle switch in one database transaction.
type SourceRuntime struct {
	Store          *Store
	Vault          *ArtifactVault
	Authority      *RuntimeAuthority
	Runner         TaskRunner
	ExecutorSHA256 string
}

type RuntimeRequest struct {
	TenantID          string       `json:"tenant_id"`
	CaseID            string       `json:"case_id"`
	RuntimeID         string       `json:"runtime_id"`
	ExecutorSHA256    string       `json:"executor_sha256"`
	AllowBootstrap    bool         `json:"allow_bootstrap"`
	ObservationBudget float64      `json:"observation_budget"`
	Tasks             []ReplayTask `json:"tasks"`
}

// RuntimePlan freezes a fresh paired observation dataset before activation.
// The authorization covers the entire plan, including rollback and bootstrap.
type RuntimePlan struct {
	RuntimeRequest
	EvaluationID  string `json:"evaluation_id"`
	ChangeSetHash string `json:"change_set_hash"`
}

type RuntimeGrant struct {
	PlanHash  string    `json:"plan_hash"`
	Issuer    string    `json:"issuer"`
	ExpiresAt time.Time `json:"expires_at"`
	Signature string    `json:"signature"`
}

// RuntimeAuthority is a host authority with a key distinct from evidence
// signing. A model-written decision/action ID has no authorization value.
// This authority is for the standalone runtime, not production HIL approval.
type RuntimeAuthority struct{ key []byte }

func NewRuntimeAuthority(key []byte) (*RuntimeAuthority, error) {
	if len(key) < 32 {
		return nil, ErrInvalid
	}
	return &RuntimeAuthority{key: append([]byte(nil), key...)}, nil
}

func (a *RuntimeAuthority) signature(g RuntimeGrant) string {
	g.Signature = ""
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(Digest(g)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *RuntimeAuthority) Authorize(plan RuntimePlan, issuer string, now time.Time, ttl time.Duration) (RuntimeGrant, error) {
	if a == nil || !textOK(issuer, 256) || ttl < time.Minute || ttl > 24*time.Hour || Digest(plan) == "" {
		return RuntimeGrant{}, ErrInvalid
	}
	g := RuntimeGrant{PlanHash: Digest(plan), Issuer: issuer, ExpiresAt: now.UTC().Add(ttl)}
	g.Signature = a.signature(g)
	return g, nil
}

func (a *RuntimeAuthority) verify(plan RuntimePlan, g RuntimeGrant, now time.Time, expiry bool) error {
	if a == nil || g.PlanHash != Digest(plan) || !textOK(g.Issuer, 256) || !hashOK(g.Signature) || !hmac.Equal([]byte(g.Signature), []byte(a.signature(g))) || (expiry && !g.ExpiresAt.After(now)) {
		return fmt.Errorf("%w: scoped runtime authorization is missing, expired or mismatched", ErrInvalid)
	}
	return nil
}

type RuntimeSnapshot struct {
	TenantID  string            `json:"tenant_id"`
	ID        string            `json:"id"`
	Task      ObjectRef         `json:"task"`
	Revision  int64             `json:"revision"`
	Sources   []SourceFile      `json:"sources"`
	Resources []ResourceVersion `json:"resources"`
	Policy    ReplayPolicy      `json:"policy"`
	ReleaseID string            `json:"release_id,omitempty"`
}

type runtimePointer struct {
	Snapshot ArtifactRef `json:"snapshot"`
	Revision int64       `json:"revision"`
}

type runtimeAction struct {
	Plan        RuntimePlan  `json:"plan"`
	Grant       RuntimeGrant `json:"grant"`
	Operation   string       `json:"operation"`
	ActionRunID string       `json:"action_run_id"`
	Before      ArtifactRef  `json:"before"`
	After       ArtifactRef  `json:"after"`
	CompletedAt time.Time    `json:"completed_at"`
}

type runtimeObservation struct {
	Plan     RuntimePlan `json:"plan"`
	Action   ArtifactRef `json:"action"`
	Snapshot ArtifactRef `json:"snapshot"`
	Baseline replayBatch `json:"baseline"`
	Actual   replayBatch `json:"actual"`
	Outcome  Outcome     `json:"outcome"` // Report omitted to avoid a hash cycle.
}

type RuntimeCycleResult struct {
	Case     Case            `json:"case"`
	Active   RuntimeSnapshot `json:"active"`
	Followup Case            `json:"followup"`
}

func (r *SourceRuntime) ready() error {
	if r == nil || r.Store == nil || r.Vault == nil || r.Authority == nil || r.Runner == nil || !hashOK(r.ExecutorSHA256) || r.Store.core != r.Vault.core || hmac.Equal(r.Authority.key, r.Vault.key) {
		return ErrUnavailable
	}
	return r.Store.ready()
}

func (r *SourceRuntime) resolve(ctx context.Context, tenant, id string) (Case, replayPlan, replayProof, error) {
	var plan replayPlan
	var proof replayProof
	if err := r.ready(); err != nil {
		return Case{}, plan, proof, err
	}
	c, err := r.Store.Get(ctx, tenant, id)
	if err != nil {
		return c, plan, proof, err
	}
	if c.Experiment == nil || c.Evaluation == nil || c.ChangeSet == nil || !eligible(c.ChangeSet.Goal, *c.Evaluation) || c.Evaluation.Cost > c.ChangeSet.Budget {
		return c, plan, proof, ErrInvalid
	}
	v := NewReplayVerifier(r.Vault)
	if err := v.Verify(ctx, c, Transition{To: Evaluated, Evaluation: c.Evaluation}); err != nil {
		return c, plan, proof, err
	}
	plan, proof, err = v.resolve(ctx, c, *c.Experiment)
	return c, plan, proof, err
}

func (r *SourceRuntime) Prepare(ctx context.Context, req RuntimeRequest) (RuntimePlan, error) {
	c, frozen, _, err := r.resolve(ctx, req.TenantID, req.CaseID)
	if err != nil {
		return RuntimePlan{}, err
	}
	plan := RuntimePlan{RuntimeRequest: privateCopy(req), EvaluationID: c.Evaluation.ID, ChangeSetHash: Digest(c.ChangeSet)}
	return plan, r.validatePlan(plan, c, frozen.Spec)
}

func (r *SourceRuntime) validatePlan(p RuntimePlan, c Case, spec ReplaySpec) error {
	if !textOK(p.RuntimeID, 256) || p.TenantID != c.TenantID || p.CaseID != c.ID || p.ExecutorSHA256 != r.ExecutorSHA256 || p.EvaluationID != c.Evaluation.ID || p.ChangeSetHash != Digest(c.ChangeSet) {
		return ErrInvalid
	}
	if !finite(p.ObservationBudget) || p.ObservationBudget <= 0 || p.ObservationBudget > spec.Goal.MaxCost || 2*float64(len(p.Tasks))*spec.Policy.TaskRunUnits > p.ObservationBudget {
		return fmt.Errorf("%w: explicit post-adoption operation budget is required", ErrInvalid)
	}
	// Reuse all task admission checks (splits, controls, business/safety samples,
	// aggregate output, operation/time budgets), and prohibit old experiment inputs.
	seen := map[string]bool{}
	for _, task := range spec.Tasks {
		h, _ := canonicalJSON(task.Input)
		seen[h] = true
	}
	for _, task := range p.Tasks {
		h, err := canonicalJSON(task.Input)
		if err != nil || seen[h] {
			return fmt.Errorf("%w: post-adoption inputs must be fresh and unique", ErrInvalid)
		}
		seen[h] = true
	}
	spec.Tasks = p.Tasks
	return validateReplaySpec(spec)
}

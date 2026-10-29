// Package improvement owns task-level, cross-domain improvement evidence.
// Recording a proposal or an evaluation never grants permission to mutate a
// resource. Domain adapters retain their own governed action boundaries.
package improvement

import (
	"context"
	"errors"
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

var (
	ErrInvalid     = errors.New("invalid improvement material")
	ErrNotFound    = errors.New("improvement case not found")
	ErrConflict    = errors.New("improvement revision or idempotency conflict")
	ErrLease       = errors.New("improvement lease unavailable or expired")
	ErrUnavailable = errors.New("improvement executor or verifier unavailable")
)

type TargetKind string

const (
	TargetWorkflow      TargetKind = "workflow"
	TargetSkill         TargetKind = "skill"
	TargetTool          TargetKind = "tool"
	TargetBusinessLogic TargetKind = "business_logic"
	TargetAgentLoop     TargetKind = "agent_loop"
	TargetKnowledge     TargetKind = "knowledge"
	TargetMemory        TargetKind = "memory"
	TargetOntology      TargetKind = "ontology"
	TargetOptimizer     TargetKind = "optimizer"
)

func TargetKinds() []TargetKind {
	return []TargetKind{TargetWorkflow, TargetSkill, TargetTool, TargetBusinessLogic, TargetAgentLoop, TargetKnowledge, TargetMemory, TargetOntology, TargetOptimizer}
}

type State string

const (
	Observed         State = "observed"
	PendingDiagnosis State = "pending_diagnosis"
	Blocked          State = "blocked"
	Superseded       State = "superseded"
	Diagnosed        State = "diagnosed"
	Proposed         State = "proposed"
	Tested           State = "tested"
	Evaluated        State = "evaluated"
	Monitoring       State = "monitoring"
	Verified         State = "verified"
	Rejected         State = "rejected"
	RollbackRequired State = "rollback_required"
	RolledBack       State = "rolled_back"
	Cancelled        State = "cancelled"
)

type ObjectRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// ArtifactRef identifies immutable bytes. Verifier must resolve the artifact,
// check its digest and provenance; a well-formed reference alone is not proof.
type ArtifactRef struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

type ResourceVersion struct {
	Kind    TargetKind `json:"kind"`
	ID      string     `json:"id"`
	Version string     `json:"version"`
	SHA256  string     `json:"sha256,omitempty"`
}

type Observation struct {
	ID              string                  `json:"id"`
	TenantID        string                  `json:"tenant_id"`
	Task            ObjectRef               `json:"task"`
	Run             ObjectRef               `json:"run"`
	Trigger         string                  `json:"trigger"`
	Summary         string                  `json:"summary"`
	EvidenceIDs     []string                `json:"evidence_ids"`
	Dependencies    []ResourceVersion       `json:"dependencies,omitempty"`
	RelatedObjects  []ObjectRef             `json:"related_objects,omitempty"`
	Metrics         map[string]float64      `json:"metrics,omitempty"`
	ParentCaseID    string                  `json:"parent_case_id,omitempty"`
	OccurredAt      time.Time               `json:"occurred_at"`
	SourceAccessRef graphprotocol.AccessRef `json:"source_access_ref"`
}

type Diagnosis struct {
	Summary         string       `json:"summary"`
	EvidenceIDs     []string     `json:"evidence_ids"`
	AffectedKinds   []TargetKind `json:"affected_kinds"`
	DiagnosisCaseID string       `json:"diagnosis_case_id,omitempty"`
}

type Goal struct {
	Metric        string  `json:"metric"`
	Direction     string  `json:"direction"` // higher or lower
	MinimumGain   float64 `json:"minimum_gain"`
	MaxCost       float64 `json:"max_cost"`
	MaxDurationMS int64   `json:"max_duration_ms"`
}

type TargetChange struct {
	Baseline  ResourceVersion `json:"baseline"`
	Candidate ResourceVersion `json:"candidate"`
	DependsOn []string        `json:"depends_on,omitempty"` // keys from ResourceKey
	Patch     ArtifactRef     `json:"patch"`
	Rollback  ArtifactRef     `json:"rollback"`
}

type ChangeSet struct {
	ID        string            `json:"id"`
	Goal      Goal              `json:"goal"`
	Baseline  []ResourceVersion `json:"baseline"`
	Changes   []TargetChange    `json:"changes"`
	Dataset   ArtifactRef       `json:"dataset"`
	Policy    ArtifactRef       `json:"policy"`
	Generator ArtifactRef       `json:"generator"`
	Evaluator ArtifactRef       `json:"evaluator"`
	Budget    float64           `json:"budget"`
}

type Experiment struct {
	ID            string      `json:"id"`
	ChangeSetHash string      `json:"change_set_hash"`
	BaselineRun   ObjectRef   `json:"baseline_run"`
	CandidateRun  ObjectRef   `json:"candidate_run"`
	Manifest      ArtifactRef `json:"manifest"`
	Result        ArtifactRef `json:"result"`
}

type Evaluation struct {
	ID                        string      `json:"id"`
	ExperimentID              string      `json:"experiment_id"`
	ChangeSetHash             string      `json:"change_set_hash"`
	Report                    ArtifactRef `json:"report"`
	BaselineScore             float64     `json:"baseline_score"`
	CandidateScore            float64     `json:"candidate_score"`
	Cost                      float64     `json:"cost"`
	DurationMS                int64       `json:"duration_ms"`
	RegressionCount           int         `json:"regression_count"`
	HeldOutPassed             bool        `json:"held_out_passed"`
	BusinessConstraintsPassed bool        `json:"business_constraints_passed"`
	SafetyPassed              bool        `json:"safety_passed"`
}

type TargetReceipt struct {
	Version     ResourceVersion `json:"version"`
	ActionRunID string          `json:"action_run_id"`
	Receipt     ArtifactRef     `json:"receipt"`
}

// Release records completed, verified domain actions. It is not an apply command.
type Release struct {
	ID            string          `json:"id"`
	EvaluationID  string          `json:"evaluation_id"`
	ChangeSetHash string          `json:"change_set_hash"`
	Targets       []TargetReceipt `json:"targets"`
}

type Outcome struct {
	ID                        string      `json:"id"`
	ReleaseID                 string      `json:"release_id"`
	Runs                      []ObjectRef `json:"runs"`
	Report                    ArtifactRef `json:"report"`
	Gain                      float64     `json:"gain"`
	Cost                      float64     `json:"cost"`
	DurationMS                int64       `json:"duration_ms"`
	RegressionCount           int         `json:"regression_count"`
	BusinessConstraintsPassed bool        `json:"business_constraints_passed"`
}

type Rollback struct {
	ReleaseID string          `json:"release_id"`
	Targets   []TargetReceipt `json:"targets"`
	Reason    string          `json:"reason"`
}

type Lease struct {
	Owner     string    `json:"owner"`
	Token     string    `json:"token"`
	Epoch     int64     `json:"epoch"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Case struct {
	ID           string      `json:"id"`
	TenantID     string      `json:"tenant_id"`
	Revision     int64       `json:"revision"`
	State        State       `json:"state"`
	Observation  Observation `json:"observation"`
	Diagnosis    *Diagnosis  `json:"diagnosis,omitempty"`
	ChangeSet    *ChangeSet  `json:"change_set,omitempty"`
	Experiment   *Experiment `json:"experiment,omitempty"`
	Evaluation   *Evaluation `json:"evaluation,omitempty"`
	Release      *Release    `json:"release,omitempty"`
	Outcome      *Outcome    `json:"outcome,omitempty"`
	Rollback     *Rollback   `json:"rollback,omitempty"`
	Lease        *Lease      `json:"lease,omitempty"`
	LeaseEpoch   int64       `json:"lease_epoch"`
	Reason       string      `json:"reason,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
	SupersededBy string      `json:"superseded_by,omitempty"`
}

type Transition struct {
	ID               string      `json:"id"`
	TenantID         string      `json:"tenant_id"`
	CaseID           string      `json:"case_id"`
	ExpectedRevision int64       `json:"expected_revision"`
	LeaseToken       string      `json:"lease_token"`
	Actor            ObjectRef   `json:"actor"`
	To               State       `json:"to"`
	Diagnosis        *Diagnosis  `json:"diagnosis,omitempty"`
	ChangeSet        *ChangeSet  `json:"change_set,omitempty"`
	Experiment       *Experiment `json:"experiment,omitempty"`
	Evaluation       *Evaluation `json:"evaluation,omitempty"`
	Release          *Release    `json:"release,omitempty"`
	Outcome          *Outcome    `json:"outcome,omitempty"`
	Rollback         *Rollback   `json:"rollback,omitempty"`
	Reason           string      `json:"reason,omitempty"`
}

// Verifier is a trusted integration port, independent of candidate generation.
// It resolves frozen experiment/evaluation artifacts, checks actual governed
// action receipts and resource versions, and checks post-release run provenance.
// Without this port, evidence-bearing transitions fail closed.
type Verifier interface {
	Verify(context.Context, Case, Transition) error
}

type Page struct {
	Items      []Case `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func ResourceKey(v ResourceVersion) string { return string(v.Kind) + ":" + v.ID }

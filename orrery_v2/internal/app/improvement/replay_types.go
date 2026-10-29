package improvement

import (
	"context"
	"encoding/json"
	"time"
)

const replayProtocol = "orrery-code-task-replay-v1"
const replayOracle = "host-exact-json-v1"

// SourceFile is a regular UTF-8 file, never a host path or a symlink. An admitted
// source snapshot includes its task driver, but never the host oracle/dataset.
type SourceFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ReplayTarget struct {
	Resource ResourceVersion `json:"resource"`
	Path     string          `json:"path"`
}

type ReplayTask struct {
	ID       string          `json:"id"`
	Input    json.RawMessage `json:"input"`
	Expected json.RawMessage `json:"expected"`
	Split    string          `json:"split"` // repair, regression or held_out
	Business bool            `json:"business"`
	Safety   bool            `json:"safety"`
}

// ReplayPolicy is operator-admitted, frozen before generation, identical for
// both arms. Cost is bounded operation units, not a claim about provider bills.
type ReplayPolicy struct {
	Image          string   `json:"image"` // local immutable sha256 Docker image ID
	Entrypoint     string   `json:"entrypoint"`
	Args           []string `json:"args"`
	TimeoutMS      int64    `json:"timeout_ms"`
	MemoryMB       int      `json:"memory_mb"`
	CPUs           float64  `json:"cpus"`
	MaxOutputBytes int      `json:"max_output_bytes"`
	ModelTimeoutMS int64    `json:"model_timeout_ms"`
	ModelCallUnits float64  `json:"model_call_units"`
	TaskRunUnits   float64  `json:"task_run_units"`
}

type GeneratorIdentity struct {
	ProfileID    string `json:"profile_id"`
	Model        string `json:"model"`
	Endpoint     string `json:"endpoint"`
	Version      string `json:"version"`
	PromptSHA256 string `json:"prompt_sha256"`
}

type ReplaySpec struct {
	ExecutorSHA256 string            `json:"executor_sha256"`
	Observation    Observation       `json:"observation"`
	Goal           Goal              `json:"goal"`
	Budget         float64           `json:"budget"`
	Sources        []SourceFile      `json:"sources"`
	Targets        []ReplayTarget    `json:"targets"`
	Tasks          []ReplayTask      `json:"tasks"`
	Policy         ReplayPolicy      `json:"policy"`
	Generator      GeneratorIdentity `json:"generator"`
}

type CandidateEdit struct {
	Target  string `json:"target"` // ResourceKey; only admitted files are editable
	Content string `json:"content"`
}

type CodeProposal struct {
	Diagnosis string          `json:"diagnosis"`
	Edits     []CandidateEdit `json:"edits"`
}

type GenerationInput struct {
	Summary  string         `json:"summary"`
	Sources  []SourceFile   `json:"sources"`
	Targets  []ReplayTarget `json:"targets"`
	Failures []ReplayTask   `json:"repair_failures"`
}

// CandidateModel receives repair failures only. Hidden fixtures and task
// results, evaluator credentials and authority are not in this interface.
type CandidateModel interface {
	Identity() GeneratorIdentity
	Generate(context.Context, GenerationInput) (CodeProposal, error)
}

type TaskExecution struct {
	TaskID      string `json:"task_id"`
	InputHash   string `json:"input_hash"`
	SourceHash  string `json:"source_hash"`
	PolicyHash  string `json:"policy_hash"`
	ContainerID string `json:"container_id"`
	Output      string `json:"output"`
	Stderr      string `json:"stderr,omitempty"`
	ExitCode    int    `json:"exit_code"`
	DurationMS  int64  `json:"duration_ms"`
	TimedOut    bool   `json:"timed_out"`
	Truncated   bool   `json:"truncated"`
}

type TaskRunner interface {
	Run(context.Context, []SourceFile, ReplayPolicy, ReplayTask) (TaskExecution, error)
}

type replayPlan struct {
	SpecHash  string      `json:"spec_hash"`
	Spec      ReplaySpec  `json:"spec"`
	Dataset   ArtifactRef `json:"dataset"`
	Policy    ArtifactRef `json:"policy"`
	Generator ArtifactRef `json:"generator"`
	Evaluator ArtifactRef `json:"evaluator"`
}

type replayBatch struct {
	Run        ObjectRef       `json:"run"`
	Executions []TaskExecution `json:"executions"`
}

type replayProof struct {
	Protocol      string       `json:"protocol"`
	TenantID      string       `json:"tenant_id"`
	CaseID        string       `json:"case_id"`
	ChangeSetHash string       `json:"change_set_hash"`
	Plan          ArtifactRef  `json:"plan"`
	Proposal      CodeProposal `json:"proposal"`
	Baseline      replayBatch  `json:"baseline"`
	Candidate     replayBatch  `json:"candidate"`
}

type replayReport struct {
	Oracle     string      `json:"oracle"`
	Result     ArtifactRef `json:"result"`
	CostUnit   string      `json:"cost_unit"`
	Evaluation Evaluation  `json:"evaluation"` // Report ref omitted to avoid a hash cycle
}

// ReplayCoordinator performs an explicitly admitted experiment. It records
// evidence through Evaluated and never applies a resource or authorizes release.
type ReplayCoordinator struct {
	Store  *Store
	Vault  *ArtifactVault
	Model  CandidateModel
	Runner TaskRunner
}

type replayCheckpoint struct {
	SpecHash  string      `json:"spec_hash"`
	StartedAt time.Time   `json:"started_at"`
	Result    ArtifactRef `json:"result,omitempty"`
}

package improvement

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

const runtimeEvidenceTable = "ontology_improvement_runtime_evidence"

// RuntimeBindingEvidence identifies the contract exposed to this execution.
// Fingerprint is a binding hash, never a claim about remote implementation or
// a complete skill definition. PackageDigest is the digest declared by the
// resolved skill binding; admission must independently verify package bytes.
type RuntimeBindingEvidence struct {
	Kind          TargetKind `json:"kind"`
	ID            string     `json:"id"`
	Alias         string     `json:"alias,omitempty"`
	Version       string     `json:"version,omitempty"`
	Fingerprint   string     `json:"binding_sha256"`
	PackageDigest string     `json:"package_sha256,omitempty"`
}

type RuntimeEvidenceScope struct {
	TenantID string                  `json:"tenant_id"`
	Task     ObjectRef               `json:"task"`
	Run      ObjectRef               `json:"run"`
	Source   graphprotocol.AccessRef `json:"source_access_ref"`
}

// RuntimeRequestEvidence contains hashes and identifiers, not raw prompts,
// memory content, credentials, request bodies or tool output. Configuration
// SHA256 covers the declared runtime-controls projection only, not agent code.
type RuntimeRequestEvidence struct {
	ID                string                   `json:"id"`
	Protocol          string                   `json:"protocol"`
	Phase             string                   `json:"request_phase"`
	AttemptID         string                   `json:"attempt_id"`
	Scope             RuntimeEvidenceScope     `json:"scope"`
	ExecutionTenantID string                   `json:"execution_tenant_id"`
	RunID             string                   `json:"runtime_run_id"`
	AgentID           string                   `json:"agent_id"`
	ProfileID         string                   `json:"profile_id,omitempty"`
	TraceRef          string                   `json:"trace_ref"`
	ControlsSHA256    string                   `json:"runtime_controls_sha256"`
	InputSHA256       string                   `json:"input_sha256"`
	PromptSHA256      string                   `json:"effective_prompt_sha256"`
	ContextSHA256     string                   `json:"context_sha256"`
	Bindings          []RuntimeBindingEvidence `json:"bindings"`
	CapturedAt        time.Time                `json:"captured_at"`
}

type RuntimeTerminalEvidence struct {
	Dependencies      *RuntimeDependencies            `json:"dependencies,omitempty"`
	ID                string                          `json:"id"`
	AttemptID         string                          `json:"attempt_id"`
	RequestID         string                          `json:"request_id"`
	Status            agentcore.RuntimeStatus         `json:"status"`
	TraceFinalization string                          `json:"trace_finalization"`
	DurationMS        int64                           `json:"duration_ms"`
	Usage             map[string]agentcore.TokenUsage `json:"usage,omitempty"`
	CompletedAt       time.Time                       `json:"completed_at"`
}

type RuntimeEvidenceStore struct {
	store *Store
}

func NewRuntimeEvidenceStore(core *db.Store) *RuntimeEvidenceStore {
	return &RuntimeEvidenceStore{store: NewStore(core)}
}

func (s *Store) RuntimeEvidence() *RuntimeEvidenceStore { return &RuntimeEvidenceStore{store: s} }

// BuildRuntimeRequestEvidence is called on the actual resolved request at the
// durable trace boundary immediately before provider execution. It never reads
// current registries again at completion and never trusts provider metadata.
func BuildRuntimeRequestEvidence(req agentcore.RuntimeRequest, scope RuntimeEvidenceScope, attempt, trace string, at time.Time) (RuntimeRequestEvidence, error) {
	e := RuntimeRequestEvidence{Protocol: "orrery-runtime-evidence-v1", Phase: "prepared_for_dispatch", AttemptID: attempt, Scope: privateCopy(scope), ExecutionTenantID: req.TenantID, RunID: req.RunID.String(), AgentID: req.AgentID.String(), TraceRef: trace, CapturedAt: at.UTC(), Bindings: []RuntimeBindingEvidence{}}
	if req.ExecutionEnvelope != nil {
		if err := req.ExecutionEnvelope.Verify(); err != nil || req.ExecutionEnvelope.RunID != e.RunID || req.ExecutionEnvelope.TenantID != e.ExecutionTenantID || req.ExecutionEnvelope.AgentID != e.AgentID || req.ExecutionEnvelope.MissionID != req.MissionID || req.ExecutionEnvelope.TaskID != req.TaskID {
			return e, ErrInvalid
		}
		e.ProfileID = req.ExecutionEnvelope.AgentProfileID
	}
	o := req.RuntimeOptions
	// Keep task/memory-dependent prompt text out of the configuration version.
	// These are explicit controls, not the entire provider configuration.
	controls := struct {
		Model        string
		MaxTurns     int
		TimeoutNS    int64
		InactiveNS   int64
		ContextLimit string
		SandboxMode  agentcore.RuntimeSandboxMode
		Approval     agentcore.RuntimeApprovalMode
		Network      agentcore.RuntimeNetworkMode
		Boundary     bool
	}{o.Model, o.MaxTurns, int64(o.Timeout), int64(o.SemanticInactivityTimeout), o.ProviderSpecific[agentcore.RuntimeContextTokenLimitProviderKey], o.SandboxProfile.Mode, o.SandboxProfile.Approval, o.SandboxProfile.Network, o.SandboxProfile.EnforceBoundary}
	e.ControlsSHA256 = Digest(controls)
	e.InputSHA256 = Digest(struct {
		Messages []agentcore.RuntimeChatMessage
		Media    []agentcore.ContentBlock
	}{req.InputMessages, req.InputMedia})
	e.ContextSHA256 = Digest(req.ContextPack)
	e.PromptSHA256 = Digest(struct {
		System, Context string
		Blocks          []agentcore.RuntimePromptBlock
	}{o.SystemPrompt, o.SystemContextPrompt, o.PromptBlocks})
	if len(req.ToolBindings)+len(req.SkillBindings) > 512 {
		return e, ErrInvalid
	}
	for _, binding := range req.ToolBindings {
		// SecretEnv is deliberately excluded from both storage and fingerprint.
		binding.SecretEnv = nil
		e.Bindings = append(e.Bindings, RuntimeBindingEvidence{Kind: TargetTool, ID: string(binding.ToolID), Alias: binding.Alias, Version: binding.Version, Fingerprint: Digest(binding)})
	}
	for _, binding := range req.SkillBindings {
		packageHash := strings.TrimPrefix(binding.PackageDigest, "sha256:")
		if !hashOK(packageHash) {
			packageHash = ""
		}
		e.Bindings = append(e.Bindings, RuntimeBindingEvidence{Kind: TargetSkill, ID: binding.SkillID, Alias: binding.Alias, Version: binding.Version, Fingerprint: Digest(binding), PackageDigest: packageHash})
	}
	e.ID = "runtime-request-evidence:" + Digest(e)
	return e, validateRuntimeEvidence(e)
}

func validateRuntimeEvidence(e RuntimeRequestEvidence) error {
	if e.Protocol != "orrery-runtime-evidence-v1" || e.Phase != "prepared_for_dispatch" || !textOK(e.AttemptID, 256) || !textOK(e.ExecutionTenantID, 256) || !textOK(e.RunID, 256) || !textOK(e.AgentID, 256) || !textOK(e.TraceRef, 512) || (e.ProfileID != "" && !textOK(e.ProfileID, 256)) || e.CapturedAt.IsZero() || !hashOK(e.ControlsSHA256) || !hashOK(e.InputSHA256) || !hashOK(e.PromptSHA256) || !hashOK(e.ContextSHA256) || len(e.Bindings) > 512 {
		return ErrInvalid
	}
	scope := e.Scope
	if !textOK(scope.TenantID, 256) || !objectOK(scope.Task) || !objectOK(scope.Run) || scope.Run.Kind != "agent_run" || scope.Source.TenantID != scope.TenantID || scope.Source.Kind != "agent_run" || scope.Source.ID != e.RunID {
		return ErrInvalid
	}
	for _, b := range e.Bindings {
		if (b.Kind != TargetTool && b.Kind != TargetSkill) || !textOK(b.ID, 512) || (b.Alias != "" && !textOK(b.Alias, 512)) || (b.Version != "" && !textOK(b.Version, 256)) || !hashOK(b.Fingerprint) || (b.PackageDigest != "" && !hashOK(b.PackageDigest)) {
			return ErrInvalid
		}
	}
	copy := e
	copy.ID = ""
	if e.ID != "runtime-request-evidence:"+Digest(copy) {
		return ErrInvalid
	}
	return nil
}

func runtimeEvidenceKey(tenant, attempt, stage string) string {
	return tenantPrefix(tenant) + Digest(attempt) + "/" + stage
}

func (s *RuntimeEvidenceStore) Capture(ctx context.Context, e RuntimeRequestEvidence) error {
	if s == nil || s.store.ready() != nil {
		return ErrUnavailable
	}
	if err := validateRuntimeEvidence(e); err != nil {
		return err
	}
	return s.store.core.WithTx(ctx, func(tx db.Tx) error {
		return immutableRuntimeEvidenceTx(ctx, tx, runtimeEvidenceKey(e.Scope.TenantID, e.AttemptID, "request"), e)
	})
}

func immutableRuntimeEvidenceTx(ctx context.Context, tx db.Tx, key string, value any) error {
	created, err := db.InsertRecordTx(ctx, tx, runtimeEvidenceTable, key, encode(value))
	if err != nil || created {
		return err
	}
	record, exists, err := db.GetRecordTx(ctx, tx, runtimeEvidenceTable, key)
	if err != nil {
		return err
	}
	if !exists || record["sha256"] != Digest(value) || record["payload"] != encode(value)["payload"] {
		return ErrConflict
	}
	return nil
}

func (s *RuntimeEvidenceStore) GetRequest(ctx context.Context, tenant, attempt string) (RuntimeRequestEvidence, error) {
	if s == nil || s.store.ready() != nil {
		return RuntimeRequestEvidence{}, ErrUnavailable
	}
	if !validScope(tenant, attempt) {
		return RuntimeRequestEvidence{}, ErrInvalid
	}
	record, exists, err := s.store.core.GetRecord(ctx, runtimeEvidenceTable, runtimeEvidenceKey(tenant, attempt, "request"))
	if err != nil {
		return RuntimeRequestEvidence{}, err
	}
	if !exists {
		return RuntimeRequestEvidence{}, ErrNotFound
	}
	e, err := decode[RuntimeRequestEvidence](record)
	if err == nil && (e.Scope.TenantID != tenant || e.AttemptID != attempt || validateRuntimeEvidence(e) != nil) {
		err = ErrInvalid
	}
	return e, err
}

func (s *RuntimeEvidenceStore) GetTerminal(ctx context.Context, tenant, attempt string) (RuntimeTerminalEvidence, error) {
	request, err := s.GetRequest(ctx, tenant, attempt)
	if err != nil {
		return RuntimeTerminalEvidence{}, err
	}
	record, exists, err := s.store.core.GetRecord(ctx, runtimeEvidenceTable, runtimeEvidenceKey(tenant, attempt, "terminal"))
	if err != nil {
		return RuntimeTerminalEvidence{}, err
	}
	if !exists {
		return RuntimeTerminalEvidence{}, ErrNotFound
	}
	e, err := decode[RuntimeTerminalEvidence](record)
	if err == nil {
		err = validateRuntimeTerminal(request, e)
	}
	return e, err
}

// Complete commits immutable terminal evidence with the observed Case and
// projection outbox. A graph outage cannot discard the task feedback.
func (s *RuntimeEvidenceStore) Complete(ctx context.Context, e RuntimeRequestEvidence, terminal agentcore.TraceTerminal) (Case, error) {
	return s.complete(ctx, e, terminal, "not_attested")
}

func (s *RuntimeEvidenceStore) complete(ctx context.Context, e RuntimeRequestEvidence, terminal agentcore.TraceTerminal, traceFinalization string) (Case, error) {
	if s == nil || s.store.ready() != nil {
		return Case{}, ErrUnavailable
	}
	if err := validateRuntimeEvidence(e); err != nil {
		return Case{}, err
	}
	end, err := buildRuntimeTerminal(e, terminal, traceFinalization)
	if err != nil {
		return Case{}, err
	}
	return s.completeTerminal(ctx, e, end)
}

func buildRuntimeTerminal(e RuntimeRequestEvidence, terminal agentcore.TraceTerminal, traceFinalization string) (RuntimeTerminalEvidence, error) {
	end := RuntimeTerminalEvidence{AttemptID: e.AttemptID, RequestID: e.ID, Status: terminal.Status, TraceFinalization: traceFinalization, DurationMS: terminal.Response.DurationMs, Usage: privateCopy(terminal.Response.Usage), CompletedAt: terminal.CompletedAt.UTC()}
	end.ID = "runtime-terminal-evidence:" + Digest(end)
	return end, validateRuntimeTerminal(e, end)
}

func validateRuntimeTerminal(e RuntimeRequestEvidence, terminal RuntimeTerminalEvidence) error {
	if err := validateRuntimeEvidence(e); err != nil {
		return err
	}
	if err := validateRuntimeDependencies(e, terminal); err != nil {
		return err
	}
	switch terminal.Status {
	case agentcore.RuntimeStatusCompleted, agentcore.RuntimeStatusFailed, agentcore.RuntimeStatusTimeout, agentcore.RuntimeStatusCancelled, agentcore.RuntimeStatusBlocked:
	default:
		return ErrInvalid
	}
	if terminal.TraceFinalization != "succeeded" && terminal.TraceFinalization != "failed" && terminal.TraceFinalization != "not_attested" {
		return ErrInvalid
	}
	if terminal.CompletedAt.IsZero() || terminal.CompletedAt.Before(e.CapturedAt) || terminal.DurationMS < 0 || len(terminal.Usage) > 128 {
		return ErrInvalid
	}
	for model, usage := range terminal.Usage {
		if !textOK(model, 256) || usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheReadTokens < 0 || usage.CacheWriteTokens < 0 ||
			usage.CachedInputTokens < 0 || usage.CachedInputTokens > usage.InputTokens || usage.ReasoningTokens < 0 || usage.ReasoningTokens > usage.OutputTokens {
			return ErrInvalid
		}
	}
	copy := terminal
	copy.ID = ""
	if terminal.AttemptID != e.AttemptID || terminal.RequestID != e.ID || terminal.ID != "runtime-terminal-evidence:"+Digest(copy) {
		return ErrInvalid
	}
	return nil
}

// Recovery uses exactly the originally journaled terminal, including its time
// and trace status. It never reconstructs results from a newer run or a retry.
func (s *RuntimeEvidenceStore) completeTerminal(ctx context.Context, e RuntimeRequestEvidence, end RuntimeTerminalEvidence) (Case, error) {
	if s == nil || s.store.ready() != nil {
		return Case{}, ErrUnavailable
	}
	if err := validateRuntimeTerminal(e, end); err != nil {
		return Case{}, err
	}
	o, err := capturedRuntimeObservation(e, end)
	if err != nil {
		return Case{}, err
	}
	var result Case
	err = s.store.core.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, runtimeEvidenceTable, runtimeEvidenceKey(e.Scope.TenantID, e.AttemptID, "request")); err != nil {
			return err
		}
		record, exists, err := db.GetRecordTx(ctx, tx, runtimeEvidenceTable, runtimeEvidenceKey(e.Scope.TenantID, e.AttemptID, "request"))
		if err != nil {
			return err
		}
		stored, decodeErr := decode[RuntimeRequestEvidence](record)
		if !exists || decodeErr != nil || !reflect.DeepEqual(stored, e) {
			return fmt.Errorf("%w: terminal result requires its captured request", ErrInvalid)
		}
		execution, err := executionEvidenceTx(ctx, tx, e)
		if err != nil {
			return err
		}
		review, err := BuildRuntimeReviewWithExecution(e, &end, execution)
		if err != nil {
			return err
		}
		if err := immutableRuntimeEvidenceTx(ctx, tx, runtimeEvidenceKey(e.Scope.TenantID, e.AttemptID, "terminal"), end); err != nil {
			return err
		}
		if err := immutableRuntimeReviewTx(ctx, tx, review); err != nil {
			return err
		}
		result, err = s.store.observeTx(ctx, tx, o, s.store.now().UTC())
		if err != nil {
			return err
		}
		if err := enqueueRuntimeDiagnosisTx(ctx, tx, result, e, execution, end, s.store.now().UTC()); err != nil {
			return err
		}
		current, _, err := db.GetRecordTx(ctx, tx, caseTable, caseKey(result.TenantID, result.ID))
		if err != nil {
			return err
		}
		result, err = decode[Case](current)
		return err
	})
	return result, err
}

// Keep this observation identical across review-policy upgrades. Existing Case
// IDs, immutable observations and journal replays must remain compatible.
func capturedRuntimeObservation(e RuntimeRequestEvidence, end RuntimeTerminalEvidence) (Observation, error) {
	if err := validateRuntimeTerminal(e, end); err != nil {
		return Observation{}, err
	}
	o := Observation{ID: "captured-runtime-result:" + Digest(end), TenantID: e.Scope.TenantID, Task: e.Scope.Task, Run: e.Scope.Run, Trigger: "captured_runtime_" + string(end.Status), Summary: "Runtime pipeline reached terminal status; task and business quality evaluation is pending", EvidenceIDs: []string{e.ID, end.ID, e.TraceRef}, OccurredAt: end.CompletedAt, SourceAccessRef: e.Scope.Source,
		RelatedObjects: []ObjectRef{{Kind: "runtime_attempt", ID: e.AttemptID}},
		Dependencies:   []ResourceVersion{{Kind: TargetAgentLoop, ID: "runtime-controls:" + e.AgentID, Version: "runtime-controls-v1:" + e.ControlsSHA256, SHA256: e.ControlsSHA256}}}
	if end.DurationMS > 0 {
		o.Metrics = map[string]float64{"runtime_duration_ms": float64(end.DurationMS)}
	}
	// Different aliases can expose the same resource. Conflicting versions are
	// kept in evidence rather than collapsed into an invented single baseline.
	byKey, ambiguous := map[string]ResourceVersion{}, map[string]bool{}
	for _, b := range e.Bindings {
		if b.Version == "" {
			continue
		}
		v := ResourceVersion{Kind: b.Kind, ID: b.ID, Version: b.Version, SHA256: b.PackageDigest}
		key := ResourceKey(v)
		if previous, ok := byKey[key]; ok && previous != v {
			ambiguous[key] = true
		}
		byKey[key] = v
	}
	seen := map[string]bool{}
	for _, b := range e.Bindings {
		key := string(b.Kind) + ":" + b.ID
		if v, ok := byKey[key]; ok && !ambiguous[key] && !seen[key] && len(o.Dependencies) < 128 {
			o.Dependencies = append(o.Dependencies, v)
			seen[key] = true
		}
	}
	return o, validateObservation(o)
}

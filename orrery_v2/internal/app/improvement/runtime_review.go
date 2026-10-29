package improvement

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

const runtimeReviewTable = "ontology_improvement_runtime_reviews"

// A review is a host policy over captured facts. It is neither a business
// quality judgment nor a model diagnosis. A new policy needs a new protocol/key.
const runtimeReviewProtocol = "orrery-runtime-review-v1"
const runtimeReviewPolicy = "v1:request-and-terminal-only;all-nine-domains;bindings-are-declarations;controls-are-not-code;quality-and-causality-unassessed;reported-usage-is-not-billing"

const runtimeReviewProtocolV2 = "orrery-runtime-review-v2"
const runtimeReviewPolicyV2 = "v2:request-execution-and-terminal;host-binary-and-resolved-policy;all-nine-domains;bindings-are-declarations;remote-implementation-unverified;quality-and-causality-unassessed;reported-usage-is-not-billing"

type RuntimeDomainCoverage struct {
	Kind           TargetKind               `json:"kind"`
	Coverage       string                   `json:"coverage"`
	ControlsSHA256 string                   `json:"controls_sha256,omitempty"`
	Bindings       []RuntimeBindingEvidence `json:"bindings"`
	Gaps           []string                 `json:"gaps"`
}

type RuntimeReview struct {
	ID                   string                          `json:"id"`
	Protocol             string                          `json:"protocol"`
	PolicySHA256         string                          `json:"policy_sha256"`
	TenantID             string                          `json:"tenant_id"`
	AttemptID            string                          `json:"attempt_id"`
	RequestID            string                          `json:"request_id"`
	TerminalID           string                          `json:"terminal_id,omitempty"`
	ExecutionID          string                          `json:"execution_evidence_id,omitempty"`
	RuntimePolicySHA256  string                          `json:"runtime_policy_sha256,omitempty"`
	ImplementationSHA256 string                          `json:"implementation_sha256,omitempty"`
	PipelineStatus       string                          `json:"pipeline_status"`
	TraceStatus          string                          `json:"trace_finalization"`
	BusinessQuality      string                          `json:"business_quality"`
	CausalAttribution    string                          `json:"causal_attribution"`
	BaselineReady        bool                            `json:"baseline_ready"`
	Domains              []RuntimeDomainCoverage         `json:"domains"`
	Signals              []string                        `json:"signals"`
	Blockers             []string                        `json:"blockers"`
	DurationMS           *int64                          `json:"duration_ms,omitempty"`
	ReportedUsage        map[string]agentcore.TokenUsage `json:"reported_usage,omitempty"`
	AssessedAt           time.Time                       `json:"assessed_at"`
}

// RuntimeBaselineVerifier is an additional trusted integration boundary for
// production-captured cases. Implementations must resolve the original attempt's
// complete dependency inventory, verify immutable implementation/package bytes
// and match the frozen ChangeSet baseline. They resolve baseline coverage gaps
// with evidence; business quality can remain unassessed until evaluation.
// A caller's baseline_ready flag or binding hash cannot do this.
// This does not grant domain action authorization or replace Verifier.Verify.
type RuntimeBaselineVerifier interface {
	Verifier
	VerifyRuntimeBaseline(context.Context, RuntimeReview, Case, Transition) error
}

// BuildRuntimeReview does not consult current registries or infer absent domains
// to be inapplicable. The v1 capture format cannot prove a complete task baseline.
func BuildRuntimeReview(request RuntimeRequestEvidence, terminal *RuntimeTerminalEvidence) (RuntimeReview, error) {
	if err := validateRuntimeEvidence(request); err != nil {
		return RuntimeReview{}, err
	}
	r := RuntimeReview{
		Protocol: runtimeReviewProtocol, PolicySHA256: Digest(runtimeReviewPolicy),
		TenantID: request.Scope.TenantID, AttemptID: request.AttemptID, RequestID: request.ID,
		PipelineStatus: "unknown", TraceStatus: "unknown", BusinessQuality: "unassessed", CausalAttribution: "unresolved",
		Domains: []RuntimeDomainCoverage{}, Signals: []string{},
		Blockers:   []string{"baseline_incomplete", "business_quality_unassessed", "causal_attribution_unresolved", "billing_cost_unverified"},
		AssessedAt: request.CapturedAt,
	}
	for _, kind := range []TargetKind{TargetWorkflow, TargetSkill, TargetTool, TargetBusinessLogic, TargetAgentLoop, TargetKnowledge, TargetMemory, TargetOntology, TargetOptimizer} {
		d := RuntimeDomainCoverage{Kind: kind, Coverage: "uncaptured", Bindings: []RuntimeBindingEvidence{}, Gaps: []string{"dependency_inventory_missing"}}
		if kind == TargetAgentLoop {
			d.Coverage, d.ControlsSHA256 = "controls_only", request.ControlsSHA256
			d.Gaps = append(d.Gaps, "runtime_implementation_missing", "provider_implementation_missing")
		}
		byResource := map[string]RuntimeBindingEvidence{}
		missingVersion, missingDigest, conflicting := false, false, false
		for _, binding := range request.Bindings {
			if binding.Kind != kind {
				continue
			}
			d.Bindings = append(d.Bindings, binding)
			missingVersion = missingVersion || binding.Version == ""
			missingDigest = missingDigest || binding.PackageDigest == ""
			if prior, ok := byResource[binding.ID]; ok && (prior.Version != binding.Version || prior.PackageDigest != binding.PackageDigest) {
				conflicting = true
			}
			byResource[binding.ID] = binding
		}
		if len(d.Bindings) > 0 {
			d.Coverage = "declared_bindings_only"
			d.Gaps = append(d.Gaps, "binding_use_unproven")
			if missingVersion {
				d.Gaps = append(d.Gaps, "resource_version_missing")
			}
			if conflicting {
				d.Gaps = append(d.Gaps, "resource_version_ambiguous")
			}
			if kind == TargetTool {
				d.Gaps = append(d.Gaps, "tool_implementation_unverified")
			} else if kind == TargetSkill {
				d.Gaps = append(d.Gaps, "package_bytes_unverified")
				if missingDigest {
					d.Gaps = append(d.Gaps, "package_digest_missing")
				}
			}
		}
		sort.Slice(d.Bindings, func(i, j int) bool { return Digest(d.Bindings[i]) < Digest(d.Bindings[j]) })
		r.Domains = append(r.Domains, d)
	}
	if terminal == nil {
		r.Blockers = append(r.Blockers, "terminal_missing")
	} else {
		if err := validateRuntimeTerminal(request, *terminal); err != nil {
			return RuntimeReview{}, err
		}
		r.TerminalID, r.PipelineStatus, r.TraceStatus = terminal.ID, string(terminal.Status), terminal.TraceFinalization
		duration := terminal.DurationMS
		r.DurationMS, r.AssessedAt = &duration, terminal.CompletedAt
		if len(terminal.Usage) > 0 {
			r.ReportedUsage = privateCopy(terminal.Usage)
		}
		r.Signals = append(r.Signals, "pipeline_"+r.PipelineStatus)
		if terminal.TraceFinalization != "succeeded" {
			r.Blockers = append(r.Blockers, "trace_finalization_"+terminal.TraceFinalization)
		}
	}
	r.ID = "runtime-review:" + Digest(r)
	return r, nil
}

func runtimeReviewKey(tenant, attempt string) string {
	return runtimeEvidenceKey(tenant, attempt, runtimeReviewProtocol)
}

// V1 remains byte-for-byte compatible. V2 is selected only by an immutable,
// host-captured execution record belonging to this original request.
func BuildRuntimeReviewWithExecution(request RuntimeRequestEvidence, terminal *RuntimeTerminalEvidence, execution *RuntimeExecutionEvidence) (RuntimeReview, error) {
	r, err := BuildRuntimeReview(request, terminal)
	if terminal != nil && terminal.Dependencies != nil && execution == nil {
		return RuntimeReview{}, ErrInvalid
	}
	if err != nil || execution == nil {
		return r, err
	}
	if err := validateRuntimeExecutionEvidence(request, *execution); err != nil {
		return RuntimeReview{}, err
	}
	if terminal != nil && terminal.Dependencies != nil && (terminal.Dependencies.ExecutionID != execution.ID || execution.Snapshot.DependencyProtocol != agentcore.RuntimeDependencyProtocol) {
		return RuntimeReview{}, ErrInvalid
	}
	if terminal != nil && execution.CapturedAt.After(terminal.CompletedAt) {
		return RuntimeReview{}, ErrInvalid
	}
	r.ID = ""
	r.Protocol, r.PolicySHA256 = runtimeReviewProtocolV2, Digest(runtimeReviewPolicyV2)
	r.ExecutionID, r.RuntimePolicySHA256, r.ImplementationSHA256 = execution.ID, execution.PolicySHA256, execution.Implementation.BinarySHA256
	for i := range r.Domains {
		d := &r.Domains[i]
		if d.Kind == TargetAgentLoop {
			d.Coverage, d.ControlsSHA256 = "host_binary_and_resolved_policy", execution.PolicySHA256
			gaps := []string{}
			for _, gap := range d.Gaps {
				if gap != "runtime_implementation_missing" {
					gaps = append(gaps, gap)
				}
			}
			d.Gaps = gaps
		}
		if d.Kind == TargetMemory {
			d.Coverage, d.ControlsSHA256 = "runtime_context_policy_only", execution.PolicySHA256
			d.Gaps = append(d.Gaps, "memory_content_and_versions_unverified")
		}
	}
	r.Signals = append(r.Signals, "host_execution_manifest_captured")
	if execution.Snapshot.Continuing || execution.Snapshot.ReplayCompleted {
		r.Signals = append(r.Signals, "runtime_checkpoint_restored")
	}
	r.ID = "runtime-review:" + Digest(r)
	return r, nil
}

func validRuntimeReviewPolicy(r RuntimeReview) bool {
	return (r.Protocol == runtimeReviewProtocol && r.PolicySHA256 == Digest(runtimeReviewPolicy) && r.ExecutionID == "" && r.RuntimePolicySHA256 == "" && r.ImplementationSHA256 == "") ||
		(r.Protocol == runtimeReviewProtocolV2 && r.PolicySHA256 == Digest(runtimeReviewPolicyV2) && strings.HasPrefix(r.ExecutionID, "runtime-execution:") && hashOK(r.RuntimePolicySHA256) && hashOK(r.ImplementationSHA256))
}

func immutableRuntimeReviewTx(ctx context.Context, tx db.Tx, review RuntimeReview) error {
	key := runtimeEvidenceKey(review.TenantID, review.AttemptID, review.Protocol)
	created, err := db.InsertRecordTx(ctx, tx, runtimeReviewTable, key, encode(review))
	if err != nil || created {
		return err
	}
	record, exists, err := db.GetRecordTx(ctx, tx, runtimeReviewTable, key)
	if err != nil {
		return err
	}
	stored, err := decode[RuntimeReview](record)
	if !exists || err != nil || !reflect.DeepEqual(stored, review) {
		return ErrConflict
	}
	return nil
}

// GetReview is read-only. Older records and prepared-only attempts get a derived
// review with persisted=false; inspection never rewrites their evidence/history.
func (s *RuntimeEvidenceStore) GetReview(ctx context.Context, tenant, attempt string) (RuntimeReview, bool, error) {
	request, err := s.GetRequest(ctx, tenant, attempt)
	if err != nil {
		return RuntimeReview{}, false, err
	}
	var terminal *RuntimeTerminalEvidence
	end, err := s.GetTerminal(ctx, tenant, attempt)
	if err == nil {
		terminal = &end
	} else if !errors.Is(err, ErrNotFound) {
		return RuntimeReview{}, false, err
	}
	var execution *RuntimeExecutionEvidence
	actual, err := s.GetExecution(ctx, tenant, attempt)
	if err == nil {
		execution = &actual
	} else if !errors.Is(err, ErrNotFound) {
		return RuntimeReview{}, false, err
	}
	review, err := BuildRuntimeReviewWithExecution(request, terminal, execution)
	if err != nil {
		return RuntimeReview{}, false, err
	}
	if terminal != nil {
		otherProtocol := runtimeReviewProtocolV2
		if review.Protocol == runtimeReviewProtocolV2 {
			otherProtocol = runtimeReviewProtocol
		}
		if _, exists, err := s.store.core.GetRecord(ctx, runtimeReviewTable, runtimeEvidenceKey(tenant, attempt, otherProtocol)); err != nil {
			return RuntimeReview{}, false, err
		} else if exists {
			return RuntimeReview{}, false, fmt.Errorf("%w: execution evidence conflicts with persisted review protocol", ErrInvalid)
		}
	}
	record, exists, err := s.store.core.GetRecord(ctx, runtimeReviewTable, runtimeEvidenceKey(tenant, attempt, review.Protocol))
	if err != nil || !exists {
		return review, false, err
	}
	stored, err := decode[RuntimeReview](record)
	if err != nil || terminal == nil || !reflect.DeepEqual(stored, review) {
		return RuntimeReview{}, false, fmt.Errorf("%w: runtime review does not match captured evidence", ErrInvalid)
	}
	return stored, true, nil
}

func IsCapturedRuntimeCase(c Case) bool {
	return strings.HasPrefix(c.Observation.Trigger, "captured_runtime_")
}

func needsRuntimeBaseline(c Case) bool {
	if IsCapturedRuntimeCase(c) {
		return true
	}
	switch c.Observation.Trigger {
	case "runtime_completed", "runtime_failed", "runtime_timed_out", "runtime_cancelled", "runtime_blocked":
		return true
	}
	return false
}

// RuntimeReviewForCase checks the case's complete original observation, not just
// a caller-supplied attempt ID, before allowing projection or admission review.
func (s *Store) RuntimeReviewForCase(ctx context.Context, c Case) (RuntimeReview, error) {
	if s.ready() != nil {
		return RuntimeReview{}, ErrUnavailable
	}
	if !IsCapturedRuntimeCase(c) {
		return RuntimeReview{}, ErrNotFound
	}
	attempt := ""
	for _, ref := range c.Observation.RelatedObjects {
		if ref.Kind == "runtime_attempt" {
			if attempt != "" {
				return RuntimeReview{}, ErrInvalid
			}
			attempt = ref.ID
		}
	}
	evidence := NewRuntimeEvidenceStore(s.core)
	request, err := evidence.GetRequest(ctx, c.TenantID, attempt)
	if err != nil {
		return RuntimeReview{}, err
	}
	terminal, err := evidence.GetTerminal(ctx, c.TenantID, attempt)
	if err != nil {
		return RuntimeReview{}, err
	}
	observation, err := capturedRuntimeObservation(request, terminal)
	if err != nil || c.ID != caseID(observation) || !reflect.DeepEqual(c.Observation, observation) {
		return RuntimeReview{}, fmt.Errorf("%w: case does not belong to runtime evidence", ErrInvalid)
	}
	review, _, err := evidence.GetReview(ctx, c.TenantID, attempt)
	return review, err
}

func (s *Store) verifyRuntimeAdmission(ctx context.Context, c Case, t Transition, verifier Verifier) error {
	if !needsRuntimeBaseline(c) || (t.To != Tested && t.To != Monitoring) {
		return nil
	}
	if !IsCapturedRuntimeCase(c) {
		return fmt.Errorf("%w: lifecycle-only observation has no captured runtime baseline", ErrUnavailable)
	}
	trusted, ok := verifier.(RuntimeBaselineVerifier)
	if !ok {
		return fmt.Errorf("%w: captured runtime requires a trusted baseline verifier", ErrUnavailable)
	}
	review, err := s.RuntimeReviewForCase(ctx, c)
	if err != nil {
		return err
	}
	return trusted.VerifyRuntimeBaseline(ctx, privateCopy(review), privateCopy(c), privateCopy(t))
}

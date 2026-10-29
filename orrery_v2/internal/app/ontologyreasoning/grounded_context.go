package ontologyreasoning

import (
	"context"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

const GroundedContextProtocol = "orrery.grounded-context.v1"

// A claim names a statement, independent of which source currently supports
// it. An exact support additionally binds an assertion to its checked version.
type ExactSupport struct {
	AssertionRef string `json:"assertion_ref"`
	Version      string `json:"version"`
}

type GroundingCandidate struct {
	ID               string         `json:"id"`
	RequiredClaims   []string       `json:"required_claims,omitempty"`
	RequiredSupports []ExactSupport `json:"required_supports,omitempty"`
}

type GroundedContextRequest struct {
	TenantID   string               `json:"tenant_id"`
	RootID     string               `json:"root_id"`
	Rules      []Rule               `json:"rules"`
	Candidates []GroundingCandidate `json:"candidates,omitempty"`
}

type GroundedSource struct {
	RootID      string `json:"root_id"`
	DocumentID  string `json:"provider_document_id"`
	Revision    int    `json:"revision"`
	ContentHash string `json:"content_hash"`
	Stamp       string `json:"stamp"`
}

type GroundedSupport struct {
	ExactSupport
	ID           string             `json:"id"`
	Atom         Atom               `json:"atom"`
	SourceState  string             `json:"source_state"`
	Source       *GroundedSource    `json:"source,omitempty"`
	EvidenceRefs []contextgraph.Ref `json:"evidence_refs,omitempty"`
}

// Proofs are alternatives (OR). Each proof's claims/supports are conjunctive
// (AND). References form a bounded proof graph rather than exponential paths.
type GroundedProof struct {
	RuleID      string         `json:"rule_id,omitempty"`
	RuleVersion string         `json:"rule_version,omitempty"`
	Claims      []string       `json:"claims,omitempty"`
	Supports    []ExactSupport `json:"supports,omitempty"`
}

type GroundedClaim struct {
	ID        string          `json:"id"`
	Atom      Atom            `json:"atom"`
	State     string          `json:"state"`
	ProofHash string          `json:"proof_hash"`
	Proofs    []GroundedProof `json:"proofs"`
}

type GroundingDecision struct {
	ID       string `json:"id"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason"`
}

type GroundedContext struct {
	Protocol     string              `json:"protocol"`
	TenantID     string              `json:"tenant_id"`
	RootID       string              `json:"root_id"`
	BusinessAsOf time.Time           `json:"business_as_of"`
	KnownAt      time.Time           `json:"known_at"`
	CheckedAt    time.Time           `json:"checked_at"`
	NextBoundary *time.Time          `json:"next_boundary,omitempty"`
	Semantics    string              `json:"semantics"`
	RuleHash     string              `json:"rule_hash"`
	SnapshotHash string              `json:"snapshot_hash"`
	AccessHash   string              `json:"access_hash"`
	Claims       []GroundedClaim     `json:"claims"`
	Supports     []GroundedSupport   `json:"supports"`
	Decisions    []GroundingDecision `json:"decisions,omitempty"`
	Conflicts    []FactConflict      `json:"conflicts,omitempty"`
	Disputed     []contextgraph.Fact `json:"disputed_assertions,omitempty"`
	Limitations  []string            `json:"limitations"`
	Guidance     string              `json:"guidance"`
}

const GroundedContextGuidance = "Supported in this bounded, authorized ontology snapshot means a source assertion or a structurally valid rule proof exists, not that the statement is universally true. Inspect source certainty and rule meaning yourself. Alternative proofs are OR; requirements within a proof or candidate are AND. An exact support version cannot be replaced by another source. Disputed assertions are material, never definite premises. Omitted claims may be outside this root's bounded context. Re-read before reuse; this read stamp never authorizes a write."

func ValidateGroundedContextRequest(req GroundedContextRequest) error {
	validID := func(id string) bool { return id != "" && len(id) <= 256 && strings.TrimSpace(id) == id }
	if !validID(req.TenantID) || !validID(req.RootID) || len(req.Candidates) > 100 {
		return ErrInvalidRequest
	}
	if err := ValidateRules(req.Rules); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range req.Candidates {
		if !validID(c.ID) || seen[c.ID] || len(c.RequiredClaims)+len(c.RequiredSupports) == 0 || len(c.RequiredClaims)+len(c.RequiredSupports) > 32 {
			return ErrInvalidRequest
		}
		seen[c.ID] = true
		for _, id := range c.RequiredClaims {
			if !validID(id) {
				return ErrInvalidRequest
			}
		}
		for _, support := range c.RequiredSupports {
			if !validID(support.AssertionRef) || !validID(support.Version) {
				return ErrInvalidRequest
			}
		}
	}
	return nil
}

// GroundContext must receive host-built, policy-projected inputs and sources.
// It always recomputes the closure; callers cannot submit a cached proof stamp.
func (s *Service) GroundContext(ctx context.Context, pack ContextPack, record EvaluationRecord, req GroundedContextRequest, sources map[string]GroundedSource) (GroundedContext, error) {
	if err := ValidateGroundedContextRequest(req); err != nil {
		return GroundedContext{}, err
	}
	if req.TenantID != pack.TenantID || record.RootID != req.RootID || record.Historical || record.PreviousRunID != "" {
		return GroundedContext{}, ErrInvalidRequest
	}
	root := record.RootID
	// The authenticated snapshot reader has already bounded reachability. Do
	// not make document navigation links independent logical source assertions.
	record.RootID, record.Rules = "", req.Rules
	run, err := s.evaluate(ctx, pack, record, nil, false)
	if err != nil {
		return GroundedContext{}, err
	}
	e := run.Request.Evaluation
	out := GroundedContext{Protocol: GroundedContextProtocol, TenantID: pack.TenantID, RootID: root, BusinessAsOf: record.BusinessAsOf, KnownAt: record.KnownAt, Semantics: InferenceSemanticsVersion, RuleHash: e.Closure.RuleHash, NextBoundary: e.NextBoundary, Conflicts: e.FactConflicts, Claims: []GroundedClaim{}, Supports: []GroundedSupport{}, Limitations: []string{"bounded_root_snapshot", "source_heads_are_local_attestations", "projection_is_not_upstream_truth"}, Guidance: GroundedContextGuidance}
	claimID := func(a Atom) string { return stableID("grounded-claim:", []string{pack.TenantID, atomKey(a)}) }
	claims := map[string]*GroundedClaim{}
	statementClaims := map[string]string{}
	ensure := func(a Atom) *GroundedClaim {
		id := claimID(a)
		if claims[id] == nil {
			claims[id] = &GroundedClaim{ID: id, Atom: a, State: "supported_in_snapshot"}
		}
		return claims[id]
	}
	versions := groundingAssertionVersions(run.ContextPack)
	_, evidence, err := premisesFromPack(run.ContextPack)
	if err != nil {
		return GroundedContext{}, err
	}
	for _, p := range e.Closure.Premises {
		version := stableID("assertion-version:", []any{p, versions[p.AssertionRef], sources[p.AssertionRef]})
		support := GroundedSupport{ExactSupport: ExactSupport{p.AssertionRef, version}, ID: p.ID, Atom: p.Atom, SourceState: "current_projection", EvidenceRefs: evidence[p.ID]}
		if source, ok := sources[p.AssertionRef]; ok {
			support.Source, support.SourceState = &source, "attested_source_head"
		}
		out.Supports = append(out.Supports, support)
		claim := ensure(p.Atom)
		claim.Proofs = append(claim.Proofs, GroundedProof{Supports: []ExactSupport{support.ExactSupport}})
		statementClaims[p.ID] = claim.ID
	}
	for _, c := range e.Closure.Conclusions {
		if e.SupportHashes[c.ID] != "" {
			statementClaims[c.ID] = ensure(c.Atom).ID
		}
	}
	for _, c := range e.Closure.Conclusions {
		id := statementClaims[c.ID]
		if id == "" {
			continue
		}
		for _, d := range c.Derivations {
			proof := GroundedProof{RuleID: d.RuleID, RuleVersion: d.RuleVersion}
			for _, p := range d.PremiseIDs {
				proof.Claims = append(proof.Claims, statementClaims[p])
			}
			sort.Strings(proof.Claims)
			claims[id].Proofs = append(claims[id].Proofs, proof)
		}
	}
	for _, claim := range claims {
		sort.Slice(claim.Proofs, func(i, j int) bool { return hashJSON(claim.Proofs[i]) < hashJSON(claim.Proofs[j]) })
	}
	for _, claim := range claims {
		claim.ProofHash = groundedProofHash(claim.ID, claims)
		out.Claims = append(out.Claims, *claim)
	}
	sort.Slice(out.Claims, func(i, j int) bool { return out.Claims[i].ID < out.Claims[j].ID })
	blocked, _ := conflictedFacts(run.ContextPack)
	for _, f := range run.ContextPack.Facts {
		if f.Status == "disputed" || blocked[factSlot{f.Subject.ID, f.Predicate}] {
			out.Disputed = append(out.Disputed, f)
		}
	}
	for _, candidate := range req.Candidates {
		decision := GroundingDecision{ID: candidate.ID, Eligible: true, Reason: "all_dependencies_supported"}
		for _, id := range candidate.RequiredClaims {
			if claims[id] == nil {
				decision.Eligible, decision.Reason = false, "claim_not_supported_in_snapshot"
			}
		}
		for _, required := range candidate.RequiredSupports {
			found := false
			for _, support := range out.Supports {
				found = found || support.ExactSupport == required
			}
			if !found {
				decision.Eligible, decision.Reason = false, "exact_support_not_current"
			}
		}
		out.Decisions = append(out.Decisions, decision)
	}
	// Coordinates and checked_at are observations, not artificial versions.
	out.SnapshotHash = stableID("grounded-snapshot:", []any{pack.TenantID, req.RootID, InferenceSemanticsVersion, out.RuleHash, out.Claims, out.Supports, out.Conflicts, out.Disputed})
	return out, nil
}

func groundingAssertionVersions(pack ContextPack) map[string]any {
	out := map[string]any{}
	evidence := map[string]contextgraph.Evidence{}
	for _, item := range pack.Evidence {
		evidence[item.ID] = item
	}
	version := func(value any, refs []contextgraph.Ref) any {
		attached := map[string]contextgraph.Evidence{}
		for _, ref := range refs {
			if item, ok := evidence[ref.ID]; ok && ref.Kind == "evidence" {
				attached[ref.ID] = item
			}
		}
		return []any{value, attached}
	}
	for _, o := range pack.CandidateObjects {
		out["object:"+o.ID] = []any{version(o, o.EvidenceRefs), o.SourceStreamID, o.SourceVersion}
	}
	for _, l := range pack.NeighborLinks {
		out["link:"+l.ID] = []any{version(l, l.EvidenceRefs), l.SourceStreamID, l.SourceVersion}
	}
	for _, f := range pack.Facts {
		out["fact:"+f.ID] = []any{version(f, f.EvidenceRefs), f.SourceStreamID, f.SourceVersion}
	}
	return out
}

func groundedProofHash(id string, claims map[string]*GroundedClaim) string {
	graph := map[string][]GroundedProof{}
	var visit func(string)
	visit = func(id string) {
		if _, seen := graph[id]; seen {
			return
		}
		claim := claims[id]
		if claim == nil {
			return
		}
		graph[id] = claim.Proofs
		for _, p := range claim.Proofs {
			for _, dependency := range p.Claims {
				visit(dependency)
			}
		}
	}
	visit(id)
	return stableID("proof:", graph)
}

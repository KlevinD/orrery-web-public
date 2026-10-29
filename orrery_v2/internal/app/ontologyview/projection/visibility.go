package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type projectionPolicyState struct {
	scope      ontologyscope.AuthorizedScopeContext
	constraint contextgraph.VisibilityConstraint
	revision   string
	cacheable  bool
}

type visibleAudit struct {
	audit    contextgraph.ProjectionAudit
	decision datavisibility.ResourceDecision
}

type visibleReplayHistory struct {
	history  contextgraph.ProjectionAuditReplayHistory
	decision datavisibility.ResourceDecision
}

func (s *Service) resolveVisibility(
	ctx context.Context,
	principal identity.Principal,
	authorized ontologyscope.AuthorizedScopeContext,
	budget *ontologycontract.BudgetTracker,
) (projectionPolicyState, error) {
	probe := projectionScopeDescriptor(authorized, "projection-scope-probe", nil, nil)
	probeDecision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.projection.read",
		Resources: []datavisibility.ResourceDescriptor{probe},
	})
	if err != nil {
		return projectionPolicyState{}, dependencyError(err)
	}
	resource, ok := probeDecision.Resource(probe.Key)
	if !ok {
		return projectionPolicyState{}, dependencyError(errors.New("projection scope visibility decision is incomplete"))
	}
	state := projectionPolicyState{
		scope: authorized, revision: combinedPolicyRevision(authorized.Snapshot().PolicyRevision, probeDecision.PolicyRevision),
		cacheable: false,
	}
	if hiddenDecision(resource) {
		return state, nil
	}
	if budget == nil {
		return projectionPolicyState{}, ontologycontract.ErrQueryBudgetExceeded
	}
	state.constraint = contextgraph.CandidateVisibilityConstraint()
	return state, nil
}

func (s *Service) visibleAudits(
	ctx context.Context,
	principal identity.Principal,
	state projectionPolicyState,
	audits []contextgraph.ProjectionAudit,
	fields []string,
) ([]visibleAudit, error) {
	if len(audits) == 0 {
		return nil, nil
	}
	descriptors := make([]datavisibility.ResourceDescriptor, 0, len(audits))
	seen := make(map[string]struct{}, len(audits))
	for _, audit := range audits {
		if audit.TenantID != state.scope.Binding().TenantID || !validID(audit.ID) || audit.ObservedAt.IsZero() || !contextgraph.NormalizeAccessRef(audit.SourceAccessRef).Resolved() {
			return nil, dependencyError(errors.New("projection audit reader returned an invalid or cross-tenant row"))
		}
		if _, duplicate := seen[audit.ID]; duplicate {
			return nil, dependencyError(fmt.Errorf("projection audit reader returned duplicate id %s", audit.ID))
		}
		seen[audit.ID] = struct{}{}
		descriptors = append(descriptors, datavisibility.ResourceDescriptor{
			Key: "projection-audit:" + audit.ID, Kind: datavisibility.ResourceAudit, TenantID: audit.TenantID,
			Type: audit.SourceEventType, FieldPaths: append([]string(nil), fields...),
			AccessRefs: projectionAccessRefs(state.scope, audit.SourceAccessRef), Payload: audit,
		})
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.projection.read", Resources: descriptors,
	})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyProjectionRevision(state, decision); err != nil {
		return nil, err
	}
	out := make([]visibleAudit, 0, len(audits))
	for _, audit := range audits {
		resource, ok := decision.Resource("projection-audit:" + audit.ID)
		if !ok {
			return nil, dependencyError(errors.New("projection audit visibility decision is incomplete"))
		}
		if !hiddenDecision(resource) {
			out = append(out, visibleAudit{audit: audit, decision: resource})
		}
	}
	return out, nil
}

func (s *Service) visibleReplayHistoryRows(
	ctx context.Context,
	principal identity.Principal,
	state projectionPolicyState,
	auditID string,
	history []contextgraph.ProjectionAuditReplayHistory,
	fields []string,
) ([]visibleReplayHistory, error) {
	if len(history) == 0 {
		return nil, nil
	}
	descriptors := make([]datavisibility.ResourceDescriptor, 0, len(history))
	seen := make(map[string]struct{}, len(history))
	for _, item := range history {
		if item.TenantID != state.scope.Binding().TenantID || item.AuditID != auditID || !validID(item.ID) || item.AttemptedAt.IsZero() || !contextgraph.NormalizeAccessRef(item.SourceAccessRef).Resolved() {
			return nil, dependencyError(errors.New("projection replay history reader returned an invalid or cross-tenant row"))
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, dependencyError(fmt.Errorf("projection replay history reader returned duplicate id %s", item.ID))
		}
		seen[item.ID] = struct{}{}
		descriptors = append(descriptors, datavisibility.ResourceDescriptor{
			Key: "projection-replay-history:" + item.ID, Kind: datavisibility.ResourceAudit, TenantID: item.TenantID,
			Type: item.SourceEventType, FieldPaths: append([]string(nil), fields...),
			AccessRefs: projectionAccessRefs(state.scope, item.SourceAccessRef), Payload: item,
		})
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.projection.read", Resources: descriptors,
	})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyProjectionRevision(state, decision); err != nil {
		return nil, err
	}
	out := make([]visibleReplayHistory, 0, len(history))
	for _, item := range history {
		resource, ok := decision.Resource("projection-replay-history:" + item.ID)
		if !ok {
			return nil, dependencyError(errors.New("projection replay history visibility decision is incomplete"))
		}
		if !hiddenDecision(resource) {
			out = append(out, visibleReplayHistory{history: item, decision: resource})
		}
	}
	return out, nil
}

func projectionScopeDescriptor(scope ontologyscope.AuthorizedScopeContext, key string, fields []string, payload any) datavisibility.ResourceDescriptor {
	binding := scope.Binding()
	return datavisibility.ResourceDescriptor{
		Key: key, Kind: datavisibility.ResourceAudit, TenantID: binding.TenantID, Type: string(binding.Kind),
		FieldPaths: append([]string(nil), fields...),
		AccessRefs: []datavisibility.AccessRef{{
			Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID,
		}},
		Payload: payload,
	}
}

func projectionAccessRefs(scope ontologyscope.AuthorizedScopeContext, refs ...contextgraph.AccessRef) []datavisibility.AccessRef {
	binding := scope.Binding()
	out := []datavisibility.AccessRef{{Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID}}
	seen := map[datavisibility.AccessRef]struct{}{out[0]: struct{}{}}
	for _, ref := range refs {
		ref = contextgraph.NormalizeAccessRef(ref)
		converted := datavisibility.AccessRef{Kind: ref.Kind, ID: ref.ID, TenantID: ref.TenantID}
		if !converted.Resolved() {
			continue
		}
		if _, duplicate := seen[converted]; duplicate {
			continue
		}
		seen[converted] = struct{}{}
		out = append(out, converted)
	}
	return out
}

func verifyProjectionRevision(state projectionPolicyState, decision datavisibility.Decision) error {
	want := strings.TrimSpace(state.revision)
	got := combinedPolicyRevision(state.scope.Snapshot().PolicyRevision, decision.PolicyRevision)
	if got != want {
		return dependencyError(errors.New("projection visibility revision changed during query"))
	}
	return nil
}

func combinedPolicyRevision(snapshotRevision, decisionRevision string) string {
	snapshotRevision = strings.TrimSpace(snapshotRevision)
	decisionRevision = strings.TrimSpace(decisionRevision)
	if snapshotRevision == "" && decisionRevision == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(snapshotRevision + "\x00" + decisionRevision))
	return hex.EncodeToString(sum[:])
}

func hiddenDecision(decision datavisibility.ResourceDecision) bool {
	return decision.Hidden || decision.ExistenceDisclosure == datavisibility.ExistenceConceal
}

func allowedFields(decision datavisibility.ResourceDecision) map[string]bool {
	out := make(map[string]bool, len(decision.Fields.Allowed))
	for _, field := range decision.Fields.Allowed {
		out[field] = true
	}
	return out
}

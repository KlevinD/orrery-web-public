package governance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

const maxGovernanceAuditAccessRefs = 8

type visibleAuditEvent struct {
	event    ProposalAuditRecord
	decision datavisibility.ResourceDecision
}

func (s *Service) ListAuditEvents(ctx context.Context, principal identity.Principal, query AuditEventListQuery) (ontologycontract.CursorPage[AuditEventItem], error) {
	query, limit, err := normalizeAuditEventListQuery(query)
	if err != nil {
		return ontologycontract.CursorPage[AuditEventItem]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[AuditEventItem]{}, err
	}
	state, err := s.resolvePolicyState(ctx, principal, authorized, "ontology.governance.audit.read", datavisibility.ResourceProposal, datavisibility.ResourceAudit)
	if err != nil {
		return ontologycontract.CursorPage[AuditEventItem]{}, err
	}
	queryHash, err := auditEventListQueryHash(query, limit)
	if err != nil {
		return ontologycontract.CursorPage[AuditEventItem]{}, err
	}
	binding := governanceCursorBinding(state, queryHash)
	seek, err := s.resolveAuditCursor(query.Cursor, binding, state.cacheable)
	if err != nil {
		return ontologycontract.CursorPage[AuditEventItem]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[AuditEventItem]{}, err
	}
	defer cancel()
	if _, err := s.requireVisibleProposal(bounded, principal, state, budget, query.TenantID, query.ProposalID); err != nil {
		return ontologycontract.CursorPage[AuditEventItem]{}, err
	}

	visible := make([]visibleAuditEvent, 0, limit+1)
	for len(visible) <= limit {
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[AuditEventItem]{}, err
		}
		page, err := s.audits.ListProposalAuditEvents(bounded, ProposalAuditQuery{
			TenantID: query.TenantID, ProposalID: query.ProposalID, Limit: governanceScanBatch, After: seek,
		})
		if err != nil {
			return ontologycontract.CursorPage[AuditEventItem]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(scannedCount(len(page.Events), page.HasMore)); err != nil {
			return ontologycontract.CursorPage[AuditEventItem]{}, err
		}
		if len(page.Events) == 0 {
			if page.HasMore {
				return ontologycontract.CursorPage[AuditEventItem]{}, dependencyError(errors.New("audit reader returned an empty truncated page"))
			}
			break
		}
		rows, err := s.visibleAuditEvents(bounded, principal, state, query.TenantID, query.ProposalID, page.Events)
		if err != nil {
			return ontologycontract.CursorPage[AuditEventItem]{}, queryError(bounded, budget, err)
		}
		visible = append(visible, rows...)
		if len(visible) > limit || !page.HasMore {
			break
		}
		last := page.Events[len(page.Events)-1]
		seek = ProposalAuditSeek{OccurredAt: last.OccurredAt, AuditID: last.AuditID}
	}

	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]AuditEventItem, len(visible))
	for index, row := range visible {
		items[index] = mapAuditEventItem(row.event, row.decision)
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[AuditEventItem]{}, err
	}
	next := ""
	if hasMore {
		last := visible[len(visible)-1].event
		next, err = s.issueAuditCursor(binding, state.cacheable, ProposalAuditSeek{OccurredAt: last.OccurredAt, AuditID: last.AuditID})
		if err != nil {
			return ontologycontract.CursorPage[AuditEventItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, next)
}

func (s *Service) visibleAuditEvents(
	ctx context.Context,
	principal identity.Principal,
	state governancePolicyState,
	tenantID, proposalID string,
	events []ProposalAuditRecord,
) ([]visibleAuditEvent, error) {
	if len(events) == 0 {
		return nil, nil
	}
	binding := state.scope.Binding()
	descriptors := make([]datavisibility.ResourceDescriptor, 0, len(events))
	seen := map[string]struct{}{}
	for _, event := range events {
		if event.TenantID != tenantID || event.ProposalID != proposalID || !validID(event.AuditID) || event.OccurredAt.IsZero() {
			return nil, dependencyError(errors.New("audit reader returned an invalid or cross-scope row"))
		}
		if _, duplicate := seen[event.AuditID]; duplicate {
			return nil, dependencyError(errorsNewDuplicate("audit", event.AuditID))
		}
		seen[event.AuditID] = struct{}{}
		refs := []datavisibility.AccessRef{{Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID}}
		refs = append(refs, event.AccessRefs...)
		refs, err := normalizeAuditAccessRefs(refs)
		if err != nil {
			return nil, err
		}
		descriptors = append(descriptors, datavisibility.ResourceDescriptor{
			Key: "audit:" + event.AuditID, Kind: datavisibility.ResourceAudit, TenantID: tenantID, Type: "governance",
			FieldPaths: append([]string(nil), auditEventListFields...), AccessRefs: refs, Payload: event,
		})
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: state.purpose, Resources: descriptors,
	})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyKindRevision(state, datavisibility.ResourceAudit, decision); err != nil {
		return nil, err
	}
	out := make([]visibleAuditEvent, 0, len(events))
	for _, event := range events {
		resource, ok := decision.Resource("audit:" + event.AuditID)
		if !ok {
			return nil, dependencyError(errors.New("audit visibility decision is incomplete"))
		}
		if resource.Hidden || resource.ExistenceDisclosure == datavisibility.ExistenceConceal {
			continue
		}
		out = append(out, visibleAuditEvent{event: event, decision: resource})
	}
	return out, nil
}

func normalizeAuditAccessRefs(refs []datavisibility.AccessRef) ([]datavisibility.AccessRef, error) {
	out := make([]datavisibility.AccessRef, 0, len(refs))
	seen := map[datavisibility.AccessRef]struct{}{}
	for _, ref := range refs {
		ref.Kind = strings.ToLower(strings.TrimSpace(ref.Kind))
		ref.ID = strings.TrimSpace(ref.ID)
		ref.TenantID = strings.TrimSpace(ref.TenantID)
		if !ref.Resolved() {
			return nil, dependencyError(errors.New("audit access reference is unresolved"))
		}
		if _, duplicate := seen[ref]; duplicate {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	if len(out) == 0 || len(out) > maxGovernanceAuditAccessRefs {
		return nil, fmt.Errorf("%w: audit access references exceed their bound", ontologycontract.ErrQueryBudgetExceeded)
	}
	return out, nil
}

func normalizeAuditEventListQuery(query AuditEventListQuery) (AuditEventListQuery, int, error) {
	tenantID, proposalID, err := normalizeExactRequest(query.TenantID, query.ProposalID)
	if err != nil {
		return AuditEventListQuery{}, 0, err
	}
	query.TenantID, query.ProposalID = tenantID, proposalID
	query.Cursor = normalizeCursorToken(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultChildListLimit, ontologycontract.MaxPageLimit)
	if err != nil || len(query.Cursor) > 4096 {
		return AuditEventListQuery{}, 0, ontologycontract.ErrInvalidRequest
	}
	return query, limit, nil
}

func mapAuditEventItem(event ProposalAuditRecord, decision datavisibility.ResourceDecision) AuditEventItem {
	allowed := allowedFields(decision)
	item := AuditEventItem{AuditID: event.AuditID}
	if allowed["action"] {
		item.Action = event.Action
	}
	if allowed["result"] {
		item.Result = event.Result
	}
	if allowed["severity"] {
		item.Severity = event.Severity
	}
	if allowed["actor_id"] {
		item.ActorID = event.ActorID
	}
	if allowed["actor_kind"] {
		item.ActorKind = event.ActorKind
	}
	if allowed["summary"] {
		item.Summary = event.Summary
	}
	if allowed["occurred_at"] {
		item.OccurredAt = visibleTime(event.OccurredAt)
	}
	return item
}

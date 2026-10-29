package lineage

import (
	"context"
	"errors"
	"sort"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func (s *Service) GetProvenance(ctx context.Context, principal identity.Principal, tenantID, factID string) (ProvenanceAnchor, error) {
	tenantID, factID = strings.TrimSpace(tenantID), strings.TrimSpace(factID)
	if !validID(tenantID) || !validID(factID) {
		return ProvenanceAnchor{}, ontologycontract.ErrInvalidRequest
	}
	authorized, err := s.authorize(ctx, principal, tenantID)
	if err != nil {
		return ProvenanceAnchor{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ProvenanceAnchor{}, err
	}
	defer cancel()
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget,
		datavisibility.ResourceFact, datavisibility.ResourceObject, datavisibility.ResourceEvent, datavisibility.ResourceDefinition,
	)
	if err != nil {
		return ProvenanceAnchor{}, err
	}
	detail, factDecision, subjectDecision, err := s.getVisibleFact(
		bounded, principal, visibility.scope.Binding().TenantID, factID, visibility, budget,
		[]string{"predicate", "status", "confidence", "updated_at", "provenance"}, []string{"label"},
	)
	if err != nil {
		return ProvenanceAnchor{}, err
	}
	anchor := ProvenanceAnchor{
		Fact:             mapFactIdentity(detail, factDecision, subjectDecision),
		ProvenanceStatus: ReferenceUnresolved,
		Mapping:          MappingResolution{Status: MappingUnresolved},
	}
	if maskedFieldSet(factDecision)["provenance"] || strings.ToLower(strings.TrimSpace(detail.Fact.Provenance.Kind)) != "event" || !validID(detail.Fact.Provenance.ID) {
		if maskedFieldSet(factDecision)["provenance"] {
			anchor.ProvenanceStatus = ReferenceUnavailable
		}
		return anchor, nil
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return ProvenanceAnchor{}, err
	}
	event, err := s.events.GetSafeOntologyEvent(bounded, visibility.scope.Binding().TenantID, detail.Fact.Provenance.ID, detail.Fact.SourceAccessRef)
	if errors.Is(err, ErrSafeEventUnavailable) || errors.Is(err, contextgraph.ErrNotFound) {
		anchor.ProvenanceStatus = ReferenceUnavailable
		return anchor, nil
	}
	if err != nil {
		return ProvenanceAnchor{}, dependencyError(err)
	}
	if strings.TrimSpace(event.TenantID) != visibility.scope.Binding().TenantID || contextgraph.NormalizeAccessRef(event.SourceAccessRef) != contextgraph.NormalizeAccessRef(detail.Fact.SourceAccessRef) || event.Metadata.ID != detail.Fact.Provenance.ID {
		anchor.ProvenanceStatus = ReferenceUnavailable
		return anchor, nil
	}
	visibleEvent, ok, err := s.visibleEvent(bounded, principal, visibility, event)
	if err != nil {
		return ProvenanceAnchor{}, err
	}
	if !ok {
		anchor.ProvenanceStatus = ReferenceUnavailable
		return anchor, nil
	}
	anchor.ProvenanceStatus = ReferenceResolved
	anchor.Event = &visibleEvent
	if visibleEvent.Type == "" {
		return anchor, nil
	}
	mapping, err := s.resolveMappings(bounded, principal, visibility, visibility.scope.Binding().TenantID, visibleEvent.Type, budget)
	if err != nil {
		return ProvenanceAnchor{}, err
	}
	anchor.Mapping = mapping
	return anchor, nil
}

func mapFactIdentity(detail contextgraph.FactDetail, factDecision, subjectDecision datavisibility.ResourceDecision) FactIdentity {
	factMasked := maskedFieldSet(factDecision)
	subjectMasked := maskedFieldSet(subjectDecision)
	item := FactIdentity{
		ID:         detail.Fact.ID,
		Subject:    SubjectIdentity{Kind: detail.Fact.Subject.Kind, ID: detail.Fact.Subject.ID},
		Predicate:  detail.Fact.Predicate,
		Status:     detail.Fact.Status,
		Confidence: floatValue(detail.Fact.Confidence),
		UpdatedAt:  visibleTime(detail.Fact.UpdatedAt),
	}
	if !subjectMasked["label"] {
		item.Subject.Label = detail.Subject.Label
	}
	if factMasked["predicate"] {
		item.Predicate = ""
	}
	if factMasked["status"] {
		item.Status = ""
	}
	if factMasked["confidence"] {
		item.Confidence = nil
	}
	if factMasked["updated_at"] {
		item.UpdatedAt = nil
	}
	return item
}

func (s *Service) visibleEvent(ctx context.Context, principal identity.Principal, state visibilityState, event SafeEvent) (SafeEventMetadata, bool, error) {
	key := "event:" + event.Metadata.ID
	descriptor := datavisibility.ResourceDescriptor{
		Key: key, Kind: datavisibility.ResourceEvent, TenantID: event.TenantID, Type: event.Metadata.Type,
		FieldPaths: []string{"type", "stream_id", "version", "schema_version", "occurred_at"},
		AccessRefs: visibilityAccessRefs(event.SourceAccessRef), Payload: event,
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.lineage.read", Resources: []datavisibility.ResourceDescriptor{descriptor},
	})
	if err != nil {
		return SafeEventMetadata{}, false, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceEvent, decision); err != nil {
		return SafeEventMetadata{}, false, err
	}
	resource, ok := decision.Resource(key)
	if !ok {
		return SafeEventMetadata{}, false, dependencyError(datavisibility.ErrIncompleteDecision)
	}
	if hiddenDecision(resource) {
		return SafeEventMetadata{}, false, nil
	}
	out := event.Metadata
	masked := maskedFieldSet(resource)
	if masked["type"] {
		out.Type = ""
	}
	if masked["stream_id"] {
		out.StreamID = ""
	}
	if masked["version"] {
		out.Version = 0
	}
	if masked["schema_version"] {
		out.SchemaVersion = ""
	}
	if masked["occurred_at"] {
		out.OccurredAt = nil
	}
	return out, true, nil
}

func (s *Service) resolveMappings(ctx context.Context, principal identity.Principal, state visibilityState, tenantID, eventType string, budget *ontologycontract.BudgetTracker) (MappingResolution, error) {
	if err := budget.ConsumeStoreReads(1); err != nil {
		return MappingResolution{}, err
	}
	page, err := s.rules.ListEnabledMappingRulesByEventType(ctx, tenantID, eventType, maxMappingCandidates)
	if err != nil {
		return MappingResolution{}, dependencyError(err)
	}
	if err := budget.ConsumeCandidates(len(page.Items)); err != nil {
		return MappingResolution{}, err
	}
	if len(page.Items) == 0 {
		return MappingResolution{Status: MappingUnresolved}, nil
	}
	descriptors := make([]datavisibility.ResourceDescriptor, 0, len(page.Items))
	validRules := make([]ontology.MappingRule, 0, len(page.Items))
	for _, rule := range page.Items {
		if !rule.Enabled || strings.TrimSpace(rule.EventType) != eventType || !mappingTenantVisible(rule.TenantID, tenantID) {
			continue
		}
		validRules = append(validRules, rule)
		descriptors = append(descriptors, datavisibility.ResourceDescriptor{
			Key: "mapping:" + rule.ID, Kind: datavisibility.ResourceDefinition,
			TenantID: mappingPolicyTenant(rule.TenantID), Type: "mapping",
			FieldPaths: []string{"status", "version", "updated_at", "attributes.event_type"}, Payload: rule,
		})
	}
	if len(descriptors) == 0 {
		return MappingResolution{Status: MappingUnresolved, Truncated: page.HasMore}, nil
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.lineage.read", Resources: descriptors,
	})
	if err != nil {
		return MappingResolution{}, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceDefinition, decision); err != nil {
		return MappingResolution{}, err
	}
	result := MappingResolution{Status: MappingUnresolved, Truncated: page.HasMore}
	for _, rule := range validRules {
		resource, ok := decision.Resource("mapping:" + rule.ID)
		if !ok {
			return MappingResolution{}, dependencyError(datavisibility.ErrIncompleteDecision)
		}
		masked := maskedFieldSet(resource)
		if hiddenDecision(resource) || masked["status"] || masked["attributes.event_type"] {
			continue
		}
		item := MappingRuleRef{ID: rule.ID, Origin: "tenant", Version: rule.Version, UpdatedAt: visibleTime(rule.UpdatedAt)}
		if strings.TrimSpace(rule.TenantID) == "" || strings.TrimSpace(rule.TenantID) == "system" {
			item.Origin = "system"
		}
		if masked["version"] {
			item.Version = 0
		}
		if masked["updated_at"] {
			item.UpdatedAt = nil
		}
		result.Candidates = append(result.Candidates, item)
	}
	sort.Slice(result.Candidates, func(i, j int) bool { return result.Candidates[i].ID < result.Candidates[j].ID })
	if len(result.Candidates) > 0 {
		result.Status = MappingCandidate
	}
	return result, nil
}

func mappingTenantVisible(definitionTenant, tenantID string) bool {
	definitionTenant = strings.TrimSpace(definitionTenant)
	return definitionTenant == "" || definitionTenant == "system" || definitionTenant == strings.TrimSpace(tenantID)
}

func mappingPolicyTenant(definitionTenant string) string {
	definitionTenant = strings.TrimSpace(definitionTenant)
	if definitionTenant == "" || definitionTenant == "system" {
		return "system"
	}
	return definitionTenant
}

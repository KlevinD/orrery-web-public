package contextview

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type visibleObjectRow struct {
	raw      contextgraph.Object
	detail   ObjectDetail
	decision datavisibility.ResourceDecision
}

func (s *Service) visibleObjectRows(ctx context.Context, principal identity.Principal, state visibilityState, objects []contextgraph.Object, detail bool, extraFields ...string) ([]visibleObjectRow, error) {
	if len(objects) == 0 {
		return nil, nil
	}
	unique := make([]contextgraph.Object, 0, len(objects))
	seen := make(map[string]struct{}, len(objects))
	for _, object := range objects {
		if !visibilityAccessRefsValid(object.OwnerAccessRef) {
			continue
		}
		if _, exists := seen[object.ID]; exists {
			continue
		}
		seen[object.ID] = struct{}{}
		unique = append(unique, object)
	}
	descriptors := make([]datavisibility.ResourceDescriptor, 0, len(unique))
	for _, object := range unique {
		fields, err := objectFieldPaths(object, detail)
		if err != nil {
			return nil, err
		}
		fields = append(fields, extraFields...)
		sort.Strings(fields)
		fields = compactSortedStrings(fields)
		descriptors = append(descriptors, datavisibility.ResourceDescriptor{
			Key: "object:" + object.ID, Kind: datavisibility.ResourceObject, TenantID: object.TenantID,
			Type: object.ObjectType, FieldPaths: fields, AccessRefs: visibilityAccessRefs(object.OwnerAccessRef), Payload: object,
		})
	}
	if len(descriptors) == 0 {
		return nil, nil
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.context.read", Resources: descriptors})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceObject, decision); err != nil {
		return nil, err
	}
	out := make([]visibleObjectRow, 0, len(unique))
	for _, object := range unique {
		resource, ok := decision.Resource("object:" + object.ID)
		if !ok {
			return nil, dependencyError(fmt.Errorf("object visibility decision is incomplete"))
		}
		if resource.Hidden || resource.ExistenceDisclosure == datavisibility.ExistenceConceal {
			continue
		}
		out = append(out, visibleObjectRow{raw: object, detail: mapObjectDetail(object, resource, detail), decision: resource})
	}
	return out, nil
}

func (s *Service) visibleObjects(ctx context.Context, principal identity.Principal, state visibilityState, objects []contextgraph.Object, detail bool) ([]ObjectDetail, error) {
	rows, err := s.visibleObjectRows(ctx, principal, state, objects, detail)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectDetail, len(rows))
	for index := range rows {
		out[index] = rows[index].detail
	}
	return out, nil
}

func compactSortedStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func objectFieldPaths(object contextgraph.Object, detail bool) ([]string, error) {
	fields := []string{"confidence", "label", "object_type", "sensitivity", "status", "updated_at", "temporal", "temporal.mode", "temporal.business_time_unspecified", "temporal.business_valid_from", "temporal.business_valid_to", "temporal.source_recorded_at", "temporal.source_superseded_at", "temporal.orrery_ingested_at"}
	if detail {
		fields = append(fields, "canonical_key")
		keys := make([]string, 0, len(object.Properties))
		for key := range object.Properties {
			key = strings.TrimSpace(key)
			if key == "" || len(key) > 240 {
				return nil, ontologycontract.ErrQueryBudgetExceeded
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fields = append(fields, "properties."+key)
		}
	}
	if len(fields) > 128 {
		return nil, ontologycontract.ErrQueryBudgetExceeded
	}
	return fields, nil
}

func mapObjectDetail(object contextgraph.Object, decision datavisibility.ResourceDecision, detail bool) ObjectDetail {
	masked := maskedFieldSet(decision)
	item := ObjectItem{
		Temporal: visibleTemporal(object.Temporal, masked),
		Redacted: masked["object_type"] || masked["status"] || temporalMasked(masked),
		ID:       object.ID, ObjectType: object.ObjectType, Label: object.Label, Status: object.Status,
		Sensitivity: object.Sensitivity, Confidence: object.Confidence, UpdatedAt: visibleTime(object.UpdatedAt),
	}
	if masked["object_type"] {
		item.ObjectType = ""
	}
	if masked["label"] {
		item.Label = object.ID
	}
	if masked["status"] {
		item.Status = ""
	}
	if masked["sensitivity"] {
		item.Sensitivity = ""
	}
	if masked["confidence"] {
		item.Confidence = 0
	}
	if masked["updated_at"] {
		item.UpdatedAt = nil
	}
	result := ObjectDetail{ObjectItem: item}
	if !detail {
		return result
	}
	result.CanonicalKey = object.CanonicalKey
	if masked["canonical_key"] {
		result.CanonicalKey = ""
	}
	if len(object.Properties) > 0 {
		result.Properties = make(map[string]any, len(object.Properties))
		for key, value := range object.Properties {
			if !masked["properties"] && !masked["properties."+strings.TrimSpace(key)] {
				result.Properties[key] = value
			}
		}
		if len(result.Properties) == 0 {
			result.Properties = nil
		}
	}
	return result
}

func (s *Service) visibleFacts(ctx context.Context, principal identity.Principal, state visibilityState, parent contextgraph.Object, facts []contextgraph.Fact) ([]FactItem, error) {
	if len(facts) == 0 {
		return nil, nil
	}
	descriptors := make([]datavisibility.ResourceDescriptor, 0, len(facts))
	selected := make([]contextgraph.Fact, 0, len(facts))
	for _, fact := range facts {
		if !visibilityAccessRefsValid(fact.SourceAccessRef, parent.OwnerAccessRef) {
			continue
		}
		descriptors = append(descriptors, datavisibility.ResourceDescriptor{
			Key: "fact:" + fact.ID, Kind: datavisibility.ResourceFact, TenantID: fact.TenantID, Type: "fact",
			FieldPaths: []string{"confidence", "predicate", "status", "updated_at", "value", "temporal", "temporal.mode", "temporal.business_time_unspecified", "temporal.orrery_ingested_at", "temporal.business_valid_from", "temporal.business_valid_to", "temporal.source_recorded_at", "temporal.source_superseded_at"},
			AccessRefs: visibilityAccessRefs(fact.SourceAccessRef, parent.OwnerAccessRef), Payload: fact,
		})
		selected = append(selected, fact)
	}
	if len(selected) == 0 {
		return nil, nil
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.context.read", Resources: descriptors})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceFact, decision); err != nil {
		return nil, err
	}
	out := make([]FactItem, 0, len(selected))
	for _, fact := range selected {
		resource, ok := decision.Resource("fact:" + fact.ID)
		if !ok {
			return nil, dependencyError(fmt.Errorf("fact visibility decision is incomplete"))
		}
		if resource.Hidden || resource.ExistenceDisclosure == datavisibility.ExistenceConceal {
			continue
		}
		masked := maskedFieldSet(resource)
		item := FactItem{ID: fact.ID, Predicate: fact.Predicate, Value: fact.Value, Status: fact.Status, Confidence: fact.Confidence, UpdatedAt: visibleTime(fact.UpdatedAt)}
		item.Redacted = masked["predicate"] || masked["value"] || masked["status"]
		if !item.Redacted {
			item.Status = contextgraph.AssertionStatusAt(fact, s.now().UTC())
		}
		item.Temporal = visibleTemporal(fact.Temporal, masked)
		item.EvidenceRefs = append([]contextgraph.Ref(nil), fact.EvidenceRefs...)
		item.Redacted = item.Redacted || temporalMasked(masked)
		if masked["predicate"] {
			item.Predicate = ""
		}
		if masked["value"] {
			item.Value = nil
		}
		if masked["status"] {
			item.Status = ""
		}
		if masked["confidence"] {
			item.Confidence = 0
		}
		if masked["updated_at"] {
			item.UpdatedAt = nil
		}
		out = append(out, item)
	}
	return out, nil
}

func temporalMasked(masked map[string]bool) bool {
	for field, hidden := range masked {
		if hidden && (field == "temporal" || strings.HasPrefix(field, "temporal.")) {
			return true
		}
	}
	return false
}

func visibleTemporal(t contextgraph.TemporalMetadata, masked map[string]bool) *contextgraph.TemporalMetadata {
	if temporalMasked(masked) || (t.Mode == "" && t.SourceRecordedAt.IsZero() && t.BusinessValidFrom.IsZero() && t.BusinessValidTo == nil) {
		return nil
	}
	return &t
}

func (s *Service) visibleRelations(ctx context.Context, principal identity.Principal, state visibilityState, parent contextgraph.Object, relations []contextgraph.Relation) ([]RelationItem, error) {
	if len(relations) == 0 {
		return nil, nil
	}
	links := make([]datavisibility.ResourceDescriptor, 0, len(relations))
	others := make([]contextgraph.Object, 0, len(relations))
	selected := make([]contextgraph.Relation, 0, len(relations))
	for _, relation := range relations {
		if !visibilityAccessRefsValid(relation.Link.SourceAccessRef, parent.OwnerAccessRef, relation.OtherObject.OwnerAccessRef) {
			continue
		}
		links = append(links, datavisibility.ResourceDescriptor{
			Key: "link:" + relation.Link.ID, Kind: datavisibility.ResourceLink, TenantID: relation.Link.TenantID, Type: "link",
			FieldPaths: []string{"confidence", "link_type", "status", "updated_at", "properties.relation_code"},
			AccessRefs: visibilityAccessRefs(relation.Link.SourceAccessRef, parent.OwnerAccessRef, relation.OtherObject.OwnerAccessRef), Payload: relation.Link,
		})
		others = append(others, relation.OtherObject)
		selected = append(selected, relation)
	}
	if len(selected) == 0 {
		return nil, nil
	}
	linkDecision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.context.read", Resources: links})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceLink, linkDecision); err != nil {
		return nil, err
	}
	visibleOther, err := s.visibleObjects(ctx, principal, state, others, false)
	if err != nil {
		return nil, err
	}
	otherByID := make(map[string]ObjectItem, len(visibleOther))
	for _, other := range visibleOther {
		otherByID[other.ID] = other.ObjectItem
	}
	out := make([]RelationItem, 0, len(selected))
	for _, relation := range selected {
		resource, ok := linkDecision.Resource("link:" + relation.Link.ID)
		other, otherVisible := otherByID[relation.OtherObject.ID]
		if !ok {
			return nil, dependencyError(fmt.Errorf("link visibility decision is incomplete"))
		}
		if resource.Hidden || resource.ExistenceDisclosure == datavisibility.ExistenceConceal || !otherVisible {
			continue
		}
		masked := maskedFieldSet(resource)
		item := RelationItem{
			RelationCode: visibleRelationCode(relation.Link, masked),
			ID:           relation.Link.ID, LinkType: relation.Link.LinkType, Direction: string(relation.Direction), OtherObject: other,
			Status: relation.Link.Status, Confidence: relation.Link.Confidence, UpdatedAt: visibleTime(relation.Link.UpdatedAt),
		}
		if masked["link_type"] {
			item.LinkType = ""
		}
		if masked["status"] {
			item.Status = ""
		}
		if masked["confidence"] {
			item.Confidence = 0
		}
		if masked["updated_at"] {
			item.UpdatedAt = nil
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) visibleEvidence(ctx context.Context, principal identity.Principal, state visibilityState, evidence []contextgraph.Evidence) ([]EvidenceItem, error) {
	if len(evidence) == 0 {
		return nil, nil
	}
	descriptors := make([]datavisibility.ResourceDescriptor, 0, len(evidence))
	selected := make([]contextgraph.Evidence, 0, len(evidence))
	for _, item := range evidence {
		if !visibilityAccessRefsValid(item.SourceAccessRef) {
			continue
		}
		descriptors = append(descriptors, datavisibility.ResourceDescriptor{
			Key: "evidence:" + item.ID, Kind: datavisibility.ResourceEvidence, TenantID: item.TenantID, Type: "evidence",
			FieldPaths: []string{"confidence", "observed_at", "source_id", "source_kind", "summary"},
			AccessRefs: visibilityAccessRefs(item.SourceAccessRef), Payload: item,
		})
		selected = append(selected, item)
	}
	if len(selected) == 0 {
		return nil, nil
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.context.read", Resources: descriptors})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceEvidence, decision); err != nil {
		return nil, err
	}
	out := make([]EvidenceItem, 0, len(selected))
	for _, evidence := range selected {
		resource, ok := decision.Resource("evidence:" + evidence.ID)
		if !ok {
			return nil, dependencyError(fmt.Errorf("evidence visibility decision is incomplete"))
		}
		if resource.Hidden || resource.ExistenceDisclosure == datavisibility.ExistenceConceal {
			continue
		}
		masked := maskedFieldSet(resource)
		item := EvidenceItem{ID: evidence.ID, SourceKind: evidence.SourceRef.Kind, SourceID: evidence.SourceRef.ID, Summary: evidence.Summary, Confidence: evidence.Confidence, ObservedAt: visibleTime(evidence.ObservedAt)}
		if masked["source_kind"] {
			item.SourceKind = ""
		}
		if masked["source_id"] {
			item.SourceID = ""
		}
		if masked["summary"] {
			item.Summary = ""
		}
		if masked["confidence"] {
			item.Confidence = 0
		}
		if masked["observed_at"] {
			item.ObservedAt = nil
		}
		out = append(out, item)
	}
	return out, nil
}

func visibilityAccessRefs(refs ...contextgraph.AccessRef) []datavisibility.AccessRef {
	out := make([]datavisibility.AccessRef, 0, len(refs))
	seen := map[contextgraph.AccessRef]struct{}{}
	for _, ref := range refs {
		ref = contextgraph.NormalizeAccessRef(ref)
		if !ref.Resolved() {
			continue
		}
		if _, exists := seen[ref]; exists {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, datavisibility.AccessRef{Kind: ref.Kind, ID: ref.ID, TenantID: ref.TenantID})
	}
	return out
}

func visibilityAccessRefsValid(refs ...contextgraph.AccessRef) bool {
	if len(refs) == 0 {
		return false
	}
	for _, ref := range refs {
		ref = contextgraph.NormalizeAccessRef(ref)
		candidate := datavisibility.AccessRef{Kind: ref.Kind, ID: ref.ID, TenantID: ref.TenantID}
		if !ref.Resolved() || !candidate.WithinLimits() {
			return false
		}
	}
	return true
}

func maskedFieldSet(decision datavisibility.ResourceDecision) map[string]bool {
	out := make(map[string]bool, len(decision.Fields.Masked))
	for _, field := range decision.Fields.Masked {
		out[field] = true
	}
	return out
}

func visibleTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value.UTC()
	return &copy
}

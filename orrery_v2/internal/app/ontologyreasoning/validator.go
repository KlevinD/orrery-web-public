package ontologyreasoning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
)

const (
	issueSeverityError          = "error"
	issueSeverityReviewRequired = "review_required"
)

func (s *Service) ValidateRun(ctx context.Context, tenantID string, runID string) (ReasoningRun, error) {
	run, err := s.GetRun(ctx, tenantID, runID)
	if err != nil {
		return ReasoningRun{}, err
	}
	index, err := s.validationIndex(ctx, tenantID)
	if err != nil {
		return ReasoningRun{}, err
	}
	results := make([]ValidationResult, 0, len(run.Candidates))
	validCount := 0
	rejectedCount := 0
	reviewRequiredCount := 0
	for idx, candidate := range run.Candidates {
		candidateID := candidate.ID
		if candidateID == "" {
			candidateID = fmt.Sprintf("candidate-%d", idx+1)
		}
		issues := s.validateCandidate(candidateID, candidate, index)
		status := validationStatusForIssues(issues)
		switch status {
		case ValidationAccepted:
			validCount++
		case ValidationRejected:
			rejectedCount++
		case ValidationReviewRequired:
			reviewRequiredCount++
		}
		results = append(results, ValidationResult{
			CandidateID: candidateID,
			Status:      status,
			Issues:      issues,
		})
	}
	run.Validation = results
	run.Status = RunStatusValidated
	run.ValidCandidateCount = validCount
	run.RejectedCandidateCount = rejectedCount
	run.ReviewRequiredCount = reviewRequiredCount
	return s.saveUpdatedRun(ctx, run)
}

type validationIndex struct {
	objectTypes   map[string]ontology.ObjectType
	linkTypes     map[string]ontology.LinkType
	propertyTypes map[string]ontology.PropertyType
	objects       map[string]contextgraph.Object
	links         []contextgraph.Link
	facts         []contextgraph.Fact
	actionTypes   map[string]actioncontract.ActionType
	evidence      map[string]bool
}

func (s *Service) validationIndex(ctx context.Context, tenantID string) (validationIndex, error) {
	index := validationIndex{
		objectTypes:   map[string]ontology.ObjectType{},
		linkTypes:     map[string]ontology.LinkType{},
		propertyTypes: map[string]ontology.PropertyType{},
		objects:       map[string]contextgraph.Object{},
		actionTypes:   map[string]actioncontract.ActionType{},
		evidence:      map[string]bool{},
	}
	if s.registry != nil {
		objectTypes, err := s.registry.ListObjectTypes(ctx)
		if err != nil {
			return index, err
		}
		for _, objectType := range objectTypes {
			if definitionVisibleToTenant(objectType.TenantID, tenantID) {
				index.objectTypes[objectType.ID] = objectType
				propertyTypes, err := s.registry.ListPropertyTypes(ctx, objectType.ID)
				if err != nil {
					return index, err
				}
				for _, propertyType := range propertyTypes {
					if definitionVisibleToTenant(propertyType.TenantID, tenantID) {
						index.propertyTypes[propertyType.ObjectTypeID+"."+propertyType.PropertyID] = propertyType
					}
				}
			}
		}
		linkTypes, err := s.registry.ListLinkTypes(ctx)
		if err != nil {
			return index, err
		}
		for _, linkType := range linkTypes {
			if definitionVisibleToTenant(linkType.TenantID, tenantID) {
				index.linkTypes[linkType.ID] = linkType
			}
		}
	}
	if s.graph != nil {
		objects, links, _, evidence, err := s.graph.Snapshot(ctx, tenantID)
		if err != nil {
			return index, err
		}
		for _, object := range objects {
			if object.TenantID != tenantID {
				continue
			}
			index.objects[object.ID] = object
		}
		for _, link := range links {
			if link.TenantID == tenantID {
				index.links = append(index.links, link)
			}
		}
		for _, item := range evidence {
			if item.TenantID == tenantID {
				index.evidence[item.ID] = true
				index.evidence[item.SourceRef.Kind+":"+item.SourceRef.ID] = true
			}
		}
	}
	if s.actions != nil {
		actionTypes, err := s.actions.ListActionTypes(ctx)
		if err != nil {
			return index, err
		}
		for _, actionType := range actionTypes {
			if definitionVisibleToTenant(actionType.TenantID, tenantID) {
				index.actionTypes[string(actionType.ID)] = actionType
			}
		}
	}
	return index, nil
}

func (s *Service) validateCandidate(candidateID string, candidate Candidate, index validationIndex) []ValidationIssue {
	issues := validateCommonCandidate(candidateID, candidate, index)
	var specific []ValidationIssue
	switch candidate.Kind {
	case CandidateEntity:
		specific = validateEntityCandidate(candidateID, candidate, index)
	case CandidateLink:
		specific = validateLinkCandidate(candidateID, candidate, index)
	case CandidateFact:
		specific = validateFactCandidate(candidateID, candidate, index)
	case CandidateMetadata:
		specific = validateMetadataCandidate(candidateID, candidate, index)
	case CandidateAction:
		specific = validateActionCandidate(candidateID, candidate, index)
	default:
		specific = []ValidationIssue{newIssue(candidateID, "unknown_candidate_kind", issueSeverityError, "candidate kind is not supported")}
	}
	return append(issues, specific...)
}

func validateCommonCandidate(candidateID string, candidate Candidate, index validationIndex) []ValidationIssue {
	issues := []ValidationIssue{}
	switch candidate.Operation {
	case OperationCreate, OperationUpsert, OperationUpdate, OperationSuggest:
	default:
		issues = append(issues, newIssue(candidateID, "invalid_candidate_operation", issueSeverityError, "candidate operation is not supported"))
	}
	if candidate.Confidence < 0 || candidate.Confidence > 1 {
		issues = append(issues, newIssue(candidateID, "invalid_confidence", issueSeverityError, "confidence must be between 0 and 1"))
	}
	for _, ref := range candidate.EvidenceRefs {
		if !index.evidence[ref.ID] && !index.evidence[ref.Kind+":"+ref.ID] {
			issues = append(issues, newIssue(candidateID, "evidence_not_found", issueSeverityError, "evidence reference does not exist in tenant scope"))
		}
	}
	return issues
}

func validateEntityCandidate(candidateID string, candidate Candidate, index validationIndex) []ValidationIssue {
	issues := []ValidationIssue{}
	if _, ok := index.objectTypes[candidate.ObjectType]; !ok {
		issues = append(issues, newIssue(candidateID, "unknown_object_type", issueSeverityError, "object type is not registered"))
	}
	if (candidate.Operation == OperationCreate || candidate.Operation == OperationUpsert) && (strings.TrimSpace(candidate.ObjectID) == "" || strings.TrimSpace(candidate.CanonicalKey) == "") {
		issues = append(issues, newIssue(candidateID, "invalid_entity_identity", issueSeverityError, "entity create/upsert requires object_id and canonical_key"))
	}
	if candidate.Operation == OperationUpdate && candidate.ObjectID != "" {
		object, ok := index.objects[candidate.ObjectID]
		if !ok {
			issues = append(issues, newIssue(candidateID, "object_not_found", issueSeverityError, "object does not exist"))
		} else if object.ObjectType != candidate.ObjectType {
			issues = append(issues, newIssue(candidateID, "cross_tenant_reference", issueSeverityError, "object type does not match referenced object"))
		}
	}
	return issues
}

func validateLinkCandidate(candidateID string, candidate Candidate, index validationIndex) []ValidationIssue {
	issues := []ValidationIssue{}
	linkType, ok := index.linkTypes[candidate.LinkType]
	if !ok {
		return append(issues, newIssue(candidateID, "unknown_link_type", issueSeverityError, "link type is not registered"))
	}
	from, fromOK := validateObjectRef(candidateID, candidate.From, index)
	to, toOK := validateObjectRef(candidateID, candidate.To, index)
	if !fromOK {
		issues = append(issues, missingObjectIssue(candidateID, candidate.From))
	}
	if !toOK {
		issues = append(issues, missingObjectIssue(candidateID, candidate.To))
	}
	if fromOK && from.ObjectType != linkType.FromObjectType {
		issues = append(issues, newIssue(candidateID, "link_direction_mismatch", issueSeverityError, "from object type does not match link type"))
	}
	if toOK && to.ObjectType != linkType.ToObjectType {
		issues = append(issues, newIssue(candidateID, "link_direction_mismatch", issueSeverityError, "to object type does not match link type"))
	}
	if linkType.EvidenceRequired && len(candidate.EvidenceRefs) == 0 {
		issues = append(issues, newIssue(candidateID, "missing_required_evidence", issueSeverityError, "link type requires evidence"))
	}
	if fromOK && toOK && hasCardinalityConflict(linkType, from.ID, to.ID, index.links) {
		issues = append(issues, newIssue(candidateID, "link_cardinality_conflict", issueSeverityError, "link violates cardinality"))
	}
	return issues
}

func validateFactCandidate(candidateID string, candidate Candidate, index validationIndex) []ValidationIssue {
	issues := []ValidationIssue{}
	subject, ok := validateObjectRef(candidateID, candidate.Subject, index)
	if !ok {
		return append(issues, missingObjectIssue(candidateID, candidate.Subject))
	}
	propertyType, ok := index.propertyTypes[subject.ObjectType+"."+candidate.Predicate]
	if !ok {
		return append(issues, newIssue(candidateID, "unknown_property_type", issueSeverityError, "property type is not registered for subject"))
	}
	if !valueMatchesType(candidate.Value, propertyType.ValueType) {
		issues = append(issues, newIssue(candidateID, "fact_value_type_mismatch", issueSeverityError, "fact value does not match property type"))
	}
	for _, fact := range index.facts {
		if fact.Subject.ID != subject.ID || fact.Predicate != candidate.Predicate || !assertionEligible(fact.Status) || atomValueKey(fact.Value) == atomValueKey(candidate.Value) {
			continue
		}
		issues = append(issues, newIssue(candidateID, "fact_value_conflict", issueSeverityReviewRequired, "candidate differs from an existing eligible fact"))
		if propertyType.AuthoritySource != "" {
			issues = append(issues, newIssue(candidateID, "fact_authority_review", issueSeverityReviewRequired, "registered authority source: "+propertyType.AuthoritySource))
		}
		if propertyType.ConflictPolicy != "" {
			issues = append(issues, newIssue(candidateID, "fact_conflict_policy", issueSeverityReviewRequired, "registered conflict policy: "+string(propertyType.ConflictPolicy)))
		}
		break
	}
	return issues
}

func validateMetadataCandidate(candidateID string, candidate Candidate, index validationIndex) []ValidationIssue {
	if candidate.MetadataTarget == nil || strings.TrimSpace(candidate.MetadataTarget.Kind) == "" || strings.TrimSpace(candidate.MetadataTarget.ID) == "" || strings.TrimSpace(candidate.MetadataKey) == "" || candidate.MetadataValue == nil {
		return []ValidationIssue{newIssue(candidateID, "invalid_metadata_candidate", issueSeverityError, "metadata target, key, and value are required")}
	}
	var found bool
	switch strings.TrimSpace(candidate.MetadataTarget.Kind) {
	case "object_type":
		_, found = index.objectTypes[candidate.MetadataTarget.ID]
	case "link_type":
		_, found = index.linkTypes[candidate.MetadataTarget.ID]
	case "property_type":
		for _, propertyType := range index.propertyTypes {
			if propertyType.ID == candidate.MetadataTarget.ID {
				found = true
				break
			}
		}
	default:
		return []ValidationIssue{newIssue(candidateID, "invalid_metadata_target_kind", issueSeverityError, "metadata target kind is not supported")}
	}
	if !found {
		return []ValidationIssue{newIssue(candidateID, "metadata_target_not_found", issueSeverityError, "metadata target is not registered in tenant scope")}
	}
	return nil
}

func validateActionCandidate(candidateID string, candidate Candidate, index validationIndex) []ValidationIssue {
	issues := []ValidationIssue{}
	actionType, ok := index.actionTypes[candidate.ActionTypeID]
	if !ok {
		return append(issues, newIssue(candidateID, "unknown_action_type", issueSeverityError, "action type is not registered"))
	}
	if !actionType.Enabled {
		issues = append(issues, newIssue(candidateID, "unknown_action_type", issueSeverityError, "action type is disabled"))
	}
	allowedKinds := map[string]bool{}
	for _, kind := range actionType.ObjectTypeRefs {
		allowedKinds[kind] = true
	}
	if len(candidate.ObjectRefs) == 0 {
		issues = append(issues, newIssue(candidateID, "action_target_required", issueSeverityError, "action candidate requires at least one object reference"))
	}
	for _, ref := range candidate.ObjectRefs {
		object, ok := index.objects[ref.ID]
		if !ok {
			issues = append(issues, newIssue(candidateID, "object_not_found", issueSeverityError, "action object reference does not exist"))
			continue
		}
		if object.ObjectType != ref.Kind || !allowedKinds[ref.Kind] {
			issues = append(issues, newIssue(candidateID, "action_target_mismatch", issueSeverityError, "action target is not compatible with action type"))
		}
	}
	if actionType.RiskLevel == actioncontract.RiskHigh || actionType.RiskLevel == actioncontract.RiskCritical {
		issues = append(issues, newIssue(candidateID, "action_review_required", issueSeverityReviewRequired, "high risk action requires review"))
	}
	return issues
}

func validateObjectRef(candidateID string, ref *ObjectRef, index validationIndex) (contextgraph.Object, bool) {
	if ref == nil || ref.ID == "" {
		return contextgraph.Object{}, false
	}
	object, ok := index.objects[ref.ID]
	if !ok {
		return contextgraph.Object{}, false
	}
	if ref.Type != "" && object.ObjectType != ref.Type {
		return object, false
	}
	return object, true
}

func missingObjectIssue(candidateID string, ref *ObjectRef) ValidationIssue {
	if ref != nil && ref.ID != "" {
		return newIssue(candidateID, "cross_tenant_reference", issueSeverityError, "object reference does not exist in tenant scope")
	}
	return newIssue(candidateID, "object_not_found", issueSeverityError, "object reference is required")
}

func hasCardinalityConflict(linkType ontology.LinkType, fromID string, toID string, links []contextgraph.Link) bool {
	for _, link := range links {
		if link.LinkType != linkType.ID {
			continue
		}
		if link.FromObjectID == fromID && link.ToObjectID == toID {
			continue
		}
		switch linkType.Cardinality {
		case ontology.CardinalityOneToOne:
			if link.FromObjectID == fromID || link.ToObjectID == toID {
				return true
			}
		case ontology.CardinalityOneToMany:
			if link.ToObjectID == toID {
				return true
			}
		case ontology.CardinalityManyToOne:
			if link.FromObjectID == fromID {
				return true
			}
		}
	}
	return false
}

func valueMatchesType(value any, valueType ontology.ValueType) bool {
	switch valueType {
	case ontology.ValueTypeString, ontology.ValueTypeEnum:
		_, ok := value.(string)
		return ok
	case ontology.ValueTypeNumber:
		switch value.(type) {
		case int, int64, float64, float32, json.Number:
			return true
		default:
			return false
		}
	case ontology.ValueTypeBoolean:
		_, ok := value.(bool)
		return ok
	case ontology.ValueTypeObject:
		_, ok := value.(map[string]any)
		return ok
	case ontology.ValueTypeArray:
		_, ok := value.([]any)
		return ok
	case ontology.ValueTypeRef:
		_, ok := value.(map[string]any)
		return ok
	case ontology.ValueTypeDate:
		text, ok := value.(string)
		if !ok {
			return false
		}
		_, err := time.Parse("2006-01-02", text)
		return err == nil
	case ontology.ValueTypeDateTime:
		if t, ok := value.(time.Time); ok {
			return !t.IsZero()
		}
		text, ok := value.(string)
		if !ok {
			return false
		}
		_, err := time.Parse(time.RFC3339Nano, text)
		return err == nil
	case ontology.ValueTypeDuration:
		text, ok := value.(string)
		if !ok {
			return false
		}
		_, err := time.ParseDuration(text)
		return err == nil
	default:
		return false
	}
}

func validationStatusForIssues(issues []ValidationIssue) ValidationStatus {
	status := ValidationAccepted
	for _, issue := range issues {
		if issue.Severity == issueSeverityError {
			return ValidationRejected
		}
		if issue.Severity == issueSeverityReviewRequired {
			status = ValidationReviewRequired
		}
	}
	return status
}

func newIssue(candidateID string, code string, severity string, message string) ValidationIssue {
	return ValidationIssue{
		CandidateID: candidateID,
		Code:        code,
		Severity:    severity,
		Message:     message,
	}
}

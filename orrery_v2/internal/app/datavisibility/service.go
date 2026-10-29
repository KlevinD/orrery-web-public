package datavisibility

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
)

const (
	MaxEvaluationResources = 500
	maxResourceFields      = 128
	maxResourceAccessRefs  = 8
)

type Service struct {
	adapters map[ResourceKind][]Adapter
}

type decisionAggregate struct {
	hidden        bool
	allowed       map[string]struct{}
	masked        map[string]struct{}
	filterable    map[string]struct{}
	disclosure    ExistenceDisclosure
	entityCovered bool
	fieldsCovered bool
	filterCovered bool
}

func NewService(registrations ...Registration) *Service {
	service := &Service{adapters: map[ResourceKind][]Adapter{}}
	for _, registration := range registrations {
		if !validResourceKind(registration.Kind) || registration.Adapter == nil {
			continue
		}
		service.adapters[registration.Kind] = append(service.adapters[registration.Kind], registration.Adapter)
	}
	return service
}

func (s *Service) Evaluate(ctx context.Context, req EvaluateRequest) (Decision, error) {
	if s == nil {
		return Decision{}, ErrDependencyUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	req.Principal = identity.NormalizePrincipal(req.Principal)
	req.Purpose = strings.TrimSpace(req.Purpose)
	if !req.Principal.Authenticated() || req.Purpose == "" || len(req.Purpose) > 128 || len(req.Resources) == 0 || len(req.Resources) > MaxEvaluationResources {
		return Decision{}, fmt.Errorf("%w: authenticated principal, purpose and 1-%d resources are required", ErrInvalidRequest, MaxEvaluationResources)
	}
	resources, err := normalizeResources(req.Resources)
	if err != nil {
		return Decision{}, err
	}

	aggregates := make(map[string]*decisionAggregate, len(resources))
	byKind := map[ResourceKind][]ResourceDescriptor{}
	for _, resource := range resources {
		allowed := make(map[string]struct{}, len(resource.FieldPaths))
		for _, field := range resource.FieldPaths {
			allowed[field] = struct{}{}
		}
		aggregates[resource.Key] = &decisionAggregate{allowed: allowed, masked: map[string]struct{}{}, filterable: map[string]struct{}{}, disclosure: ExistenceVisible}
		byKind[resource.Kind] = append(byKind[resource.Kind], resource)
	}

	revisionParts := make([]string, 0)
	revisionComplete := true
	for _, kind := range orderedResourceKinds() {
		kindResources := byKind[kind]
		if len(kindResources) == 0 {
			continue
		}
		adapters := s.adapters[kind]
		if len(adapters) == 0 {
			return Decision{}, fmt.Errorf("%w: no adapter registered for resource kind %s", ErrDependencyUnavailable, kind)
		}
		for index, adapter := range adapters {
			adapterDecision, err := adapter.Evaluate(ctx, AdapterRequest{Principal: req.Principal, Purpose: req.Purpose, Resources: cloneDescriptors(kindResources)})
			if err != nil {
				if ctx.Err() != nil {
					return Decision{}, ctx.Err()
				}
				return Decision{}, fmt.Errorf("evaluate %s visibility adapter: %w", kind, err)
			}
			validated, err := validateAdapterDecision(kindResources, adapterDecision)
			if err != nil {
				return Decision{}, err
			}
			adapterApplied := false
			for key, decision := range validated {
				adapterApplied = adapterApplied || !decision.Skip
				mergeAdapterDecision(aggregates[key], decision, adapterDecision.Coverage)
			}
			if adapterApplied {
				revision := strings.TrimSpace(adapterDecision.PolicyRevision)
				if revision == "" {
					revisionComplete = false
				} else {
					revisionParts = append(revisionParts, fmt.Sprintf("%s:%d:%s", kind, index, revision))
				}
			}
		}
	}

	out := Decision{Resources: make([]ResourceDecision, 0, len(resources))}
	for _, resource := range resources {
		aggregate := aggregates[resource.Key]
		if !aggregate.entityCovered || len(resource.FieldPaths) > 0 && !aggregate.fieldsCovered {
			return Decision{}, fmt.Errorf("%w: entity or field policy coverage is missing for resource %s", ErrDependencyUnavailable, resource.Key)
		}
		for field := range aggregate.filterable {
			if _, visible := aggregate.allowed[field]; !visible {
				delete(aggregate.filterable, field)
			}
		}
		fields := FieldDecision{Allowed: sortedSet(aggregate.allowed), Masked: sortedSet(aggregate.masked)}
		filterable := sortedSet(aggregate.filterable)
		if aggregate.hidden {
			fields = FieldDecision{}
			filterable = nil
		}
		out.Resources = append(out.Resources, ResourceDecision{
			Key:                 resource.Key,
			Hidden:              aggregate.hidden,
			Fields:              fields,
			FilterableFields:    filterable,
			ExistenceDisclosure: aggregate.disclosure,
		})
	}
	if revisionComplete && len(revisionParts) > 0 {
		sum := sha256.Sum256([]byte(strings.Join(revisionParts, "\x00")))
		out.PolicyRevision = hex.EncodeToString(sum[:])
	}
	return out, nil
}

func normalizeResources(resources []ResourceDescriptor) ([]ResourceDescriptor, error) {
	out := make([]ResourceDescriptor, 0, len(resources))
	seen := map[string]struct{}{}
	for _, resource := range resources {
		resource.Key = strings.TrimSpace(resource.Key)
		resource.Kind = ResourceKind(strings.ToLower(strings.TrimSpace(string(resource.Kind))))
		resource.TenantID = strings.TrimSpace(resource.TenantID)
		resource.Type = strings.ToLower(strings.TrimSpace(resource.Type))
		if len(resource.FieldPaths) > maxResourceFields || len(resource.AccessRefs) > maxResourceAccessRefs {
			return nil, fmt.Errorf("%w: resource fields or access refs exceed the policy batch limit", ErrInvalidRequest)
		}
		resource.FieldPaths = normalizePaths(resource.FieldPaths)
		for _, fieldPath := range resource.FieldPaths {
			if len(fieldPath) > 256 {
				return nil, fmt.Errorf("%w: resource field path exceeds 256 bytes", ErrInvalidRequest)
			}
		}
		for index := range resource.AccessRefs {
			resource.AccessRefs[index].Kind = strings.ToLower(strings.TrimSpace(resource.AccessRefs[index].Kind))
			resource.AccessRefs[index].ID = strings.TrimSpace(resource.AccessRefs[index].ID)
			resource.AccessRefs[index].TenantID = strings.TrimSpace(resource.AccessRefs[index].TenantID)
			if !resource.AccessRefs[index].WithinLimits() {
				return nil, fmt.Errorf("%w: access ref exceeds its size limit", ErrInvalidRequest)
			}
		}
		if resource.Key == "" || len(resource.Key) > 512 || resource.TenantID == "" || len(resource.TenantID) > contextgraph.MaxAccessRefTenantIDBytes || len(resource.Type) > 128 || !validResourceKind(resource.Kind) {
			return nil, fmt.Errorf("%w: resource key, supported kind and tenant_id are required", ErrInvalidRequest)
		}
		if _, exists := seen[resource.Key]; exists {
			return nil, fmt.Errorf("%w: duplicate resource key %s", ErrInvalidRequest, resource.Key)
		}
		seen[resource.Key] = struct{}{}
		out = append(out, resource)
	}
	return out, nil
}

func validateAdapterDecision(resources []ResourceDescriptor, decision AdapterDecision) (map[string]AdapterResourceDecision, error) {
	if decision.Coverage == 0 || decision.Coverage&^(CoverageAll|CoverageFilter) != 0 {
		return nil, fmt.Errorf("%w: adapter decision coverage is missing or invalid", ErrIncompleteDecision)
	}
	expected := make(map[string]ResourceDescriptor, len(resources))
	for _, resource := range resources {
		expected[resource.Key] = resource
	}
	if len(decision.Resources) != len(resources) {
		return nil, fmt.Errorf("%w: adapter returned %d decisions for %d resources", ErrIncompleteDecision, len(decision.Resources), len(resources))
	}
	out := make(map[string]AdapterResourceDecision, len(resources))
	for _, item := range decision.Resources {
		item.Key = strings.TrimSpace(item.Key)
		resource, ok := expected[item.Key]
		if !ok {
			return nil, fmt.Errorf("%w: adapter returned unknown resource key", ErrIncompleteDecision)
		}
		if _, duplicate := out[item.Key]; duplicate {
			return nil, fmt.Errorf("%w: adapter returned duplicate resource key %s", ErrIncompleteDecision, item.Key)
		}
		if item.Skip {
			if item.Hidden || len(item.AllowedFieldPaths) > 0 || len(item.MaskedFieldPaths) > 0 || item.ExistenceDisclosure != "" {
				return nil, fmt.Errorf("%w: skipped adapter decision contains policy for resource %s", ErrIncompleteDecision, item.Key)
			}
			out[item.Key] = item
			continue
		}
		if decision.Coverage.Includes(CoverageEntity) || decision.Coverage.Includes(CoverageFilter) {
			if !validDisclosure(item.ExistenceDisclosure) || item.Hidden && item.ExistenceDisclosure == ExistenceVisible {
				return nil, fmt.Errorf("%w: invalid existence disclosure for resource %s", ErrIncompleteDecision, item.Key)
			}
		} else if item.Hidden || item.ExistenceDisclosure != "" && item.ExistenceDisclosure != ExistenceVisible {
			return nil, fmt.Errorf("%w: field-only adapter returned entity policy for resource %s", ErrIncompleteDecision, item.Key)
		}
		item.AllowedFieldPaths = normalizePaths(item.AllowedFieldPaths)
		item.MaskedFieldPaths = normalizePaths(item.MaskedFieldPaths)
		if decision.Coverage.Includes(CoverageFields) {
			if !item.Hidden && !fieldsCompletelyClassified(resource.FieldPaths, item.AllowedFieldPaths, item.MaskedFieldPaths) {
				return nil, fmt.Errorf("%w: fields are not completely classified for resource %s", ErrIncompleteDecision, item.Key)
			}
		} else if len(item.AllowedFieldPaths) > 0 || len(item.MaskedFieldPaths) > 0 {
			return nil, fmt.Errorf("%w: entity-only adapter returned field policy for resource %s", ErrIncompleteDecision, item.Key)
		}
		out[item.Key] = item
	}
	return out, nil
}

func fieldsCompletelyClassified(expected, allowed, masked []string) bool {
	expectedSet := sliceSet(expected)
	classified := map[string]struct{}{}
	for _, field := range allowed {
		if _, ok := expectedSet[field]; !ok {
			return false
		}
		classified[field] = struct{}{}
	}
	for _, field := range masked {
		if _, ok := expectedSet[field]; !ok {
			return false
		}
		if _, duplicate := classified[field]; duplicate {
			return false
		}
		classified[field] = struct{}{}
	}
	return len(classified) == len(expectedSet)
}

func mergeAdapterDecision(target *decisionAggregate, decision AdapterResourceDecision, coverage DecisionCoverage) {
	if target == nil || decision.Skip {
		return
	}
	if coverage.Includes(CoverageEntity) || coverage.Includes(CoverageFilter) {
		target.entityCovered = target.entityCovered || coverage.Includes(CoverageEntity)
		target.hidden = target.hidden || decision.Hidden
		if disclosureRank(decision.ExistenceDisclosure) < disclosureRank(target.disclosure) {
			target.disclosure = decision.ExistenceDisclosure
		}
	}
	if coverage.Includes(CoverageFilter) && coverage.Includes(CoverageFields) {
		allowed := sliceSet(decision.AllowedFieldPaths)
		if !target.filterCovered {
			for field := range allowed {
				target.filterable[field] = struct{}{}
			}
		} else {
			for field := range target.filterable {
				if _, ok := allowed[field]; !ok {
					delete(target.filterable, field)
				}
			}
		}
		target.filterCovered = true
	}
	if !coverage.Includes(CoverageFields) {
		return
	}
	target.fieldsCovered = true
	if decision.Hidden {
		return
	}
	allowed := sliceSet(decision.AllowedFieldPaths)
	for field := range target.allowed {
		if _, ok := allowed[field]; !ok {
			delete(target.allowed, field)
		}
	}
	for _, field := range decision.MaskedFieldPaths {
		delete(target.allowed, field)
		target.masked[field] = struct{}{}
	}
}

func disclosureRank(value ExistenceDisclosure) int {
	switch value {
	case ExistenceConceal:
		return 0
	case ExistenceRestricted:
		return 1
	case ExistenceVisible:
		return 2
	default:
		return -1
	}
}

func validResourceKind(kind ResourceKind) bool {
	switch kind {
	case ResourceObject, ResourceLink, ResourceFact, ResourceEvidence, ResourceDefinition, ResourceEvent, ResourceProposal, ResourceRun, ResourceAudit, ResourceReceipt:
		return true
	default:
		return false
	}
}

func orderedResourceKinds() []ResourceKind {
	return []ResourceKind{ResourceObject, ResourceLink, ResourceFact, ResourceEvidence, ResourceDefinition, ResourceEvent, ResourceProposal, ResourceRun, ResourceAudit, ResourceReceipt}
}

func validDisclosure(value ExistenceDisclosure) bool {
	switch value {
	case ExistenceConceal, ExistenceRestricted, ExistenceVisible:
		return true
	default:
		return false
	}
}

func normalizePaths(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sliceSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func sortedSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cloneDescriptors(resources []ResourceDescriptor) []ResourceDescriptor {
	out := make([]ResourceDescriptor, len(resources))
	for index, resource := range resources {
		out[index] = resource
		out[index].FieldPaths = append([]string(nil), resource.FieldPaths...)
		out[index].AccessRefs = append([]AccessRef(nil), resource.AccessRefs...)
	}
	return out
}

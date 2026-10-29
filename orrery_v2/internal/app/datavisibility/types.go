package datavisibility

import (
	"context"
	"errors"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
)

var ErrInvalidRequest = errors.New("data visibility request is invalid")
var ErrDependencyUnavailable = errors.New("data visibility dependency is unavailable")
var ErrIncompleteDecision = errors.New("data visibility decision is incomplete")

type ResourceKind string

const (
	ResourceObject     ResourceKind = "object"
	ResourceLink       ResourceKind = "link"
	ResourceFact       ResourceKind = "fact"
	ResourceEvidence   ResourceKind = "evidence"
	ResourceDefinition ResourceKind = "definition"
	ResourceEvent      ResourceKind = "event"
	ResourceProposal   ResourceKind = "proposal"
	ResourceRun        ResourceKind = "run"
	ResourceAudit      ResourceKind = "audit"
	ResourceReceipt    ResourceKind = "receipt"
)

type ExistenceDisclosure string

const (
	ExistenceConceal    ExistenceDisclosure = "conceal"
	ExistenceRestricted ExistenceDisclosure = "restricted"
	ExistenceVisible    ExistenceDisclosure = "visible"
)

type AccessRef struct {
	Kind     string
	ID       string
	TenantID string
}

const (
	MaxAccessRefKindBytes     = contextgraph.MaxAccessRefKindBytes
	MaxAccessRefIDBytes       = contextgraph.MaxAccessRefIDBytes
	MaxAccessRefTenantIDBytes = contextgraph.MaxAccessRefTenantIDBytes
)

func (r AccessRef) Resolved() bool {
	return strings.TrimSpace(r.Kind) != "" && strings.TrimSpace(r.ID) != "" && strings.TrimSpace(r.TenantID) != ""
}

func (r AccessRef) WithinLimits() bool {
	r.Kind = strings.TrimSpace(r.Kind)
	r.ID = strings.TrimSpace(r.ID)
	r.TenantID = strings.TrimSpace(r.TenantID)
	return len(r.Kind) <= MaxAccessRefKindBytes && len(r.ID) <= MaxAccessRefIDBytes && len(r.TenantID) <= MaxAccessRefTenantIDBytes
}

type ResourceDescriptor struct {
	Key        string
	Kind       ResourceKind
	TenantID   string
	Type       string
	FieldPaths []string
	AccessRefs []AccessRef
	Payload    any
}

// AggregateProbe asks adapters whether an exact aggregate can be computed
// without exposing rows hidden by resource-level policy.
type AggregateProbe struct{}

type EvaluateRequest struct {
	Principal identity.Principal
	Purpose   string
	Resources []ResourceDescriptor
}

type FieldDecision struct {
	Allowed []string
	Masked  []string
}

type ResourceDecision struct {
	Key                 string
	Hidden              bool
	Fields              FieldDecision
	FilterableFields    []string
	ExistenceDisclosure ExistenceDisclosure
}

type Decision struct {
	Resources      []ResourceDecision
	PolicyRevision string
}

func (d Decision) Cacheable() bool {
	return strings.TrimSpace(d.PolicyRevision) != ""
}

func (d Decision) Resource(key string) (ResourceDecision, bool) {
	key = strings.TrimSpace(key)
	for _, resource := range d.Resources {
		if resource.Key == key {
			return resource, true
		}
	}
	return ResourceDecision{}, false
}

func (d Decision) HiddenResourceKeys() []string {
	out := make([]string, 0)
	for _, resource := range d.Resources {
		if resource.Hidden {
			out = append(out, resource.Key)
		}
	}
	return out
}

type AdapterRequest struct {
	Principal identity.Principal
	Purpose   string
	Resources []ResourceDescriptor
}

type AdapterResourceDecision struct {
	Key                 string
	Skip                bool
	Hidden              bool
	AllowedFieldPaths   []string
	MaskedFieldPaths    []string
	ExistenceDisclosure ExistenceDisclosure
}

type DecisionCoverage uint8

const (
	CoverageEntity DecisionCoverage = 1 << iota
	CoverageFields
	CoverageFilter
	CoverageAll = CoverageEntity | CoverageFields
)

func (c DecisionCoverage) Includes(required DecisionCoverage) bool {
	return c&required == required
}

type AdapterDecision struct {
	Coverage       DecisionCoverage
	Resources      []AdapterResourceDecision
	PolicyRevision string
}

type Adapter interface {
	Evaluate(context.Context, AdapterRequest) (AdapterDecision, error)
}

type Registration struct {
	Kind    ResourceKind
	Adapter Adapter
}

func Register(kind ResourceKind, adapter Adapter) Registration {
	return Registration{Kind: kind, Adapter: adapter}
}

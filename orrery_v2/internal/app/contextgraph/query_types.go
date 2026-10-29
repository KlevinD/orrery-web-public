package contextgraph

import (
	"errors"
	"time"
)

var ErrQueryInvalid = errors.New("context graph query is invalid")
var ErrNotFound = errors.New("context graph resource is unavailable")
var ErrQueryBudgetExceeded = errors.New("context graph query budget exceeded")

const (
	MaxVisibilityAccessRefs       = 80
	MaxQueryPageSize              = 100
	MaxNeighborhoodNodes          = 200
	MaxNeighborhoodEdges          = 400
	MaxNeighborhoodCandidateNodes = 400
	MaxNeighborhoodCandidateEdges = 600
	MaxNeighborhoodLinkTypes      = 8
	MaxContextOverviewGroups      = 10000
)

type CurrentSubgraphLimits struct {
	Objects  int
	Links    int
	Facts    int
	Evidence int
}

func DefaultCurrentSubgraphLimits() CurrentSubgraphLimits {
	return CurrentSubgraphLimits{Objects: 200, Links: 400, Facts: 1000, Evidence: 400}
}

func (l CurrentSubgraphLimits) valid() bool {
	maxima := DefaultCurrentSubgraphLimits()
	return l.Objects > 0 && l.Objects <= maxima.Objects &&
		l.Links > 0 && l.Links <= maxima.Links &&
		l.Facts > 0 && l.Facts <= maxima.Facts &&
		l.Evidence > 0 && l.Evidence <= maxima.Evidence
}

type VisibilityConstraint struct {
	AllowedAccessRefs []AccessRef
	candidateScan     bool
}

// CandidateVisibilityConstraint is for bounded application-layer scans whose
// rows are policy-evaluated before any result is returned. Aggregate queries
// reject this constraint because candidate rows are not yet authorized.
func CandidateVisibilityConstraint() VisibilityConstraint {
	return VisibilityConstraint{candidateScan: true}
}

func (c VisibilityConstraint) IsCandidateScan() bool {
	return c.candidateScan
}

type AccessRefSeek struct {
	Kind     string
	ID       string
	TenantID string
}

type AccessRefQuery struct {
	TenantID string
	After    AccessRefSeek
	Limit    int
}

type AccessRefPage struct {
	Items   []AccessRef
	HasMore bool
}

type ContextTenantPage struct {
	TenantIDs []string
	HasMore   bool
}

type VisibleContextCounts struct {
	Objects  int64
	Links    int64
	Facts    int64
	OldestAt *time.Time
	NewestAt *time.Time
}

type ContextOverviewGroupKind string

const (
	ContextOverviewObject   ContextOverviewGroupKind = "object"
	ContextOverviewFact     ContextOverviewGroupKind = "fact"
	ContextOverviewLink     ContextOverviewGroupKind = "relation"
	ContextOverviewEvidence ContextOverviewGroupKind = "evidence"
)

type ContextOverviewGroup struct {
	Kind       ContextOverviewGroupKind
	ObjectType string
	Count      int64
	OldestAt   *time.Time
	NewestAt   *time.Time
	AccessRefs []AccessRef
}

type ObjectSeek struct {
	UpdatedAt time.Time
	ID        string
}

type ObjectQuery struct {
	TenantID   string
	Visibility VisibilityConstraint
	ObjectType string
	Status     string
	SourceKind string
	Search     string
	Limit      int
	After      ObjectSeek
}

type ObjectPage struct {
	Items   []Object
	HasMore bool
}

type FactQuery struct {
	TenantID   string
	Visibility VisibilityConstraint
	ObjectType string
	ObjectID   string
	Limit      int
	After      ObjectSeek
}

type FactPage struct {
	Items   []Fact
	HasMore bool
}

// FactDetail keeps the fact and its current subject together so callers cannot
// accidentally authorize one without the other.
type FactDetail struct {
	Fact    Fact
	Subject Object
}

type VisibleFactQuery struct {
	TenantID   string
	Visibility VisibilityConstraint
	Limit      int
	After      ObjectSeek
}

type FactAssertion struct {
	AssertionID string
	Fact        Fact
	ObservedAt  time.Time
}

type FactAssertionSeek struct {
	ObservedAt time.Time
	ID         string
}

type FactAssertionQuery struct {
	TenantID   string
	FactID     string
	Visibility VisibilityConstraint
	Limit      int
	After      FactAssertionSeek
}

type FactAssertionPage struct {
	Items   []FactAssertion
	HasMore bool
}

type RelationDirection string

const (
	RelationIncoming RelationDirection = "in"
	RelationOutgoing RelationDirection = "out"
	RelationBoth     RelationDirection = "both"
)

type RelationQuery struct {
	TenantID   string
	Visibility VisibilityConstraint
	ObjectID   string
	Direction  RelationDirection
	LinkType   string
	Limit      int
	After      ObjectSeek
}

type Relation struct {
	Link        Link
	OtherObject Object
	Direction   RelationDirection
}

type RelationPage struct {
	Items   []Relation
	HasMore bool
}

type EvidenceQuery struct {
	TenantID    string
	Visibility  VisibilityConstraint
	SourceKind  string
	SourceID    string
	SourceRefs  []Ref
	EvidenceIDs []string
	Limit       int
	After       ObjectSeek
}

type EvidencePage struct {
	Items   []Evidence
	HasMore bool
}

type ProjectionAuditSeek struct {
	ObservedAt time.Time
	ID         string
}

type ProjectionAuditCursorQuery struct {
	TenantID   string
	Visibility VisibilityConstraint
	Status     string
	ReasonCode string
	Limit      int
	After      ProjectionAuditSeek
}

type ProjectionAuditCursorPage struct {
	Items   []ProjectionAudit
	HasMore bool
}

type ProjectionReplayHistorySeek struct {
	AttemptedAt time.Time
	ID          string
}

type ProjectionReplayHistoryCursorQuery struct {
	TenantID   string
	AuditID    string
	Visibility VisibilityConstraint
	Limit      int
	After      ProjectionReplayHistorySeek
}

type ProjectionReplayHistoryCursorPage struct {
	Items   []ProjectionAuditReplayHistory
	HasMore bool
}

type ProjectionTraceQuery struct {
	TenantID        string
	SourceEventID   string
	SourceEventType string
	Visibility      VisibilityConstraint
	Limit           int
	After           ProjectionAuditSeek
}

type ProjectionTracePage struct {
	Items   []ProjectionAudit
	HasMore bool
}

type NeighborhoodQuery struct {
	TenantID   string
	Visibility VisibilityConstraint
	RootID     string
	LinkType   string
	LinkTypes  []string
	Depth      int
	MaxNodes   int
	MaxEdges   int
}

type Neighborhood struct {
	RootID    string
	Objects   []Object
	Links     []Link
	Truncated bool
}

package lineage

import (
	"context"
	"errors"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/ontology"
)

var ErrSafeEventUnavailable = errors.New("safe ontology event is unavailable")

type GraphReader interface {
	GetVisibleFact(context.Context, string, string, contextgraph.VisibilityConstraint) (contextgraph.FactDetail, error)
	ListVisibleLineageFacts(context.Context, contextgraph.VisibleFactQuery) (contextgraph.FactPage, error)
	ListVisibleFactAssertions(context.Context, contextgraph.FactAssertionQuery) (contextgraph.FactAssertionPage, error)
	ListVisibleEvidence(context.Context, contextgraph.EvidenceQuery) (contextgraph.EvidencePage, error)
	ListVisibleProjectionTrace(context.Context, contextgraph.ProjectionTraceQuery) (contextgraph.ProjectionTracePage, error)
}

type SafeEvent struct {
	Metadata        SafeEventMetadata
	TenantID        string
	SourceAccessRef contextgraph.AccessRef
}

type SafeEventReader interface {
	GetSafeOntologyEvent(context.Context, string, string, contextgraph.AccessRef) (SafeEvent, error)
}

type MappingRuleReader interface {
	ListEnabledMappingRulesByEventType(context.Context, string, string, int) (ontology.MappingRuleCandidatePage, error)
}

type VisibilityPolicy interface {
	Evaluate(context.Context, datavisibility.EvaluateRequest) (datavisibility.Decision, error)
}

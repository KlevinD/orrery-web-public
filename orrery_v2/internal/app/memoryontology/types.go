package memoryontology

import (
	"context"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

const (
	ExtractorVersion       = "memory-entity-v1"
	MinimumLinkConfidence  = 0.90
	DefaultProcessingLimit = 10
	MaximumAttempts        = 3
)

type Mention struct {
	Surface    string  `json:"surface"`
	ObjectType string  `json:"object_type"`
	ObjectHint string  `json:"object_hint,omitempty"`
	Confidence float64 `json:"confidence"`
	Anchor     string  `json:"anchor,omitempty"`
}

type Extraction struct {
	Mentions []Mention `json:"mentions"`
}

type Extractor interface {
	Extract(context.Context, memcore.MemoryRecord) (Extraction, error)
	ModelID() string
}

type Resolution string

const (
	ResolutionResolved      Resolution = "resolved"
	ResolutionUnresolved    Resolution = "unresolved"
	ResolutionAmbiguous     Resolution = "ambiguous"
	ResolutionLowConfidence Resolution = "low_confidence"
	ResolutionInvalidType   Resolution = "invalid_type"
)

type MentionProjection struct {
	Index            int
	Mention          Mention
	Resolution       Resolution
	ResolvedObjectID string
	LinkType         string
	EvidenceID       string
	LinkID           string
}

type RunStatus string

const (
	RunPending    RunStatus = "pending"
	RunRunning    RunStatus = "running"
	RunSucceeded  RunStatus = "succeeded"
	RunFailed     RunStatus = "failed"
	RunSuperseded RunStatus = "superseded"
)

type RunKey struct {
	TenantID         string
	MemoryID         string
	ContentHash      string
	ExtractorVersion string
}

type Run struct {
	RunKey
	Status      RunStatus
	Attempts    int
	NextRetryAt time.Time
	LastError   string
	ModelID     string
	StartedAt   time.Time
	CompletedAt time.Time
	UpdatedAt   time.Time
}

type SourceProjectionRecoveryReport struct {
	Reset      int
	Superseded int
}

type RunStore interface {
	GetRun(context.Context, RunKey) (Run, bool, error)
	ClaimRun(context.Context, RunKey, string, time.Time) (Run, bool, error)
	SaveRun(context.Context, Run) error
	ReplaceMentions(context.Context, RunKey, []MentionProjection, time.Time) error
}

type MemoryReader interface {
	FindRecords(context.Context, memstore.RecordQuery) ([]memcore.MemoryRecord, error)
}

// ResolutionScopeProvider returns additional, authorization-derived graph
// tenants whose entity dictionaries are visible from one memory scope.
type ResolutionScopeProvider interface {
	ResolutionTenantIDs(context.Context, memcore.MemoryRecord) ([]string, error)
}

type MemoryRecordProjector interface {
	PublishMaterializedMemoryRecord(context.Context, memcore.MemoryRecord) error
}

type GraphStore interface {
	SaveLink(context.Context, contextgraph.Link) error
	SaveEvidence(context.Context, contextgraph.Evidence) error
	EnsureObjectReference(context.Context, contextgraph.Object) error
	DeleteLink(context.Context, string, string, string) error
	Snapshot(context.Context, string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error)
}

type Registry interface {
	GetLinkType(context.Context, string) (ontology.LinkType, error)
}

type SweepReport struct {
	Scanned   int
	Processed int
	Succeeded int
	Failed    int
	Skipped   int
	Linked    int
}

var mentionTargetTypes = []string{
	"person",
	"agent",
	"organization",
	"team",
	"department",
	"mission",
	"project",
	"task",
	"tool",
	"idea",
	"todo",
	"requirement",
	"decision",
	"design_artifact",
	"repository",
	"issue",
	"release",
	"knowledge_document",
}

func SupportedObjectTypes() []string {
	return append([]string(nil), mentionTargetTypes...)
}

func LinkTypeForObjectType(objectType string) (string, bool) {
	for _, supported := range mentionTargetTypes {
		if objectType == supported {
			return "memory_mentions_" + supported, true
		}
	}
	return "", false
}

package capture

import (
	"context"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memaudit "orrery_v2/internal/memory/audit"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

type CandidateDraft struct {
	ID                string
	MissionID         string
	DiscussionGroupID string
	SourceType        memcore.SourceType
	SourceRef         string
	ProposedScope     memcore.MemoryScope
	ProposedKind      agentcore.MemoryKind
	Layer             memcore.MemoryLayer
	Topic             string
	Summary           string
	Narrative         string
	Lessons           []string
	Entities          map[string]string
	Tags              []string
	EvidenceRefs      []string
	LineageRefs       []string
	CreatedBy         agentcore.AgentID
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type PolicyDecision struct {
	Status         memcore.CandidateStatus
	Sensitivity    memcore.Sensitivity
	DeIDLevel      memcore.DeIDLevel
	Consent        memcore.ConsentState
	ReviewRequired bool
	ReviewReason   string
}

type CapturePolicy interface {
	Decide(draft CandidateDraft) PolicyDecision
}

type Repository interface {
	SaveProfile(ctx context.Context, profile memcore.MemoryProfile) error
	SaveCandidate(ctx context.Context, candidate memcore.MemoryCandidate) error
	GetCandidate(ctx context.Context, id string) (memcore.MemoryCandidate, error)
	ListCandidates(ctx context.Context, query memstore.CandidateQuery) ([]memcore.MemoryCandidate, error)
	ListProfiles(ctx context.Context) ([]memcore.MemoryProfile, error)
	SaveRecord(ctx context.Context, record memcore.MemoryRecord) error
	GetRecord(ctx context.Context, id string) (memcore.MemoryRecord, error)
}

type AuditRecorder interface {
	RecordMemoryAudit(ctx context.Context, event memaudit.Event) error
}

type RecordMetadataSink interface {
	PublishMaterializedMemoryRecord(ctx context.Context, record memcore.MemoryRecord) error
}

type ClockFunc func() time.Time

type EpisodicWriterOptions struct {
	Repository                    Repository
	Audit                         AuditRecorder
	Metadata                      RecordMetadataSink
	Policy                        CapturePolicy
	Now                           ClockFunc
	AutoRecordAgentRunDiarySource bool
}

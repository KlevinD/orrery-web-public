package store

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

type Repository interface {
	SaveProfile(ctx context.Context, profile memcore.MemoryProfile) error
	GetProfile(ctx context.Context, id agentcore.MemoryProfileID) (memcore.MemoryProfile, error)
	ListProfiles(ctx context.Context) ([]memcore.MemoryProfile, error)
	SaveCandidate(ctx context.Context, candidate memcore.MemoryCandidate) error
	GetCandidate(ctx context.Context, id string) (memcore.MemoryCandidate, error)
	ListCandidates(ctx context.Context, query CandidateQuery) ([]memcore.MemoryCandidate, error)
	UpdateCandidateStatus(ctx context.Context, id string, status memcore.CandidateStatus) error
	SaveRecord(ctx context.Context, record memcore.MemoryRecord) error
	GetRecord(ctx context.Context, id string) (memcore.MemoryRecord, error)
	FindRecords(ctx context.Context, query RecordQuery) ([]memcore.MemoryRecord, error)
	UpdateRecordLifecycle(ctx context.Context, id string, status memcore.MemoryLifecycleStatus) error
}

type RecordWriteTx interface {
	GetCandidate(ctx context.Context, id string) (memcore.MemoryCandidate, error)
	GetRecord(ctx context.Context, id string) (memcore.MemoryRecord, error)
	SaveCandidate(ctx context.Context, candidate memcore.MemoryCandidate) error
	SaveRecord(ctx context.Context, record memcore.MemoryRecord) error
}

type TransactionalRecordWriter interface {
	WithRecordWriteTx(ctx context.Context, recordKey string, fn func(RecordWriteTx) error) error
}

type RecordQuery struct {
	ProfileIDs        []agentcore.MemoryProfileID
	ScopeKinds        []memcore.ScopeKind
	ScopeIDs          []string
	Layers            []memcore.MemoryLayer
	Topics            []string
	Tags              []string
	LifecycleStatuses []memcore.MemoryLifecycleStatus
	MinConfidence     float64
	IncludeArchived   bool
	Limit             int
}

// RecordScanPage bounds the number of physical records read, including records
// later excluded by a consumer. Keys are stable cursors, not page offsets.
type RecordScanItem struct {
	Key    string
	Record memcore.MemoryRecord
}

type RecordScanPage struct {
	Items   []RecordScanItem
	NextKey string
	HasMore bool
	Scanned int
}

type RecordScanner interface {
	ScanRecords(context.Context, string, int) (RecordScanPage, error)
}

type CandidateQuery struct {
	MissionID      string
	ScopeKind      memcore.ScopeKind
	ScopeID        string
	Layer          memcore.MemoryLayer
	Topic          string
	Status         memcore.CandidateStatus
	ReviewRequired *bool
	Limit          int
}

package agenttrace

import (
	"errors"
	"time"
)

type TraceStatus string

const (
	TraceRunning  TraceStatus = "running"
	TraceHot      TraceStatus = "hot"
	TraceArchived TraceStatus = "archived"
	TracePurged   TraceStatus = "purged"
	TraceMissing  TraceStatus = "missing"
)

var (
	ErrTraceNotFound         = errors.New("agent runtime trace not found")
	ErrTraceRevisionConflict = errors.New("agent runtime trace revision conflict")
)

type TraceRecord struct {
	TenantID        string
	RunID           string
	TraceVersion    int
	TraceRef        string
	Status          TraceStatus
	TerminalStatus  string
	EventCount      int
	ByteCount       int64
	ManifestSHA256  string
	ArchiveRef      string
	ArchiveSHA256   string
	StartedAt       time.Time
	LatestEventAt   time.Time
	CompletedAt     time.Time
	RetainUntil     time.Time
	ArchivedAt      time.Time
	HotDeletedAt    time.Time
	PurgeAfter      time.Time
	PurgedAt        time.Time
	LegalHoldActor  string
	LegalHoldReason string
	LegalHoldAt     time.Time
	Revision        int64
	UpdatedAt       time.Time
}

func (s TraceStatus) IsValid() bool {
	switch s {
	case TraceRunning, TraceHot, TraceArchived, TracePurged, TraceMissing:
		return true
	default:
		return false
	}
}

// Package memorypreference maintains explicit interaction preferences in the
// existing personal memory store. Task experiences remain on-demand memories.
package memorypreference

import (
	"context"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcapture "orrery_v2/internal/memory/capture"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

const (
	Tag             = "stable_preference"
	TopicPrefix     = "preference."
	MaxFacts        = 3
	MaxContextBytes = 2000
)

// Fixed topics prevent synonymous model-generated keys from accumulating
// contradictory preferences. Detailed domain knowledge belongs in recall.
var preferenceKeys = []string{
	"response_language", "response_format", "response_detail", "report_format",
	"communication_style", "timezone", "units", "currency",
}

type Fact struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	SourceQuote string `json:"source_quote"`
}

type ExtractionInput struct {
	UserText string            `json:"user_text"`
	Existing map[string]string `json:"existing_preferences"`
}

type Extractor interface {
	Extract(context.Context, ExtractionInput) ([]Fact, error)
}

type Repository interface {
	memstore.Repository
	memstore.TransactionalRecordWriter
}

type Options struct {
	Repository Repository
	Extractor  Extractor
	Enabled    func(context.Context) (bool, error)
	Now        func() time.Time
	Audit      memcapture.AuditRecorder
	Metadata   memcapture.RecordMetadataSink
}

// Request is submitted by the trusted personal-assistant capture adapter.
// Its user text must also match the accepted source exchange in storage.
type Request struct {
	ProfileID      agentcore.MemoryProfileID `json:"profile_id"`
	AgentID        agentcore.AgentID         `json:"agent_id"`
	SourceRecordID string                    `json:"source_record_id"`
	UserMessageID  string                    `json:"user_message_id"`
	UserText       string                    `json:"user_text"`
}

type Service struct {
	options Options
}

type RecordReader interface {
	FindRecords(context.Context, memstore.RecordQuery) ([]memcore.MemoryRecord, error)
	GetRecord(context.Context, string) (memcore.MemoryRecord, error)
	GetCandidate(context.Context, string) (memcore.MemoryCandidate, error)
}

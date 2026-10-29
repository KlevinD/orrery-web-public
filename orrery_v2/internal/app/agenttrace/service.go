package agenttrace

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

var ErrInvalidTraceRequest = errors.New("invalid agent trace request")

type ServiceRepository interface {
	Get(context.Context, string, string, int) (TraceRecord, bool, error)
	ApplyLegalHold(context.Context, string, string, int, string, string, time.Time) error
	ReleaseLegalHold(context.Context, string, string, int) error
}

type TraceReader interface {
	ReadTrace(context.Context, string) ([]ArchiveEntry, error)
}

type MetadataDTO struct {
	RunID           string      `json:"run_id"`
	TraceVersion    int         `json:"trace_version"`
	TraceAvailable  bool        `json:"trace_available"`
	TraceStatus     TraceStatus `json:"trace_status"`
	TerminalStatus  string      `json:"terminal_status,omitempty"`
	EventCount      int         `json:"event_count"`
	ByteCount       int64       `json:"byte_count"`
	StartedAt       time.Time   `json:"started_at,omitempty"`
	CompletedAt     time.Time   `json:"completed_at,omitempty"`
	RetainUntil     time.Time   `json:"retain_until,omitempty"`
	ArchivedAt      time.Time   `json:"archived_at,omitempty"`
	PurgeAfter      time.Time   `json:"purge_after,omitempty"`
	LegalHold       bool        `json:"legal_hold"`
	LegalHoldActor  string      `json:"legal_hold_actor,omitempty"`
	LegalHoldReason string      `json:"legal_hold_reason,omitempty"`
}

type EventPageDTO struct {
	Events  []any  `json:"events"`
	After   uint64 `json:"after"`
	Limit   int    `json:"limit"`
	HasMore bool   `json:"has_more"`
}

type Service struct {
	repository ServiceRepository
	reader     TraceReader
}

func NewService(repository ServiceRepository, reader TraceReader) *Service {
	return &Service{repository: repository, reader: reader}
}

func (s *Service) Metadata(ctx context.Context, tenantID, runID string) (MetadataDTO, error) {
	record, err := s.get(ctx, tenantID, runID)
	if err != nil {
		return MetadataDTO{}, err
	}
	return MetadataDTO{
		RunID: record.RunID, TraceVersion: record.TraceVersion, TraceAvailable: record.Status != TracePurged && record.Status != TraceMissing,
		TraceStatus: record.Status, TerminalStatus: record.TerminalStatus, EventCount: record.EventCount, ByteCount: record.ByteCount,
		StartedAt: record.StartedAt, CompletedAt: record.CompletedAt, RetainUntil: record.RetainUntil,
		ArchivedAt: record.ArchivedAt, PurgeAfter: record.PurgeAfter, LegalHold: !record.LegalHoldAt.IsZero(),
		LegalHoldActor: record.LegalHoldActor, LegalHoldReason: record.LegalHoldReason,
	}, nil
}

func (s *Service) Manifest(ctx context.Context, tenantID, runID string) (any, error) {
	record, err := s.get(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if record.Status == TraceMissing || record.Status == TracePurged {
		return nil, ErrTraceNotFound
	}
	entries, err := s.reader.ReadTrace(ctx, readableTraceRef(record))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Kind != "manifest" {
			continue
		}
		var value any
		if err := json.Unmarshal(entry.Data, &value); err != nil {
			return nil, err
		}
		return scrubTraceAPIValue(value, ""), nil
	}
	return nil, ErrTraceNotFound
}

func (s *Service) Events(ctx context.Context, tenantID, runID string, after uint64, limit int) (EventPageDTO, error) {
	if limit <= 0 || limit > 200 {
		return EventPageDTO{}, ErrInvalidTraceRequest
	}
	record, err := s.get(ctx, tenantID, runID)
	if err != nil {
		return EventPageDTO{}, err
	}
	if record.Status == TraceMissing || record.Status == TracePurged {
		return EventPageDTO{}, ErrTraceNotFound
	}
	entries, err := s.reader.ReadTrace(ctx, readableTraceRef(record))
	if err != nil {
		return EventPageDTO{}, err
	}
	type item struct {
		ordinal uint64
		value   any
	}
	payloads := make(map[string]any)
	for _, entry := range entries {
		if entry.Kind != "payload" {
			continue
		}
		var payload any
		if json.Unmarshal(entry.Data, &payload) == nil {
			payloads[entry.Ref] = payload
		}
	}
	items := make([]item, 0)
	for _, entry := range entries {
		if entry.Kind != "event" {
			continue
		}
		var raw map[string]any
		if json.Unmarshal(entry.Data, &raw) != nil {
			continue
		}
		ordinal, _ := raw["ordinal"].(float64)
		if payloadRef, ok := raw["payload_ref"].(map[string]any); ok {
			if ref, _ := payloadRef["ref"].(string); payloads[ref] != nil {
				raw["payload"] = payloads[ref]
			}
		}
		if uint64(ordinal) > after {
			items = append(items, item{ordinal: uint64(ordinal), value: scrubTraceAPIValue(raw, "")})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ordinal < items[j].ordinal })
	page := EventPageDTO{Events: make([]any, 0, min(limit, len(items))), After: after, Limit: limit, HasMore: len(items) > limit}
	for index := 0; index < len(items) && index < limit; index++ {
		page.Events = append(page.Events, items[index].value)
	}
	return page, nil
}

func (s *Service) ApplyLegalHold(ctx context.Context, tenantID, runID, actor, reason string, heldAt time.Time) error {
	if _, err := s.get(ctx, tenantID, runID); err != nil {
		return err
	}
	return s.repository.ApplyLegalHold(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(runID), 2, strings.TrimSpace(actor), strings.TrimSpace(reason), heldAt)
}

func (s *Service) ReleaseLegalHold(ctx context.Context, tenantID, runID string) error {
	if _, err := s.get(ctx, tenantID, runID); err != nil {
		return err
	}
	return s.repository.ReleaseLegalHold(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(runID), 2)
}

func (s *Service) get(ctx context.Context, tenantID, runID string) (TraceRecord, error) {
	if s == nil || s.repository == nil || s.reader == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" {
		return TraceRecord{}, ErrInvalidTraceRequest
	}
	record, ok, err := s.repository.Get(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(runID), 2)
	if err != nil {
		return TraceRecord{}, err
	}
	if !ok {
		return TraceRecord{}, ErrTraceNotFound
	}
	return record, nil
}

func scrubTraceAPIValue(value any, key string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			switch normalizeTraceAPIKey(childKey) {
			case "startref", "terminalref", "eventrefs", "payloadref", "traceref", "archiveref", "internaltraceref":
				continue
			}
			out[childKey] = scrubTraceAPIValue(child, childKey)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = scrubTraceAPIValue(child, key)
		}
		return out
	case string:
		trimmed := strings.TrimSpace(typed)
		if strings.HasPrefix(strings.ToLower(trimmed), "file://") || filepath.IsAbs(trimmed) ||
			strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) ||
			(len(trimmed) >= 3 && ((trimmed[0] >= 'A' && trimmed[0] <= 'Z') || (trimmed[0] >= 'a' && trimmed[0] <= 'z')) &&
				trimmed[1] == ':' && (trimmed[2] == '/' || trimmed[2] == '\\')) {
			return "[REDACTED]"
		}
		return typed
	default:
		return typed
	}
}

func normalizeTraceAPIKey(key string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
}

func readableTraceRef(record TraceRecord) string {
	if record.Status == TraceArchived && strings.TrimSpace(record.ArchiveRef) != "" {
		return record.ArchiveRef
	}
	return record.TraceRef
}

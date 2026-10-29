package agenttrace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestServiceReturnsTenantScopedMetadataWithoutTraceRef(t *testing.T) {
	repository := &serviceTraceRepository{record: TraceRecord{
		TenantID: "tenant-a", RunID: "run-1", TraceVersion: 2, TraceRef: "file:///private/trace.json",
		Status: TraceHot, TerminalStatus: "completed", EventCount: 2, ByteCount: 42,
	}}
	service := NewService(repository, &serviceTraceReader{})
	metadata, err := service.Metadata(context.Background(), "tenant-a", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(metadata)
	if strings.Contains(string(encoded), "file:///private") || !strings.Contains(string(encoded), `"trace_available":true`) {
		t.Fatalf("metadata=%s", encoded)
	}
	if _, err := service.Metadata(context.Background(), "tenant-b", "run-1"); err != ErrTraceNotFound {
		t.Fatalf("cross-tenant error=%v", err)
	}
}

func TestServiceUnavailableTraceKeepsMetadataAndReturnsNotFound(t *testing.T) {
	for _, status := range []TraceStatus{TraceMissing, TracePurged} {
		t.Run(string(status), func(t *testing.T) {
			repository := &serviceTraceRepository{record: TraceRecord{
				TenantID: "tenant-a", RunID: "run-1", TraceVersion: 2,
				Status: status, TerminalStatus: "completed", EventCount: 28,
			}}
			service := NewService(repository, &serviceTraceReader{})
			metadata, err := service.Metadata(context.Background(), "tenant-a", "run-1")
			if err != nil || metadata.TraceAvailable || metadata.EventCount != 28 || metadata.TerminalStatus != "completed" {
				t.Fatalf("metadata=%+v err=%v", metadata, err)
			}
			if _, err := service.Manifest(context.Background(), "tenant-a", "run-1"); err != ErrTraceNotFound {
				t.Fatalf("manifest error=%v", err)
			}
			if _, err := service.Events(context.Background(), "tenant-a", "run-1", 0, 100); err != ErrTraceNotFound {
				t.Fatalf("events error=%v", err)
			}
		})
	}
}

func TestServiceScrubsStorageRefsAndLocalPathsFromManifestAndEvents(t *testing.T) {
	reader := &serviceTraceReader{entries: []ArchiveEntry{
		{Kind: "manifest", Ref: "private", Data: []byte(`{"status":"failed","start_ref":"file:///private/start","artifact":"/srv/private/result","Result":{"InternalTraceRef":"artifact://private/trace"}}`)},
		{Kind: "event", Ref: "event-1", Data: []byte(`{"ordinal":1,"payload":{"safe":"ok","path":"/srv/private/work"},"payload_ref":{"ref":"private/payload"}}`)},
		{Kind: "event", Ref: "event-2", Data: []byte(`{"ordinal":2,"payload":{"drive_path":"D:\\private\\work","unc_path":"\\\\server\\private\\trace","public_url":"https://example.com/report"}}`)},
	}}
	service := NewService(&serviceTraceRepository{record: TraceRecord{TenantID: "tenant-a", RunID: "run-1", TraceVersion: 2, TraceRef: "private", Status: TraceHot}}, reader)
	manifest, err := service.Manifest(context.Background(), "tenant-a", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	events, err := service.Events(context.Background(), "tenant-a", "run-1", 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal([]any{manifest, events})
	if strings.Contains(string(encoded), "/srv/private") || strings.Contains(string(encoded), "file:///private") || strings.Contains(string(encoded), "private/payload") || strings.Contains(string(encoded), "artifact://private/trace") {
		t.Fatalf("private storage detail leaked: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"safe":"ok"`) {
		t.Fatalf("safe event content missing: %s", encoded)
	}
	if strings.Contains(string(encoded), "private") || !strings.Contains(string(encoded), "https://example.com/report") {
		t.Fatalf("cross-platform path redaction changed public content or leaked a path: %s", encoded)
	}
}

func TestServiceCapsEventPageAndManagesLegalHold(t *testing.T) {
	repository := &serviceTraceRepository{record: TraceRecord{TenantID: "tenant-a", RunID: "run-1", TraceVersion: 2, TraceRef: "private", Status: TraceHot}}
	service := NewService(repository, &serviceTraceReader{})
	if _, err := service.Events(context.Background(), "tenant-a", "run-1", 0, 201); err != ErrInvalidTraceRequest {
		t.Fatalf("limit error=%v", err)
	}
	if err := service.ApplyLegalHold(context.Background(), "tenant-a", "run-1", "operator-1", "incident review", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := service.ReleaseLegalHold(context.Background(), "tenant-a", "run-1"); err != nil {
		t.Fatal(err)
	}
	if repository.applyCalls != 1 || repository.releaseCalls != 1 {
		t.Fatalf("apply=%d release=%d", repository.applyCalls, repository.releaseCalls)
	}
}

func TestServiceReadsArchivedTraceFromArchiveRef(t *testing.T) {
	repository := &serviceTraceRepository{record: TraceRecord{
		TenantID: "tenant-a", RunID: "run-1", TraceVersion: 2, TraceRef: "deleted-hot", ArchiveRef: "archive://run-1", Status: TraceArchived,
	}}
	reader := &serviceTraceReader{wantedRef: "archive://run-1", entries: []ArchiveEntry{{Kind: "manifest", Data: []byte(`{"status":"completed"}`)}}}
	service := NewService(repository, reader)
	if _, err := service.Manifest(context.Background(), "tenant-a", "run-1"); err != nil {
		t.Fatal(err)
	}
}

func TestServiceResolvesExternalizedEventPayloadWithoutReturningRef(t *testing.T) {
	reader := &serviceTraceReader{entries: []ArchiveEntry{
		{Kind: "event", Ref: "event-1", Data: []byte(`{"ordinal":1,"payload_ref":{"ref":"payload-1"}}`)},
		{Kind: "payload", Ref: "payload-1", Data: []byte(`{"safe":"externalized"}`)},
	}}
	service := NewService(&serviceTraceRepository{record: TraceRecord{TenantID: "tenant-a", RunID: "run-1", TraceVersion: 2, TraceRef: "private", Status: TraceHot}}, reader)
	page, err := service.Events(context.Background(), "tenant-a", "run-1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(page)
	if !strings.Contains(string(encoded), "externalized") || strings.Contains(string(encoded), "payload-1") {
		t.Fatalf("page=%s", encoded)
	}
}

type serviceTraceRepository struct {
	record       TraceRecord
	applyCalls   int
	releaseCalls int
}

func (r *serviceTraceRepository) Get(_ context.Context, tenantID, runID string, version int) (TraceRecord, bool, error) {
	if tenantID != r.record.TenantID || runID != r.record.RunID || version != r.record.TraceVersion {
		return TraceRecord{}, false, nil
	}
	return r.record, true, nil
}

func (r *serviceTraceRepository) ApplyLegalHold(context.Context, string, string, int, string, string, time.Time) error {
	r.applyCalls++
	return nil
}

func (r *serviceTraceRepository) ReleaseLegalHold(context.Context, string, string, int) error {
	r.releaseCalls++
	return nil
}

type serviceTraceReader struct {
	wantedRef string
	entries   []ArchiveEntry
}

func (r *serviceTraceReader) ReadTrace(_ context.Context, ref string) ([]ArchiveEntry, error) {
	if r.wantedRef != "" && ref != r.wantedRef {
		return nil, ErrTraceNotFound
	}
	return append([]ArchiveEntry(nil), r.entries...), nil
}

package agenttrace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/filestore"
)

type FilestoreObjects struct {
	store filestore.Store
}

func NewFilestoreObjects(store filestore.Store) *FilestoreObjects {
	return &FilestoreObjects{store: store}
}

func (s *FilestoreObjects) ReadTrace(ctx context.Context, ref string) ([]ArchiveEntry, error) {
	if strings.HasSuffix(strings.ToLower(s.objectKey(ref)), ".gz") {
		data, err := s.ReadObject(ctx, ref)
		if err != nil {
			return nil, err
		}
		return ParseArchive(data)
	}
	manifestData, err := s.ReadObject(ctx, ref)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		StartRef    string   `json:"start_ref"`
		TerminalRef string   `json:"terminal_ref"`
		EventRefs   []string `json:"event_refs"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("decode trace manifest: %w", err)
	}
	entries := []ArchiveEntry{{Kind: "manifest", Ref: ref, Data: manifestData}}
	if manifest.StartRef != "" {
		data, err := s.ReadObject(ctx, manifest.StartRef)
		if err != nil {
			return nil, err
		}
		entries = append(entries, ArchiveEntry{Kind: "start", Ref: manifest.StartRef, Data: data})
	}
	eventRefs := append([]string(nil), manifest.EventRefs...)
	sort.Strings(eventRefs)
	payloadRefs := map[string]struct{}{}
	for _, eventRef := range eventRefs {
		data, err := s.ReadObject(ctx, eventRef)
		if err != nil {
			return nil, err
		}
		entries = append(entries, ArchiveEntry{Kind: "event", Ref: eventRef, Data: data})
		var event struct {
			PayloadRef *struct {
				Ref string `json:"ref"`
			} `json:"payload_ref"`
		}
		if json.Unmarshal(data, &event) == nil && event.PayloadRef != nil && strings.TrimSpace(event.PayloadRef.Ref) != "" {
			payloadRefs[event.PayloadRef.Ref] = struct{}{}
		}
	}
	orderedPayloadRefs := make([]string, 0, len(payloadRefs))
	for payloadRef := range payloadRefs {
		orderedPayloadRefs = append(orderedPayloadRefs, payloadRef)
	}
	sort.Strings(orderedPayloadRefs)
	for _, payloadRef := range orderedPayloadRefs {
		data, err := s.ReadObject(ctx, payloadRef)
		if err != nil {
			return nil, err
		}
		entries = append(entries, ArchiveEntry{Kind: "payload", Ref: payloadRef, Data: data})
	}
	if manifest.TerminalRef != "" {
		data, err := s.ReadObject(ctx, manifest.TerminalRef)
		if err != nil {
			return nil, err
		}
		entries = append(entries, ArchiveEntry{Kind: "terminal", Ref: manifest.TerminalRef, Data: data})
	}
	return entries, nil
}

func (s *FilestoreObjects) WriteArchive(ctx context.Context, record TraceRecord, data []byte) (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("trace archive filestore is required")
	}
	key := path.Join("agent-run-archives", tracePathSegment(record.TenantID), tracePathSegment(record.RunID), fmt.Sprintf("trace-v%d.ndjson.gz", record.TraceVersion))
	return s.store.Upload(ctx, key, data, "application/gzip", path.Base(key))
}

func (s *FilestoreObjects) ReadObject(ctx context.Context, ref string) ([]byte, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("trace archive filestore is required")
	}
	reader, err := s.store.GetReader(ctx, s.objectKey(ref))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func (s *FilestoreObjects) DeleteHot(ctx context.Context, ref string) error {
	key := s.objectKey(ref)
	prefix := strings.TrimSuffix(path.Dir(key), "/") + "/"
	if prefix == "./" || prefix == "/" {
		return fmt.Errorf("invalid trace hot prefix")
	}
	return s.store.DeletePrefix(ctx, prefix)
}

func (s *FilestoreObjects) DeleteArchive(ctx context.Context, ref string) error {
	return s.store.Delete(ctx, s.objectKey(ref))
}

func (s *FilestoreObjects) AbortTrace(ctx context.Context, record TraceRecord, at time.Time) error {
	manifestData, err := s.ReadObject(ctx, record.TraceRef)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return err
	}
	if terminalRef, _ := manifest["terminal_ref"].(string); strings.TrimSpace(terminalRef) != "" {
		return nil
	}
	manifestKey := s.objectKey(record.TraceRef)
	terminalKey := path.Join(path.Dir(manifestKey), "terminal.json")
	terminal := map[string]any{
		"trace_version": record.TraceVersion, "status": "aborted", "error_code": "host_process_interrupted",
		"error_message": "host process interrupted before terminal trace persistence", "completed_at": at.UTC(),
	}
	terminalData, err := json.Marshal(terminal)
	if err != nil {
		return err
	}
	terminalRef, err := s.store.Upload(ctx, terminalKey, terminalData, "application/json", "terminal.json")
	if err != nil {
		return err
	}
	manifest["status"] = "aborted"
	manifest["error_code"] = "host_process_interrupted"
	manifest["error_message"] = "host process interrupted before terminal trace persistence"
	manifest["terminal_ref"] = terminalRef
	manifest["completed_at"] = at.UTC()
	manifest["recorded_at"] = at.UTC()
	updated, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	_, err = s.store.Upload(ctx, manifestKey, updated, "application/json", "runtime-trace.json")
	return err
}

func (s *FilestoreObjects) objectKey(ref string) string {
	ref = strings.TrimSpace(ref)
	if s != nil && s.store != nil && (strings.Contains(ref, "://") || strings.HasPrefix(ref, "/")) {
		if key := strings.TrimSpace(s.store.KeyFromURL(ref)); key != "" {
			return key
		}
	}
	return strings.TrimPrefix(ref, "/")
}

func tracePathSegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("/", "_", "\\", "_").Replace(value)
	if value == "" || value == "." || value == ".." {
		return "unknown"
	}
	return value
}

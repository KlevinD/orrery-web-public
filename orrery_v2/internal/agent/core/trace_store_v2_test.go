package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreTraceRecorderV2PersistsStartEventTerminalAndManifest(t *testing.T) {
	store := newMapTraceStore()
	recorder := StoreTraceRecorder{Store: store, Now: fixedTraceNow}
	session, err := recorder.BeginTrace(context.Background(), validAgentInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RecordRuntime(context.Background(), validTraceToolCallEnvelope("safe")); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(context.Background(), TraceTerminal{
		Status:    RuntimeStatusFailed,
		ErrorCode: "tool_failed",
		Result:    AgentResult{InternalTraceRef: "evidence://result-1"},
	}); err != nil {
		t.Fatal(err)
	}

	for _, suffix := range []string{"start.json", "events/00000000000000000001.json", "terminal.json", "runtime-trace.json"} {
		if !store.hasSuffix(suffix) {
			t.Fatalf("missing %s: %v", suffix, store.keys())
		}
	}
	manifest := decodeTraceManifestV2(t, store.objectEnding("runtime-trace.json"))
	if manifest.TraceVersion != TraceVersion2 || manifest.Status != RuntimeStatusFailed || manifest.EventCount != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if session.Ref() == "" || manifest.StartRef == "" || manifest.TerminalRef == "" || len(manifest.EventRefs) != 1 {
		t.Fatalf("trace references incomplete: session=%q manifest=%+v", session.Ref(), manifest)
	}
	if manifest.Result.InternalTraceRef != "evidence://result-1" {
		t.Fatalf("result missing from manifest: %+v", manifest.Result)
	}
}

func TestStoreTraceRecorderV2DoesNotPublishManifestBeforeFinish(t *testing.T) {
	store := newMapTraceStore()
	session, err := (StoreTraceRecorder{Store: store, Now: fixedTraceNow}).BeginTrace(context.Background(), validAgentInput())
	if err != nil {
		t.Fatal(err)
	}
	if store.hasSuffix("runtime-trace.json") {
		t.Fatalf("initial manifest was published before terminal: %v", store.keys())
	}
	if !strings.HasSuffix(session.Ref(), "/trace-v2/runtime-trace.json") {
		t.Fatalf("trace ref=%q", session.Ref())
	}
	if err := session.Finish(context.Background(), TraceTerminal{Status: RuntimeStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if !store.hasSuffix("runtime-trace.json") {
		t.Fatalf("final manifest missing after terminal: %v", store.keys())
	}
}

func TestStoreTraceRecorderV2SkipsEphemeralEvents(t *testing.T) {
	store := newMapTraceStore()
	session, err := (StoreTraceRecorder{Store: store, Now: fixedTraceNow}).BeginTrace(context.Background(), validAgentInput())
	if err != nil {
		t.Fatal(err)
	}
	events := []SessionEnvelope{
		validTraceEnvelope(SessionEvent{Kind: EventHeartbeat, Heartbeat: &HeartbeatEvent{}}),
		validTraceEnvelope(SessionEvent{Kind: EventMessageDelta, MessageDelta: &MessageDeltaEvent{ItemID: "message-1", Text: "partial"}}),
		validTraceEnvelope(SessionEvent{Kind: EventMessageFinalized, MessageFinalized: &MessageFinalizedEvent{Message: FinalizedMessage{ItemID: "message-1", Kind: ItemText, Content: "complete"}}}),
	}
	for _, event := range events {
		if err := session.RecordRuntime(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Finish(context.Background(), TraceTerminal{Status: RuntimeStatusCompleted}); err != nil {
		t.Fatal(err)
	}

	manifest := decodeTraceManifestV2(t, store.objectEnding("runtime-trace.json"))
	if manifest.EventCount != 1 || len(manifest.EventRefs) != 1 {
		t.Fatalf("ephemeral events were persisted: %+v", manifest)
	}
}

func TestStoreTraceRecorderV2ExternalizesLargePayloadByHash(t *testing.T) {
	store := newMapTraceStore()
	session, err := (StoreTraceRecorder{Store: store, Now: fixedTraceNow}).BeginTrace(context.Background(), validAgentInput())
	if err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", traceMaxInlinePayloadBytes+1)
	if err := session.RecordRuntime(context.Background(), validTraceToolResultEnvelope(large)); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(context.Background(), TraceTerminal{Status: RuntimeStatusCompleted}); err != nil {
		t.Fatal(err)
	}

	payloadKeys := store.keysContaining("/payloads/")
	if len(payloadKeys) != 1 {
		t.Fatalf("payload keys = %v", payloadKeys)
	}
	event := store.objectEnding("events/00000000000000000001.json")
	if len(event) >= traceMaxInlinePayloadBytes || strings.Contains(string(event), large) || !strings.Contains(string(event), `"payload_ref"`) {
		t.Fatalf("large event was not externalized: event bytes=%d", len(event))
	}
}

func TestStoreTraceRecorderV2SanitizesEventsAndTerminal(t *testing.T) {
	store := newMapTraceStore()
	session, err := (StoreTraceRecorder{
		Store:      store,
		Now:        fixedTraceNow,
		Redactions: []string{"configured-secret"},
	}).BeginTrace(context.Background(), validAgentInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RecordRuntime(context.Background(), validTraceToolResultEnvelope("Bearer provider-token configured-secret")); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(context.Background(), TraceTerminal{
		Status:       RuntimeStatusFailed,
		ErrorMessage: "Authorization: Bearer terminal-token configured-secret",
	}); err != nil {
		t.Fatal(err)
	}
	for key, data := range store.snapshot() {
		text := string(data)
		for _, secret := range []string{"provider-token", "terminal-token", "configured-secret"} {
			if strings.Contains(text, secret) {
				t.Fatalf("secret %q leaked in %s: %s", secret, key, text)
			}
		}
	}
}

func TestStoreTraceRecorderV2RedactsPerRunToolSecretsAfterRequest(t *testing.T) {
	store := newMapTraceStore()
	session, err := (StoreTraceRecorder{Store: store, Now: fixedTraceNow}).BeginTrace(context.Background(), validAgentInput())
	if err != nil {
		t.Fatal(err)
	}
	request := RuntimeRequest{
		RunID: "run-1",
		ToolBindings: []RuntimeToolBinding{{
			ToolID: "search", SecretEnv: map[string]string{"SEARCH_TOKEN": "opaque-runtime-secret"},
		}},
	}
	if err := session.RecordRequest(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := session.RecordRuntime(context.Background(), validTraceToolResultEnvelope("echo opaque-runtime-secret")); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(context.Background(), TraceTerminal{Status: RuntimeStatusFailed, ErrorMessage: "opaque-runtime-secret"}); err != nil {
		t.Fatal(err)
	}
	for key, data := range store.snapshot() {
		if strings.Contains(string(data), "opaque-runtime-secret") {
			t.Fatalf("per-run tool secret leaked in %s: %s", key, data)
		}
	}
}

func TestStoreTraceRecorderV2PropagatesEventWriteFailure(t *testing.T) {
	store := newMapTraceStore()
	store.failSuffix = "events/00000000000000000001.json"
	session, err := (StoreTraceRecorder{Store: store, Now: fixedTraceNow}).BeginTrace(context.Background(), validAgentInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RecordRuntime(context.Background(), validTraceToolCallEnvelope("safe")); !errors.Is(err, errTraceTestStore) {
		t.Fatalf("record error = %v, want %v", err, errTraceTestStore)
	}
	if err := session.Finish(context.Background(), TraceTerminal{Status: RuntimeStatusFailed}); !errors.Is(err, errTraceTestStore) {
		t.Fatalf("finish error = %v, want %v", err, errTraceTestStore)
	}
}

func TestStoreTraceRecorderV2FinishIsIdempotent(t *testing.T) {
	store := newMapTraceStore()
	index := &capturingTraceIndexSink{}
	session, err := (StoreTraceRecorder{Store: store, IndexSink: index, Now: fixedTraceNow}).BeginTrace(context.Background(), validAgentInput())
	if err != nil {
		t.Fatal(err)
	}
	terminal := TraceTerminal{Status: RuntimeStatusCompleted}
	if err := session.Finish(context.Background(), terminal); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(context.Background(), terminal); err != nil {
		t.Fatal(err)
	}
	if got := store.writeCountEnding("terminal.json"); got != 1 {
		t.Fatalf("terminal writes = %d", got)
	}
	if index.calls != 2 {
		t.Fatalf("index writes = %d", index.calls)
	}
	if index.records[0].TerminalStatus != "" || index.records[1].TerminalStatus != string(RuntimeStatusCompleted) {
		t.Fatalf("index lifecycle = %+v", index.records)
	}
}

func TestStoreTraceRecorderV2EnforcesRunByteBudget(t *testing.T) {
	store := newMapTraceStore()
	session, err := (StoreTraceRecorder{Store: store, Now: fixedTraceNow}).BeginTrace(context.Background(), validAgentInput())
	if err != nil {
		t.Fatal(err)
	}
	concrete := session.(*storeTraceSession)
	concrete.maxRunBytes = concrete.byteCount + 128
	err = session.RecordRuntime(context.Background(), validTraceToolResultEnvelope(strings.Repeat("x", 256)))
	if !errors.Is(err, ErrTraceRunBudgetExceeded) {
		t.Fatalf("record error = %v, want %v", err, ErrTraceRunBudgetExceeded)
	}
}

func TestStoreTraceRecorderV2ReservesQueueCapacityForFinish(t *testing.T) {
	store := newMapTraceStore()
	store.blockEvents = make(chan struct{})
	store.eventStarted = make(chan struct{}, 1)
	session, err := (StoreTraceRecorder{Store: store, Now: fixedTraceNow}).BeginTrace(context.Background(), validAgentInput())
	if err != nil {
		t.Fatal(err)
	}

	errorsCh := make(chan error, traceQueueDataLimit+1)
	go func() { errorsCh <- session.RecordRuntime(context.Background(), validTraceToolCallEnvelope("first")) }()
	select {
	case <-store.eventStarted:
	case <-time.After(time.Second):
		t.Fatal("writer did not reach blocked event store")
	}
	for index := 0; index < traceQueueDataLimit; index++ {
		go func() { errorsCh <- session.RecordRuntime(context.Background(), validTraceToolCallEnvelope("queued")) }()
	}

	deadline := time.Now().Add(time.Second)
	for len(concreteTraceSession(session).queue) < traceQueueDataLimit && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := session.RecordRuntime(context.Background(), validTraceToolCallEnvelope("overflow")); !errors.Is(err, ErrTraceQueueFull) {
		t.Fatalf("overflow error = %v, want %v", err, ErrTraceQueueFull)
	}
	close(store.blockEvents)
	for index := 0; index < traceQueueDataLimit+1; index++ {
		if err := <-errorsCh; err != nil {
			t.Fatalf("queued record failed: %v", err)
		}
	}
	if err := session.Finish(context.Background(), TraceTerminal{Status: RuntimeStatusFailed}); err != nil {
		t.Fatalf("reserved finish failed: %v", err)
	}
}

func concreteTraceSession(session DurableTraceSession) *storeTraceSession {
	return session.(*storeTraceSession)
}

func validTraceEnvelope(event SessionEvent) SessionEnvelope {
	return SessionEnvelope{
		SessionID:      "session-1",
		TurnGeneration: 1,
		Sequence:       1,
		Timestamp:      fixedTraceNow(),
		Source:         EventSourceProvider,
		Projection:     EventProjectionCanonical,
		Event:          event,
	}
}

func validTraceToolCallEnvelope(value string) SessionEnvelope {
	return validTraceEnvelope(SessionEvent{
		Kind: EventToolCall,
		ToolCall: &ToolCallEvent{
			ToolUseID: "tool-1",
			Name:      "search",
			Input:     json.RawMessage(`{"query":"` + value + `"}`),
		},
	})
}

func validTraceToolResultEnvelope(value string) SessionEnvelope {
	return validTraceEnvelope(SessionEvent{
		Kind: EventToolResult,
		ToolResult: &ToolResultEvent{
			ToolUseID: "tool-1",
			Content:   []ToolResultContent{{Kind: ToolResultText, Text: value}},
		},
	})
}

func fixedTraceNow() time.Time {
	return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
}

type traceManifestV2Test struct {
	TraceVersion int           `json:"trace_version"`
	Status       RuntimeStatus `json:"status"`
	EventCount   int           `json:"event_count"`
	StartRef     string        `json:"start_ref"`
	TerminalRef  string        `json:"terminal_ref"`
	EventRefs    []string      `json:"event_refs"`
	Result       AgentResult   `json:"result"`
}

func decodeTraceManifestV2(t *testing.T, data []byte) traceManifestV2Test {
	t.Helper()
	var manifest traceManifestV2Test
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v: %s", err, data)
	}
	return manifest
}

var errTraceTestStore = errors.New("trace store failed")

type mapTraceStore struct {
	mu           sync.Mutex
	objects      map[string][]byte
	writes       map[string]int
	failSuffix   string
	blockEvents  chan struct{}
	eventStarted chan struct{}
}

func newMapTraceStore() *mapTraceStore {
	return &mapTraceStore{objects: make(map[string][]byte), writes: make(map[string]int)}
}

func (s *mapTraceStore) PutTrace(ctx context.Context, key string, data []byte) (string, error) {
	if s.failSuffix != "" && strings.HasSuffix(key, s.failSuffix) {
		return "", errTraceTestStore
	}
	if s.blockEvents != nil && strings.Contains(key, "/events/") {
		select {
		case s.eventStarted <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-s.blockEvents:
		}
	}
	s.mu.Lock()
	s.objects[key] = append([]byte(nil), data...)
	s.writes[key]++
	s.mu.Unlock()
	return "store://" + key, nil
}

func (s *mapTraceStore) hasSuffix(suffix string) bool {
	return s.objectEnding(suffix) != nil
}

func (s *mapTraceStore) objectEnding(suffix string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, data := range s.objects {
		if strings.HasSuffix(key, suffix) {
			return append([]byte(nil), data...)
		}
	}
	return nil
}

func (s *mapTraceStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.objects))
	for key := range s.objects {
		keys = append(keys, key)
	}
	return keys
}

func (s *mapTraceStore) keysContaining(fragment string) []string {
	keys := s.keys()
	out := make([]string, 0)
	for _, key := range keys {
		if strings.Contains(key, fragment) {
			out = append(out, key)
		}
	}
	return out
}

func (s *mapTraceStore) snapshot() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]byte, len(s.objects))
	for key, data := range s.objects {
		out[key] = append([]byte(nil), data...)
	}
	return out
}

func (s *mapTraceStore) writeCountEnding(suffix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, count := range s.writes {
		if strings.HasSuffix(key, suffix) {
			return count
		}
	}
	return 0
}

type capturingTraceIndexSink struct {
	calls   int
	record  TraceIndexRecord
	records []TraceIndexRecord
}

func (s *capturingTraceIndexSink) SaveTraceIndex(_ context.Context, record TraceIndexRecord) error {
	s.calls++
	s.record = record
	s.records = append(s.records, record)
	return nil
}

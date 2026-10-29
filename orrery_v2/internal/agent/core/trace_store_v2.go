package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	traceMaxInlinePayloadBytes = 2 << 20
	traceMaxRunBytes           = 64 << 20
	traceQueueCapacity         = 256
	traceQueueDataLimit        = 224
)

var (
	ErrTraceQueueFull         = errors.New("durable trace queue is full")
	ErrTraceRunBudgetExceeded = errors.New("durable trace run byte budget exceeded")
	ErrTraceFinished          = errors.New("durable trace is already finishing")
)

type traceWriteKind uint8

const (
	traceWriteLifecycle traceWriteKind = iota + 1
	traceWriteRuntimeRequest
	traceWriteRuntime
	traceWriteFinish
)

type traceWriteRequest struct {
	ctx      context.Context
	kind     traceWriteKind
	payload  any
	terminal TraceTerminal
	done     chan error
}

type storeTraceSession struct {
	store      TraceStore
	indexSink  TraceIndexSink
	redactions []string
	now        func() time.Time
	input      AgentInput
	baseKey    string
	ref        string
	startRef   string
	startedAt  time.Time

	queue chan traceWriteRequest

	stateMu    sync.Mutex
	finishing  bool
	finishDone chan struct{}
	finishErr  error
	pending    int

	writeMu       sync.Mutex
	firstWriteErr error
	maxRunBytes   int64
	byteCount     int64
	eventRefs     []string
	request       any
}

type traceStartV2 struct {
	TraceVersion          int                `json:"trace_version"`
	TenantID              string             `json:"tenant_id,omitempty"`
	RunID                 string             `json:"run_id"`
	AgentID               string             `json:"agent_id"`
	MissionID             string             `json:"mission_id,omitempty"`
	TaskID                string             `json:"task_id,omitempty"`
	ConversationID        string             `json:"conversation_id,omitempty"`
	DiscussionGroupID     string             `json:"discussion_group_id,omitempty"`
	ExecutionPrincipal    ExecutionPrincipal `json:"execution_principal"`
	ResponsibilityContext any                `json:"responsibility_context,omitempty"`
	StartedAt             time.Time          `json:"started_at"`
}

type tracePayloadRefV2 struct {
	Ref         string `json:"ref"`
	SHA256      string `json:"sha256"`
	Bytes       int    `json:"bytes"`
	ContentType string `json:"content_type"`
}

type traceEventV2 struct {
	TraceVersion int                `json:"trace_version"`
	Ordinal      uint64             `json:"ordinal"`
	Kind         string             `json:"kind"`
	PersistTier  PersistTier        `json:"persist_tier,omitempty"`
	RecordedAt   time.Time          `json:"recorded_at"`
	Payload      any                `json:"payload,omitempty"`
	PayloadRef   *tracePayloadRefV2 `json:"payload_ref,omitempty"`
}

type traceTerminalV2 struct {
	TraceVersion int `json:"trace_version"`
	TraceTerminal
}

type traceRuntimeRequestV2 struct {
	Request            RuntimeRequest               `json:"request"`
	Tools              []traceRuntimeToolBindingV2  `json:"tools,omitempty"`
	Skills             []traceRuntimeSkillBindingV2 `json:"skills,omitempty"`
	SandboxMode        RuntimeSandboxMode           `json:"sandbox_mode,omitempty"`
	ApprovalMode       RuntimeApprovalMode          `json:"approval_mode,omitempty"`
	SystemPromptSHA256 string                       `json:"system_prompt_sha256,omitempty"`
	InputSHA256        string                       `json:"input_sha256,omitempty"`
}

type traceRuntimeToolBindingV2 struct {
	ToolID             ToolID   `json:"tool_id"`
	Alias              string   `json:"alias,omitempty"`
	Version            string   `json:"version,omitempty"`
	GrantedPermissions []string `json:"granted_permissions,omitempty"`
	InputSchemaSHA256  string   `json:"input_schema_sha256,omitempty"`
	OutputSchemaSHA256 string   `json:"output_schema_sha256,omitempty"`
}

type traceRuntimeSkillBindingV2 struct {
	SkillID string `json:"skill_id"`
	Alias   string `json:"alias,omitempty"`
	Version string `json:"version,omitempty"`
}

type traceManifestV2 struct {
	TraceVersion          int                            `json:"trace_version"`
	RunID                 string                         `json:"run_id"`
	AgentID               string                         `json:"agent_id"`
	MissionID             string                         `json:"mission_id,omitempty"`
	TaskID                string                         `json:"task_id,omitempty"`
	ResponsibilityContext *responsibilityTraceProjection `json:"responsibility_context,omitempty"`
	RawOutputRef          string                         `json:"raw_output_ref,omitempty"`
	Messages              []RuntimeMessage               `json:"messages,omitempty"`
	ToolCalls             []RuntimeToolCall              `json:"tool_calls,omitempty"`
	Artifacts             []ArtifactRef                  `json:"artifacts,omitempty"`
	ProviderMetadata      map[string]string              `json:"provider_metadata,omitempty"`
	Usage                 map[string]TokenUsage          `json:"usage,omitempty"`
	Status                RuntimeStatus                  `json:"status,omitempty"`
	ErrorCode             string                         `json:"error_code,omitempty"`
	ErrorMessage          string                         `json:"error_message,omitempty"`
	Retryable             bool                           `json:"retryable,omitempty"`
	Partial               bool                           `json:"partial_output,omitempty"`
	Input                 any                            `json:"input,omitempty"`
	Result                AgentResult                    `json:"result,omitempty"`
	StartRef              string                         `json:"start_ref"`
	TerminalRef           string                         `json:"terminal_ref,omitempty"`
	EventRefs             []string                       `json:"event_refs,omitempty"`
	EventCount            int                            `json:"event_count"`
	ByteCount             int64                          `json:"byte_count"`
	StartedAt             time.Time                      `json:"started_at"`
	CompletedAt           time.Time                      `json:"completed_at,omitempty"`
	RecordedAt            time.Time                      `json:"recorded_at"`
}

func (r StoreTraceRecorder) BeginTrace(ctx context.Context, input AgentInput) (DurableTraceSession, error) {
	if r.Store == nil {
		return nil, fmt.Errorf("trace store is required")
	}
	runID := strings.TrimSpace(input.RunID.String())
	if runID == "" {
		return nil, fmt.Errorf("trace run id is required")
	}
	now := r.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	startedAt := now().UTC()
	baseKey := traceV2BaseKeyForInput(input)
	start := traceStartV2{
		TraceVersion:          TraceVersion2,
		TenantID:              strings.TrimSpace(input.TenantID),
		RunID:                 runID,
		AgentID:               input.ContextPack.AgentID.String(),
		MissionID:             string(missionIDFromInput(input)),
		TaskID:                string(taskIDFromInput(input)),
		ConversationID:        strings.TrimSpace(input.ContextPack.ConversationID),
		DiscussionGroupID:     strings.TrimSpace(string(input.ContextPack.DiscussionGroupID)),
		ExecutionPrincipal:    NormalizeExecutionPrincipal(input.ExecutionPrincipal, input.TenantID, input.ContextPack.AgentID),
		ResponsibilityContext: traceResponsibilityContext(input.ContextPack.ResponsibilityContext),
		StartedAt:             startedAt,
	}
	startData, err := marshalSanitizedTrace(start, r.Redactions)
	if err != nil {
		return nil, fmt.Errorf("marshal trace start: %w", err)
	}
	if int64(len(startData)) > traceMaxRunBytes {
		return nil, ErrTraceRunBudgetExceeded
	}
	startRef, err := r.Store.PutTrace(ctx, path.Join(baseKey, "start.json"), startData)
	if err != nil {
		return nil, fmt.Errorf("write trace start: %w", err)
	}

	ref, err := traceSiblingRef(startRef, "runtime-trace.json")
	if err != nil {
		return nil, fmt.Errorf("resolve final trace manifest ref: %w", err)
	}
	session := &storeTraceSession{
		store:       r.Store,
		indexSink:   r.IndexSink,
		redactions:  append([]string(nil), r.Redactions...),
		now:         now,
		input:       input,
		baseKey:     baseKey,
		ref:         ref,
		startRef:    startRef,
		startedAt:   startedAt,
		queue:       make(chan traceWriteRequest, traceQueueCapacity),
		maxRunBytes: traceMaxRunBytes,
		byteCount:   int64(len(startData)),
	}
	if r.IndexSink != nil {
		if err := r.IndexSink.SaveTraceIndex(ctx, TraceIndexRecord{
			TenantID:     strings.TrimSpace(input.TenantID),
			RunID:        runID,
			TraceRef:     ref,
			TraceVersion: TraceVersion2,
			ByteCount:    session.byteCount,
			StartedAt:    startedAt,
		}); err != nil {
			return nil, fmt.Errorf("save initial trace index: %w", err)
		}
	}
	go session.writeLoop()
	return session, nil
}

func (s *storeTraceSession) Ref() string {
	return s.ref
}

func (s *storeTraceSession) RecordLifecycle(ctx context.Context, event TraceLifecycleEvent) error {
	event.Stage = strings.TrimSpace(event.Stage)
	if event.Stage == "" {
		return fmt.Errorf("trace lifecycle stage is required")
	}
	if event.At.IsZero() {
		event.At = s.now().UTC()
	}
	return s.enqueueData(ctx, traceWriteLifecycle, event)
}

func (s *storeTraceSession) RecordRequest(ctx context.Context, request RuntimeRequest) error {
	s.addExactSecrets(traceRuntimeRequestSecrets(request))
	return s.enqueueData(ctx, traceWriteRuntimeRequest, projectTraceRuntimeRequest(request))
}

func (s *storeTraceSession) RecordRuntime(ctx context.Context, envelope SessionEnvelope) error {
	if err := envelope.Validate(); err != nil {
		return err
	}
	if envelope.Event.PersistTier() == PersistEphemeral {
		return nil
	}
	return s.enqueueData(ctx, traceWriteRuntime, envelope)
}

func (s *storeTraceSession) enqueueData(ctx context.Context, kind traceWriteKind, payload any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	detached := SanitizeTraceValue(payload, s.sanitizerOptions())
	request := traceWriteRequest{ctx: ctx, kind: kind, payload: detached, done: make(chan error, 1)}
	s.stateMu.Lock()
	if s.finishing {
		s.stateMu.Unlock()
		return ErrTraceFinished
	}
	if s.pending >= traceQueueDataLimit {
		s.stateMu.Unlock()
		return ErrTraceQueueFull
	}
	s.pending++
	select {
	case s.queue <- request:
		s.stateMu.Unlock()
	case <-ctx.Done():
		s.pending--
		s.stateMu.Unlock()
		return ctx.Err()
	default:
		s.pending--
		s.stateMu.Unlock()
		return ErrTraceQueueFull
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *storeTraceSession) Finish(ctx context.Context, terminal TraceTerminal) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.stateMu.Lock()
	if s.finishDone != nil {
		done := s.finishDone
		s.stateMu.Unlock()
		select {
		case <-done:
			s.stateMu.Lock()
			err := s.finishErr
			s.stateMu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.finishing = true
	s.finishDone = make(chan struct{})
	done := s.finishDone
	request := traceWriteRequest{ctx: ctx, kind: traceWriteFinish, terminal: terminal, done: make(chan error, 1)}
	select {
	case s.queue <- request:
		s.stateMu.Unlock()
	case <-ctx.Done():
		s.finishErr = ctx.Err()
		close(done)
		s.stateMu.Unlock()
		return ctx.Err()
	}

	err := <-request.done
	s.stateMu.Lock()
	s.finishErr = err
	close(done)
	s.stateMu.Unlock()
	return err
}

func (s *storeTraceSession) writeLoop() {
	for request := range s.queue {
		if request.kind != traceWriteFinish {
			s.stateMu.Lock()
			s.pending--
			s.stateMu.Unlock()
		}
		if request.kind == traceWriteFinish {
			err := s.writeTerminal(request.ctx, request.terminal)
			request.done <- err
			close(s.queue)
			return
		}
		err := s.writeEvent(request.ctx, request.kind, request.payload)
		if err != nil {
			s.rememberWriteError(err)
		}
		request.done <- err
	}
}

func (s *storeTraceSession) writeEvent(ctx context.Context, kind traceWriteKind, payload any) error {
	payloadData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal trace event payload: %w", err)
	}
	ordinal := uint64(len(s.eventRefs) + 1)
	event := traceEventV2{
		TraceVersion: TraceVersion2,
		Ordinal:      ordinal,
		Kind:         traceWriteKindName(kind),
		RecordedAt:   s.now().UTC(),
		Payload:      payload,
	}
	if kind == traceWriteRuntime {
		if envelope, ok := decodeSanitizedEnvelope(payload); ok {
			event.PersistTier = envelope.Event.PersistTier()
		}
	}
	var payloadKey string
	if len(payloadData) > traceMaxInlinePayloadBytes {
		digest := sha256.Sum256(payloadData)
		hash := hex.EncodeToString(digest[:])
		payloadKey = path.Join(s.baseKey, "payloads", hash+".json")
		event.Payload = nil
		event.PayloadRef = &tracePayloadRefV2{
			Ref:         payloadKey,
			SHA256:      hash,
			Bytes:       len(payloadData),
			ContentType: "application/json",
		}
	}
	eventData, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal trace event: %w", err)
	}
	additionalBytes := int64(len(eventData))
	if payloadKey != "" {
		additionalBytes += int64(len(payloadData))
	}
	if err := s.reserveBytes(additionalBytes); err != nil {
		return err
	}
	if payloadKey != "" {
		if _, err := s.store.PutTrace(ctx, payloadKey, payloadData); err != nil {
			return fmt.Errorf("write trace payload: %w", err)
		}
	}
	eventKey := path.Join(s.baseKey, "events", fmt.Sprintf("%020d.json", ordinal))
	eventRef, err := s.store.PutTrace(ctx, eventKey, eventData)
	if err != nil {
		return fmt.Errorf("write trace event: %w", err)
	}
	s.eventRefs = append(s.eventRefs, eventRef)
	if kind == traceWriteRuntimeRequest {
		s.request = payload
	}
	if s.indexSink != nil {
		if err := s.indexSink.SaveTraceIndex(ctx, TraceIndexRecord{
			TenantID: strings.TrimSpace(s.input.TenantID), RunID: s.input.RunID.String(), TraceRef: s.ref,
			TraceVersion: TraceVersion2, EventCount: len(s.eventRefs), ByteCount: s.byteCount,
			StartedAt: s.startedAt, LatestEventAt: event.RecordedAt,
		}); err != nil {
			return fmt.Errorf("save trace progress index: %w", err)
		}
	}
	return nil
}

func (s *storeTraceSession) writeTerminal(ctx context.Context, terminal TraceTerminal) error {
	if terminal.CompletedAt.IsZero() {
		terminal.CompletedAt = s.now().UTC()
	}
	sanitizedTerminal := SanitizeTraceValue(traceTerminalV2{TraceVersion: TraceVersion2, TraceTerminal: terminal}, s.sanitizerOptions())
	terminalData, err := json.Marshal(sanitizedTerminal)
	if err != nil {
		return errors.Join(s.firstError(), fmt.Errorf("marshal trace terminal: %w", err))
	}
	if err := s.reserveBytes(int64(len(terminalData))); err != nil {
		return errors.Join(s.firstError(), err)
	}
	terminalKey := path.Join(s.baseKey, "terminal.json")
	terminalRef, terminalErr := s.store.PutTrace(ctx, terminalKey, terminalData)
	if terminalErr != nil {
		terminalErr = fmt.Errorf("write trace terminal: %w", terminalErr)
	}

	manifest := s.finalManifest(terminal, terminalRef)
	manifestData, manifestErr := marshalSanitizedTrace(manifest, s.sanitizerOptions().ExactSecrets)
	if manifestErr == nil {
		_, manifestErr = s.store.PutTrace(ctx, path.Join(s.baseKey, "runtime-trace.json"), manifestData)
	}
	if manifestErr != nil {
		manifestErr = fmt.Errorf("write final trace manifest: %w", manifestErr)
	}
	var indexErr error
	if terminalErr == nil && manifestErr == nil && s.indexSink != nil {
		indexErr = s.indexSink.SaveTraceIndex(ctx, TraceIndexRecord{
			TenantID:       strings.TrimSpace(s.input.TenantID),
			RunID:          s.input.RunID.String(),
			TraceRef:       s.ref,
			TerminalStatus: string(terminal.Status),
			TraceVersion:   TraceVersion2,
			EventCount:     len(s.eventRefs),
			ByteCount:      s.byteCount,
			StartedAt:      s.startedAt,
			LatestEventAt:  terminal.CompletedAt,
			CompletedAt:    terminal.CompletedAt,
		})
		if indexErr != nil {
			indexErr = fmt.Errorf("save trace index: %w", indexErr)
		}
	}
	return errors.Join(s.firstError(), terminalErr, manifestErr, indexErr)
}

func (s *storeTraceSession) finalManifest(terminal TraceTerminal, terminalRef string) traceManifestV2 {
	response := terminal.Response
	return traceManifestV2{
		TraceVersion:          TraceVersion2,
		RunID:                 s.input.RunID.String(),
		AgentID:               s.input.ContextPack.AgentID.String(),
		MissionID:             string(missionIDFromInput(s.input)),
		TaskID:                string(taskIDFromInput(s.input)),
		ResponsibilityContext: traceResponsibilityContext(s.input.ContextPack.ResponsibilityContext),
		RawOutputRef:          response.RawOutputRef,
		Messages:              response.Messages,
		ToolCalls:             response.ToolCalls,
		Artifacts:             response.Artifacts,
		ProviderMetadata:      response.ProviderMetadata,
		Usage:                 response.Usage,
		Status:                terminal.Status,
		ErrorCode:             terminal.ErrorCode,
		ErrorMessage:          terminal.ErrorMessage,
		Retryable:             terminal.Retryable,
		Partial:               terminal.Partial,
		Input:                 s.request,
		Result:                terminal.Result,
		StartRef:              s.startRef,
		TerminalRef:           terminalRef,
		EventRefs:             append([]string(nil), s.eventRefs...),
		EventCount:            len(s.eventRefs),
		ByteCount:             s.byteCount,
		StartedAt:             s.startedAt,
		CompletedAt:           terminal.CompletedAt,
		RecordedAt:            s.now().UTC(),
	}
}

func (s *storeTraceSession) reserveBytes(count int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if count < 0 || s.byteCount > s.maxRunBytes-count {
		return fmt.Errorf("%w: limit=%d current=%d requested=%d", ErrTraceRunBudgetExceeded, s.maxRunBytes, s.byteCount, count)
	}
	s.byteCount += count
	return nil
}

func (s *storeTraceSession) rememberWriteError(err error) {
	if err == nil {
		return
	}
	s.writeMu.Lock()
	if s.firstWriteErr == nil {
		s.firstWriteErr = err
	}
	s.writeMu.Unlock()
}

func (s *storeTraceSession) firstError() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.firstWriteErr
}

func (s *storeTraceSession) addExactSecrets(values []string) {
	if len(values) == 0 {
		return
	}
	s.writeMu.Lock()
	seen := make(map[string]struct{}, len(s.redactions)+len(values))
	for _, value := range s.redactions {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		s.redactions = append(s.redactions, value)
	}
	s.writeMu.Unlock()
}

func (s *storeTraceSession) sanitizerOptions() TraceSanitizerOptions {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return TraceSanitizerOptions{ExactSecrets: append([]string(nil), s.redactions...)}
}

func traceWriteKindName(kind traceWriteKind) string {
	switch kind {
	case traceWriteLifecycle:
		return "lifecycle"
	case traceWriteRuntimeRequest:
		return "runtime_request"
	case traceWriteRuntime:
		return "runtime_event"
	default:
		return "unknown"
	}
}

func traceV2BaseKeyForInput(input AgentInput) string {
	if conversationID := strings.TrimSpace(input.ContextPack.ConversationID); conversationID != "" {
		return path.Join("discussion-conversations", traceKeySegment(conversationID), "runs", traceKeySegment(input.RunID.String()), "trace-v2")
	}
	if discussionID := strings.TrimSpace(string(input.ContextPack.DiscussionGroupID)); discussionID != "" {
		return path.Join("discussion-conversations", traceKeySegment(discussionID), "runs", traceKeySegment(input.RunID.String()), "trace-v2")
	}
	return path.Join("agent-runs", traceKeySegment(input.RunID.String()), "trace-v2")
}

func traceSiblingRef(anchorRef, name string) (string, error) {
	anchorRef = strings.TrimSpace(anchorRef)
	name = strings.TrimSpace(name)
	if anchorRef == "" || name == "" || path.Base(name) != name {
		return "", fmt.Errorf("trace object reference and sibling name are required")
	}
	parsed, err := url.Parse(anchorRef)
	if err != nil {
		return "", err
	}
	if parsed.Path == "" {
		return "", fmt.Errorf("trace object reference has no path")
	}
	parsed.Path = path.Join(path.Dir(parsed.Path), name)
	return parsed.String(), nil
}

func marshalSanitizedTrace(value any, redactions []string) ([]byte, error) {
	return json.Marshal(SanitizeTraceValue(value, TraceSanitizerOptions{ExactSecrets: redactions}))
}

func decodeSanitizedEnvelope(value any) (SessionEnvelope, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return SessionEnvelope{}, false
	}
	var envelope SessionEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return SessionEnvelope{}, false
	}
	return envelope, true
}

func projectTraceRuntimeRequest(request RuntimeRequest) traceRuntimeRequestV2 {
	projected := traceRuntimeRequestV2{
		Request:      RuntimeRequestRecordProjection(request),
		SandboxMode:  request.RuntimeOptions.SandboxProfile.Mode,
		ApprovalMode: request.RuntimeOptions.SandboxProfile.Approval,
	}
	if projected.SandboxMode == "" {
		projected.SandboxMode = RuntimeSandboxMode(request.RuntimeOptions.WorkspacePolicy.SandboxMode)
	}
	if projected.ApprovalMode == "" {
		projected.ApprovalMode = RuntimeApprovalMode(request.RuntimeOptions.WorkspacePolicy.ApprovalPolicy)
	}
	projected.SystemPromptSHA256 = traceValueSHA256(request.RuntimeOptions.SystemPrompt)
	projected.InputSHA256 = traceValueSHA256(request.InputMessages)
	for _, binding := range request.ToolBindings {
		projected.Tools = append(projected.Tools, traceRuntimeToolBindingV2{
			ToolID: binding.ToolID, Alias: strings.TrimSpace(binding.Alias), Version: strings.TrimSpace(binding.Version),
			GrantedPermissions: append([]string(nil), binding.GrantedPermissions...),
			InputSchemaSHA256:  traceValueSHA256(binding.InputSchema),
			OutputSchemaSHA256: traceValueSHA256(binding.OutputSchema),
		})
	}
	for _, binding := range request.SkillBindings {
		projected.Skills = append(projected.Skills, traceRuntimeSkillBindingV2{
			SkillID: strings.TrimSpace(binding.SkillID), Alias: strings.TrimSpace(binding.Alias), Version: strings.TrimSpace(binding.Version),
		})
	}
	return projected
}

func traceValueSHA256(value any) string {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || string(data) == `null` || string(data) == `""` {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func traceRuntimeRequestSecrets(request RuntimeRequest) []string {
	values := make([]string, 0)
	for _, binding := range request.ToolBindings {
		for _, value := range binding.SecretEnv {
			values = append(values, value)
		}
	}
	for key, value := range request.RuntimeOptions.Environment {
		if isSensitiveTraceKey(key) {
			values = append(values, value)
		}
	}
	values = append(values, traceSensitiveJSONValues(request.RuntimeOptions.ProviderConfig)...)
	values = append(values, traceSensitiveJSONValues(request.RuntimeOptions.MCPConfig)...)
	return values
}

func traceSensitiveJSONValues(data json.RawMessage) []string {
	if len(data) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil
	}
	return collectTraceSensitiveValues(value, "", 0)
}

func collectTraceSensitiveValues(value any, key string, depth int) []string {
	if depth >= traceSanitizerMaxDepth {
		return nil
	}
	if isSensitiveTraceKey(key) {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return []string{text}
		}
	}
	var out []string
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			out = append(out, collectTraceSensitiveValues(child, childKey, depth+1)...)
		}
	case []any:
		for _, child := range typed {
			out = append(out, collectTraceSensitiveValues(child, key, depth+1)...)
		}
	}
	return out
}

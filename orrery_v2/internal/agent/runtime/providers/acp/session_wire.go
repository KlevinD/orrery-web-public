package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/process"
)

type wireEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *wireError      `json:"error,omitempty"`
}

type wireError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type processBinding struct {
	session     *Session
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	stderr      *process.StderrTail
	cancel      context.CancelFunc
	done        chan struct{}
	writeMu     sync.Mutex
	stopOnce    sync.Once
	intentional atomic.Bool
}

func startProcess(session *Session) (*processBinding, error) {
	processCtx, cancel := context.WithCancel(session.ctx)
	command := exec.CommandContext(processCtx, session.workspace.Executable, session.workspace.Args...)
	process.ConfigureProcessTree(command)
	command.Dir = session.workspace.Directory
	command.Env = process.BuildEnv(session.workspace.Environment)
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP stdout pipe failed", true)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP stdin pipe failed", true)
	}
	stderr := process.NewStderrTail(nil, 4096)
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP process failed to start", true)
	}
	if err := process.AttachProcessTree(command); err != nil {
		_ = process.TerminateProcessTree(command)
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP process tree could not be attached", true)
	}
	binding := &processBinding{session: session, cmd: command, stdin: stdin, stdout: stdout, stderr: stderr, cancel: cancel, done: make(chan struct{})}
	go binding.readLoop()
	return binding, nil
}

func (b *processBinding) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b.stopOnce.Do(func() {
		b.intentional.Store(true)
		_ = b.stdin.Close()
	})
	grace := time.NewTimer(time.Second)
	defer grace.Stop()
	select {
	case <-b.done:
		b.cancel()
		return nil
	case <-ctx.Done():
		_ = process.TerminateProcessTree(b.cmd)
		b.cancel()
		return ctx.Err()
	case <-grace.C:
		_ = process.TerminateProcessTree(b.cmd)
		b.cancel()
	}
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP process did not stop after termination", true)
	}
}

func (b *processBinding) write(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "ACP frame could not be encoded", false)
	}
	if len(raw) > b.session.native.MaxRecordBytes {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "ACP frame exceeds the payload limit", false)
	}
	raw = append(raw, '\n')
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if b.session.dead.Load() {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP process is detached", false)
	}
	written, err := b.stdin.Write(raw)
	if err != nil || written != len(raw) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP frame could not be written", true)
	}
	return nil
}

func (b *processBinding) readLoop() {
	defer close(b.done)
	reader := bufio.NewReaderSize(b.stdout, 64*1024)
	var readErr error
	for {
		line, err := readRecord(reader, b.session.native.MaxRecordBytes)
		if len(line) != 0 {
			b.session.wireOrderMu.Lock()
			handleErr := b.session.handleLine(line)
			b.session.wireOrderMu.Unlock()
			if handleErr != nil {
				b.session.protocolFailed(handleErr)
				readErr = handleErr
				break
			}
		}
		if err != nil {
			readErr = err
			break
		}
	}
	waitErr := b.cmd.Wait()
	process.ReleaseProcessTree(b.cmd)
	b.session.wireOrderMu.Lock()
	b.session.processExited(b, waitErr, readErr)
	b.session.wireOrderMu.Unlock()
}

func readRecord(reader *bufio.Reader, maxBytes int) ([]byte, error) {
	var record []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		record = append(record, fragment...)
		if len(record) > maxBytes {
			return nil, fmt.Errorf("ACP record exceeds %d bytes", maxBytes)
		}
		if err == nil {
			return []byte(strings.TrimSuffix(strings.TrimSuffix(string(record), "\n"), "\r")), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(record) > 0 {
			return record, io.EOF
		}
		return nil, err
	}
}

func (s *Session) writeFrame(value any) error {
	s.mu.Lock()
	binding := s.binding
	s.mu.Unlock()
	if binding == nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP process is unavailable", true)
	}
	return binding.write(value)
}

func (s *Session) sendRequest(method string, params any, pending *pendingRPC) (string, error) {
	id := fmt.Sprintf("orrery-acp-%d", s.nextID.Add(1))
	if pending == nil {
		pending = &pendingRPC{method: method}
	}
	if pending.response == nil {
		pending.response = make(chan rpcResponse, 1)
	}
	s.mu.Lock()
	if s.dead.Load() {
		s.mu.Unlock()
		return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP process is detached", false)
	}
	s.pending[id] = pending
	s.mu.Unlock()
	if err := s.writeFrame(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return "", err
	}
	return id, nil
}

func (s *Session) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	pending := &pendingRPC{method: method, response: make(chan rpcResponse, 1)}
	id, err := s.sendRequest(method, params, pending)
	if err != nil {
		return nil, err
	}
	select {
	case response := <-pending.response:
		return response.result, response.err
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP process is detached", false)
	}
}

func (s *Session) writeRPCResult(id json.RawMessage, result any) error {
	decoded, err := decodeWireID(id)
	if err != nil {
		return err
	}
	return s.writeFrame(map[string]any{"jsonrpc": "2.0", "id": decoded, "result": result})
}

func (s *Session) writeRPCError(id json.RawMessage, code int, message string) error {
	decoded, err := decodeWireID(id)
	if err != nil {
		return err
	}
	return s.writeFrame(map[string]any{"jsonrpc": "2.0", "id": decoded, "error": map[string]any{"code": code, "message": message}})
}

func decodeWireID(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "ACP request ID is missing", false)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "ACP request ID is invalid", false)
	}
	switch decoded.(type) {
	case string, float64:
		return decoded, nil
	default:
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "ACP request ID has an unsupported type", false)
	}
}

func wireIDKey(raw json.RawMessage) (string, bool) {
	decoded, err := decodeWireID(raw)
	if err != nil {
		return "", false
	}
	switch value := decoded.(type) {
	case string:
		return value, value != ""
	case float64:
		if value < 0 || value != float64(uint64(value)) {
			return "", false
		}
		return fmt.Sprintf("%.0f", value), true
	default:
		return "", false
	}
}

func (s *Session) handleLine(line []byte) error {
	var frame wireEnvelope
	if err := json.Unmarshal(line, &frame); err != nil || frame.JSONRPC != "2.0" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "ACP emitted an invalid JSON-RPC frame", false)
	}
	method := strings.TrimSpace(frame.Method)
	if len(frame.ID) != 0 && method != "" {
		return s.handleReverseRequest(frame.ID, method, frame.Params)
	}
	if len(frame.ID) != 0 {
		key, ok := wireIDKey(frame.ID)
		if !ok {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "ACP response ID is invalid", false)
		}
		s.mu.Lock()
		pending := s.pending[key]
		delete(s.pending, key)
		s.mu.Unlock()
		if pending == nil {
			s.emitDiagnostic("acp_unmatched_response", "ACP returned an unmatched response")
			return nil
		}
		if pending.prompt {
			s.handlePromptResponse(pending, frame)
			return nil
		}
		response := rpcResponse{result: append(json.RawMessage(nil), frame.Result...)}
		if frame.Error != nil {
			response.err = agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, boundedText(frame.Error.Message, "ACP request was rejected"), false)
		}
		pending.response <- response
		return nil
	}
	if method == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "ACP frame has neither method nor id", false)
	}
	if method != "session/update" {
		s.emitDiagnostic("acp_notification", "unrecognized ACP notification was ignored")
		return nil
	}
	return s.handleSessionUpdate(frame.Params)
}

func (s *Session) handlePromptResponse(pending *pendingRPC, frame wireEnvelope) {
	if frame.Error != nil {
		s.completeTurn(pending.generation, agentcore.TurnResultEvent{IsError: true, ResultText: boundedText(frame.Error.Message, "ACP prompt was rejected"), Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}})
		return
	}
	var result struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(frame.Result, &result); err != nil || strings.TrimSpace(result.StopReason) == "" {
		s.completeTurn(pending.generation, agentcore.TurnResultEvent{IsError: true, ResultText: "ACP prompt response was invalid", Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}})
		return
	}
	s.completeTurn(pending.generation, promptTurnResult(result.StopReason, s.wasCancelled(pending.generation)))
}

func (s *Session) wasCancelled(generation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation == generation && s.cancelled
}

func promptTurnResult(reason string, cancelled bool) agentcore.TurnResultEvent {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if cancelled || reason == "cancelled" {
		return agentcore.TurnResultEvent{Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCancelled, CancelReason: agentcore.CancelReasonUserCancel}}
	}
	switch reason {
	case "end_turn", "stop_sequence":
		return agentcore.TurnResultEvent{Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonEndTurn}}}
	case "max_tokens", "max_turn_requests":
		return agentcore.TurnResultEvent{Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonTruncated, Truncation: agentcore.TruncationMaxTokens}}}
	case "refusal", "refused":
		return agentcore.TurnResultEvent{Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonRefused}}}
	default:
		return agentcore.TurnResultEvent{IsError: true, ResultText: "ACP returned an unknown stop reason", Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}}
	}
}

func (s *Session) handleReverseRequest(id json.RawMessage, method string, raw json.RawMessage) error {
	if method != "session/request_permission" {
		return s.writeRPCError(id, -32601, "method is not supported by this client")
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return s.writeRPCError(id, -32602, "invalid permission request")
	}
	if bound := s.boundSession(); bound != "" && jsonString(params, "sessionId") != bound {
		return s.writeRPCError(id, -32602, "permission request session does not match")
	}
	toolCall := jsonMap(params, "toolCall")
	requestID := jsonString(toolCall, "toolCallId")
	if requestID == "" {
		key, _ := wireIDKey(id)
		requestID = "acp-permission-" + key
	}
	optionsRaw, _ := params["options"].([]any)
	options := make([]permissionOption, 0, len(optionsRaw))
	contextOptions := make([]agentcore.PermissionOption, 0, len(optionsRaw))
	for _, rawOption := range optionsRaw {
		option, _ := rawOption.(map[string]any)
		id := jsonString(option, "optionId")
		kind := jsonString(option, "kind")
		if id == "" || kind == "" {
			continue
		}
		options = append(options, permissionOption{ID: id, Kind: kind})
		contextOptions = append(contextOptions, agentcore.PermissionOption{OptionID: id, Kind: kind})
	}
	if len(options) == 0 {
		return s.writeRPCError(id, -32602, "permission request has no valid options")
	}
	tool := jsonString(toolCall, "title")
	if tool == "" {
		tool = jsonString(toolCall, "kind")
	}
	if tool == "" {
		tool = "ACP tool"
	}
	s.mu.Lock()
	previous, replaced := s.permissions[requestID]
	s.permissions[requestID] = pendingPermission{wireID: append(json.RawMessage(nil), id...), options: options, tool: boundedText(tool, "ACP tool")}
	generation := s.generation
	s.mu.Unlock()
	if replaced {
		_ = s.writeRPCResult(previous.wireID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
	}
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{
		RequestID: requestID, Kind: agentcore.PermissionKindTool, ToolName: boundedText(tool, "ACP tool"),
		Context: &agentcore.PermissionContext{Options: contextOptions},
	}})
	return nil
}

func (s *Session) processExited(binding *processBinding, waitErr, readErr error) {
	if binding.intentional.Load() || s.ctx.Err() != nil {
		return
	}
	s.dead.Store(true)
	s.failPending(agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP process exited", true))
	s.mu.Lock()
	generation, active := s.generation, s.active && !s.terminated[s.generation]
	if active {
		s.active = false
		s.terminated[generation] = true
	}
	s.mu.Unlock()
	if active {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: generation, IsError: true, ResultText: "ACP process exited before the turn completed", Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}}})
	}
	summary := "ACP process exited"
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		summary = "ACP output stream failed"
	}
	if detail := sanitizeACPDiagnostic(binding.stderr.Tail()); detail != "" {
		summary += ": " + detail
	}
	exit := &agentcore.ExitStatus{}
	if waitErr == nil {
		zero := 0
		exit.Code = &zero
	} else {
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			code := exitError.ExitCode()
			exit.Code = &code
		}
	}
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{Exit: exit, RedactedSummary: boundedText(summary, "ACP process exited")}})
	s.closeEvents()
}

func (s *Session) protocolFailed(_ error) {
	s.emitDiagnostic("acp_protocol_error", "ACP emitted an invalid protocol frame")
	s.dead.Store(true)
	go func() { _ = s.Close(context.Background()) }()
}

func (s *Session) emitDiagnostic(tag, summary string) {
	s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: tag, Summary: boundedText(summary, "ACP diagnostic")}})
}

func boundedText(value, fallback string) string {
	value = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(value))
	if value == "" {
		value = fallback
	}
	if len(value) > 240 {
		value = value[:240]
		for len(value) != 0 && !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

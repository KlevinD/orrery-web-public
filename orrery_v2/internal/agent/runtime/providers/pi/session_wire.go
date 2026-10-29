package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/process"
)

type piNativeProcess struct {
	session     *NativeSession
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

type piRPCRejectedError struct{ message string }

func (e *piRPCRejectedError) Error() string { return e.message }

func startPiNativeProcess(session *NativeSession, executable string, args []string, environment map[string]string) (*piNativeProcess, error) {
	processCtx, cancel := context.WithCancel(session.ctx)
	command := exec.CommandContext(processCtx, executable, args...)
	process.ConfigureProcessTree(command)
	command.Dir = session.opts.Cwd
	command.Env = piProcessEnvironment(environment)
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi stdout pipe failed", true)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi stdin pipe failed", true)
	}
	stderr := process.NewStderrTail(nil, 4096)
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, fmt.Sprintf("pi RPC process failed to start: %v", err), true)
	}
	if err := process.AttachProcessTree(command); err != nil {
		_ = process.TerminateProcessTree(command)
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi process tree could not be attached", true)
	}
	binding := &piNativeProcess{session: session, cmd: command, stdin: stdin, stdout: stdout, stderr: stderr, cancel: cancel, done: make(chan struct{})}
	go binding.readLoop()
	return binding, nil
}

func piProcessEnvironment(environment map[string]string) []string {
	merged := process.BuildEnv(environment)
	filtered := merged[:0]
	for _, pair := range merged {
		key, value, _ := strings.Cut(pair, "=")
		normalizedKey := strings.ToUpper(key)
		if strings.HasPrefix(normalizedKey, "OPENCLAW_") || strings.HasPrefix(normalizedKey, "ORRERY_OPENCLAW_") {
			continue
		}
		if isPiHTTPProxyEnvironmentKey(normalizedKey) && !isSupportedPiHTTPProxy(value) {
			continue
		}
		filtered = append(filtered, pair)
	}
	// Node's HTTP dispatcher does not consume ALL_PROXY. Use a supported
	// HTTP(S) ALL_PROXY only when neither spelling of the protocol proxy
	// survived filtering; retain explicitly configured protocol proxies.
	proxies := map[string]string{}
	for _, pair := range filtered {
		key, value, _ := strings.Cut(pair, "=")
		if isPiHTTPProxyEnvironmentKey(key) {
			proxies[key] = value
		}
	}
	fallback := firstNonEmpty(proxies["ALL_PROXY"], proxies["all_proxy"])
	if fallback != "" {
		for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
			lower := strings.ToLower(key)
			if proxies[key] == "" && proxies[lower] == "" {
				filtered = append(filtered, key+"="+fallback, lower+"="+fallback)
			}
		}
	}
	return filtered
}

func piVersionEnvironment(environment map[string]string) map[string]string {
	cleaned := cloneStringMap(environment)
	if cleaned == nil {
		cleaned = map[string]string{}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		value, overridden := cleaned[key]
		if !overridden {
			value = os.Getenv(key)
		}
		if !isSupportedPiHTTPProxy(value) {
			// process.Runner merges with os.Environ, so an empty override is
			// required to neutralize an inherited unsupported proxy.
			cleaned[key] = ""
		}
	}
	return cleaned
}

func isPiHTTPProxyEnvironmentKey(key string) bool {
	switch strings.ToUpper(strings.TrimSpace(key)) {
	case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY":
		return true
	default:
		return false
	}
}

func isSupportedPiHTTPProxy(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || strings.TrimSpace(parsed.Host) == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func (b *piNativeProcess) Stop(ctx context.Context) error {
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
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-deadline.C:
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi process did not stop after termination", true)
	}
}

func (b *piNativeProcess) write(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "pi RPC command could not be encoded", false)
	}
	raw = append(raw, '\n')
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if b.session.dead.Load() {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi RPC process is detached", false)
	}
	written, err := b.stdin.Write(raw)
	if err != nil || written != len(raw) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi RPC command could not be written", true)
	}
	return nil
}

func (b *piNativeProcess) readLoop() {
	defer close(b.done)
	reader := bufio.NewReaderSize(b.stdout, 64*1024)
	var readErr error
	for {
		line, err := readStrictJSONLRecord(reader, piRPCMaxRecordBytes)
		if len(line) != 0 {
			if handleErr := b.session.handlePiLine(line); handleErr != nil {
				b.session.handlePiProtocolError(handleErr)
			}
		}
		if err != nil {
			readErr = err
			break
		}
	}
	waitErr := b.cmd.Wait()
	process.ReleaseProcessTree(b.cmd)
	b.session.piProcessExited(b, waitErr, readErr)
}

func (s *NativeSession) request(ctx context.Context, commandType string, fields map[string]any) (rpcEnvelope, error) {
	return s.requestWithPromptAcceptance(ctx, commandType, fields, "", 0)
}

func (s *NativeSession) requestWithPromptAcceptance(ctx context.Context, commandType string, fields map[string]any, clientMsgID string, generation uint64) (rpcEnvelope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestID := fmt.Sprintf("orrery-%s-%d", commandType, s.requestSequence.Add(1))
	command := make(map[string]any, len(fields)+2)
	command["id"] = requestID
	command["type"] = commandType
	for key, value := range fields {
		command[key] = value
	}
	response := make(chan piNativeResponse, 1)
	s.mu.Lock()
	if s.dead.Load() {
		s.mu.Unlock()
		return rpcEnvelope{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi RPC process is detached", false)
	}
	s.pending[requestID] = piPendingRequest{
		response: response, promptClientMsgID: strings.TrimSpace(clientMsgID), promptGeneration: generation,
	}
	s.mu.Unlock()
	if err := s.writeFrame(command); err != nil {
		s.removePending(requestID)
		return rpcEnvelope{}, err
	}
	select {
	case reply := <-response:
		if reply.err != nil {
			return rpcEnvelope{}, reply.err
		}
		if strings.TrimSpace(reply.envelope.Command) != commandType {
			go func() { _ = s.Close(context.Background()) }()
			return rpcEnvelope{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "pi RPC response command mismatch", false)
		}
		if !reply.envelope.Success {
			return rpcEnvelope{}, &piRPCRejectedError{message: firstNonEmpty(reply.envelope.Error, "pi RPC command rejected")}
		}
		return reply.envelope, nil
	case <-ctx.Done():
		s.removePending(requestID)
		return rpcEnvelope{}, ctx.Err()
	case <-s.ctx.Done():
		s.removePending(requestID)
		return rpcEnvelope{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi RPC process is detached", false)
	}
}

func (s *NativeSession) writeFrame(value any) error {
	if s.binding == nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi RPC process is unavailable", true)
	}
	return s.binding.write(value)
}

func (s *NativeSession) removePending(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func (s *NativeSession) deliverPiResponse(envelope rpcEnvelope) bool {
	if strings.TrimSpace(envelope.ID) == "" {
		return false
	}
	s.mu.Lock()
	pending, ok := s.pending[envelope.ID]
	delete(s.pending, envelope.ID)
	s.mu.Unlock()
	if !ok {
		return false
	}
	if envelope.Success && strings.TrimSpace(envelope.Command) == "prompt" && pending.promptClientMsgID != "" {
		s.emit(pending.promptGeneration, agentcore.SessionEvent{Kind: agentcore.EventPromptAccepted, PromptAccepted: &agentcore.PromptAcceptedEvent{ClientMsgID: pending.promptClientMsgID}})
	}
	pending.response <- piNativeResponse{envelope: envelope}
	return true
}

func (s *NativeSession) handlePiLine(line []byte) error {
	envelope, err := decodeRPCEnvelope(line)
	if err != nil {
		return err
	}
	if envelope.Type == "response" {
		if !s.deliverPiResponse(envelope) {
			s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "pi_unmatched_response", Summary: "unmatched RPC response was ignored"}})
		}
		return nil
	}
	return s.mapPiEvent(line)
}

func (s *NativeSession) handshake(ctx context.Context) error {
	stateResponse, err := s.request(ctx, "get_state", nil)
	if err != nil {
		return piHandshakeRequestError(err, "pi RPC state handshake failed")
	}
	var state piNativeState
	if err := json.Unmarshal(stateResponse.Data, &state); err != nil || strings.TrimSpace(state.SessionID) == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "pi RPC state handshake is invalid", false)
	}
	backendID := strings.TrimSpace(state.SessionID)
	if s.spec.Mode == agentcore.SessionOpenResume && backendID != strings.TrimSpace(s.spec.BackendSessionID) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeSessionMismatch, "pi resumed a different backend session", false)
	}
	modelsResponse, err := s.request(ctx, "get_available_models", nil)
	if err != nil {
		return piHandshakeRequestError(err, "pi model discovery failed")
	}
	var models piNativeModels
	if err := json.Unmarshal(modelsResponse.Data, &models); err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "pi model catalog is invalid", false)
	}
	commandsResponse, err := s.request(ctx, "get_commands", nil)
	if err != nil {
		return piHandshakeRequestError(err, "pi command discovery failed")
	}
	var commands piNativeCommands
	if err := json.Unmarshal(commandsResponse.Data, &commands); err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "pi command catalog is invalid", false)
	}
	catalogModels, modelMap := canonicalPiModels(models.Models)
	slash := canonicalPiCommands(commands.Commands)
	s.mu.Lock()
	s.backendID = backendID
	s.models = modelMap
	s.capability.AvailableModels = catalogModels
	s.capability.SlashCommands = slash
	if state.Model != nil {
		current := piCatalogModelID(*state.Model)
		if _, ok := modelMap[current]; ok {
			s.capability.CurrentModel = current
		}
	}
	if piEffortAllowed(state.ThinkingLevel) {
		s.capability.ConfigOptions[0].Current = state.ThinkingLevel
	}
	s.mu.Unlock()
	s.emit(0, agentcore.SessionEvent{Kind: agentcore.EventBackendBound, BackendBound: &agentcore.BackendBoundEvent{BackendSessionID: backendID}})
	s.emit(0, agentcore.SessionEvent{Kind: agentcore.EventCatalogUpdated, CatalogUpdated: &agentcore.CatalogUpdatedEvent{Models: catalogModels, ConfigOptions: s.Capabilities().ConfigOptions, SlashCommands: slash}})
	return nil
}

func piHandshakeRequestError(err error, message string) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeHandshakeTimeout, message, true)
	}
	var bridgeErr *agentcore.RuntimeBridgeError
	if errors.As(err, &bridgeErr) {
		return err
	}
	var rejectedErr *piRPCRejectedError
	if errors.As(err, &rejectedErr) {
		if detail := sanitizePiDiagnostic(rejectedErr.Error()); detail != "" {
			message = boundedPiText(message+": "+detail, 240)
		}
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, message, false)
	}
	return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, message, true)
}

func canonicalPiModels(models []piNativeModel) ([]agentcore.ModelInfo, map[string]piNativeModel) {
	out := make([]agentcore.ModelInfo, 0, len(models))
	modelMap := make(map[string]piNativeModel, len(models))
	for _, model := range models {
		model.ID = strings.TrimSpace(model.ID)
		model.Provider = strings.TrimSpace(model.Provider)
		if model.ID == "" || model.Provider == "" {
			continue
		}
		catalogID := piCatalogModelID(model)
		if _, exists := modelMap[catalogID]; exists {
			continue
		}
		efforts := []string(nil)
		if model.Reasoning {
			efforts = []string{"off", "minimal", "low", "medium", "high", "xhigh"}
		}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = catalogID
		}
		out = append(out, agentcore.ModelInfo{ID: catalogID, Name: name, ReasoningEfforts: efforts})
		modelMap[catalogID] = model
	}
	return out, modelMap
}

func piCatalogModelID(model piNativeModel) string {
	return strings.TrimSpace(model.Provider) + "/" + strings.TrimSpace(model.ID)
}

func canonicalPiCommands(commands []piNativeCommand) []agentcore.SlashCommandInfo {
	out := make([]agentcore.SlashCommandInfo, 0, len(commands))
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		name := strings.TrimSpace(strings.TrimPrefix(command.Name, "/"))
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, agentcore.SlashCommandInfo{Name: name, Description: sanitizePiText(command.Description)})
	}
	return out
}

func (s *NativeSession) handlePiProtocolError(_ error) {
	s.mu.Lock()
	generation := s.generation
	active := s.active && !s.terminated[generation]
	if active {
		s.active = false
		s.terminated[generation] = true
	}
	s.mu.Unlock()
	if active {
		s.emit(generation, piFailedTurn(generation, "pi RPC protocol error"))
	} else {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "pi_protocol_error", Summary: "invalid pi RPC frame"}})
	}
	go func() { _ = s.Close(context.Background()) }()
}

func (s *NativeSession) piProcessExited(binding *piNativeProcess, waitErr, readErr error) {
	if binding.intentional.Load() || s.ctx.Err() != nil {
		return
	}
	s.dead.Store(true)
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
	summary := "pi RPC process exited"
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		summary = "pi RPC output stream failed"
	}
	if exit.Code != nil {
		summary += fmt.Sprintf(" (exit code %d)", *exit.Code)
	}
	if detail := sanitizePiDiagnostic(binding.stderr.Tail()); detail != "" {
		summary += ": " + detail
	}
	summary = boundedPiText(summary, 240)

	s.failPending(agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, summary, true))
	s.failApprovals("pi RPC process exited")
	s.mu.Lock()
	generation := s.generation
	active := s.active && !s.terminated[generation]
	if active {
		s.active = false
		s.terminated[generation] = true
	}
	s.mu.Unlock()
	if active {
		s.emit(generation, piFailedTurn(generation, "pi RPC process exited before a terminal event"))
	}
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{Exit: exit, RedactedSummary: summary}})
	s.closeEvents()
}

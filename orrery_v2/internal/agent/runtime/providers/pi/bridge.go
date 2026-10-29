package pi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/toolbridge"
	"orrery_v2/internal/app/actioncontract"
	toolstd "orrery_v2/internal/resource/tool/std"
)

type piBridgeConfig struct {
	BaseURL           string                  `json:"base_url"`
	AuthToken         string                  `json:"auth_token"`
	Tools             []piBridgeTool          `json:"tools"`
	ToolCallTimeoutMs int64                   `json:"tool_call_timeout_ms,omitempty"`
	Approval          *piBridgeApprovalConfig `json:"approval,omitempty"`
	Diary             *piBridgeDiaryConfig    `json:"diary,omitempty"`
}

type piBridgeApprovalConfig struct {
	Enabled            bool     `json:"enabled"`
	HumanApprovalTools []string `json:"human_approval_tools,omitempty"`
}

type piBridgeDiaryConfig struct {
	Enabled bool `json:"enabled"`
}

type piBridgeTool struct {
	Name          string         `json:"name"`
	Label         string         `json:"label,omitempty"`
	Description   string         `json:"description,omitempty"`
	PromptSnippet string         `json:"promptSnippet,omitempty"`
	Parameters    map[string]any `json:"parameters"`
	SourceToolID  string         `json:"source_tool_id"`
}

type preparedBridge struct {
	BaseURL       string
	ConfigPath    string
	ExtensionPath string
	Environment   map[string]string
	Server        *piBridgeServer
	authToken     string
	cleanup       func()
}

func (b preparedBridge) Cleanup() {
	if b.cleanup != nil {
		b.cleanup()
	}
}

type preparedProviderRequestCapture struct {
	BaseURL string
	Path    string
}

func prepareBridge(ctx context.Context, dispatcher runtimecore.RuntimeToolDispatcher, broker runtimecore.ApprovalBroker, opts runtimecore.ExecOptions) (preparedBridge, func(), error) {
	hasRuntimeTools := dispatcher != nil && len(opts.ToolBindings) > 0
	hasProviderCapture := strings.TrimSpace(opts.Environment["ORRERY_PI_PROVIDER_REQUEST_CAPTURE_URL"]) != ""
	humanApprovalTools := piHumanApprovalRuntimeTools(opts.ToolBindings)
	hasRuntimeApproval := !piBridgeApprovalBrokerUnavailable(broker) && len(humanApprovalTools) > 0
	hasDynamicPrompt := strings.TrimSpace(opts.Environment[piSystemPromptPathEnv]) != ""
	hasDiaryFlush := diaryFlushEnabled(opts.ProviderSpecific)
	if !hasRuntimeTools && !hasProviderCapture && !hasRuntimeApproval && !hasDynamicPrompt && !hasDiaryFlush {
		return preparedBridge{}, func() {}, nil
	}
	var bridgeTools []piBridgeTool
	if hasRuntimeTools || hasRuntimeApproval || hasDiaryFlush {
		var err error
		bridgeTools, err = piBridgeToolsForRun(opts)
		if err != nil {
			return preparedBridge{}, func() {}, fmt.Errorf("prepare pi bridge tools: %w", err)
		}
	}
	bridgeDir := piBridgeDir(opts)
	if err := os.MkdirAll(bridgeDir, 0o755); err != nil {
		return preparedBridge{}, func() {}, fmt.Errorf("create pi bridge dir: %w", err)
	}
	if err := writePiRequestTimeoutSettings(opts); err != nil {
		return preparedBridge{}, func() {}, err
	}
	extensionPath := filepath.Join(bridgeDir, "orrery-runtime-extension.ts")
	if err := os.WriteFile(extensionPath, []byte(piBridgeExtensionSource()), 0o600); err != nil {
		return preparedBridge{}, func() {}, fmt.Errorf("write pi bridge extension: %w", err)
	}
	if !hasRuntimeTools && !hasRuntimeApproval && !hasDiaryFlush {
		return preparedBridge{
			ExtensionPath: extensionPath,
			Environment: map[string]string{
				"ORRERY_PI_EXTENSION_PATH": extensionPath,
			},
			cleanup: func() {},
		}, func() {}, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return preparedBridge{}, func() {}, fmt.Errorf("listen pi bridge: %w", err)
	}
	baseURL := "http://" + listener.Addr().String()
	authToken, err := newPiBridgeAuthToken()
	if err != nil {
		_ = listener.Close()
		return preparedBridge{}, func() {}, err
	}
	bridge := newBridgeServer(ctx, dispatcher, broker, opts)
	bridge.authToken = authToken
	server := &http.Server{
		Handler:           bridge,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			// There is no runtime logger at this layer; the next tool call will fail if serving stopped unexpectedly.
		}
	}()
	configPath := filepath.Join(bridgeDir, "bridge-config.json")
	cfg := piBridgeConfig{
		BaseURL:           baseURL,
		AuthToken:         authToken,
		Tools:             bridgeTools,
		ToolCallTimeoutMs: piBridgeToolCallTimeoutMs(opts),
	}
	if hasRuntimeApproval {
		cfg.Approval = &piBridgeApprovalConfig{
			Enabled:            true,
			HumanApprovalTools: humanApprovalTools,
		}
	}
	if hasDiaryFlush {
		cfg.Diary = &piBridgeDiaryConfig{Enabled: true}
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		_ = server.Close()
		return preparedBridge{}, func() {}, fmt.Errorf("encode pi bridge config: %w", err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		_ = server.Close()
		return preparedBridge{}, func() {}, fmt.Errorf("write pi bridge config: %w", err)
	}
	cleanup := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		select {
		case <-done:
		case <-ctx.Done():
		case <-shutdownCtx.Done():
		}
	}
	return preparedBridge{
		BaseURL:       baseURL,
		ConfigPath:    configPath,
		ExtensionPath: extensionPath,
		Environment: map[string]string{
			"ORRERY_PI_BRIDGE_CONFIG":  configPath,
			"ORRERY_PI_EXTENSION_PATH": extensionPath,
		},
		Server:    bridge,
		authToken: authToken,
		cleanup:   cleanup,
	}, cleanup, nil
}

func piBridgeToolCallTimeoutMs(opts runtimecore.ExecOptions) int64 {
	seconds, err := strconv.ParseInt(strings.TrimSpace(opts.ProviderSpecific[runtimecore.ToolCallTimeoutSecondsProviderKey]), 10, 64)
	if err != nil || seconds <= 0 || seconds > (1<<63-1)/1000 {
		return 0
	}
	return seconds * 1000
}

func writePiRequestTimeoutSettings(opts runtimecore.ExecOptions) error {
	timeoutMs := piBridgeToolCallTimeoutMs(opts)
	if timeoutMs <= 0 {
		return nil
	}
	agentDir := strings.TrimSpace(opts.Environment["PI_CODING_AGENT_DIR"])
	if agentDir == "" {
		return fmt.Errorf("configure PI HTTP idle timeout: PI_CODING_AGENT_DIR is required")
	}
	settingsPath := filepath.Join(agentDir, "settings.json")
	settings := map[string]any{}
	if raw, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("decode PI settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read PI settings: %w", err)
	}
	settings["httpIdleTimeoutMs"] = timeoutMs
	retry, _ := settings["retry"].(map[string]any)
	if retry == nil {
		retry = map[string]any{}
		settings["retry"] = retry
	}
	provider, _ := retry["provider"].(map[string]any)
	if provider == nil {
		provider = map[string]any{}
		retry["provider"] = provider
	}
	provider["timeoutMs"] = timeoutMs
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode PI settings: %w", err)
	}
	temporary, err := os.CreateTemp(agentDir, ".settings-*.json")
	if err != nil {
		return fmt.Errorf("create PI settings: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect PI settings: %w", err)
	}
	if _, err := temporary.Write(append(raw, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write PI settings: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close PI settings: %w", err)
	}
	if err := os.Rename(temporaryPath, settingsPath); err != nil {
		return fmt.Errorf("replace PI settings: %w", err)
	}
	return nil
}

func newPiBridgeAuthToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate pi bridge auth token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func prepareProviderRequestCapture(ctx context.Context, capturePath string) (preparedProviderRequestCapture, func(), error) {
	capturePath = strings.TrimSpace(capturePath)
	if capturePath == "" {
		return preparedProviderRequestCapture{}, func() {}, fmt.Errorf("provider request capture path is required")
	}
	if err := os.MkdirAll(filepath.Dir(capturePath), 0o755); err != nil {
		return preparedProviderRequestCapture{}, func() {}, fmt.Errorf("create provider request capture dir: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return preparedProviderRequestCapture{}, func() {}, fmt.Errorf("listen provider request capture: %w", err)
	}
	baseURL := "http://" + listener.Addr().String()
	server := &http.Server{Handler: providerRequestCaptureServer{path: capturePath}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		}
	}()
	cleanup := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		select {
		case <-done:
		case <-ctx.Done():
		case <-shutdownCtx.Done():
		}
	}
	return preparedProviderRequestCapture{BaseURL: baseURL, Path: capturePath}, cleanup, nil
}

func piBridgeDir(opts runtimecore.ExecOptions) string {
	if value := strings.TrimSpace(opts.Environment["PI_CODING_AGENT_DIR"]); value != "" {
		return filepath.Join(value, "orrery-runtime")
	}
	if value := strings.TrimSpace(opts.Cwd); value != "" {
		return filepath.Join(value, ".orrery", "pi-bridge")
	}
	return filepath.Join(os.TempDir(), "orrery-pi-bridge")
}

func piBridgeTools(bindings []agentcore.RuntimeToolBinding, includePlanTools bool) ([]piBridgeTool, error) {
	out := []piBridgeTool(nil)
	if includePlanTools {
		out = planBridgeTools()
	}
	seen := map[string]string{}
	for _, tool := range out {
		if !validPiBridgeToolName(tool.Name) {
			return nil, fmt.Errorf("built-in tool %q has invalid name %q", tool.SourceToolID, tool.Name)
		}
		seen[tool.Name] = tool.SourceToolID
	}
	for _, binding := range bindings {
		name := toolbridge.RuntimeToolName(binding)
		sourceToolID := strings.TrimSpace(string(binding.ToolID))
		if sourceToolID == "" {
			sourceToolID = strings.TrimSpace(binding.Alias)
		}
		if !validPiBridgeToolName(name) {
			return nil, fmt.Errorf("runtime tool %q has no provider-safe name", sourceToolID)
		}
		if previousSource, exists := seen[name]; exists {
			if previousSource == sourceToolID {
				continue
			}
			return nil, fmt.Errorf("runtime tool name %q collides between %q and %q", name, previousSource, sourceToolID)
		}
		seen[name] = sourceToolID
		parameters := cloneSchema(binding.InputSchema)
		if len(parameters) == 0 {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, piBridgeTool{
			Name:          name,
			Label:         firstNonBlank(binding.Alias, string(binding.ToolID), name),
			Description:   firstNonBlank(binding.Summary, "Orrery runtime tool "+name),
			PromptSnippet: binding.Summary,
			Parameters:    parameters,
			SourceToolID:  string(binding.ToolID),
		})
	}
	return out, nil
}

func piBridgeToolsForRun(opts runtimecore.ExecOptions) ([]piBridgeTool, error) {
	tools, err := piBridgeTools(opts.ToolBindings, !parseBool(opts.ProviderSpecific["diary_maintenance"]))
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(opts.ProviderSpecific[agentcore.RuntimeAllowedToolIDsProviderKey])
	if raw == "" {
		return tools, nil
	}
	var ids []string
	if json.Unmarshal([]byte(raw), &ids) != nil {
		return nil, nil
	}
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[strings.TrimSpace(id)] = true
	}
	out := make([]piBridgeTool, 0, len(tools))
	for _, tool := range tools {
		if allowed[tool.SourceToolID] {
			out = append(out, tool)
		}
	}
	return out, nil
}

func piPlanToolAllowed(name string, opts runtimecore.ExecOptions) bool {
	tools, err := piBridgeToolsForRun(opts)
	if err != nil {
		return false
	}
	for _, tool := range tools {
		if tool.Name == name && isPlanTool(tool.Name) {
			return true
		}
	}
	return false
}

func validPiBridgeToolName(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range name {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func piHumanApprovalRuntimeTools(bindings []agentcore.RuntimeToolBinding) []string {
	tools := make([]string, 0, len(bindings))
	seen := map[string]bool{}
	for _, binding := range bindings {
		name := toolbridge.RuntimeToolName(binding)
		if name == "" || seen[name] || !actioncontract.RuntimeToolRequiresHumanBusinessDecision("tool", name) {
			continue
		}
		seen[name] = true
		tools = append(tools, name)
	}
	return tools
}

const (
	marketingHILReceiptMetaKey     = "com.minvia/orrery-hil-receipt"
	marketingHILSigningKeyIDEnv    = "ORRERY_MARKETING_HIL_SIGNING_KEY_ID"
	marketingHILPrivateKeyPathEnv  = "ORRERY_MARKETING_HIL_PRIVATE_KEY_PATH"
	marketingHILReceiptLifetime    = 10 * time.Minute
	marketingWechatDraftToolSuffix = "marketing_wechat_draft_create"
)

type piApprovedToolCall struct {
	tool      string
	inputHash string
	receipt   map[string]any
	expiresAt time.Time
}

type piBridgeServer struct {
	mu                             sync.Mutex
	ctx                            context.Context
	dispatcher                     runtimecore.RuntimeToolDispatcher
	approvalBroker                 runtimecore.ApprovalBroker
	opts                           runtimecore.ExecOptions
	metadata                       map[string]string
	approvedCalls                  map[string]piApprovedToolCall
	continuedMarketingApprovalUsed bool
	authToken                      string
	now                            func() time.Time
}

func (s *piBridgeServer) Update(ctx context.Context, dispatcher runtimecore.RuntimeToolDispatcher, broker runtimecore.ApprovalBroker, opts runtimecore.ExecOptions) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	s.ctx = ctx
	s.dispatcher = dispatcher
	s.approvalBroker = broker
	s.opts = opts
	s.metadata = map[string]string{}
	s.approvedCalls = map[string]piApprovedToolCall{}
	s.continuedMarketingApprovalUsed = false
	s.mu.Unlock()
}

func (s *piBridgeServer) runState() (context.Context, runtimecore.RuntimeToolDispatcher, runtimecore.ApprovalBroker, runtimecore.ExecOptions) {
	if s == nil {
		return context.Background(), nil, nil, runtimecore.ExecOptions{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx, s.dispatcher, s.approvalBroker, s.opts
}

func newBridgeServer(ctx context.Context, dispatcher runtimecore.RuntimeToolDispatcher, broker runtimecore.ApprovalBroker, opts runtimecore.ExecOptions) *piBridgeServer {
	if ctx == nil {
		ctx = context.Background()
	}
	return &piBridgeServer{
		ctx: ctx, dispatcher: dispatcher, approvalBroker: broker, opts: opts,
		metadata: map[string]string{}, approvedCalls: map[string]piApprovedToolCall{}, now: time.Now,
	}
}

func (s *piBridgeServer) Metadata() map[string]string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStringMap(s.metadata)
}

func (s *piBridgeServer) startDiaryCompaction() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metadata == nil {
		s.metadata = map[string]string{}
	}
	sequence, _ := strconv.Atoi(strings.TrimSpace(s.metadata["diary_precompact_latest_sequence"]))
	sequence++
	s.metadata["diary_precompact_latest_sequence"] = strconv.Itoa(sequence)
	return sequence
}

func (s *piBridgeServer) isCurrentDiarySequence(sequence int) bool {
	if s == nil || sequence <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.metadata["diary_precompact_latest_sequence"]) == strconv.Itoa(sequence)
}

func (s *piBridgeServer) recordDiaryOutcome(status, revision string, sequence int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metadata == nil {
		s.metadata = map[string]string{}
	}
	status = strings.TrimSpace(status)
	revision = strings.TrimSpace(revision)
	previousStatus := strings.TrimSpace(s.metadata["diary_precompact_status"])
	previousRevision := strings.TrimSpace(s.metadata["diary_precompact_revision"])
	if revision == previousRevision && diaryFlushSucceeded(previousStatus) && (status == "failed" || status == "exhausted") {
		return
	}
	s.metadata["diary_precompact_status"] = status
	s.metadata["diary_precompact_revision"] = revision
	if sequence > 0 {
		s.metadata["diary_precompact_latest_sequence"] = strconv.Itoa(sequence)
		if diaryFlushSucceeded(status) {
			s.metadata["diary_precompact_success_sequence"] = strconv.Itoa(sequence)
		}
	}
}

func diaryFlushSucceeded(status string) bool {
	status = strings.TrimSpace(status)
	return status == "written" || status == "replayed" || status == "no_content"
}

type providerRequestCaptureServer struct {
	path string
}

func (s providerRequestCaptureServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/provider-request" {
		http.NotFound(w, r)
		return
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 32*1024*1024))
	if err != nil {
		writeBridgeError(w, http.StatusBadRequest, "read provider request body: "+err.Error())
		return
	}
	entry := map[string]any{
		"captured_at": time.Now().UTC().Format(time.RFC3339Nano),
		"method":      r.Method,
		"path":        r.URL.Path,
		"query":       r.URL.RawQuery,
		"headers":     redactedHeaders(r.Header),
		"body":        string(body),
	}
	line, err := json.Marshal(entry)
	if err != nil {
		writeBridgeError(w, http.StatusInternalServerError, "encode provider request capture: "+err.Error())
		return
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		writeBridgeError(w, http.StatusInternalServerError, "open provider request capture: "+err.Error())
		return
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		writeBridgeError(w, http.StatusInternalServerError, "write provider request capture: "+err.Error())
		return
	}
	if err := file.Close(); err != nil {
		writeBridgeError(w, http.StatusInternalServerError, "close provider request capture: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *piBridgeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeBridgeError(w, http.StatusUnauthorized, "pi bridge authentication failed")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/diary-config" {
		s.handleDiaryConfig(w)
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	switch r.URL.Path {
	case "/tool-call":
		s.handleToolCall(w, r)
	case "/approval":
		s.handleApproval(w, r)
	case "/diary-tool-call":
		s.handleDiaryToolCall(w, r)
	case "/diary-flush-start":
		s.handleDiaryFlushStart(w, r)
	case "/diary-flush-attempt":
		s.handleDiaryFlushAttempt(w, r)
	case "/diary-flush-outcome":
		s.handleDiaryFlushOutcome(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *piBridgeServer) handleDiaryFlushStart(w http.ResponseWriter, r *http.Request) {
	_, _, _, opts := s.runState()
	if !diaryFlushEnabled(opts.ProviderSpecific) {
		writeBridgeError(w, http.StatusBadRequest, "diary maintenance is not configured")
		return
	}
	var payload struct {
		CompactionSequence int `json:"compaction_sequence"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeBridgeError(w, http.StatusBadRequest, "decode PI diary flush start: "+err.Error())
		return
	}
	if payload.CompactionSequence <= 0 {
		writeBridgeError(w, http.StatusBadRequest, "positive compaction_sequence is required")
		return
	}
	sequence := s.startDiaryCompaction()
	writeBridgeJSON(w, http.StatusOK, map[string]any{"status": "started", "compaction_sequence": sequence})
}

func (s *piBridgeServer) authorized(r *http.Request) bool {
	if s == nil || strings.TrimSpace(s.authToken) == "" {
		return true
	}
	provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	expected := []byte(s.authToken)
	actual := []byte(provided)
	return len(actual) == len(expected) && subtle.ConstantTimeCompare(actual, expected) == 1
}

func (s *piBridgeServer) handleToolCall(w http.ResponseWriter, r *http.Request) {
	runCtx, dispatcher, _, opts := s.runState()
	var payload struct {
		ToolCallID string         `json:"tool_call_id"`
		Tool       string         `json:"tool"`
		Input      map[string]any `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeBridgeError(w, http.StatusBadRequest, "decode pi bridge tool call: "+err.Error())
		return
	}
	if isPlanTool(payload.Tool) {
		if !piPlanToolAllowed(payload.Tool, opts) {
			writeBridgeError(w, http.StatusForbidden, "internal plan tools are not allowed for this run")
			return
		}
		output, err := handlePlanTool(payload.Tool, cloneInput(payload.Input))
		if err != nil {
			writeBridgeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeBridgeJSON(w, http.StatusOK, map[string]any{"output": output})
		return
	}
	if dispatcher == nil {
		writeBridgeError(w, http.StatusBadRequest, "runtime tool dispatcher is not configured")
		return
	}
	binding, ok := toolbridge.RuntimeToolBindingForCall(payload.Tool, opts.ToolBindings)
	if !ok {
		writeBridgeError(w, http.StatusBadRequest, "runtime tool "+payload.Tool+" is not bound")
		return
	}
	requestMeta, err := s.consumeMarketingApprovalGrant(payload.ToolCallID, payload.Tool, payload.Input)
	if err != nil {
		writeBridgeError(w, http.StatusForbidden, err.Error())
		return
	}
	if runCtx == nil {
		runCtx = context.Background()
	}
	callCtx, cancel := context.WithCancel(runCtx)
	stopRequestCancellation := context.AfterFunc(r.Context(), cancel)
	defer stopRequestCancellation()
	defer cancel()
	result, err := dispatcher.CallRuntimeTool(callCtx, runtimecore.RuntimeToolCallRequest{
		TenantID:              strings.TrimSpace(opts.TenantID),
		RunID:                 opts.RunID,
		AgentID:               opts.AgentID,
		ExecutionEnvelope:     agentcore.CloneExecutionEnvelope(opts.ExecutionEnvelope),
		ExecutionPrincipal:    opts.ExecutionPrincipal,
		Name:                  firstNonBlank(string(binding.ToolID), payload.Tool),
		CallID:                strings.TrimSpace(payload.ToolCallID),
		Input:                 cloneInput(payload.Input),
		RequestMeta:           requestMeta,
		ToolBindings:          cloneToolBindings(opts.ToolBindings),
		SkillBindings:         append([]agentcore.RuntimeSkillBinding(nil), opts.SkillBindings...),
		ResponsibilityContext: agentcore.CloneResponsibilityContext(opts.ContextPack.ResponsibilityContext),
		ProviderSpecific:      cloneStringMap(opts.ProviderSpecific),
		WorkspaceRoot:         strings.TrimSpace(opts.WorkspacePolicy.Root),
		WorkingDirectory:      firstNonBlank(opts.Cwd, opts.WorkspacePolicy.WorkDir),
		WorkspacePolicy:       cloneWorkspacePolicy(opts.WorkspacePolicy),
	})
	if err != nil {
		if isOptionalRetrievalBinding(binding) {
			writeBridgeJSON(w, http.StatusOK, map[string]any{"output": map[string]any{
				"status":        "failed",
				"tool":          firstNonBlank(string(binding.ToolID), payload.Tool),
				"tool_call_id":  strings.TrimSpace(payload.ToolCallID),
				"arguments":     cloneInput(payload.Input),
				"error":         strings.TrimSpace(err.Error()),
				"error_code":    "optional_retrieval_failed",
				"retryable":     false,
				"recovery_hint": "Continue without this optional context, or retry once with a narrower query. Do not end the interaction solely because optional retrieval failed.",
			}})
			return
		}
		writeBridgeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	output := result.Output
	if output == nil {
		output = map[string]any{}
	}
	if result.OutcomeUncertain {
		output["outcome_uncertain"] = true
		output["retryable"] = false
	}
	writeBridgeJSON(w, http.StatusOK, map[string]any{"output": output})
}

func isOptionalRetrievalBinding(binding agentcore.RuntimeToolBinding) bool {
	switch toolbridge.NormalizeToolName(string(binding.ToolID)) {
	case toolbridge.NormalizeToolName(string(toolstd.KnowledgeSearch)),
		toolbridge.NormalizeToolName(string(toolstd.MemorySearch)),
		toolbridge.NormalizeToolName(string(toolstd.EnterpriseGraphBuildContextBundle)):
		return true
	default:
		return false
	}
}

func (s *piBridgeServer) handleDiaryConfig(w http.ResponseWriter) {
	_, _, _, opts := s.runState()
	date, path := s.diaryTarget(opts)
	writeBridgeJSON(w, http.StatusOK, map[string]any{
		"enabled":                diaryFlushEnabled(opts.ProviderSpecific),
		"date":                   date,
		"path":                   path,
		"max_attempts":           bridgeInt(opts.ProviderSpecific["diary_max_attempts"], 3),
		"context_window_tokens":  bridgeInt(opts.ProviderSpecific["diary_context_window_tokens"], 0),
		"reserve_tokens":         bridgeInt(opts.ProviderSpecific["diary_reserve_tokens"], 0),
		"soft_threshold_tokens":  bridgeInt(opts.ProviderSpecific["diary_soft_threshold_tokens"], 4000),
		"force_transcript_bytes": bridgeInt64(opts.ProviderSpecific["diary_force_transcript_bytes"], 2*1024*1024),
	})
}

func (s *piBridgeServer) diaryTarget(opts runtimecore.ExecOptions) (string, string) {
	date := strings.TrimSpace(opts.ProviderSpecific["diary_target_date"])
	path := strings.TrimSpace(opts.ProviderSpecific["diary_target_path"])
	if timezone := strings.TrimSpace(opts.ProviderSpecific["diary_timezone"]); timezone != "" {
		location, err := time.LoadLocation(timezone)
		if err != nil {
			return date, path
		}
		now := time.Now
		if s != nil && s.now != nil {
			now = s.now
		}
		date = now().In(location).Format(time.DateOnly)
		path = "memory/" + date + ".md"
	}
	return date, path
}

func (s *piBridgeServer) handleDiaryFlushAttempt(w http.ResponseWriter, r *http.Request) {
	_, dispatcher, _, opts := s.runState()
	controller, ok := dispatcher.(runtimecore.RuntimeDiaryFlushController)
	if !ok || controller == nil || !diaryFlushEnabled(opts.ProviderSpecific) {
		writeBridgeError(w, http.StatusBadRequest, "diary flush controller is not configured")
		return
	}
	var payload struct {
		SessionID           string `json:"session_id"`
		ContextRevision     string `json:"context_revision"`
		ContextWindowTokens int    `json:"context_window_tokens"`
		ReserveTokens       int    `json:"reserve_tokens"`
		ProjectedTokens     int    `json:"projected_tokens"`
		TranscriptBytes     int64  `json:"transcript_bytes"`
		CompactionSequence  int    `json:"compaction_sequence"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeBridgeError(w, http.StatusBadRequest, "decode PI diary flush attempt: "+err.Error())
		return
	}
	if !s.isCurrentDiarySequence(payload.CompactionSequence) {
		writeBridgeError(w, http.StatusBadRequest, "diary compaction_sequence is not current")
		return
	}
	decision, err := controller.BeginRuntimeDiaryFlush(r.Context(), runtimecore.RuntimeDiaryFlushAttemptRequest{
		TenantID: piDiaryTenantID(opts), AgentID: opts.AgentID,
		SessionID: strings.TrimSpace(payload.SessionID), ContextRevision: strings.TrimSpace(payload.ContextRevision),
		ContextWindowTokens: payload.ContextWindowTokens, ReserveTokens: payload.ReserveTokens,
		ProjectedTokens: payload.ProjectedTokens, TranscriptBytes: payload.TranscriptBytes,
	})
	if err != nil {
		writeBridgeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeBridgeJSON(w, http.StatusOK, map[string]any{"allowed": decision.Allowed, "attempt": decision.Attempt, "reason": decision.Reason})
}

type piDiaryToolCallPayload struct {
	ToolCallID         string         `json:"tool_call_id"`
	Tool               string         `json:"tool"`
	Input              map[string]any `json:"input"`
	SessionID          string         `json:"session_id"`
	ContextRevision    string         `json:"context_revision"`
	CompactionSequence int            `json:"compaction_sequence"`
}

func (s *piBridgeServer) handleDiaryToolCall(w http.ResponseWriter, r *http.Request) {
	runCtx, dispatcher, _, opts := s.runState()
	if dispatcher == nil || !diaryFlushEnabled(opts.ProviderSpecific) {
		writeBridgeError(w, http.StatusBadRequest, "diary maintenance is not configured")
		return
	}
	var payload piDiaryToolCallPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeBridgeError(w, http.StatusBadRequest, "decode PI diary tool call: "+err.Error())
		return
	}
	if !s.isCurrentDiarySequence(payload.CompactionSequence) {
		writeBridgeError(w, http.StatusBadRequest, "diary compaction_sequence is not current")
		return
	}
	toolName := toolbridge.NormalizeToolName(payload.Tool)
	if toolName != string(toolstd.MemoryDiaryRead) && toolName != string(toolstd.MemoryDiaryAppend) {
		writeBridgeError(w, http.StatusBadRequest, "unsupported diary maintenance tool")
		return
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	revision := strings.TrimSpace(payload.ContextRevision)
	if sessionID == "" || revision == "" {
		writeBridgeError(w, http.StatusBadRequest, "diary session_id and context_revision are required")
		return
	}
	bindings := piDiaryMaintenanceBindings()
	providerSpecific := piDiaryProviderSpecific(opts, sessionID, revision)
	providerSpecific["diary_target_date"], providerSpecific["diary_target_path"] = s.diaryTarget(opts)
	if runCtx == nil {
		runCtx = context.Background()
	}
	callCtx, cancel := context.WithCancel(runCtx)
	stopRequestCancellation := context.AfterFunc(r.Context(), cancel)
	defer stopRequestCancellation()
	defer cancel()
	result, err := dispatcher.CallRuntimeTool(callCtx, runtimecore.RuntimeToolCallRequest{
		TenantID:              piDiaryTenantID(opts),
		RunID:                 opts.RunID,
		AgentID:               opts.AgentID,
		ExecutionEnvelope:     agentcore.CloneExecutionEnvelope(opts.ExecutionEnvelope),
		ExecutionPrincipal:    opts.ExecutionPrincipal,
		Name:                  toolName,
		CallID:                strings.TrimSpace(payload.ToolCallID),
		Input:                 cloneInput(payload.Input),
		ToolBindings:          bindings,
		ResponsibilityContext: agentcore.CloneResponsibilityContext(opts.ContextPack.ResponsibilityContext),
		ProviderSpecific:      providerSpecific,
		WorkspaceRoot:         strings.TrimSpace(opts.WorkspacePolicy.Root),
		WorkingDirectory:      firstNonBlank(opts.Cwd, opts.WorkspacePolicy.WorkDir),
		WorkspacePolicy:       cloneWorkspacePolicy(opts.WorkspacePolicy),
	})
	if err != nil {
		writeBridgeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	output := result.Output
	if output == nil {
		output = map[string]any{}
	}
	if toolName == string(toolstd.MemoryDiaryAppend) {
		status := strings.TrimSpace(fmt.Sprint(output["status"]))
		if status == "written" || status == "replayed" {
			s.recordDiaryOutcome(status, revision, payload.CompactionSequence)
		}
	}
	writeBridgeJSON(w, http.StatusOK, map[string]any{"output": output})
}

func (s *piBridgeServer) handleDiaryFlushOutcome(w http.ResponseWriter, r *http.Request) {
	_, dispatcher, _, opts := s.runState()
	if !diaryFlushEnabled(opts.ProviderSpecific) {
		writeBridgeError(w, http.StatusBadRequest, "diary maintenance is not configured")
		return
	}
	var payload struct {
		Status             string `json:"status"`
		SessionID          string `json:"session_id"`
		ContextRevision    string `json:"context_revision"`
		Date               string `json:"date"`
		CompactionSequence int    `json:"compaction_sequence"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeBridgeError(w, http.StatusBadRequest, "decode PI diary outcome: "+err.Error())
		return
	}
	if !s.isCurrentDiarySequence(payload.CompactionSequence) {
		writeBridgeError(w, http.StatusBadRequest, "diary compaction_sequence is not current")
		return
	}
	status := strings.TrimSpace(payload.Status)
	if status != "no_content" && status != "exhausted" && status != "failed" {
		writeBridgeError(w, http.StatusBadRequest, "unsupported diary flush outcome")
		return
	}
	if strings.TrimSpace(payload.SessionID) == "" || strings.TrimSpace(payload.ContextRevision) == "" {
		writeBridgeError(w, http.StatusBadRequest, "diary session_id and context_revision are required")
		return
	}
	controller, ok := dispatcher.(runtimecore.RuntimeDiaryFlushController)
	if !ok || controller == nil {
		writeBridgeError(w, http.StatusBadRequest, "diary flush controller is not configured")
		return
	}
	if err := controller.CompleteRuntimeDiaryFlush(r.Context(), runtimecore.RuntimeDiaryFlushOutcomeRequest{
		TenantID: piDiaryTenantID(opts), AgentID: opts.AgentID,
		SessionID: strings.TrimSpace(payload.SessionID), ContextRevision: strings.TrimSpace(payload.ContextRevision), Status: status,
		Date: strings.TrimSpace(payload.Date),
	}); err != nil {
		writeBridgeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordDiaryOutcome(status, payload.ContextRevision, payload.CompactionSequence)
	writeBridgeJSON(w, http.StatusOK, map[string]any{"status": status})
}

func bridgeInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func bridgeInt64(value string, fallback int64) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func piDiaryMaintenanceBindings() []agentcore.RuntimeToolBinding {
	return []agentcore.RuntimeToolBinding{
		{
			ToolID: toolstd.MemoryDiaryRead, GrantedPermissions: []string{toolstd.PermissionMemoryDiaryRead},
			Summary:     "Read only the host-selected canonical daily diary.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []any{"path"}},
		},
		{
			ToolID: toolstd.MemoryDiaryAppend, GrantedPermissions: []string{toolstd.PermissionMemoryDiaryWrite},
			Summary:     "Append new durable Markdown only to the host-selected canonical daily diary.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}, "required": []any{"path", "content"}},
		},
	}
}

func piDiaryProviderSpecific(opts runtimecore.ExecOptions, sessionID, revision string) map[string]string {
	values := cloneStringMap(opts.ProviderSpecific)
	if values == nil {
		values = map[string]string{}
	}
	values["diary_maintenance"] = "true"
	values["diary_session_id"] = strings.TrimSpace(sessionID)
	values["diary_context_revision"] = strings.TrimSpace(revision)
	values["diary_trigger"] = "pre_compaction"
	values["diary_idempotency_key"] = piDiaryIdempotencyKey(opts, sessionID, revision)
	return values
}

func piDiaryIdempotencyKey(opts runtimecore.ExecOptions, sessionID, revision string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{piDiaryTenantID(opts), opts.AgentID.String(), sessionID, revision, "pre_compaction"}, "\x00")))
	return "diary-flush:" + hex.EncodeToString(sum[:16])
}

func piDiaryTenantID(opts runtimecore.ExecOptions) string {
	if tenantID := strings.TrimSpace(opts.ProviderSpecific["diary_tenant_id"]); tenantID != "" {
		return tenantID
	}
	return strings.TrimSpace(opts.TenantID)
}

func diaryFlushEnabled(values map[string]string) bool {
	value := strings.TrimSpace(values["diary_flush_enabled"])
	return value == "1" || strings.EqualFold(value, "true")
}

func (s *piBridgeServer) handleApproval(w http.ResponseWriter, r *http.Request) {
	approvalCtx, _, approvalBroker, opts := s.runState()
	var payload struct {
		ToolCallID string         `json:"tool_call_id"`
		Tool       string         `json:"tool"`
		Input      map[string]any `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeBridgeError(w, http.StatusBadRequest, "decode pi bridge approval: "+err.Error())
		return
	}
	if isPlanTool(payload.Tool) {
		if !piPlanToolAllowed(payload.Tool, opts) {
			writeBridgeJSON(w, http.StatusOK, map[string]any{"allow": false, "decision": string(runtimecore.ApprovalDecisionDenied), "reason": "internal plan tools are not allowed for this run"})
			return
		}
		writeBridgeJSON(w, http.StatusOK, map[string]any{"allow": true, "decision": string(runtimecore.ApprovalDecisionApproved), "reason": "internal runtime plan tool"})
		return
	}
	if response, handled, err := s.consumeMarketingContinuationApproval(payload.Tool, payload.Input, opts); handled {
		if err == nil {
			err = s.storeMarketingApprovalGrant(payload.ToolCallID, payload.Tool, payload.Input, response)
		}
		if err != nil {
			writeBridgeJSON(w, http.StatusOK, map[string]any{"allow": false, "decision": string(runtimecore.ApprovalDecisionDenied), "reason": err.Error()})
			return
		}
		writeBridgeJSON(w, http.StatusOK, map[string]any{"allow": true, "decision": string(runtimecore.ApprovalDecisionApproved), "reason": "approved HIL continuation"})
		return
	}
	if piBridgeApprovalBrokerUnavailable(approvalBroker) {
		writeBridgeJSON(w, http.StatusOK, map[string]any{"allow": false, "decision": string(runtimecore.ApprovalDecisionDenied), "reason": "approval broker unavailable"})
		return
	}
	req := piBridgeApprovalRequest(payload.ToolCallID, payload.Tool, payload.Input, opts)
	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	if approvalCtx == nil {
		approvalCtx = context.Background()
	}
	if err := approvalBroker.RequestApproval(approvalCtx, req, func(resp runtimecore.ApprovalResponse) error {
		if resp.Decision == runtimecore.ApprovalDecisionApproved && isMarketingWechatDraftTool(payload.Tool) {
			if err := s.storeMarketingApprovalGrant(payload.ToolCallID, payload.Tool, payload.Input, resp); err != nil {
				denied := runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionDenied, Reason: err.Error()}
				select {
				case responseCh <- denied:
				default:
				}
				return err
			}
		}
		select {
		case responseCh <- resp:
		default:
		}
		return nil
	}); err != nil {
		writeBridgeJSON(w, http.StatusOK, map[string]any{"allow": false, "decision": string(runtimecore.ApprovalDecisionDenied), "reason": err.Error()})
		return
	}
	var timeout <-chan time.Time
	var timer *time.Timer
	if hold := piBridgeApprovalHoldTimeout(approvalBroker); hold > 0 {
		timer = time.NewTimer(hold)
		timeout = timer.C
		defer stopPiBridgeTimer(timer)
	}
	select {
	case resp := <-responseCh:
		decision := runtimecore.ApprovalDecisionDenied
		allow := false
		if resp.Decision == runtimecore.ApprovalDecisionApproved {
			decision = runtimecore.ApprovalDecisionApproved
			allow = true
		}
		writeBridgeJSON(w, http.StatusOK, map[string]any{
			"allow":    allow,
			"decision": string(decision),
			"reason":   strings.TrimSpace(resp.Reason),
		})
	case <-timeout:
		writeBridgeJSON(w, http.StatusOK, map[string]any{"allow": false, "decision": string(runtimecore.ApprovalDecisionDenied), "reason": "runtime approval timed out"})
	case <-approvalCtx.Done():
		writeBridgeJSON(w, http.StatusOK, map[string]any{"allow": false, "decision": string(runtimecore.ApprovalDecisionDenied), "reason": approvalCtx.Err().Error()})
	case <-r.Context().Done():
		writeBridgeJSON(w, http.StatusOK, map[string]any{"allow": false, "decision": string(runtimecore.ApprovalDecisionDenied), "reason": r.Context().Err().Error()})
	}
}

func piBridgeApprovalBrokerUnavailable(broker runtimecore.ApprovalBroker) bool {
	if broker == nil {
		return true
	}
	switch broker.(type) {
	case runtimecore.DeferredApprovalBroker, *runtimecore.DeferredApprovalBroker:
		return true
	default:
		return false
	}
}

func piBridgeApprovalHoldTimeout(broker runtimecore.ApprovalBroker) time.Duration {
	if provider, ok := broker.(interface{ RuntimeApprovalHoldTimeout() time.Duration }); ok {
		return provider.RuntimeApprovalHoldTimeout()
	}
	return 0
}

func stopPiBridgeTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func piBridgeApprovalRequest(callID, nativeTool string, input map[string]any, opts runtimecore.ExecOptions) runtimecore.ApprovalRequest {
	nativeTool = firstNonBlank(nativeTool, "tool")
	kind, toolName := runtimecore.ApprovalKindAndToolName(nativeTool)
	raw, _ := json.Marshal(map[string]any{
		"tool_call_id": strings.TrimSpace(callID),
		"tool":         nativeTool,
		"input":        cloneInput(input),
	})
	metadata := map[string]string{
		"approval_native_event":     "tool_call",
		"approval_native_tool_name": nativeTool,
		"approval_tool_use_id":      strings.TrimSpace(callID),
		"approval_run_id":           strings.TrimSpace(string(opts.RunID)),
		"approval_agent_id":         strings.TrimSpace(string(opts.AgentID)),
	}
	if isMarketingWechatDraftTool(nativeTool) {
		metadata["approval_force_manual"] = "true"
		metadata["approval_allowed_actions"] = "approve_once,deny"
	}
	for key, value := range runtimecore.ApprovalPayloadMetadata(raw) {
		metadata[key] = value
	}
	return runtimecore.ApprovalRequest{
		Source:                    "pi",
		RequestID:                 firstNonBlank(callID, "pi:"+nativeTool),
		SessionID:                 strings.TrimSpace(opts.ResumeSessionID),
		Kind:                      kind,
		ToolName:                  toolName,
		Payload:                   append([]byte(nil), raw...),
		Metadata:                  metadata,
		WorkspaceRoot:             firstNonBlank(opts.WorkspacePolicy.Root, opts.Cwd, opts.WorkspacePolicy.WorkDir),
		WorkingDirectory:          firstNonBlank(opts.WorkspacePolicy.WorkDir, opts.Cwd),
		WorkspaceBoundaryEnforced: opts.WorkspacePolicy.EnforceBoundary,
		WorkspaceWriteAuthorized:  parseBool(opts.ProviderSpecific[agentcore.CanonicalDiscussionWorkspaceProviderKey]),
	}
}

func (s *piBridgeServer) storeMarketingApprovalGrant(callID, tool string, input map[string]any, response runtimecore.ApprovalResponse) error {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return fmt.Errorf("marketing HIL tool call id is required")
	}
	mode := approvalMetadataString(response.Metadata, "approval_mode")
	if mode != "manual" && mode != "hil" {
		return fmt.Errorf("marketing publish requires manual Orrery HIL approval")
	}
	if approvalMetadataString(response.Metadata, "approval_action_id") != "approve_once" {
		return fmt.Errorf("marketing publish requires approve_once")
	}
	approvalID := approvalMetadataString(response.Metadata, "approval_id")
	decidedBy := approvalMetadataString(response.Metadata, "approval_decided_by")
	decidedAt := approvalMetadataString(response.Metadata, "approval_decided_at")
	if approvalID == "" || decidedBy == "" || decidedAt == "" {
		return fmt.Errorf("Orrery HIL decision metadata is incomplete")
	}
	if _, err := time.Parse(time.RFC3339Nano, decidedAt); err != nil {
		return fmt.Errorf("Orrery HIL decision time is invalid")
	}
	intentID := firstInputString(input, "intent_id")
	intentHash := firstInputString(input, "intent_hash")
	if intentID == "" || !validLowerSHA256(intentHash) {
		return fmt.Errorf("marketing publish intent binding is invalid")
	}
	keyID, privateKey, err := marketingHILSigningKey()
	if err != nil {
		return err
	}
	now := s.now().UTC()
	expiresAt := now.Add(marketingHILReceiptLifetime)
	receipt := map[string]any{
		"receipt_version": "1",
		"key_id":          keyID,
		"source":          "orrery_hil",
		"mode":            "manual",
		"approval_id":     approvalID,
		"intent_id":       intentID,
		"intent_hash":     intentHash,
		"action":          "approve_once",
		"decided_by":      decidedBy,
		"decided_at":      decidedAt,
		"expires_at":      expiresAt.Format(time.RFC3339Nano),
	}
	unsigned, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode marketing HIL receipt: %w", err)
	}
	receipt["signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, unsigned))
	inputHash, err := marketingApprovalInputHash(input)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.approvedCalls == nil {
		s.approvedCalls = map[string]piApprovedToolCall{}
	}
	for id, grant := range s.approvedCalls {
		if !grant.expiresAt.After(now) {
			delete(s.approvedCalls, id)
		}
	}
	s.approvedCalls[callID] = piApprovedToolCall{
		tool: strings.TrimSpace(tool), inputHash: inputHash, receipt: receipt, expiresAt: expiresAt,
	}
	return nil
}

func (s *piBridgeServer) consumeMarketingContinuationApproval(tool string, input map[string]any, opts runtimecore.ExecOptions) (runtimecore.ApprovalResponse, bool, error) {
	values := opts.ProviderSpecific
	if !parseBool(values["approval_continuation"]) || !isMarketingWechatDraftTool(tool) {
		return runtimecore.ApprovalResponse{}, false, nil
	}
	deny := func(message string) (runtimecore.ApprovalResponse, bool, error) {
		return runtimecore.ApprovalResponse{}, true, fmt.Errorf("marketing HIL continuation: %s", message)
	}
	if values["approval_decision"] != string(runtimecore.ApprovalDecisionApproved) || values["approval_action_id"] != "approve_once" {
		return deny("approval is not approve_once")
	}
	if !isMarketingWechatDraftTool(values[runtimecore.ApprovalMetadataToolName]) {
		return deny("approved tool does not match")
	}
	if strings.TrimSpace(values["hil_decision_id"]) == "" || strings.TrimSpace(values["hil_receipt_id"]) == "" || values["hil_receipt_effect"] != "authorize" {
		return deny("durable HIL receipt is incomplete")
	}
	for inputKey, metadataKey := range map[string]string{
		"account_id":  "approval_material_account_id",
		"intent_id":   "approval_material_intent_id",
		"intent_hash": "approval_material_intent_hash",
	} {
		approved := strings.TrimSpace(values[metadataKey])
		if approved == "" || firstInputString(input, inputKey) != approved {
			return deny("approved intent binding does not match")
		}
	}
	decidedBy := strings.TrimSpace(values["approval_decided_by"])
	decidedAt := strings.TrimSpace(values["approval_decided_at"])
	if strings.TrimSpace(values["approval_id"]) == "" || decidedBy == "" || decidedAt == "" {
		return deny("decision metadata is incomplete")
	}
	if _, err := time.Parse(time.RFC3339Nano, decidedAt); err != nil {
		return deny("decision time is invalid")
	}
	s.mu.Lock()
	if s.continuedMarketingApprovalUsed {
		s.mu.Unlock()
		return deny("approval was already consumed")
	}
	s.continuedMarketingApprovalUsed = true
	s.mu.Unlock()
	return runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionApproved, Metadata: map[string]any{
		"approval_id":         strings.TrimSpace(values["approval_id"]),
		"approval_mode":       "manual",
		"approval_action_id":  "approve_once",
		"approval_decided_by": decidedBy,
		"approval_decided_at": decidedAt,
	}}, true, nil
}

func (s *piBridgeServer) consumeMarketingApprovalGrant(callID, tool string, input map[string]any) (map[string]any, error) {
	if !isMarketingWechatDraftTool(tool) {
		return nil, nil
	}
	callID = strings.TrimSpace(callID)
	now := s.now().UTC()
	s.mu.Lock()
	grant, ok := s.approvedCalls[callID]
	delete(s.approvedCalls, callID)
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("manual Orrery HIL approval is required for this marketing publish call")
	}
	if !grant.expiresAt.After(now) {
		return nil, fmt.Errorf("Orrery HIL approval grant has expired")
	}
	inputHash, err := marketingApprovalInputHash(input)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(grant.tool) != strings.TrimSpace(tool) || grant.inputHash != inputHash {
		return nil, fmt.Errorf("Orrery HIL approval does not match this marketing publish call")
	}
	return map[string]any{marketingHILReceiptMetaKey: cloneInput(grant.receipt)}, nil
}

func marketingHILSigningKey() (string, ed25519.PrivateKey, error) {
	keyID := strings.TrimSpace(os.Getenv(marketingHILSigningKeyIDEnv))
	path := strings.TrimSpace(os.Getenv(marketingHILPrivateKeyPathEnv))
	if keyID == "" || path == "" {
		return "", nil, fmt.Errorf("Orrery Marketing HIL signing is not configured")
	}
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", nil, fmt.Errorf("read Orrery Marketing HIL signing key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return "", nil, fmt.Errorf("decode Orrery Marketing HIL signing key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", nil, fmt.Errorf("parse Orrery Marketing HIL signing key: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return "", nil, fmt.Errorf("Orrery Marketing HIL signing key must be Ed25519")
	}
	return keyID, privateKey, nil
}

func marketingApprovalInputHash(input map[string]any) (string, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode marketing publish input: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func approvalMetadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func firstInputString(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}

func isMarketingWechatDraftTool(tool string) bool {
	normalized := strings.NewReplacer("-", "_", " ", "_", ".", "_").Replace(strings.ToLower(strings.TrimSpace(tool)))
	return strings.HasSuffix(normalized, marketingWechatDraftToolSuffix)
}

func validLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func writeBridgeError(w http.ResponseWriter, status int, message string) {
	writeBridgeJSON(w, status, map[string]any{"error": message})
}

func writeBridgeJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func cloneToolBindings(src []agentcore.RuntimeToolBinding) []agentcore.RuntimeToolBinding {
	if len(src) == 0 {
		return nil
	}
	out := make([]agentcore.RuntimeToolBinding, len(src))
	copy(out, src)
	for i := range out {
		out[i].GrantedPermissions = append([]string(nil), out[i].GrantedPermissions...)
		out[i].SecretEnv = cloneStringMap(out[i].SecretEnv)
		out[i].InputSchema = cloneSchema(out[i].InputSchema)
		out[i].OutputSchema = cloneSchema(out[i].OutputSchema)
	}
	return out
}

func cloneWorkspacePolicy(src agentcore.RuntimeWorkspacePolicy) agentcore.RuntimeWorkspacePolicy {
	src.WritableRoots = append([]string(nil), src.WritableRoots...)
	src.Profile = src.Profile.Clone()
	return src
}

func cloneInput(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func cloneSchema(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

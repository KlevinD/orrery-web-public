package mcpruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"orrery_v2/internal/agent/runtime/process"
	"orrery_v2/internal/app/resourceadmin"
	toolres "orrery_v2/internal/resource/tool"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type SecretResolver interface {
	ResolveSecret(ctx context.Context, ref string) (string, error)
}

type Options struct {
	Client         *http.Client
	Timeout        time.Duration
	Now            func() time.Time
	SecretResolver SecretResolver
}

type Runtime struct {
	client         *http.Client
	timeout        time.Duration
	now            func() time.Time
	secretResolver SecretResolver
}

type mcpHTTPStatusError struct {
	Operation  string
	StatusCode int
	Body       string
}

func (e mcpHTTPStatusError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("%s failed with status %d", e.Operation, e.StatusCode)
	}
	return fmt.Sprintf("%s failed with status %d: %s", e.Operation, e.StatusCode, body)
}

func asMCPHTTPStatusError(err error, target *mcpHTTPStatusError) bool {
	if err == nil || target == nil {
		return false
	}
	return errors.As(err, target)
}

func New(opts Options) *Runtime {
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Runtime{client: client, timeout: timeout, now: now, secretResolver: opts.SecretResolver}
}

func (r *Runtime) CheckHealth(ctx context.Context, definition toolres.ToolDefinition) (resourceadmin.MCPRuntimeHealth, error) {
	started := r.now()
	transport := effectiveTransport(definition)
	err := r.initializeOnly(ctx, definition)
	latency := r.now().Sub(started)
	if err != nil {
		httpStatus := 0
		var statusErr mcpHTTPStatusError
		if asMCPHTTPStatusError(err, &statusErr) {
			httpStatus = statusErr.StatusCode
		}
		return resourceadmin.MCPRuntimeHealth{
			Status:        "unavailable",
			FailureReason: err.Error(),
			HTTPStatus:    httpStatus,
			Latency:       latency,
		}, err
	}
	transportLabel := string(transport)
	if transportLabel == "" {
		transportLabel = "mcp"
	}
	return resourceadmin.MCPRuntimeHealth{
		Status:  "available",
		Message: "MCP " + transportLabel + " initialize succeeded",
		Latency: latency,
	}, nil
}

func (r *Runtime) ListTools(ctx context.Context, definition toolres.ToolDefinition) (resourceadmin.MCPRuntimeToolList, error) {
	started := r.now()
	var result mcpToolsListResult
	err := r.callWithSession(ctx, definition, "tools/list", map[string]any{}, &result)
	latency := r.now().Sub(started)
	if err != nil {
		return resourceadmin.MCPRuntimeToolList{FailureReason: err.Error(), Latency: latency}, err
	}
	tools := make([]string, 0, len(result.Tools))
	details := make([]toolres.MCPExposedToolDefinition, 0, len(result.Tools))
	for _, item := range result.Tools {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		tools = append(tools, name)
		details = append(details, toolres.MCPExposedToolDefinition{
			Name:         name,
			Description:  strings.TrimSpace(item.Description),
			InputSchema:  cloneSchema(item.InputSchema),
			OutputSchema: cloneSchema(item.OutputSchema),
		})
	}
	return resourceadmin.MCPRuntimeToolList{Tools: tools, ToolDetails: details, Latency: latency}, nil
}

func (r *Runtime) CallTool(ctx context.Context, req mcptransport.ExternalMCPCallRequest) (mcptransport.CallToolResult, error) {
	var result mcpToolsCallResult
	params := map[string]any{
		"name":      strings.TrimSpace(req.DownstreamToolName),
		"arguments": cloneSchema(req.Arguments),
	}
	if len(req.RequestMeta) > 0 {
		params["_meta"] = cloneSchema(req.RequestMeta)
	}
	err := r.callWithSession(ctx, req.Server, "tools/call", params, &result)
	if err != nil {
		return mcptransport.CallToolResult{}, err
	}
	output := map[string]any{}
	for key, value := range result.StructuredContent {
		output[key] = value
	}
	if len(output) == 0 || containsNonTextMCPContent(result.Content) {
		output["content"] = append([]mcpContent(nil), result.Content...)
	}
	if result.IsError {
		return mcptransport.CallToolResult{Output: output}, externalMCPToolError(result.Content)
	}
	return mcptransport.CallToolResult{Output: output}, nil
}

func containsNonTextMCPContent(content []mcpContent) bool {
	for _, item := range content {
		if strings.TrimSpace(item.Type) != "" && item.Type != "text" {
			return true
		}
	}
	return false
}

func externalMCPToolError(content []mcpContent) error {
	const maxDetailRunes = 512
	for _, item := range content {
		detail := strings.TrimSpace(item.Text)
		if item.Type != "text" || detail == "" {
			continue
		}
		runes := []rune(detail)
		if len(runes) > maxDetailRunes {
			detail = string(runes[:maxDetailRunes]) + "..."
		}
		return fmt.Errorf("external mcp tool returned an error: %s", detail)
	}
	return errors.New("external mcp tool returned an error")
}

func (r *Runtime) callWithSession(ctx context.Context, definition toolres.ToolDefinition, method string, params any, out any) error {
	switch effectiveTransport(definition) {
	case toolres.TransportStdio:
		return r.callStdioWithSession(ctx, definition, method, params, out)
	case toolres.TransportSSE:
		return r.callSSEWithSession(ctx, definition, method, params, out)
	}
	sessionID, err := r.initialize(ctx, definition)
	if err != nil {
		return err
	}
	if sessionID != "" {
		if _, err := r.postJSONRPC(ctx, definition, "notifications/initialized", nil, nil, sessionID, false); err != nil {
			return err
		}
	}
	_, err = r.postJSONRPC(ctx, definition, method, params, out, sessionID, true)
	return err
}

func (r *Runtime) initializeOnly(ctx context.Context, definition toolres.ToolDefinition) error {
	switch effectiveTransport(definition) {
	case toolres.TransportStdio:
		return r.callStdioInitialize(ctx, definition)
	case toolres.TransportSSE:
		return r.callSSEInitialize(ctx, definition)
	}
	_, err := r.initialize(ctx, definition)
	return err
}

func (r *Runtime) callSSEInitialize(ctx context.Context, definition toolres.ToolDefinition) error {
	session, err := r.startSSESession(ctx, definition)
	if err != nil {
		return err
	}
	defer session.close()
	var result map[string]any
	return session.request("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "orrery", "version": "0.1.0"},
	}, &result)
}

func (r *Runtime) callSSEWithSession(ctx context.Context, definition toolres.ToolDefinition, method string, params any, out any) error {
	session, err := r.startSSESession(ctx, definition)
	if err != nil {
		return err
	}
	defer session.close()
	var initialized map[string]any
	if err := session.request("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "orrery", "version": "0.1.0"},
	}, &initialized); err != nil {
		return err
	}
	if err := session.notify("notifications/initialized", nil); err != nil {
		return err
	}
	return session.request(method, params, out)
}

func (r *Runtime) startSSESession(ctx context.Context, definition toolres.ToolDefinition) (*sseSession, error) {
	if err := validateSSEDefinition(definition); err != nil {
		return nil, err
	}
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("mcp sse runtime client is not configured")
	}
	timeout := r.callTimeout(definition)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	req, err := http.NewRequestWithContext(runCtx, http.MethodGet, strings.TrimSpace(definition.MCP.URL), nil)
	if err != nil {
		cancel()
		return nil, err
	}
	headers, err := r.configuredMCPHeaders(runCtx, definition)
	if err != nil {
		cancel()
		return nil, err
	}
	applyMCPHeaders(req.Header, headers)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := r.client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		payload, _ := io.ReadAll(resp.Body)
		cancel()
		return nil, mcpHTTPStatusError{Operation: "mcp sse connect", StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(payload))}
	}
	session := &sseSession{
		ctx:     runCtx,
		cancel:  cancel,
		client:  r.client,
		baseURL: strings.TrimSpace(definition.MCP.URL),
		headers: headers,
		resp:    resp,
		reader:  bufio.NewReader(resp.Body),
		nextID:  1,
	}
	endpoint, err := session.readEndpoint()
	if err != nil {
		session.close()
		return nil, err
	}
	session.endpointURL, err = resolveSSEEndpointURL(session.baseURL, endpoint)
	if err != nil {
		session.close()
		return nil, err
	}
	return session, nil
}

func validateSSEDefinition(definition toolres.ToolDefinition) error {
	if definition.MCP == nil {
		return fmt.Errorf("mcp server definition is missing mcp config")
	}
	if effectiveTransport(definition) != toolres.TransportSSE {
		return fmt.Errorf("mcp transport %q is not supported by sse runtime", definition.MCP.Transport)
	}
	if strings.TrimSpace(definition.MCP.URL) == "" {
		return fmt.Errorf("mcp server %s has no sse url", definition.ID)
	}
	return nil
}

type sseSession struct {
	ctx         context.Context
	cancel      context.CancelFunc
	client      *http.Client
	baseURL     string
	endpointURL string
	headers     http.Header
	resp        *http.Response
	reader      *bufio.Reader
	nextID      int
}

func (s *sseSession) request(method string, params any, out any) error {
	id := s.nextID
	s.nextID++
	body := mcpJSONRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := s.post(body); err != nil {
		return err
	}
	for {
		response, ok, err := s.readMessage()
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if fmt.Sprint(response.ID) != fmt.Sprint(id) {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf("mcp json-rpc error %d: %s", response.Error.Code, response.Error.Message)
		}
		if out == nil || len(response.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(response.Result, out); err != nil {
			return fmt.Errorf("decode mcp json-rpc result: %w", err)
		}
		return nil
	}
}

func (s *sseSession) notify(method string, params any) error {
	return s.post(mcpJSONRPCRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (s *sseSession) post(body mcpJSONRPCRequest) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.endpointURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	applyMCPHeaders(req.Header, s.headers)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcpHTTPStatusError{Operation: "mcp sse post", StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(payload))}
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	return decodeJSONRPCPayload(payload, nil)
}

func (s *sseSession) readEndpoint() (string, error) {
	for {
		event, data, err := s.readEvent()
		if err != nil {
			return "", err
		}
		if event == "endpoint" && strings.TrimSpace(data) != "" {
			return strings.TrimSpace(data), nil
		}
	}
}

func (s *sseSession) readMessage() (mcpJSONRPCResponse, bool, error) {
	for {
		event, data, err := s.readEvent()
		if err != nil {
			return mcpJSONRPCResponse{}, false, err
		}
		if event != "" && event != "message" {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "" {
			continue
		}
		var response mcpJSONRPCResponse
		if err := json.Unmarshal([]byte(data), &response); err != nil {
			return mcpJSONRPCResponse{}, false, fmt.Errorf("decode mcp sse message: %w", err)
		}
		return response, true, nil
	}
}

func (s *sseSession) readEvent() (string, string, error) {
	var event string
	var data []string
	for {
		if err := s.ctx.Err(); err != nil {
			return "", "", err
		}
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return "", "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(data) > 0 || event != "" {
				return event, strings.Join(data, "\n"), nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if ok {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			event = value
		case "data":
			data = append(data, value)
		}
	}
}

func (s *sseSession) close() {
	if s == nil {
		return
	}
	if s.resp != nil && s.resp.Body != nil {
		_ = s.resp.Body.Close()
	}
	s.cancel()
}

func resolveSSEEndpointURL(baseURL string, endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("mcp sse endpoint event is empty")
	}
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint, nil
	}
	baseReq, err := http.NewRequest(http.MethodGet, baseURL, nil)
	if err != nil {
		return "", err
	}
	endpointReq, err := http.NewRequest(http.MethodPost, endpoint, nil)
	if err == nil && endpointReq.URL.IsAbs() {
		return endpointReq.URL.String(), nil
	}
	ref, err := baseReq.URL.Parse(endpoint)
	if err != nil {
		return "", err
	}
	return ref.String(), nil
}

func (r *Runtime) initialize(ctx context.Context, definition toolres.ToolDefinition) (string, error) {
	var result map[string]any
	return r.postJSONRPC(ctx, definition, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "orrery", "version": "0.1.0"},
	}, &result, "", true)
}

func (r *Runtime) postJSONRPC(ctx context.Context, definition toolres.ToolDefinition, method string, params any, out any, sessionID string, withID bool) (string, error) {
	if err := validateHTTPDefinition(definition); err != nil {
		return "", err
	}
	if r == nil || r.client == nil {
		return "", fmt.Errorf("mcp http runtime client is not configured")
	}
	if timeout := r.callTimeout(definition); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	body := mcpJSONRPCRequest{JSONRPC: "2.0", Method: method, Params: params}
	if withID {
		body.ID = "1"
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(definition.MCP.URL), bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	headers, err := r.configuredMCPHeaders(ctx, definition)
	if err != nil {
		return "", err
	}
	applyMCPHeaders(req.Header, headers)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	responseSessionID := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id"))
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return responseSessionID, err
	}
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return responseSessionID, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseSessionID, mcpHTTPStatusError{Operation: "mcp http " + method, StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(payload))}
	}
	if out == nil && len(bytes.TrimSpace(payload)) == 0 {
		return responseSessionID, nil
	}
	if err := decodeJSONRPCPayload(payload, out); err != nil {
		return responseSessionID, err
	}
	return responseSessionID, nil
}

func (r *Runtime) configuredMCPHeaders(ctx context.Context, definition toolres.ToolDefinition) (http.Header, error) {
	out := http.Header{}
	if definition.MCP == nil {
		return out, nil
	}
	for name, value := range definition.MCP.Headers {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			continue
		}
		if mcpHeaderNeedsSecret(value) {
			if r == nil || r.secretResolver == nil {
				return nil, fmt.Errorf("mcp header %s secret resolver is not configured", name)
			}
			resolved, err := resolveMCPHeaderSecretValue(ctx, value, r.secretResolver)
			if err != nil {
				return nil, fmt.Errorf("resolve mcp header %s secret: %w", name, err)
			}
			value = strings.TrimSpace(resolved)
			if value == "" {
				continue
			}
		}
		out.Set(name, value)
	}
	return out, nil
}

var mcpHeaderSecretTemplatePattern = regexp.MustCompile(`\$\{((?:secret|settings)://[^}\s]+)\}`)

func resolveMCPHeaderSecretValue(ctx context.Context, value string, secrets SecretResolver) (string, error) {
	value = strings.TrimSpace(value)
	if mcpHeaderIsSecretRef(value) {
		return secrets.ResolveSecret(ctx, value)
	}
	var resolveErr error
	resolved := mcpHeaderSecretTemplatePattern.ReplaceAllStringFunc(value, func(match string) string {
		if resolveErr != nil {
			return match
		}
		parts := mcpHeaderSecretTemplatePattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		secretValue, err := secrets.ResolveSecret(ctx, parts[1])
		if err != nil {
			resolveErr = err
			return match
		}
		return secretValue
	})
	if resolveErr != nil {
		return "", resolveErr
	}
	return resolved, nil
}

func mcpHeaderNeedsSecret(value string) bool {
	value = strings.TrimSpace(value)
	return mcpHeaderIsSecretRef(value) || mcpHeaderSecretTemplatePattern.MatchString(value)
}

func mcpHeaderIsSecretRef(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "secret://") || strings.HasPrefix(value, "settings://")
}

func applyMCPHeaders(header http.Header, configured http.Header) {
	for name, values := range configured {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				continue
			}
			header.Add(name, value)
		}
	}
}

func validateHTTPDefinition(definition toolres.ToolDefinition) error {
	if definition.MCP == nil {
		return fmt.Errorf("mcp server definition is missing mcp config")
	}
	switch effectiveTransport(definition) {
	case toolres.TransportStreamHTTP:
	default:
		return fmt.Errorf("mcp transport %q is not supported by the production runtime", definition.MCP.Transport)
	}
	if strings.TrimSpace(definition.MCP.URL) == "" {
		return fmt.Errorf("mcp server %s has no url", definition.ID)
	}
	return nil
}

func effectiveTransport(definition toolres.ToolDefinition) toolres.MCPTransport {
	if definition.MCP == nil {
		return ""
	}
	if definition.MCP.Transport != "" {
		return definition.MCP.Transport
	}
	if strings.TrimSpace(definition.MCP.URL) != "" {
		return toolres.TransportStreamHTTP
	}
	if strings.TrimSpace(definition.MCP.Command) != "" {
		return toolres.TransportStdio
	}
	return ""
}

func (r *Runtime) callStdioInitialize(ctx context.Context, definition toolres.ToolDefinition) error {
	session, err := r.startStdioSession(ctx, definition)
	if err != nil {
		return err
	}
	defer session.close()
	var result map[string]any
	return session.request("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "orrery", "version": "0.1.0"},
	}, &result)
}

func (r *Runtime) callStdioWithSession(ctx context.Context, definition toolres.ToolDefinition, method string, params any, out any) error {
	session, err := r.startStdioSession(ctx, definition)
	if err != nil {
		return err
	}
	defer session.close()
	var initialized map[string]any
	if err := session.request("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "orrery", "version": "0.1.0"},
	}, &initialized); err != nil {
		return err
	}
	if err := session.notify("notifications/initialized", nil); err != nil {
		return err
	}
	return session.request(method, params, out)
}

func (r *Runtime) startStdioSession(ctx context.Context, definition toolres.ToolDefinition) (*stdioSession, error) {
	if err := validateStdioDefinition(definition); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("mcp stdio runtime is not configured")
	}
	timeout := r.callTimeout(definition)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	command := strings.TrimSpace(definition.MCP.Command)
	if _, err := exec.LookPath(command); err != nil {
		cancel()
		return nil, fmt.Errorf("mcp stdio executable %q not found: %w", command, err)
	}
	cmd := exec.CommandContext(runCtx, command, definition.MCP.Args...)
	env, err := r.stdioEnv(ctx, definition)
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Env = process.BuildEnv(env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("mcp stdio stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("mcp stdio stdout pipe: %w", err)
	}
	stderr := process.NewStderrTail(nil, 4096)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("start mcp stdio server %q: %w", command, err)
	}
	return &stdioSession{
		ctx:      runCtx,
		cancel:   cancel,
		cmd:      cmd,
		stdin:    stdin,
		reader:   bufio.NewReader(stdout),
		stderr:   stderr,
		nextID:   1,
		serverID: string(definition.ID),
	}, nil
}

func (r *Runtime) callTimeout(definition toolres.ToolDefinition) time.Duration {
	if definition.MCP != nil && definition.MCP.CallTimeoutSeconds > 0 {
		return time.Duration(definition.MCP.CallTimeoutSeconds) * time.Second
	}
	if r != nil && r.timeout > 0 {
		return r.timeout
	}
	return 15 * time.Second
}

func validateStdioDefinition(definition toolres.ToolDefinition) error {
	if definition.MCP == nil {
		return fmt.Errorf("mcp server definition is missing mcp config")
	}
	if effectiveTransport(definition) != toolres.TransportStdio {
		return fmt.Errorf("mcp transport %q is not supported by stdio runtime", definition.MCP.Transport)
	}
	if strings.TrimSpace(definition.MCP.Command) == "" {
		return fmt.Errorf("mcp server %s has no stdio command", definition.ID)
	}
	return nil
}

func (r *Runtime) stdioEnv(ctx context.Context, definition toolres.ToolDefinition) (map[string]string, error) {
	if definition.MCP == nil || len(definition.MCP.EnvSecretRefs) == 0 {
		return nil, nil
	}
	if r.secretResolver == nil {
		return nil, fmt.Errorf("mcp stdio secret resolver is not configured")
	}
	env := make(map[string]string, len(definition.MCP.EnvSecretRefs))
	for _, ref := range definition.MCP.EnvSecretRefs {
		envName := strings.TrimSpace(ref.Env)
		secretName := strings.TrimSpace(ref.Name)
		if envName == "" || secretName == "" {
			continue
		}
		value, err := r.secretResolver.ResolveSecret(ctx, secretName)
		if err != nil {
			return nil, fmt.Errorf("resolve mcp stdio secret %s for env %s: %w", secretName, envName, err)
		}
		env[envName] = value
	}
	if len(env) == 0 {
		return nil, nil
	}
	return env, nil
}

type stdioSession struct {
	ctx      context.Context
	cancel   context.CancelFunc
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	reader   *bufio.Reader
	stderr   interface{ Tail() string }
	nextID   int
	serverID string
}

func (s *stdioSession) request(method string, params any, out any) error {
	id := s.nextID
	s.nextID++
	body := mcpJSONRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if _, err := s.stdin.Write(append(raw, '\n')); err != nil {
		return s.wrapError(fmt.Errorf("write mcp stdio %s request: %w", method, err))
	}
	for {
		if err := s.ctx.Err(); err != nil {
			return s.wrapError(err)
		}
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			return s.wrapError(fmt.Errorf("read mcp stdio %s response: %w", method, err))
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var response mcpJSONRPCResponse
		if err := json.Unmarshal(line, &response); err != nil {
			return s.wrapError(fmt.Errorf("decode mcp stdio response: %w", err))
		}
		if fmt.Sprint(response.ID) != fmt.Sprint(id) {
			continue
		}
		if response.Error != nil {
			return s.wrapError(fmt.Errorf("mcp json-rpc error %d: %s", response.Error.Code, response.Error.Message))
		}
		if out == nil || len(response.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(response.Result, out); err != nil {
			return s.wrapError(fmt.Errorf("decode mcp json-rpc result: %w", err))
		}
		return nil
	}
}

func (s *stdioSession) notify(method string, params any) error {
	body := mcpJSONRPCRequest{JSONRPC: "2.0", Method: method, Params: params}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if _, err := s.stdin.Write(append(raw, '\n')); err != nil {
		return s.wrapError(fmt.Errorf("write mcp stdio %s notification: %w", method, err))
	}
	return nil
}

func (s *stdioSession) close() {
	if s == nil {
		return
	}
	_ = s.stdin.Close()
	s.cancel()
	_ = s.cmd.Wait()
}

func (s *stdioSession) wrapError(err error) error {
	if err == nil {
		return nil
	}
	tail := ""
	if s.stderr != nil {
		tail = strings.TrimSpace(s.stderr.Tail())
	}
	if tail == "" {
		return err
	}
	return fmt.Errorf("%w\n[mcp stdio stderr]\n%s", err, tail)
}

func decodeJSONRPCPayload(payload []byte, out any) error {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return nil
	}
	if bytes.HasPrefix(payload, []byte(":")) || bytes.HasPrefix(payload, []byte("event:")) || bytes.HasPrefix(payload, []byte("data:")) {
		payload = extractSSEData(payload)
	}
	var response mcpJSONRPCResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return fmt.Errorf("decode mcp json-rpc response: %w", err)
	}
	if response.Error != nil {
		return fmt.Errorf("mcp json-rpc error %d: %s", response.Error.Code, response.Error.Message)
	}
	if out == nil || len(response.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(response.Result, out); err != nil {
		return fmt.Errorf("decode mcp json-rpc result: %w", err)
	}
	return nil
}

func extractSSEData(payload []byte) []byte {
	lines := strings.Split(string(payload), "\n")
	var data []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" && len(data) > 0 {
			break
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if value != "" && value != "[DONE]" {
				data = append(data, value)
			}
		}
	}
	if len(data) == 0 {
		return bytes.TrimSpace(payload)
	}
	return []byte(strings.Join(data, "\n"))
}

type mcpJSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type mcpJSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpToolsListResult struct {
	Tools []mcpToolDefinition `json:"tools"`
}

type mcpToolDefinition struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema"`
}

type mcpToolsCallResult struct {
	Content           []mcpContent   `json:"content"`
	StructuredContent map[string]any `json:"structuredContent"`
	IsError           bool           `json:"isError"`
}

type mcpContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

func cloneSchema(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

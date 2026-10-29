package httptransport

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type mcpJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpJSONRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *mcpJSONRPCError `json:"error,omitempty"`
}

type mcpJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpToolListParams struct {
	Mode      string   `json:"mode"`
	Query     string   `json:"query"`
	TopK      int      `json:"top_k"`
	AgentID   string   `json:"agent_id"`
	ProfileID string   `json:"profile_id"`
	ToolIDs   []string `json:"tool_ids"`
}

type mcpToolCallParams struct {
	Name          string                          `json:"name"`
	Arguments     map[string]any                  `json:"arguments"`
	PrincipalID   string                          `json:"principal_id"`
	Permissions   []string                        `json:"permissions"`
	AgentID       string                          `json:"agent_id"`
	ProfileID     string                          `json:"profile_id"`
	SkillBindings []agentcore.RuntimeSkillBinding `json:"skill_bindings"`
}

func (r *Router) handleMCP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "mcp endpoint requires POST", CorrelationID: correlationID(req)})
		return
	}
	if r == nil || r.mcpGateway == nil {
		writeMCPJSONRPC(w, mcpJSONRPCResponse{JSONRPC: "2.0", Error: &mcpJSONRPCError{Code: -32000, Message: "mcp gateway is not configured"}})
		return
	}
	var rpc mcpJSONRPCRequest
	if err := json.NewDecoder(req.Body).Decode(&rpc); err != nil {
		writeMCPJSONRPC(w, mcpJSONRPCResponse{JSONRPC: "2.0", Error: &mcpJSONRPCError{Code: -32700, Message: "parse error"}})
		return
	}
	if strings.TrimSpace(rpc.JSONRPC) == "" {
		rpc.JSONRPC = "2.0"
	}
	switch strings.TrimSpace(rpc.Method) {
	case "initialize":
		writeMCPJSONRPC(w, mcpJSONRPCResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
			"protocolVersion": "2025-06-18",
			"serverInfo":      map[string]any{"name": "orrery", "version": "0.1.0"},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		}})
	case "notifications/initialized":
		// Streamable HTTP notifications have no JSON-RPC response. Returning an
		// object without an id makes strict clients fail initialization.
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		r.handleMCPToolsList(w, req, rpc)
	case "tools/call":
		r.handleMCPToolsCall(w, req, rpc)
	default:
		writeMCPJSONRPC(w, mcpJSONRPCResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &mcpJSONRPCError{Code: -32601, Message: "method not found"}})
	}
}

func (r *Router) handleMCPToolsList(w http.ResponseWriter, req *http.Request, rpc mcpJSONRPCRequest) {
	var params mcpToolListParams
	if len(rpc.Params) > 0 {
		if err := json.Unmarshal(rpc.Params, &params); err != nil {
			writeMCPJSONRPC(w, mcpJSONRPCResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &mcpJSONRPCError{Code: -32602, Message: "invalid params"}})
			return
		}
	}
	responsibility, err := mcpResponsibilityContextFromHeaders(req.Header)
	if err != nil {
		writeMCPJSONRPC(w, mcpJSONRPCResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &mcpJSONRPCError{Code: -32602, Message: "invalid responsibility execution context"}})
		return
	}
	result, err := r.mcpGateway.ListToolSet(req.Context(), mcptransport.ListToolsRequest{
		Mode:                         params.Mode,
		Query:                        params.Query,
		TopK:                         params.TopK,
		AgentID:                      mcpBoundIdentity(req.Header, "X-Agent-ID", params.AgentID),
		ProfileID:                    mcpBoundIdentity(req.Header, "X-Agent-Profile-ID", params.ProfileID),
		ToolIDs:                      params.ToolIDs,
		RunID:                        strings.TrimSpace(req.Header.Get(agentcore.RuntimeRunIDHeader)),
		ExecutionEnvelopeIDClaim:     strings.TrimSpace(req.Header.Get(agentcore.RuntimeExecutionEnvelopeIDHeader)),
		ExecutionEnvelopeDigestClaim: strings.TrimSpace(req.Header.Get(agentcore.RuntimeExecutionEnvelopeDigestHeader)),
		ResponsibilityContext:        responsibility,
	})
	if err != nil {
		writeMCPJSONRPC(w, mcpJSONRPCResponse{JSONRPC: "2.0", ID: rpc.ID, Error: mcpRPCError(err)})
		return
	}
	writeMCPJSONRPC(w, mcpJSONRPCResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
		"tools":           mcpToolsFromDescriptors(result.Tools),
		"total_available": result.TotalAvailable,
		"filtered":        result.Filtered,
		"truncated":       result.Truncated,
		"top_k":           result.TopK,
		"notice":          result.Notice,
	}})
}

func (r *Router) handleMCPToolsCall(w http.ResponseWriter, req *http.Request, rpc mcpJSONRPCRequest) {
	var params mcpToolCallParams
	if len(rpc.Params) > 0 {
		if err := json.Unmarshal(rpc.Params, &params); err != nil {
			writeMCPJSONRPC(w, mcpJSONRPCResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &mcpJSONRPCError{Code: -32602, Message: "invalid params"}})
			return
		}
	}
	responsibility, err := mcpResponsibilityContextFromHeaders(req.Header)
	if err != nil {
		writeMCPJSONRPC(w, mcpJSONRPCResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &mcpJSONRPCError{Code: -32602, Message: "invalid responsibility execution context"}})
		return
	}
	permissions, permissionsAuthoritative := mcpRuntimePermissions(req.Header, params.Permissions)
	result, err := r.mcpGateway.CallTool(req.Context(), mcptransport.CallToolRequest{
		TenantID:                     firstNonEmpty(req.Header.Get("X-Orrery-Tenant"), req.Header.Get("X-Tenant-ID"), req.Header.Get("X-Tenant")),
		RunID:                        strings.TrimSpace(req.Header.Get(agentcore.RuntimeRunIDHeader)),
		ToolID:                       params.Name,
		PrincipalID:                  mcpRuntimePrincipalID(req.Header, params.PrincipalID),
		PrincipalKind:                mcpRuntimePrincipalKind(req.Header),
		Permissions:                  permissions,
		PermissionsAuthoritative:     permissionsAuthoritative,
		AgentID:                      mcpBoundIdentity(req.Header, "X-Agent-ID", params.AgentID),
		ProfileID:                    mcpBoundIdentity(req.Header, "X-Agent-Profile-ID", params.ProfileID),
		ExecutionEnvelopeIDClaim:     strings.TrimSpace(req.Header.Get(agentcore.RuntimeExecutionEnvelopeIDHeader)),
		ExecutionEnvelopeDigestClaim: strings.TrimSpace(req.Header.Get(agentcore.RuntimeExecutionEnvelopeDigestHeader)),
		SkillBindings:                append([]agentcore.RuntimeSkillBinding(nil), params.SkillBindings...),
		ResponsibilityContext:        responsibility,
		Arguments:                    params.Arguments,
	})
	if err != nil {
		writeMCPJSONRPC(w, mcpJSONRPCResponse{JSONRPC: "2.0", ID: rpc.ID, Error: mcpRPCError(err)})
		return
	}
	text := mustMarshalString(result.Output)
	if isEnterpriseGraphMCPTool(params.Name) {
		text = enterpriseGraphMCPText(params.Name, result.Output)
	}
	writeMCPJSONRPC(w, mcpJSONRPCResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
		"content":           []map[string]any{{"type": "text", "text": text}},
		"structuredContent": result.Output,
	}})
}

func isEnterpriseGraphMCPTool(toolID string) bool {
	switch strings.TrimSpace(toolID) {
	case string(toolstd.EnterpriseGraphBuildContextBundle), string(toolstd.EnterpriseGraphSearchObjects):
		return true
	default:
		return false
	}
}

func enterpriseGraphMCPText(toolID string, output map[string]any) string {
	if strings.TrimSpace(toolID) == string(toolstd.EnterpriseGraphSearchObjects) {
		return fmt.Sprintf("enterprise graph search: results=%d next_cursor=%t", anySliceLength(output["items"]), strings.TrimSpace(anyString(output["next_cursor"])) != "")
	}
	schemaHash := ""
	if schema, ok := output["schema"].(map[string]any); ok {
		schemaHash = anyString(schema["version_hash"])
	}
	return fmt.Sprintf(
		"enterprise graph bundle: id=%s schema=%s objects=%d links=%d facts=%d evidence=%d partial=%t",
		anyString(output["bundle_id"]), schemaHash,
		anySliceLength(output["objects"]), anySliceLength(output["links"]), anySliceLength(output["facts"]), anySliceLength(output["evidence"]),
		output["partial"] == true,
	)
}

func anySliceLength(value any) int {
	switch items := value.(type) {
	case []any:
		return len(items)
	case []map[string]any:
		return len(items)
	default:
		return 0
	}
}

func anyString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func mcpRuntimePrincipalID(headers http.Header, requested string) string {
	return firstNonEmpty(
		headers.Get(agentcore.RuntimePrincipalIDHeader),
		headers.Get("X-Agent-ID"),
		requested,
	)
}

func mcpRuntimePrincipalKind(headers http.Header) string {
	kind := strings.ToLower(strings.TrimSpace(headers.Get(agentcore.RuntimePrincipalKindHeader)))
	switch agentcore.ExecutionPrincipalKind(kind) {
	case agentcore.ExecutionPrincipalUser, agentcore.ExecutionPrincipalAgent, agentcore.ExecutionPrincipalSystem:
		return kind
	default:
		return ""
	}
}

func mcpBoundIdentity(headers http.Header, headerName, requested string) string {
	return firstNonEmpty(headers.Get(headerName), requested)
}

func mcpRuntimePermissions(headers http.Header, requested []string) ([]string, bool) {
	if strings.EqualFold(strings.TrimSpace(headers.Get(agentcore.RuntimePrincipalKindHeader)), string(agentcore.ExecutionPrincipalUser)) {
		return splitHeader(headers.Get(agentcore.RuntimePermissionScopeHeader)), true
	}
	return append([]string(nil), requested...), false
}

func mcpResponsibilityContextFromHeaders(headers http.Header) (*agentcore.ResponsibilityContext, error) {
	contextID := strings.TrimSpace(headers.Get(agentcore.RuntimeResponsibilityContextIDHeader))
	responsibilityID := strings.TrimSpace(headers.Get(agentcore.RuntimeResponsibilityIDHeader))
	policyID := strings.TrimSpace(headers.Get(agentcore.RuntimeResponsibilityPolicyIDHeader))
	responsibilityVersionText := strings.TrimSpace(headers.Get(agentcore.RuntimeResponsibilityVersionHeader))
	policyVersionText := strings.TrimSpace(headers.Get(agentcore.RuntimeResponsibilityPolicyVersionHeader))
	if contextID == "" && responsibilityID == "" && policyID == "" && responsibilityVersionText == "" && policyVersionText == "" {
		if strings.TrimSpace(headers.Get(agentcore.RuntimeResponsibilityRequireExplicitToolScopesHeader)) != "" {
			return nil, errors.New("explicit tool scope requirement requires responsibility authority")
		}
		return nil, nil
	}
	responsibilityVersion, err := strconv.ParseInt(responsibilityVersionText, 10, 64)
	if err != nil || responsibilityVersion < 1 {
		return nil, errors.New("invalid responsibility version")
	}
	policyVersion, err := strconv.ParseInt(policyVersionText, 10, 64)
	if err != nil || policyVersion < 1 || contextID == "" || responsibilityID == "" || policyID == "" {
		return nil, errors.New("invalid responsibility policy version")
	}
	agentProfileID := strings.TrimSpace(headers.Get(agentcore.RuntimeResponsibilityAgentProfileHeader))
	if agentProfileID == "" {
		return nil, errors.New("invalid responsibility agent profile")
	}
	requireExplicitToolScopes, err := mcpResponsibilityBooleanHeader(headers.Get(agentcore.RuntimeResponsibilityRequireExplicitToolScopesHeader))
	if err != nil {
		return nil, errors.New("invalid responsibility explicit tool scope requirement")
	}
	return &agentcore.ResponsibilityContext{
		ContextID:                 contextID,
		ResponsibilityID:          responsibilityID,
		ResponsibilityVersion:     responsibilityVersion,
		PolicyID:                  policyID,
		PolicyVersion:             policyVersion,
		Source:                    "runtime.mcp_headers",
		AgentProfileID:            agentProfileID,
		AllowedTools:              splitMCPResponsibilityHeader(headers.Get(agentcore.RuntimeResponsibilityAllowedToolsHeader)),
		AllowedDataScopes:         splitMCPResponsibilityHeader(headers.Get(agentcore.RuntimeResponsibilityAllowedDataHeader)),
		PolicyToolScopes:          splitMCPResponsibilityHeader(headers.Get(agentcore.RuntimeResponsibilityPolicyToolScopesHeader)),
		PolicyDataScopes:          splitMCPResponsibilityHeader(headers.Get(agentcore.RuntimeResponsibilityPolicyDataScopesHeader)),
		TaskPermissionScopes:      splitMCPResponsibilityHeader(headers.Get(agentcore.RuntimeResponsibilityTaskScopesHeader)),
		RequireExplicitToolScopes: requireExplicitToolScopes,
	}, nil
}

func mcpResponsibilityBooleanHeader(value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}
	return strconv.ParseBool(value)
}

func splitMCPResponsibilityHeader(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func mcpToolsFromDescriptors(tools []mcptransport.ToolDescriptor) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		item := map[string]any{
			"name":        tool.ToolID,
			"title":       firstNonEmpty(tool.Name, tool.ToolID),
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
			"annotations": map[string]any{
				"source":               tool.Source,
				"server_tool_id":       tool.ServerToolID,
				"downstream_tool_name": tool.DownstreamToolName,
				"risk_level":           tool.RiskLevel,
				"required_permissions": tool.RequiredPermissions,
			},
		}
		if item["inputSchema"] == nil {
			item["inputSchema"] = map[string]any{"type": "object", "additionalProperties": true}
		}
		out = append(out, item)
	}
	return out
}

func mcpRPCError(err error) *mcpJSONRPCError {
	switch {
	case errors.Is(err, mcptransport.ErrInvalidRequest):
		return &mcpJSONRPCError{Code: -32602, Message: err.Error()}
	case errors.Is(err, mcptransport.ErrToolNotFound):
		return &mcpJSONRPCError{Code: -32602, Message: "tool not found"}
	case errors.Is(err, mcptransport.ErrPermissionDenied):
		return &mcpJSONRPCError{Code: -32001, Message: "permission denied"}
	default:
		return &mcpJSONRPCError{Code: -32000, Message: err.Error()}
	}
}

func writeMCPJSONRPC(w http.ResponseWriter, response mcpJSONRPCResponse) {
	if len(response.ID) == 0 {
		response.ID = json.RawMessage("null")
	}
	writeJSON(w, http.StatusOK, response)
}

func mustMarshalString(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

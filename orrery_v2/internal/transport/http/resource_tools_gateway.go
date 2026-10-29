package httptransport

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/app/userauth"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func (r *Router) handleGatewayTools(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if strings.TrimSpace(req.URL.Query().Get("agent_id")) != "" {
		status, body := invalidRequest(cid, "agent_id", "agent_id is not a resource list filter; use bound_agent_id for agent bindings")
		writeJSON(w, status, body)
		return
	}
	if !viewer.Can(resourceadmin.PermissionResourceRead) {
		status, body := MapError(resourceadmin.ErrPermissionDenied, cid)
		writeJSON(w, status, body)
		return
	}
	if r == nil || r.mcpGateway == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	limit, offset := listPage(req)
	enabled, enabledSet := parseOptionalBool(req.URL.Query().Get("enabled"))
	profileID := gatewayExecutionProfileID(req)
	result, err := r.mcpGateway.ListToolSet(req.Context(), mcptransport.ListToolsRequest{
		Mode:      "all",
		AgentID:   gatewayExecutionAgentID(req, viewer, profileID),
		ProfileID: profileID,
	})
	if err != nil {
		if errors.Is(err, mcptransport.ErrToolNotFound) {
			writeJSON(w, http.StatusOK, emptyGatewayToolView(limit, offset))
			return
		}
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	all := make([]resourceadmin.ToolDTO, 0, len(result.Tools))
	for _, descriptor := range result.Tools {
		tool := mapGatewayToolDescriptor(descriptor)
		if !matchesGatewayToolFilters(tool, req, enabled, enabledSet) {
			continue
		}
		all = append(all, tool)
	}
	pageOffset, pageEnd := pageWindow(len(all), offset, limit)
	writeJSON(w, http.StatusOK, resourceadmin.ResourceAdminViewDTO{
		Tools: all[pageOffset:pageEnd],
		Page: resourceadmin.PageDTO{
			Limit:    limit,
			Offset:   offset,
			Total:    len(all),
			Returned: pageEnd - pageOffset,
			HasMore:  pageEnd < len(all),
		},
	})
}

func emptyGatewayToolView(limit int, offset int) resourceadmin.ResourceAdminViewDTO {
	return resourceadmin.ResourceAdminViewDTO{
		Tools: []resourceadmin.ToolDTO{},
		Page: resourceadmin.PageDTO{
			Limit:    limit,
			Offset:   offset,
			Total:    0,
			Returned: 0,
			HasMore:  false,
		},
	}
}

func mapGatewayToolDescriptor(tool mcptransport.ToolDescriptor) resourceadmin.ToolDTO {
	kind := "mcp_tool"
	manageable := false
	tags := []string{"mcp", "external"}
	if tool.Source == mcptransport.ToolSourceInternal {
		kind = "internal_service_tool"
		manageable = true
		tags = []string{"system", "internal"}
	}
	dto := resourceadmin.ToolDTO{
		ToolID:             tool.ToolID,
		Kind:               kind,
		Name:               firstNonEmpty(tool.Name, tool.ToolID),
		Description:        tool.Description,
		Version:            "runtime",
		InputSchema:        cloneAnyMap(tool.InputSchema),
		OutputSchema:       cloneAnyMap(tool.OutputSchema),
		Enabled:            tool.Enabled,
		RiskLevel:          firstNonEmpty(tool.RiskLevel, "medium"),
		Tags:               tags,
		Source:             string(tool.Source),
		ServerToolID:       tool.ServerToolID,
		DownstreamToolName: tool.DownstreamToolName,
		Manageable:         manageable,
		CreatedAt:          time.Time{},
		UpdatedAt:          time.Time{},
	}
	for _, scope := range tool.RequiredPermissions {
		if strings.TrimSpace(scope) == "" {
			continue
		}
		dto.Permissions = append(dto.Permissions, resourceadmin.PermissionDTO{
			Scope:    scope,
			Risk:     dto.RiskLevel,
			Required: true,
		})
	}
	return dto
}

type manualToolCallRequest struct {
	ToolID    string         `json:"tool_id"`
	Arguments map[string]any `json:"arguments"`
}

type gatewayToolCallResponse struct {
	Output                map[string]any                      `json:"output,omitempty"`
	MaskedFields          []string                            `json:"masked_fields,omitempty"`
	Hidden                bool                                `json:"hidden,omitempty"`
	PermissionConstraints []contextgraph.PermissionConstraint `json:"permission_constraints,omitempty"`
}

// gatewayToolReviewResponse preserves the existing permission decision fields
// while giving callers the durable HIL request that now owns the review.
type gatewayToolReviewResponse struct {
	operations.PermissionDecisionDTO
	HILDecision hil.DecisionRequest `json:"hil_decision"`
	Council     *hil.CouncilSession `json:"council,omitempty"`
}

func (r *Router) handleCallGatewayTool(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if !viewerHasScope(viewer.PermissionScope, operations.PermissionToolCall, operations.PermissionToolCall) {
		status, body := MapError(resourceadmin.ErrPermissionDenied, cid)
		writeJSON(w, status, body)
		return
	}
	if r == nil || r.mcpGateway == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	var body manualToolCallRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	if strings.TrimSpace(body.ToolID) == "" {
		status, response := invalidRequest(cid, "tool_id", "tool_id is required")
		writeJSON(w, status, response)
		return
	}
	profileID := gatewayExecutionProfileID(req)
	if !r.allowGatewayToolAction(w, req, cid, viewer, profileID, body) {
		return
	}
	result, err := r.mcpGateway.CallTool(req.Context(), mcptransport.CallToolRequest{
		TenantID:    strings.TrimSpace(firstNonEmpty(req.Header.Get("X-Tenant-ID"), req.Header.Get("X-Tenant"))),
		ToolID:      body.ToolID,
		PrincipalID: viewer.ViewerID,
		AgentID:     gatewayExecutionAgentID(req, viewer, profileID),
		Permissions: append([]string(nil), viewer.PermissionScope...),
		ProfileID:   profileID,
		Arguments:   cloneAnyMap(body.Arguments),
	})
	if err != nil {
		status, response := mapMCPGatewayHTTPError(err, cid)
		writeJSON(w, status, response)
		return
	}
	response, err := r.filterGatewayToolResult(req, viewer, body.ToolID, result)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (r *Router) filterGatewayToolResult(req *http.Request, viewer resourceadmin.ViewerRef, toolID string, result mcptransport.CallToolResult) (gatewayToolCallResponse, error) {
	response := gatewayToolCallResponse{Output: result.Output}
	if r == nil || r.toolResultPolicy == nil || len(result.Output) == 0 {
		return response, nil
	}
	objectID := strings.TrimSpace(toolID)
	output := cloneAnyMap(result.Output)
	decision, err := r.toolResultPolicy.DecideContext(req.Context(), contextgraph.BuildContextBundleRequest{
		TenantID: strings.TrimSpace(firstNonEmpty(req.Header.Get("X-Tenant-ID"), req.Header.Get("X-Tenant"), "system")),
		Consumer: "mcp_gateway",
		Purpose:  "tool_result",
		ActorRef: contextgraph.Ref{
			Kind: "human",
			ID:   strings.TrimSpace(viewer.ViewerID),
		},
		SubjectRefs: []contextgraph.Ref{{Kind: "tool_result", ID: objectID}},
	}, []contextgraph.Object{{
		ID:         objectID,
		TenantID:   strings.TrimSpace(firstNonEmpty(req.Header.Get("X-Tenant-ID"), req.Header.Get("X-Tenant"), "system")),
		ObjectType: "tool_result",
		Properties: output,
	}})
	if err != nil {
		return gatewayToolCallResponse{}, err
	}
	if containsExactString(decision.HiddenObjectIDs, objectID) {
		response.Output = nil
		response.Hidden = true
		response.PermissionConstraints = append([]contextgraph.PermissionConstraint(nil), decision.Constraints...)
		return response, nil
	}
	for _, field := range decision.MaskedFields[objectID] {
		field = strings.TrimSpace(field)
		if field != "" {
			deleteMaskedOutputField(output, field)
			response.MaskedFields = append(response.MaskedFields, field)
		}
	}
	response.Output = output
	response.PermissionConstraints = append([]contextgraph.PermissionConstraint(nil), decision.Constraints...)
	return response, nil
}

func deleteMaskedOutputField(output map[string]any, fieldPath string) {
	if output == nil {
		return
	}
	if fieldPath == "" {
		return
	}
	if !strings.Contains(fieldPath, ".") && !strings.HasPrefix(fieldPath, "/") {
		delete(output, fieldPath)
		return
	}
	segments := maskedOutputPathSegments(fieldPath)
	if len(segments) == 0 {
		return
	}
	deleteOutputPath(output, segments)
}

func maskedOutputPathSegments(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if strings.HasPrefix(path, "/") {
		raw := strings.Split(strings.TrimPrefix(path, "/"), "/")
		out := make([]string, 0, len(raw))
		for _, segment := range raw {
			out = append(out, strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~"))
		}
		return out
	}
	raw := strings.Split(path, ".")
	out := make([]string, 0, len(raw))
	for _, segment := range raw {
		segment = strings.TrimSpace(segment)
		if segment != "" {
			out = append(out, segment)
		}
	}
	return out
}

func deleteOutputPath(current any, segments []string) {
	if len(segments) == 0 {
		return
	}
	switch node := current.(type) {
	case map[string]any:
		if len(segments) == 1 {
			delete(node, segments[0])
			return
		}
		next, ok := node[segments[0]]
		if !ok {
			return
		}
		deleteOutputPath(next, segments[1:])
	case []any:
		index, err := strconv.Atoi(segments[0])
		if err != nil || index < 0 || index >= len(node) {
			return
		}
		if len(segments) == 1 {
			node[index] = nil
			return
		}
		deleteOutputPath(node[index], segments[1:])
	}
}

func (r *Router) allowGatewayToolAction(w http.ResponseWriter, req *http.Request, cid string, viewer resourceadmin.ViewerRef, profileID string, body manualToolCallRequest) bool {
	if r == nil || r.operations == nil {
		// Tool calls are security-sensitive. A partial server composition must
		// never turn an unavailable permission policy into implicit approval.
		status, response := MapError(errServiceDependency(), cid)
		writeJSON(w, status, response)
		return false
	}
	transportViewer, err := ViewerFromRequest(req)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return false
	}
	if r.mcpGateway == nil {
		status, response := MapError(errServiceDependency(), cid)
		writeJSON(w, status, response)
		return false
	}
	riskLevel, riskErr := r.mcpGateway.ToolRiskLevel(req.Context(), body.ToolID)
	if riskErr != nil {
		// Permission routing must be based on the server-owned descriptor. A
		// missing or unavailable registry cannot safely be treated as medium
		// risk because that can lower an otherwise high-risk action's review.
		status, response := mapMCPGatewayHTTPError(riskErr, cid)
		writeJSON(w, status, response)
		return false
	}
	permissionRequest := gatewayToolPermissionDecisionRequest(viewer, profileID, body, r.environment, riskLevel)
	decision, err := r.operations.DecidePermission(req.Context(), operations.ViewerRef{
		ViewerID:        transportViewer.ViewerID,
		ViewerKind:      string(transportViewer.ViewerKind),
		DisplayName:     displayNameFromRequest(req),
		PermissionScope: transportViewer.PermissionScope,
		RequestID:       cid,
		IdempotencyKey:  idempotencyKey(req),
	}, permissionRequest)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return false
	}
	switch decision.Decision {
	case operations.PermissionDecisionAllow:
		return true
	case operations.PermissionDecisionNeedsHIL, operations.PermissionDecisionNeedsCouncil, operations.PermissionDecisionNeedsApproval:
		writeJSON(w, http.StatusForbidden, ErrorResponse{
			Code:          ErrorForbidden,
			Message:       firstNonEmpty(decision.Reason, "automatic tool authorization denied"),
			CorrelationID: cid,
		})
		return false
	default:
		writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: firstNonEmpty(decision.Reason, "permission denied"), CorrelationID: cid})
		return false
	}
}

func gatewayToolPermissionDecisionRequest(viewer resourceadmin.ViewerRef, profileID string, body manualToolCallRequest, environment string, riskLevel string) operations.PermissionDecisionRequestDTO {
	actorID := firstNonEmpty(strings.TrimSpace(profileID), strings.TrimSpace(viewer.ViewerID))
	if actorID == "" {
		actorID = strings.TrimSpace(viewer.ViewerID)
	}
	return operations.PermissionDecisionRequestDTO{
		Subject: operations.ActorRefDTO{
			ActorType: "agent",
			ActorID:   actorID,
		},
		OnBehalfOf: operations.ActorRefDTO{
			ActorType: "human",
			ActorID:   strings.TrimSpace(viewer.ViewerID),
		},
		ActionTypeID: "mcp.tool.call",
		ResourceType: "tool",
		ResourceID:   strings.TrimSpace(body.ToolID),
		RiskLevel:    strings.ToLower(strings.TrimSpace(riskLevel)),
		Environment:  strings.TrimSpace(environment),
		RuntimeRef: operations.PermissionRuntimeRefDTO{
			RuntimeKind: "mcp_gateway",
			RuntimeID:   "http",
		},
		ObjectRefs: []operations.PermissionObjectRefDTO{{
			ResourceType: "tool",
			ResourceID:   strings.TrimSpace(body.ToolID),
		}},
		ArgsHash: gatewayToolArgsHash(body.Arguments),
		Metadata: map[string]string{
			"resource_binding": "allow",
		},
	}
}

func gatewayToolHILDecisionInput(viewer missioncontrol.ViewerRef, request operations.PermissionDecisionRequestDTO, decision operations.PermissionDecisionDTO, key string) (hil.CreateDecisionInput, bool) {
	mode := hil.ModeHIL
	input := hil.CreateDecisionInput{
		TenantID:       viewer.Tenant(),
		Requester:      hilActorFromViewer(viewer),
		Action:         firstNonEmpty(strings.TrimSpace(request.ActionTypeID), strings.TrimSpace(request.Action)),
		ActionTypeID:   strings.TrimSpace(request.ActionTypeID),
		Resource:       hil.ObjectRef{Kind: request.ResourceType, ID: request.ResourceID},
		ArgsHash:       strings.TrimSpace(request.ArgsHash),
		Purpose:        "manual gateway tool call requires human review",
		RiskLevel:      gatewayToolHILRiskLevel(request.RiskLevel),
		PreferredMode:  mode,
		SubjectRefs:    gatewayToolHILObjectRefs(request),
		PayloadPreview: gatewayToolHILPayloadPreview(request, decision),
		IdempotencyKey: strings.TrimSpace(key),
	}
	input.ObjectRefs = append([]hil.ObjectRef(nil), input.SubjectRefs...)
	switch decision.Decision {
	case operations.PermissionDecisionNeedsHIL:
		input.HILRequired = true
	case operations.PermissionDecisionNeedsCouncil:
		input.CouncilRequired = true
		input.PreferredMode = hil.ModeCouncil
	default:
		return hil.CreateDecisionInput{}, false
	}
	return input, true
}

func gatewayToolHILObjectRefs(request operations.PermissionDecisionRequestDTO) []hil.ObjectRef {
	refs := make([]hil.ObjectRef, 0, len(request.ObjectRefs)+1)
	for _, ref := range request.ObjectRefs {
		kind, id := strings.TrimSpace(ref.ResourceType), strings.TrimSpace(ref.ResourceID)
		if kind != "" && id != "" {
			refs = append(refs, hil.ObjectRef{Kind: kind, ID: id})
		}
	}
	if len(refs) == 0 && strings.TrimSpace(request.ResourceType) != "" && strings.TrimSpace(request.ResourceID) != "" {
		refs = append(refs, hil.ObjectRef{Kind: request.ResourceType, ID: request.ResourceID})
	}
	return refs
}

func gatewayToolHILPayloadPreview(request operations.PermissionDecisionRequestDTO, decision operations.PermissionDecisionDTO) map[string]any {
	return map[string]any{
		"permission_decision": string(decision.Decision),
		"tool_id":             strings.TrimSpace(request.ResourceID),
		"args_hash":           strings.TrimSpace(request.ArgsHash),
	}
}

func gatewayToolHILRiskLevel(value string) hil.RiskLevel {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(hil.RiskLow):
		return hil.RiskLow
	case string(hil.RiskMedium):
		return hil.RiskMedium
	case string(hil.RiskHigh):
		return hil.RiskHigh
	case string(hil.RiskCritical):
		return hil.RiskCritical
	default:
		return hil.RiskMedium
	}
}

func gatewayToolArgsHash(args map[string]any) string {
	payload, err := json.Marshal(args)
	if err != nil {
		payload = []byte(fmt.Sprintf("%v", args))
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum)
}

func gatewayExecutionProfileID(req *http.Request) string {
	if req == nil {
		return ""
	}
	if isBreakglassGatewayRequest(req) {
		return ""
	}
	return strings.TrimSpace(req.Header.Get("X-Agent-Profile-ID"))
}

func gatewayExecutionAgentID(req *http.Request, viewer resourceadmin.ViewerRef, profileID string) string {
	if strings.TrimSpace(profileID) != "" || isBreakglassGatewayRequest(req) {
		return ""
	}
	return strings.TrimSpace(viewer.ViewerID)
}

func isBreakglassGatewayRequest(req *http.Request) bool {
	if req == nil {
		return false
	}
	principal, ok := identity.PrincipalFromContext(req.Context())
	if !ok {
		return false
	}
	principal = identity.NormalizePrincipal(principal)
	return strings.EqualFold(principal.AgentProfileID, agentcore.SystemSuperadminPrincipalID) &&
		strings.EqualFold(principal.Username, userauth.SuperadminUsername)
}

func mapMCPGatewayHTTPError(err error, cid string) (int, ErrorResponse) {
	switch {
	case errors.Is(err, mcptransport.ErrPermissionDenied):
		return http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: cid}
	case errors.Is(err, mcptransport.ErrToolNotFound):
		return http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid}
	case errors.Is(err, mcptransport.ErrInvalidRequest):
		return http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: err.Error(), CorrelationID: cid}
	case errors.Is(err, mcptransport.ErrServiceDependency):
		return http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "service dependency unavailable", CorrelationID: cid, Retryable: true}
	default:
		return MapError(err, cid)
	}
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func matchesGatewayToolFilters(tool resourceadmin.ToolDTO, req *http.Request, enabled bool, enabledSet bool) bool {
	if enabledSet && tool.Enabled != enabled {
		return false
	}
	if !matchesGatewayToolText(req.URL.Query().Get("search"), tool) {
		return false
	}
	if !matchesGatewayToolText(req.URL.Query().Get("category"), tool) {
		return false
	}
	if risk := strings.TrimSpace(req.URL.Query().Get("risk_level")); risk != "" && !strings.EqualFold(risk, tool.RiskLevel) {
		return false
	}
	if id := strings.TrimSpace(req.URL.Query().Get("resource_id")); id != "" && !strings.EqualFold(id, tool.ToolID) && !strings.EqualFold(id, tool.ServerToolID) {
		return false
	}
	if required := strings.TrimSpace(req.URL.Query().Get("required_tool_id")); required != "" && !strings.EqualFold(required, tool.ToolID) && !strings.EqualFold(required, tool.ServerToolID) {
		return false
	}
	return true
}

func matchesGatewayToolText(query string, tool resourceadmin.ToolDTO) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	values := []string{
		tool.ToolID,
		tool.Kind,
		tool.Name,
		tool.Description,
		tool.RiskLevel,
		tool.Source,
		tool.ServerToolID,
		tool.DownstreamToolName,
	}
	values = append(values, tool.Tags...)
	for _, permission := range tool.Permissions {
		values = append(values, permission.Scope)
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func pageWindow(total, rawOffset, rawLimit int) (int, int) {
	offset := rawOffset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := rawLimit
	if limit <= 0 {
		limit = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return offset, end
}

func containsExactString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

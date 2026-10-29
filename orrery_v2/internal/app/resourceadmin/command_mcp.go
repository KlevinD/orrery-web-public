package resourceadmin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

type MCPRuntimePort interface {
	CheckHealth(ctx context.Context, definition toolres.ToolDefinition) (MCPRuntimeHealth, error)
	ListTools(ctx context.Context, definition toolres.ToolDefinition) (MCPRuntimeToolList, error)
}

type MCPRuntimeHealth struct {
	Status        string
	Message       string
	FailureReason string
	HTTPStatus    int
	Latency       time.Duration
}

type MCPRuntimeToolList struct {
	Tools         []string
	ToolDetails   []toolres.MCPExposedToolDefinition
	FailureReason string
	Latency       time.Duration
}

func (s *CommandService) RegisterMCPServer(ctx context.Context, viewer ViewerRef, req RegisterMCPServerRequest) (MCPServerDTO, error) {
	if s == nil || s.tools == nil {
		return MCPServerDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return MCPServerDTO{}, err
	}
	if err := s.generateResourceID(&req.ToolID, "mcp"); err != nil {
		return MCPServerDTO{}, err
	}
	definition, err := mcpDefinitionFromRequest(req)
	if err != nil {
		return MCPServerDTO{}, err
	}
	definition = s.applyMCPRegistrationProbe(ctx, definition)
	if err := s.tools.Register(ctx, definition); err != nil {
		return MCPServerDTO{}, err
	}
	if err := s.recordAudit(ctx, viewer, "resource.mcp.register", string(definition.ID), "registered mcp server"); err != nil {
		return MCPServerDTO{}, err
	}
	return mapMCP(definition), nil
}

func (s *CommandService) UpdateMCPServer(ctx context.Context, viewer ViewerRef, req RegisterMCPServerRequest) (MCPServerDTO, error) {
	if s == nil || s.tools == nil {
		return MCPServerDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return MCPServerDTO{}, err
	}
	if strings.TrimSpace(req.ToolID) == "" {
		return MCPServerDTO{}, fmt.Errorf("%w: tool_id is required for update", ErrInvalidRequest)
	}
	existing, err := s.tools.Get(ctx, agentcore.ToolID(req.ToolID))
	if err != nil {
		return MCPServerDTO{}, mapToolRegistryError(err)
	}
	if req.partialUpdate() {
		mergeMCPServerUpdateRequest(&req, existing)
	}
	definition, err := mcpDefinitionFromRequest(req)
	if err != nil {
		return MCPServerDTO{}, err
	}
	if req.partialUpdate() {
		preserveMCPServerUpdateInternals(&definition, existing, req)
	}
	if req.Headers == nil && existing.MCP != nil {
		definition.MCP.Headers = cleanMCPHeaderMap(existing.MCP.Headers)
	} else {
		definition.MCP.Headers = mergeMCPHeadersForUpdate(req.Headers, existing.MCP)
	}
	if shouldProbeMCPUpdate(req) {
		definition = s.applyMCPRegistrationProbe(ctx, definition)
	}
	if err := s.tools.UpdateDefinition(ctx, definition); err != nil {
		return MCPServerDTO{}, mapToolRegistryError(err)
	}
	if err := s.recordAudit(ctx, viewer, "resource.mcp.update", string(definition.ID), "updated mcp server"); err != nil {
		return MCPServerDTO{}, err
	}
	return mapMCP(definition), nil
}

func mergeMCPServerUpdateRequest(req *RegisterMCPServerRequest, existing toolres.ToolDefinition) {
	if req == nil {
		return
	}
	if !req.updatesField("name") {
		req.Name = existing.Name
	}
	if !req.updatesField("description") {
		req.Description = existing.Description
	}
	if !req.updatesField("version") {
		req.Version = existing.Version
	}
	if !req.updatesField("enabled") {
		enabled := existing.Enabled
		req.Enabled = &enabled
	}
	if !req.updatesField("risk_level") {
		req.RiskLevel = string(existing.RiskLevel)
	}
	if !req.updatesField("tags") {
		req.Tags = append([]string(nil), existing.Tags...)
	}
	if existing.MCP == nil {
		return
	}
	if !req.updatesField("server_name") {
		req.ServerName = existing.MCP.ServerName
	}
	if !req.updatesField("transport") {
		req.Transport = string(existing.MCP.Transport)
	}
	if !req.updatesField("command") {
		req.Command = existing.MCP.Command
	}
	if !req.updatesField("url") {
		req.URL = existing.MCP.URL
	}
	if !req.updatesField("auth_header_name") {
		req.AuthHeaderName = existing.MCP.Auth.HeaderName
	}
	if !req.updatesField("args") {
		req.Args = append([]string(nil), existing.MCP.Args...)
	}
	if !req.updatesField("env_secret_refs") {
		req.EnvSecrets = secretRefDTOsFromDefinitions(existing.MCP.EnvSecretRefs)
	}
	if !req.updatesField("exposed_tools") {
		req.ExposedTools = append([]string(nil), existing.MCP.ExposedTools...)
	}
	if !req.updatesField("exposed_tool_details") {
		req.ExposedToolDetails = mapMCPExposedToolDetails(existing.MCP.ExposedToolDetails)
	}
	if !req.updatesField("call_timeout_seconds") {
		req.CallTimeoutSeconds = existing.MCP.CallTimeoutSeconds
	}
}

func preserveMCPServerUpdateInternals(definition *toolres.ToolDefinition, existing toolres.ToolDefinition, req RegisterMCPServerRequest) {
	if definition == nil || definition.MCP == nil {
		return
	}
	existing = existing.Clone()
	definition.CreatedAt = existing.CreatedAt
	definition.UpdatedAt = existing.UpdatedAt
	definition.InputSchema = existing.InputSchema
	definition.OutputSchema = existing.OutputSchema
	definition.RuntimeRequirements = existing.RuntimeRequirements
	definition.Permissions = append([]toolres.PermissionDeclaration(nil), existing.Permissions...)
	if existing.MCP == nil {
		return
	}
	if !req.updatesField("auth_header_name") && !req.updatesField("headers") {
		definition.MCP.Auth = existing.MCP.Auth
	}
	definition.MCP.Availability = existing.MCP.Availability
	definition.MCP.HealthCheck = existing.MCP.HealthCheck
	definition.MCP.Permissions = append([]toolres.PermissionDeclaration(nil), existing.MCP.Permissions...)
}

func shouldProbeMCPUpdate(req RegisterMCPServerRequest) bool {
	if !req.partialUpdate() {
		return true
	}
	return req.updatesAny("enabled", "server_name", "transport", "command", "url", "headers", "auth_header_name", "args", "env_secret_refs")
}

func (s *CommandService) DeleteMCPServer(ctx context.Context, viewer ViewerRef, toolID string) error {
	if s == nil || s.tools == nil {
		return ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return err
	}
	definition, err := s.tools.Get(ctx, agentcore.ToolID(toolID))
	if err != nil {
		return mapToolRegistryError(err)
	}
	if definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
		return fmt.Errorf("%w: %s is not an mcp server", ErrInvalidRequest, toolID)
	}
	rollbackSourceDeletion, err := s.prepareResourceSourceDeletion(ctx, viewer, toolID)
	if err != nil {
		return err
	}
	if err := s.removeResourceBindingsFromProfiles(ctx, "mcp_server", toolID); err != nil {
		return errors.Join(err, rollbackSourceDeletion(ctx))
	}
	if err := s.tools.Delete(ctx, agentcore.ToolID(toolID)); err != nil {
		return errors.Join(mapToolRegistryError(err), rollbackSourceDeletion(ctx))
	}
	return errors.Join(s.recordAudit(ctx, viewer, "resource.mcp.delete", toolID, "deleted mcp server"), rollbackSourceDeletion(ctx))
}

func (s *CommandService) GetMCPServerHealth(ctx context.Context, viewer ViewerRef, toolID string) (MCPServerHealthDTO, error) {
	if s == nil || s.tools == nil {
		return MCPServerHealthDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceRead); err != nil {
		return MCPServerHealthDTO{}, err
	}
	definition, err := s.tools.Get(ctx, agentcore.ToolID(toolID))
	if err != nil {
		return MCPServerHealthDTO{}, mapToolRegistryError(err)
	}
	if definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
		return MCPServerHealthDTO{}, fmt.Errorf("%w: %s is not an mcp server", ErrInvalidRequest, toolID)
	}
	if !definition.Enabled {
		if definition.MCP.Availability.Status == toolres.MCPAvailabilityUnavailable {
			return mcpHealthDTOFromAvailability(definition), nil
		}
		return MCPServerHealthDTO{ToolID: string(definition.ID), Status: "disabled", Mode: string(definition.MCP.HealthCheck.Mode), Message: "MCP server definition is disabled", CheckedAt: time.Now().UTC()}, nil
	}
	if isSystemMCPServerDefinition(definition) {
		return MCPServerHealthDTO{
			ToolID:    string(definition.ID),
			Status:    "available",
			Mode:      string(definition.MCP.HealthCheck.Mode),
			Message:   "System MCP server is served by the platform gateway",
			CheckedAt: time.Now().UTC(),
		}, nil
	}
	if s.mcpRuntime == nil {
		return MCPServerHealthDTO{
			ToolID:        string(definition.ID),
			Status:        "not_connected",
			Mode:          string(definition.MCP.HealthCheck.Mode),
			Message:       "MCP server definition is registered but transport adapter is not connected",
			FailureReason: "MCP transport adapter is not configured",
			CheckedAt:     time.Now().UTC(),
		}, nil
	}
	health, err := s.mcpRuntime.CheckHealth(ctx, definition)
	if health.Status == "" {
		health.Status = "available"
	}
	if err != nil {
		health.Status = "unavailable"
		if health.FailureReason == "" {
			health.FailureReason = err.Error()
		}
	}
	if health.Message == "" && health.Status == "available" {
		health.Message = "MCP server runtime probe succeeded"
	}
	return MCPServerHealthDTO{
		ToolID:        string(definition.ID),
		Status:        health.Status,
		Mode:          string(definition.MCP.HealthCheck.Mode),
		Message:       health.Message,
		FailureReason: health.FailureReason,
		LatencyMS:     health.Latency.Milliseconds(),
		CheckedAt:     time.Now().UTC(),
	}, nil
}

func mcpHealthDTOFromAvailability(definition toolres.ToolDefinition) MCPServerHealthDTO {
	availability := definition.MCP.Availability
	checkedAt := availability.CheckedAt
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}
	return MCPServerHealthDTO{
		ToolID:        string(definition.ID),
		Status:        string(availability.Status),
		Mode:          string(definition.MCP.HealthCheck.Mode),
		Message:       availability.Message,
		FailureReason: availability.FailureReason,
		LatencyMS:     availability.LatencyMS,
		CheckedAt:     checkedAt,
	}
}

func (s *CommandService) SyncMCPServer(ctx context.Context, viewer ViewerRef, toolID string) (MCPServerSyncDTO, error) {
	if s == nil || s.tools == nil {
		return MCPServerSyncDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return MCPServerSyncDTO{}, err
	}
	definition, err := s.tools.Get(ctx, agentcore.ToolID(toolID))
	if err != nil {
		return MCPServerSyncDTO{}, mapToolRegistryError(err)
	}
	if definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
		return MCPServerSyncDTO{}, fmt.Errorf("%w: %s is not an mcp server", ErrInvalidRequest, toolID)
	}
	if isSystemMCPServerDefinition(definition) || isLockedMCPToolSurfaceDefinition(definition) {
		return MCPServerSyncDTO{
			ToolID:             string(definition.ID),
			Status:             "synced",
			ExposedToolCount:   len(definition.MCP.ExposedTools),
			ExposedTools:       append([]string(nil), definition.MCP.ExposedTools...),
			ExposedToolDetails: mapMCPExposedToolDetails(definition.MCP.ExposedToolDetails),
			SyncedAt:           time.Now().UTC(),
		}, nil
	}
	if s.mcpRuntime == nil {
		if err := s.recordAuditWithOutcome(ctx, viewer, "resource.mcp.sync_not_connected", string(definition.ID), "not_connected", "warning", "mcp transport adapter is not configured"); err != nil {
			return MCPServerSyncDTO{}, err
		}
		return MCPServerSyncDTO{
			ToolID:             string(definition.ID),
			Status:             "not_connected",
			ExposedToolCount:   len(definition.MCP.ExposedTools),
			ExposedTools:       append([]string(nil), definition.MCP.ExposedTools...),
			ExposedToolDetails: mapMCPExposedToolDetails(definition.MCP.ExposedToolDetails),
			FailureReason:      "MCP transport adapter is not configured",
			SyncedAt:           time.Now().UTC(),
		}, nil
	}
	var listed MCPRuntimeToolList
	status := "synced"
	listed, err = s.mcpRuntime.ListTools(ctx, definition)
	if err != nil {
		definition.MCP.Availability = toolres.MCPAvailability{
			Status:        toolres.MCPAvailabilityUnavailable,
			FailureReason: err.Error(),
			LatencyMS:     listed.Latency.Milliseconds(),
			CheckedAt:     time.Now().UTC(),
		}
		if updateErr := s.tools.UpdateDefinition(ctx, definition); updateErr != nil {
			return MCPServerSyncDTO{}, updateErr
		}
		if err := s.recordAuditWithOutcome(ctx, viewer, "resource.mcp.sync_failed", string(definition.ID), "failed", "error", "failed to sync mcp server: "+err.Error()); err != nil {
			return MCPServerSyncDTO{}, err
		}
		return MCPServerSyncDTO{
			ToolID:             string(definition.ID),
			Status:             "failed",
			ExposedToolCount:   len(definition.MCP.ExposedTools),
			ExposedTools:       append([]string(nil), definition.MCP.ExposedTools...),
			ExposedToolDetails: mapMCPExposedToolDetails(definition.MCP.ExposedToolDetails),
			FailureReason:      err.Error(),
			SyncedAt:           time.Now().UTC(),
		}, nil
	}
	definition.MCP.ExposedTools = append([]string(nil), listed.Tools...)
	definition.MCP.ExposedToolDetails = append([]toolres.MCPExposedToolDefinition(nil), listed.ToolDetails...)
	definition.MCP.Availability = toolres.MCPAvailability{
		Status:    toolres.MCPAvailabilityAvailable,
		Message:   "MCP tool cache sync succeeded",
		LatencyMS: listed.Latency.Milliseconds(),
		CheckedAt: time.Now().UTC(),
	}
	if err := s.tools.UpdateDefinition(ctx, definition); err != nil {
		return MCPServerSyncDTO{}, err
	}
	if err := s.recordAudit(ctx, viewer, "resource.mcp.sync", string(definition.ID), "synced mcp server"); err != nil {
		return MCPServerSyncDTO{}, err
	}
	return MCPServerSyncDTO{
		ToolID:             string(definition.ID),
		Status:             status,
		ExposedToolCount:   len(definition.MCP.ExposedTools),
		ExposedTools:       append([]string(nil), definition.MCP.ExposedTools...),
		ExposedToolDetails: mapMCPExposedToolDetails(definition.MCP.ExposedToolDetails),
		LatencyMS:          listed.Latency.Milliseconds(),
		SyncedAt:           time.Now().UTC(),
	}, nil
}

func isSystemMCPServerDefinition(definition toolres.ToolDefinition) bool {
	return definition.ID == toolstd.SystemMCPServer
}

func isLockedMCPToolSurfaceDefinition(definition toolres.ToolDefinition) bool {
	for _, tag := range definition.Tags {
		if strings.EqualFold(strings.TrimSpace(tag), toolres.TagMCPToolSurfaceLocked) {
			return true
		}
	}
	return false
}

func mcpDefinitionFromRequest(req RegisterMCPServerRequest) (toolres.ToolDefinition, error) {
	if strings.TrimSpace(req.ToolID) == "" || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.ServerName) == "" {
		return toolres.ToolDefinition{}, fmt.Errorf("%w: tool_id, name and server_name are required", ErrInvalidRequest)
	}
	if req.CallTimeoutSeconds < 0 {
		return toolres.ToolDefinition{}, fmt.Errorf("%w: call_timeout_seconds cannot be negative", ErrInvalidRequest)
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = "1.0.0"
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	risk := toolres.RiskLevel(req.RiskLevel)
	if risk == "" {
		risk = toolres.RiskMedium
	}
	transport := toolres.MCPTransport(req.Transport)
	if transport == "" {
		transport = toolres.TransportStdio
	}
	if err := validateMCPTransportRequest(transport, req); err != nil {
		return toolres.ToolDefinition{}, err
	}
	definition := toolres.ToolDefinition{
		ID:          agentcore.ToolID(req.ToolID),
		Kind:        toolres.KindMCPServer,
		Name:        req.Name,
		Description: req.Description,
		Version:     version,
		Enabled:     enabled,
		RiskLevel:   risk,
		SecretRefs:  secretRefsFromDTO(req.EnvSecrets),
		MCP: &toolres.MCPServerDefinition{
			ServerName:    req.ServerName,
			Transport:     transport,
			Command:       req.Command,
			URL:           req.URL,
			Headers:       cleanMCPHeaderMap(req.Headers),
			Auth:          toolres.MCPAuthPolicy{HeaderName: strings.TrimSpace(req.AuthHeaderName)},
			Args:          append([]string(nil), req.Args...),
			EnvSecretRefs: secretRefsFromDTO(req.EnvSecrets),
			ExposedTools:  append([]string(nil), req.ExposedTools...),
			ExposedToolDetails: mcpExposedToolDetailsFromDTO(
				req.ExposedToolDetails,
			),
			CallTimeoutSeconds: req.CallTimeoutSeconds,
			HealthCheck:        toolres.HealthCheckPolicy{Mode: toolres.HealthCheckStartup},
		},
		Tags: append([]string(nil), req.Tags...),
	}
	return definition, nil
}

func (s *CommandService) applyMCPRegistrationProbe(ctx context.Context, definition toolres.ToolDefinition) toolres.ToolDefinition {
	if definition.MCP == nil {
		return definition
	}
	definition.MCP.Auth = inferMCPAuthPolicy(definition.MCP)
	if s == nil || s.mcpRuntime == nil || isSystemMCPServerDefinition(definition) {
		return definition
	}
	health, err := s.mcpRuntime.CheckHealth(ctx, definition)
	if health.Status == "" {
		health.Status = "available"
	}
	status := toolres.MCPAvailabilityStatus(health.Status)
	if err != nil {
		if isMCPAuthRequiredProbe(health, err) {
			status = toolres.MCPAvailabilityRequiresAuth
			definition.Enabled = true
			definition.MCP.Auth.Required = true
			if definition.MCP.Auth.DefaultCredentialConfigured {
				definition.MCP.Auth.DefaultCredentialConfigured = false
				definition.MCP.Auth.Mode = toolres.MCPAuthModePerAgent
			}
			if definition.MCP.Auth.Mode == toolres.MCPAuthModeNone {
				definition.MCP.Auth.Mode = toolres.MCPAuthModePerAgent
			}
			if definition.MCP.Auth.HeaderName == "" {
				definition.MCP.Auth.HeaderName = "Authorization"
			}
		} else {
			status = toolres.MCPAvailabilityUnavailable
			definition.Enabled = false
		}
	} else {
		status = toolres.MCPAvailabilityAvailable
		definition.Enabled = true
	}
	definition.MCP.Availability = toolres.MCPAvailability{
		Status:        status,
		Message:       health.Message,
		FailureReason: health.FailureReason,
		HTTPStatus:    health.HTTPStatus,
		LatencyMS:     health.Latency.Milliseconds(),
		CheckedAt:     time.Now().UTC(),
	}
	return definition
}

func inferMCPAuthPolicy(definition *toolres.MCPServerDefinition) toolres.MCPAuthPolicy {
	if definition == nil {
		return toolres.MCPAuthPolicy{}
	}
	policy := definition.Auth
	if policy.HeaderName == "" {
		policy.HeaderName = "Authorization"
	}
	if _, ok := findMCPHeader(definition, policy.HeaderName); ok {
		policy.Required = true
		policy.Mode = toolres.MCPAuthModeGlobal
		policy.DefaultCredentialConfigured = true
	}
	return policy
}

func isMCPAuthRequiredProbe(health MCPRuntimeHealth, err error) bool {
	if health.HTTPStatus == 401 {
		return true
	}
	if strings.EqualFold(health.Status, "requires_auth") {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(firstNonEmpty(health.FailureReason, err.Error())))
	return strings.Contains(text, "status 401") || strings.Contains(text, "unauthorized")
}

func mergeMCPHeadersForUpdate(next map[string]string, existing *toolres.MCPServerDefinition) map[string]string {
	if len(next) == 0 {
		return nil
	}
	out := cleanMCPHeaderMap(next)
	if len(out) == 0 {
		return nil
	}
	for name, value := range out {
		if !isMaskedCredentialValue(value) {
			continue
		}
		if existingValue, ok := findMCPHeader(existing, name); ok {
			out[name] = existingValue
		} else {
			delete(out, name)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func findMCPHeader(existing *toolres.MCPServerDefinition, name string) (string, bool) {
	if existing == nil || len(existing.Headers) == 0 {
		return "", false
	}
	for existingName, value := range existing.Headers {
		if strings.EqualFold(existingName, name) {
			return value, true
		}
	}
	return "", false
}

func cleanMCPHeaderMap(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validateMCPTransportRequest(transport toolres.MCPTransport, req RegisterMCPServerRequest) error {
	switch transport {
	case toolres.TransportStdio:
		if strings.TrimSpace(req.Command) == "" {
			return fmt.Errorf("%w: stdio mcp command is required", ErrInvalidRequest)
		}
	case toolres.TransportStreamHTTP, toolres.TransportSSE:
		if strings.TrimSpace(req.URL) == "" {
			return fmt.Errorf("%w: %s mcp url is required", ErrInvalidRequest, transport)
		}
	default:
		return fmt.Errorf("%w: unsupported mcp transport %q", ErrInvalidRequest, transport)
	}
	return nil
}

func mcpExposedToolDetailsFromDTO(details []MCPExposedToolDTO) []toolres.MCPExposedToolDefinition {
	if len(details) == 0 {
		return nil
	}
	out := make([]toolres.MCPExposedToolDefinition, 0, len(details))
	for _, detail := range details {
		if strings.TrimSpace(detail.Name) == "" {
			continue
		}
		out = append(out, toolres.MCPExposedToolDefinition{
			Name:         detail.Name,
			Description:  detail.Description,
			InputSchema:  cloneSchemaMap(detail.InputSchema),
			OutputSchema: cloneSchemaMap(detail.OutputSchema),
		})
	}
	return out
}

package resourceadmin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	toolres "orrery_v2/internal/resource/tool"
)

func (s *CommandService) RegisterTool(ctx context.Context, viewer ViewerRef, req ToolDefinitionRequest) (ToolDTO, error) {
	if s == nil || s.tools == nil {
		return ToolDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return ToolDTO{}, err
	}
	if err := s.generateResourceID(&req.ToolID, "tool"); err != nil {
		return ToolDTO{}, err
	}
	definition, err := toolDefinitionFromRequest(req)
	if err != nil {
		return ToolDTO{}, err
	}
	if err := s.tools.Register(ctx, definition); err != nil {
		return ToolDTO{}, err
	}
	if err := s.recordAudit(ctx, viewer, "resource.tool.register", string(definition.ID), "registered tool"); err != nil {
		return ToolDTO{}, err
	}
	return mapTool(definition), nil
}

func (s *CommandService) UpdateTool(ctx context.Context, viewer ViewerRef, req ToolDefinitionRequest) (ToolDTO, error) {
	if s == nil || s.tools == nil {
		return ToolDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return ToolDTO{}, err
	}
	if strings.TrimSpace(req.ToolID) == "" {
		return ToolDTO{}, fmt.Errorf("%w: tool_id is required for update", ErrInvalidRequest)
	}
	var existing toolres.ToolDefinition
	if req.partialUpdate() {
		var err error
		existing, err = s.tools.Get(ctx, agentcore.ToolID(strings.TrimSpace(req.ToolID)))
		if err != nil {
			return ToolDTO{}, mapToolRegistryError(err)
		}
		mergeToolDefinitionUpdateRequest(&req, existing)
	}
	definition, err := toolDefinitionFromRequest(req)
	if err != nil {
		return ToolDTO{}, err
	}
	if req.partialUpdate() {
		preserveToolDefinitionUpdateInternals(&definition, existing, req)
	}
	if err := s.tools.UpdateDefinition(ctx, definition); err != nil {
		return ToolDTO{}, mapToolRegistryError(err)
	}
	if err := s.recordAudit(ctx, viewer, "resource.tool.update", string(definition.ID), "updated tool"); err != nil {
		return ToolDTO{}, err
	}
	return mapTool(definition), nil
}

func (s *CommandService) DisableTool(ctx context.Context, viewer ViewerRef, toolID string) error {
	if s == nil || s.tools == nil {
		return ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return err
	}
	if err := s.tools.Disable(ctx, agentcore.ToolID(toolID)); err != nil {
		return mapToolRegistryError(err)
	}
	return s.recordAudit(ctx, viewer, "resource.tool.disable", toolID, "disabled tool")
}

func (s *CommandService) DeleteTool(ctx context.Context, viewer ViewerRef, toolID string) error {
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
	if definition.Kind == toolres.KindMCPServer {
		return fmt.Errorf("%w: mcp servers must use mcp-server endpoints", ErrInvalidRequest)
	}
	rollbackSourceDeletion, err := s.prepareResourceSourceDeletion(ctx, viewer, toolID)
	if err != nil {
		return err
	}
	if err := s.removeResourceBindingsFromProfiles(ctx, "tool", toolID); err != nil {
		return errors.Join(err, rollbackSourceDeletion(ctx))
	}
	if err := s.tools.Delete(ctx, agentcore.ToolID(toolID)); err != nil {
		return errors.Join(mapToolRegistryError(err), rollbackSourceDeletion(ctx))
	}
	return errors.Join(s.recordAudit(ctx, viewer, "resource.tool.delete", toolID, "deleted tool"), rollbackSourceDeletion(ctx))
}

func (s *CommandService) prepareResourceSourceDeletion(ctx context.Context, viewer ViewerRef, toolID string) (func(context.Context) error, error) {
	if s.sourceDeletion == nil {
		return func(context.Context) error { return nil }, nil
	}
	toolID = strings.TrimSpace(toolID)
	return s.sourceDeletion.PrepareSourceDeletion(
		ctx, "resource", toolID, []string{"tool:" + toolID, "resource:" + toolID}, "resource.delete:"+viewer.ViewerID,
	)
}

func mapToolRegistryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, toolres.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if errors.Is(err, toolres.ErrInvalidDefinition) || errors.Is(err, toolres.ErrAlreadyExists) || errors.Is(err, toolres.ErrDisabled) || errors.Is(err, toolres.ErrVersionMismatch) {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if errors.Is(err, toolres.ErrPermissionDenied) {
		return fmt.Errorf("%w: %v", ErrPermissionDenied, err)
	}
	return err
}

func mergeToolDefinitionUpdateRequest(req *ToolDefinitionRequest, existing toolres.ToolDefinition) {
	if req == nil {
		return
	}
	if !req.updatesField("kind") {
		req.Kind = string(existing.Kind)
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
	if !req.updatesField("risk_level") {
		req.RiskLevel = string(existing.RiskLevel)
	}
	if !req.updatesField("enabled") {
		enabled := existing.Enabled
		req.Enabled = &enabled
	}
	if !req.updatesField("tags") {
		req.Tags = append([]string(nil), existing.Tags...)
	}
	if !req.updatesField("secret_refs") {
		req.SecretRefs = secretRefDTOsFromDefinitions(existing.SecretRefs)
	}
	if !req.updatesField("permission_declarations") {
		req.Permissions = permissionDTOsFromDeclarations(existing.Permissions)
	}
	if !req.updatesField("command") && existing.CLI != nil {
		req.Command = existing.CLI.Command
	}
	if !req.updatesField("url") {
		req.URL = existing.RuntimeRequirements.Notes
	}
}

func preserveToolDefinitionUpdateInternals(definition *toolres.ToolDefinition, existing toolres.ToolDefinition, req ToolDefinitionRequest) {
	if definition == nil {
		return
	}
	existing = existing.Clone()
	definition.CreatedAt = existing.CreatedAt
	definition.UpdatedAt = existing.UpdatedAt
	definition.InputSchema = existing.InputSchema
	definition.OutputSchema = existing.OutputSchema
	runtime := existing.RuntimeRequirements
	if req.updatesField("url") || definition.RuntimeRequirements.Notes != "" {
		runtime.Notes = definition.RuntimeRequirements.Notes
	}
	definition.RuntimeRequirements = runtime
	if definition.Kind == existing.Kind {
		if definition.Approval == nil {
			definition.Approval = existing.Approval
		}
		if definition.MCP == nil {
			definition.MCP = existing.MCP
		}
	}
}

func toolDefinitionFromRequest(req ToolDefinitionRequest) (toolres.ToolDefinition, error) {
	if strings.TrimSpace(req.ToolID) == "" || strings.TrimSpace(req.Name) == "" {
		return toolres.ToolDefinition{}, fmt.Errorf("%w: tool_id and name are required", ErrInvalidRequest)
	}
	kind := toolres.Kind(req.Kind)
	if kind == "" {
		kind = toolres.KindInternalServiceTool
	}
	if kind == toolres.KindMCPServer {
		return toolres.ToolDefinition{}, fmt.Errorf("%w: mcp servers must use mcp-server endpoints", ErrInvalidRequest)
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = "1.0.0"
	}
	risk := toolres.RiskLevel(req.RiskLevel)
	if risk == "" {
		risk = toolres.RiskMedium
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	definition := toolres.ToolDefinition{
		ID:          agentcore.ToolID(req.ToolID),
		Kind:        kind,
		Name:        req.Name,
		Description: req.Description,
		Version:     version,
		Enabled:     enabled,
		RiskLevel:   risk,
		SecretRefs:  secretRefsFromDTO(req.SecretRefs),
		Tags:        append([]string(nil), req.Tags...),
	}
	for _, permission := range req.Permissions {
		definition.Permissions = append(definition.Permissions, toolres.PermissionDeclaration{
			Scope:       permission.Scope,
			Description: permission.Description,
			Risk:        toolres.RiskLevel(permission.Risk),
			Required:    permission.Required,
		})
	}
	switch kind {
	case toolres.KindCLITool:
		if strings.TrimSpace(req.Command) == "" {
			return toolres.ToolDefinition{}, fmt.Errorf("%w: cli tool command is required", ErrInvalidRequest)
		}
		definition.CLI = &toolres.CLIToolDefinition{Command: req.Command}
	case toolres.KindAPITool:
		if strings.TrimSpace(req.URL) == "" {
			return toolres.ToolDefinition{}, fmt.Errorf("%w: api tool url is required", ErrInvalidRequest)
		}
		definition.RuntimeRequirements.Notes = req.URL
	}
	return definition, nil
}

func secretRefsFromDTO(refs []SecretRefDTO) []toolres.SecretRef {
	out := make([]toolres.SecretRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, toolres.SecretRef{Name: ref.Name, Env: ref.Env})
	}
	return out
}

func secretRefDTOsFromDefinitions(refs []toolres.SecretRef) []SecretRefDTO {
	out := make([]SecretRefDTO, 0, len(refs))
	for _, ref := range refs {
		out = append(out, SecretRefDTO{Name: ref.Name, Env: ref.Env})
	}
	return out
}

func permissionDTOsFromDeclarations(permissions []toolres.PermissionDeclaration) []PermissionDTO {
	out := make([]PermissionDTO, 0, len(permissions))
	for _, permission := range permissions {
		out = append(out, PermissionDTO{
			Scope:       permission.Scope,
			Description: permission.Description,
			Risk:        string(permission.Risk),
			Required:    permission.Required,
		})
	}
	return out
}

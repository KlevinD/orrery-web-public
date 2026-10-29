package resourceadmin

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	toolres "orrery_v2/internal/resource/tool"
)

type AgentProfileBindingStorePort interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	SaveProfile(ctx context.Context, profile agentcore.AgentProfile) error
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

func (s *CommandService) BindResourceToAgent(ctx context.Context, viewer ViewerRef, req BindResourceRequest) (ResourceBindingDTO, error) {
	if s == nil || s.profiles == nil {
		return ResourceBindingDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return ResourceBindingDTO{}, err
	}
	resourceType, err := normalizeAgentBindingResourceType(req.ResourceType)
	if err != nil {
		return ResourceBindingDTO{}, err
	}
	if resourceType.BindingKind == "skill" {
		s.skillReferences.Lock()
		defer s.skillReferences.Unlock()
	}
	profile, entry, err := s.profileForAgent(ctx, req.AgentID)
	if err != nil {
		return ResourceBindingDTO{}, err
	}
	authStatus := mcpBindingAuthStatus{}
	switch resourceType.BindingKind {
	case "tool":
		if s.tools == nil {
			return ResourceBindingDTO{}, ErrServiceDependency
		}
		bindingDefinition, bindingToolID, err := s.bindingToolDefinition(ctx, agentcore.ToolID(req.ResourceID))
		if err != nil {
			return ResourceBindingDTO{}, err
		}
		if err := validateToolBindingResourceType(resourceType.ResourceType, bindingDefinition, bindingToolID); err != nil {
			return ResourceBindingDTO{}, err
		}
		authStatus, err = prepareMCPAgentAuthorization(&profile, bindingDefinition, req)
		if err != nil {
			return ResourceBindingDTO{}, err
		}
		profile.ToolRefs = upsertToolRef(profile.ToolRefs, agentcore.ToolRef{
			ToolID:            bindingToolID,
			Alias:             req.Alias,
			VersionConstraint: req.VersionConstraint,
			PermissionScope:   req.PermissionScope,
			BindingPolicy:     req.BindingPolicy,
		})
	case "skill":
		if s.skills == nil {
			return ResourceBindingDTO{}, ErrServiceDependency
		}
		if _, err := s.skills.Get(ctx, agentcore.SkillID(req.ResourceID)); err != nil {
			return ResourceBindingDTO{}, err
		}
		profile.SkillRefs = upsertSkillRef(profile.SkillRefs, agentcore.SkillRef{
			SkillID:           agentcore.SkillID(req.ResourceID),
			Alias:             req.Alias,
			VersionConstraint: req.VersionConstraint,
		})
	}
	if err := s.profiles.SaveProfile(ctx, profile); err != nil {
		return ResourceBindingDTO{}, err
	}
	agentID := resourceAgentID(entry)
	dto := ResourceBindingDTO{
		ResourceID:          req.ResourceID,
		ResourceType:        resourceType.ResourceType,
		BindingType:         "agent_" + resourceType.ResourceType + "_ref",
		BoundToType:         "agent",
		BoundToID:           agentID,
		BoundToDisplayName:  entry.DisplayName,
		Alias:               req.Alias,
		VersionConstraint:   req.VersionConstraint,
		PermissionScope:     req.PermissionScope,
		BindingPolicy:       req.BindingPolicy,
		MCPAuthRequired:     authStatus.AuthRequired,
		AuthHeaderName:      authStatus.HeaderName,
		DefaultAKConfigured: authStatus.DefaultAKConfigured,
		AgentAKConfigured:   authStatus.AgentAKConfigured,
		AgentAKMasked:       authStatus.AgentAKMasked,
	}
	if err := s.recordAuditWithMetadata(ctx, viewer, "resource.binding.bind", req.ResourceID, "bound "+resourceType.ResourceType+" to agent", map[string]string{
		"agent_id":          agentID,
		"resource_id":       req.ResourceID,
		"resource_type":     resourceType.ResourceType,
		"permission_scope":  req.PermissionScope,
		"binding_policy":    req.BindingPolicy,
		"agent_profile_id":  entry.AgentProfileID.String(),
		"agent_instance_id": entry.AgentInstanceID.String(),
	}); err != nil {
		return ResourceBindingDTO{}, err
	}
	subject := resourceBindingSubject(entry)
	if err := s.authz.WriteResourceBindingForSubject(ctx, subject, req.ResourceID, ""); err != nil {
		return ResourceBindingDTO{}, err
	}
	return dto, nil
}

func (s *CommandService) bindingToolDefinition(ctx context.Context, resourceID agentcore.ToolID) (toolres.ToolDefinition, agentcore.ToolID, error) {
	definition, err := s.tools.Get(ctx, resourceID)
	if err == nil {
		return definition, resourceID, nil
	}
	serverID, downstream := splitMCPDownstreamToolID(ctx, s.tools, resourceID)
	if serverID == "" || downstream == "" {
		return toolres.ToolDefinition{}, "", err
	}
	definition, err = s.tools.Get(ctx, serverID)
	if err != nil {
		return toolres.ToolDefinition{}, "", err
	}
	return definition, resourceID, nil
}

func splitMCPDownstreamToolID(ctx context.Context, registry toolres.Registry, resourceID agentcore.ToolID) (agentcore.ToolID, string) {
	if registry == nil {
		return "", ""
	}
	ref := strings.TrimSpace(string(resourceID))
	if ref == "" || !strings.Contains(ref, ".") {
		return "", ""
	}
	parts := strings.Split(ref, ".")
	for i := len(parts) - 1; i > 0; i-- {
		serverID := agentcore.ToolID(strings.Join(parts[:i], "."))
		downstream := strings.TrimSpace(strings.TrimPrefix(ref, string(serverID)+"."))
		if downstream == "" {
			continue
		}
		definition, err := registry.Get(ctx, serverID)
		if err != nil || definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
			continue
		}
		if mcpDefinitionExposesTool(definition, downstream) {
			return serverID, downstream
		}
	}
	return "", ""
}

func mcpDefinitionExposesTool(definition toolres.ToolDefinition, name string) bool {
	if definition.MCP == nil {
		return false
	}
	name = strings.TrimSpace(name)
	for _, exposed := range definition.MCP.ExposedToolDetails {
		if strings.TrimSpace(exposed.Name) == name {
			return true
		}
	}
	for _, exposed := range definition.MCP.ExposedTools {
		if strings.TrimSpace(exposed) == name {
			return true
		}
	}
	return false
}

func (s *CommandService) UnbindResourceFromAgent(ctx context.Context, viewer ViewerRef, req UnbindResourceRequest) error {
	if s == nil || s.profiles == nil {
		return ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return err
	}
	resourceType, err := normalizeAgentBindingResourceType(req.ResourceType)
	if err != nil {
		return err
	}
	if resourceType.BindingKind == "skill" {
		s.skillReferences.Lock()
		defer s.skillReferences.Unlock()
	}
	profile, entry, err := s.profileForAgent(ctx, req.AgentID)
	if err != nil {
		return err
	}
	switch resourceType.BindingKind {
	case "tool":
		if resourceType.ResourceType == "mcp_server" {
			profile.ToolRefs = removeToolRefAndMCPDownstreamRefs(profile.ToolRefs, agentcore.ToolID(req.ResourceID))
		} else {
			profile.ToolRefs = removeToolRef(profile.ToolRefs, agentcore.ToolID(req.ResourceID))
		}
		profile.MCPAuthorizations = removeMCPAuthorization(profile.MCPAuthorizations, agentcore.ToolID(req.ResourceID))
	case "skill":
		profile.SkillRefs = removeSkillRef(profile.SkillRefs, agentcore.SkillID(req.ResourceID))
	}
	if err := s.profiles.SaveProfile(ctx, profile); err != nil {
		return err
	}
	agentID := resourceAgentID(entry)
	if err := s.recordAuditWithMetadata(ctx, viewer, "resource.binding.unbind", req.ResourceID, "unbound "+resourceType.ResourceType+" from agent", map[string]string{
		"agent_id":          agentID,
		"resource_id":       req.ResourceID,
		"resource_type":     resourceType.ResourceType,
		"agent_profile_id":  entry.AgentProfileID.String(),
		"agent_instance_id": entry.AgentInstanceID.String(),
	}); err != nil {
		return err
	}
	subject := resourceBindingSubject(entry)
	return s.authz.DeleteResourceBindingForSubject(ctx, subject, req.ResourceID, "")
}

func (s *CommandService) removeResourceBindingsFromProfiles(ctx context.Context, resourceType, resourceID string) error {
	if s == nil || s.profiles == nil {
		return nil
	}
	normalized, err := normalizeAgentBindingResourceType(resourceType)
	if err != nil {
		return err
	}
	page, err := s.profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return err
	}
	seen := map[agentcore.AgentProfileID]struct{}{}
	for _, entry := range page.Entries {
		if _, ok := seen[entry.AgentProfileID]; ok {
			continue
		}
		seen[entry.AgentProfileID] = struct{}{}
		profile, err := s.profiles.GetProfile(ctx, entry.AgentProfileID)
		if err != nil {
			return err
		}
		next := profile
		switch normalized.BindingKind {
		case "tool":
			next.ToolRefs = removeToolRefAndMCPDownstreamRefs(next.ToolRefs, agentcore.ToolID(resourceID))
			next.MCPAuthorizations = removeMCPAuthorization(next.MCPAuthorizations, agentcore.ToolID(resourceID))
		case "skill":
			next.SkillRefs = removeSkillRef(next.SkillRefs, agentcore.SkillID(resourceID))
		default:
			return fmt.Errorf("%w: resource_type must be tool, mcp_server or skill", ErrInvalidRequest)
		}
		if len(next.ToolRefs) == len(profile.ToolRefs) && len(next.SkillRefs) == len(profile.SkillRefs) && len(next.MCPAuthorizations) == len(profile.MCPAuthorizations) {
			continue
		}
		if err := s.profiles.SaveProfile(ctx, next); err != nil {
			return err
		}
	}
	return nil
}

type mcpBindingAuthStatus struct {
	AuthRequired        bool
	HeaderName          string
	DefaultAKConfigured bool
	AgentAKConfigured   bool
	AgentAKMasked       string
}

func prepareMCPAgentAuthorization(profile *agentcore.AgentProfile, definition toolres.ToolDefinition, req BindResourceRequest) (mcpBindingAuthStatus, error) {
	if profile == nil || definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
		return mcpBindingAuthStatus{}, nil
	}
	headerName := firstNonEmpty(strings.TrimSpace(req.AuthHeaderName), strings.TrimSpace(definition.MCP.Auth.HeaderName), "Authorization")
	agentAK := strings.TrimSpace(req.AgentAK)
	existing, hasExisting := findMCPAuthorization(profile.MCPAuthorizations, definition.ID)
	defaultConfigured := mcpDefaultAKConfigured(definition, headerName)
	authRequired := definition.MCP.Auth.Required || definition.MCP.Availability.Status == toolres.MCPAvailabilityRequiresAuth
	status := mcpBindingAuthStatus{
		AuthRequired:        authRequired,
		HeaderName:          headerName,
		DefaultAKConfigured: defaultConfigured,
		AgentAKConfigured:   hasExisting,
	}
	if hasExisting {
		status.AgentAKMasked = maskCredentialValue(existing.CredentialValue)
	}
	if agentAK != "" {
		auth := agentcore.MCPAuthorization{
			ServerToolID:    definition.ID,
			HeaderName:      headerName,
			CredentialValue: agentAK,
		}
		profile.MCPAuthorizations = upsertMCPAuthorization(profile.MCPAuthorizations, auth)
		status.AgentAKConfigured = true
		status.AgentAKMasked = maskCredentialValue(agentAK)
		return status, nil
	}
	if authRequired && !defaultConfigured && !hasExisting {
		return status, fmt.Errorf("%w: agent_ak is required for mcp server %s", ErrInvalidRequest, definition.ID)
	}
	return status, nil
}

func mcpDefaultAKConfigured(definition toolres.ToolDefinition, headerName string) bool {
	if definition.MCP == nil {
		return false
	}
	return definition.MCP.Auth.DefaultCredentialConfigured
}

func findMCPAuthorization(values []agentcore.MCPAuthorization, serverToolID agentcore.ToolID) (agentcore.MCPAuthorization, bool) {
	for _, value := range values {
		if strings.EqualFold(string(value.ServerToolID), string(serverToolID)) && strings.TrimSpace(value.CredentialValue) != "" {
			return value, true
		}
	}
	return agentcore.MCPAuthorization{}, false
}

func upsertMCPAuthorization(values []agentcore.MCPAuthorization, next agentcore.MCPAuthorization) []agentcore.MCPAuthorization {
	if strings.TrimSpace(string(next.ServerToolID)) == "" || strings.TrimSpace(next.CredentialValue) == "" {
		return values
	}
	if strings.TrimSpace(next.HeaderName) == "" {
		next.HeaderName = "Authorization"
	}
	for i, value := range values {
		if strings.EqualFold(string(value.ServerToolID), string(next.ServerToolID)) {
			values[i] = next
			return values
		}
	}
	return append(values, next)
}

func removeMCPAuthorization(values []agentcore.MCPAuthorization, serverToolID agentcore.ToolID) []agentcore.MCPAuthorization {
	out := values[:0]
	for _, value := range values {
		if !strings.EqualFold(string(value.ServerToolID), string(serverToolID)) {
			out = append(out, value)
		}
	}
	return out
}

func (s *CommandService) profileForAgent(ctx context.Context, agentID string) (agentcore.AgentProfile, agentcore.AgentDirectoryEntry, error) {
	if strings.TrimSpace(agentID) == "" {
		return agentcore.AgentProfile{}, agentcore.AgentDirectoryEntry{}, fmt.Errorf("%w: agent_id is required", ErrInvalidRequest)
	}
	page, err := s.profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return agentcore.AgentProfile{}, agentcore.AgentDirectoryEntry{}, err
	}
	for _, entry := range page.Entries {
		if !resourceAgentMatches(entry, agentID) {
			continue
		}
		profile, err := s.profiles.GetProfile(ctx, entry.AgentProfileID)
		if err == nil && profile.IsPersonalAssistant() {
			return agentcore.AgentProfile{}, agentcore.AgentDirectoryEntry{}, fmt.Errorf("%w: agent %q not found", ErrInvalidRequest, agentID)
		}
		return profile, entry, err
	}
	return agentcore.AgentProfile{}, agentcore.AgentDirectoryEntry{}, fmt.Errorf("%w: agent %q not found", ErrInvalidRequest, agentID)
}

func resourceAgentID(entry agentcore.AgentDirectoryEntry) string {
	return firstNonEmpty(entry.AgentProfileID.String(), entry.AgentInstanceID.String())
}

func resourceBindingSubject(entry agentcore.AgentDirectoryEntry) authorization.SubjectRef {
	return authorization.ResolveResourceBindingSubject(
		entry.AgentProfileID.String(),
		entry.AgentInstanceID.String(),
		entry.AgentKind == agentcore.AgentKindHuman,
	)
}

func resourceAgentMatches(entry agentcore.AgentDirectoryEntry, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	return strings.EqualFold(resourceAgentID(entry), agentID)
}

type agentBindingResourceType struct {
	ResourceType string
	BindingKind  string
}

func (t agentBindingResourceType) String() string {
	return t.ResourceType
}

func normalizeAgentBindingResourceType(resourceType string) (agentBindingResourceType, error) {
	switch strings.TrimSpace(strings.ToLower(resourceType)) {
	case "tool":
		return agentBindingResourceType{ResourceType: "tool", BindingKind: "tool"}, nil
	case "mcp", "mcp_server":
		return agentBindingResourceType{ResourceType: "mcp_server", BindingKind: "tool"}, nil
	case "skill":
		return agentBindingResourceType{ResourceType: "skill", BindingKind: "skill"}, nil
	default:
		return agentBindingResourceType{}, fmt.Errorf("%w: resource_type must be tool, mcp_server or skill", ErrInvalidRequest)
	}
}

func validateToolBindingResourceType(resourceType string, definition toolres.ToolDefinition, bindingToolID agentcore.ToolID) error {
	switch resourceType {
	case "mcp_server":
		if definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
			return fmt.Errorf("%w: %s is not an mcp server", ErrInvalidRequest, definition.ID)
		}
		if bindingToolID != definition.ID {
			return fmt.Errorf("%w: %s is an mcp downstream tool and must be bound as tool", ErrInvalidRequest, bindingToolID)
		}
	case "tool":
		if definition.Kind == toolres.KindMCPServer && bindingToolID == definition.ID {
			return fmt.Errorf("%w: %s is an mcp server and must be bound as mcp_server", ErrInvalidRequest, definition.ID)
		}
	}
	return nil
}

func upsertToolRef(refs []agentcore.ToolRef, next agentcore.ToolRef) []agentcore.ToolRef {
	for i, ref := range refs {
		if strings.EqualFold(string(ref.ToolID), string(next.ToolID)) {
			refs[i] = next
			return refs
		}
	}
	return append(refs, next)
}

func upsertSkillRef(refs []agentcore.SkillRef, next agentcore.SkillRef) []agentcore.SkillRef {
	for i, ref := range refs {
		if strings.EqualFold(string(ref.SkillID), string(next.SkillID)) {
			refs[i] = next
			return refs
		}
	}
	return append(refs, next)
}

func removeToolRef(refs []agentcore.ToolRef, id agentcore.ToolID) []agentcore.ToolRef {
	out := refs[:0]
	for _, ref := range refs {
		if !strings.EqualFold(string(ref.ToolID), string(id)) {
			out = append(out, ref)
		}
	}
	return out
}

func removeToolRefAndMCPDownstreamRefs(refs []agentcore.ToolRef, id agentcore.ToolID) []agentcore.ToolRef {
	serverID := strings.TrimSpace(string(id))
	out := refs[:0]
	for _, ref := range refs {
		refID := strings.TrimSpace(string(ref.ToolID))
		if strings.EqualFold(refID, serverID) || strings.HasPrefix(refID, serverID+".") {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func removeSkillRef(refs []agentcore.SkillRef, id agentcore.SkillID) []agentcore.SkillRef {
	out := refs[:0]
	for _, ref := range refs {
		if !strings.EqualFold(string(ref.SkillID), string(id)) {
			out = append(out, ref)
		}
	}
	return out
}

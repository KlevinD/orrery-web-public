package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

type ToolSource string

const (
	ToolSourceInternal    ToolSource = "internal"
	ToolSourceExternalMCP ToolSource = "external_mcp"
)

type GatewayOptions struct {
	Registry                toolres.Registry
	Router                  *ToolRouter
	Profiles                AgentProfileToolSource
	Lifecycle               ToolCallLifecycle
	ResponsibilityAuthority ResponsibilityAuthorityResolver
}

type Gateway struct {
	registry                toolres.Registry
	router                  *ToolRouter
	profiles                AgentProfileToolSource
	lifecycle               ToolCallLifecycle
	responsibilityAuthority ResponsibilityAuthorityResolver
}

type ListToolsRequest struct {
	Mode                         string                           `json:"mode,omitempty"`
	Query                        string                           `json:"query,omitempty"`
	TopK                         int                              `json:"top_k,omitempty"`
	AgentID                      string                           `json:"agent_id,omitempty"`
	ProfileID                    string                           `json:"profile_id,omitempty"`
	ToolIDs                      []string                         `json:"tool_ids,omitempty"`
	RunID                        string                           `json:"run_id,omitempty"`
	ResponsibilityContext        *agentcore.ResponsibilityContext `json:"responsibility_context,omitempty"`
	ExecutionEnvelopeIDClaim     string                           `json:"-"`
	ExecutionEnvelopeDigestClaim string                           `json:"-"`
}

type ListToolsResult struct {
	Tools          []ToolDescriptor `json:"tools"`
	TotalAvailable int              `json:"total_available"`
	Filtered       bool             `json:"filtered"`
	Truncated      bool             `json:"truncated"`
	TopK           int              `json:"top_k,omitempty"`
	Notice         string           `json:"notice,omitempty"`
}

type ToolDescriptor struct {
	ToolID              string         `json:"tool_id"`
	Name                string         `json:"name"`
	Description         string         `json:"description,omitempty"`
	Source              ToolSource     `json:"source"`
	ServerToolID        string         `json:"server_tool_id,omitempty"`
	DownstreamToolName  string         `json:"downstream_tool_name,omitempty"`
	InputSchema         map[string]any `json:"input_schema,omitempty"`
	OutputSchema        map[string]any `json:"output_schema,omitempty"`
	RequiredPermissions []string       `json:"required_permissions,omitempty"`
	RiskLevel           string         `json:"risk_level,omitempty"`
	Enabled             bool           `json:"enabled"`
}

// ToolRiskLevel returns the registry-owned risk classification for a gateway
// tool. It intentionally ignores client-provided metadata and resolves an
// external tool ID back to its MCP server definition.
func (g *Gateway) ToolRiskLevel(ctx context.Context, toolID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if g == nil || g.registry == nil || strings.TrimSpace(toolID) == "" {
		return "", ErrServiceDependency
	}
	toolID = strings.TrimSpace(toolID)
	definitions, err := g.registry.List(ctx)
	if err != nil {
		return "", err
	}
	best := ""
	bestLen := -1
	for _, definition := range definitions {
		if !definition.Enabled {
			continue
		}
		id := strings.TrimSpace(string(definition.ID))
		if id == "" || (toolID != id && !strings.HasPrefix(toolID, id+".")) {
			continue
		}
		if len(id) > bestLen {
			best = string(definition.RiskLevel)
			bestLen = len(id)
		}
	}
	if best == "" {
		return "", ErrToolNotFound
	}
	return best, nil
}

type AgentProfileToolSource interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

// ResponsibilityExecutionAuthority binds an immutable responsibility snapshot
// to the server-owned run, tenant, Agent, and profile that may use it.
type ResponsibilityExecutionAuthority struct {
	RunID          string
	TenantID       string
	AgentID        string
	AgentProfileID string
	Context        *agentcore.ResponsibilityContext
	Envelope       *agentcore.ExecutionEnvelope
}

// ResponsibilityAuthorityResolver returns the server-owned execution authority
// for a run. Client headers are only identity assertions and never permission
// scope sources.
type ResponsibilityAuthorityResolver interface {
	ResolveResponsibilityExecution(ctx context.Context, runID string) (*ResponsibilityExecutionAuthority, error)
}

func NewGateway(opts GatewayOptions) *Gateway {
	return &Gateway{
		registry: opts.Registry, router: opts.Router, profiles: opts.Profiles, lifecycle: opts.Lifecycle,
		responsibilityAuthority: opts.ResponsibilityAuthority,
	}
}

func (g *Gateway) ListTools(ctx context.Context, req ListToolsRequest) ([]ToolDescriptor, error) {
	result, err := g.ListToolSet(ctx, req)
	if err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (g *Gateway) ListToolSet(ctx context.Context, req ListToolsRequest) (ListToolsResult, error) {
	if err := ctx.Err(); err != nil {
		return ListToolsResult{}, err
	}
	if g == nil || g.registry == nil {
		return ListToolsResult{}, ErrServiceDependency
	}
	authority, err := g.resolveResponsibilityExecution(ctx, req.RunID, req.ResponsibilityContext)
	if err != nil {
		return ListToolsResult{}, err
	}
	if authority == nil && (strings.TrimSpace(req.ExecutionEnvelopeIDClaim) != "" || strings.TrimSpace(req.ExecutionEnvelopeDigestClaim) != "") {
		return ListToolsResult{}, ErrPermissionDenied
	}
	if authority != nil {
		if !executionEnvelopeClaimsMatch(req.ExecutionEnvelopeIDClaim, req.ExecutionEnvelopeDigestClaim, authority.Envelope) {
			return ListToolsResult{}, ErrPermissionDenied
		}
		req.ResponsibilityContext = agentcore.CloneResponsibilityContext(authority.Context)
		if err := bindResponsibilityListIdentity(&req, authority); err != nil {
			return ListToolsResult{}, err
		}
	}
	definitions, err := g.registry.List(ctx)
	if err != nil {
		return ListToolsResult{}, err
	}
	tools := make([]ToolDescriptor, 0, len(definitions))
	for _, definition := range definitions {
		if !definition.Enabled {
			continue
		}
		switch definition.Kind {
		case toolres.KindInternalServiceTool:
			tools = append(tools, descriptorFromInternalTool(definition))
		case toolres.KindMCPServer:
			if isSystemMCPServer(definition) {
				continue
			}
			tools = append(tools, descriptorsFromExternalMCP(definition)...)
		}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].ToolID < tools[j].ToolID })
	toolIDs, err := g.configuredToolIDs(ctx, req)
	if err != nil {
		return ListToolsResult{}, err
	}
	tools = filterConfiguredTools(tools, toolIDs)
	tools = filterResponsibilityTools(tools, req.ResponsibilityContext)
	if authority != nil && authority.Envelope != nil {
		tools = filterExecutionEnvelopeTools(tools, authority.Envelope)
	}
	total := len(tools)
	result := ListToolsResult{Tools: tools, TotalAvailable: total}
	if shouldReturnAllTools(req, total) {
		return result, nil
	}
	topK := req.TopK
	if topK <= 0 {
		topK = 10
	}
	if topK > total {
		topK = total
	}
	ranked := bm25RankTools(tools, req.Query)
	if len(ranked) > topK {
		ranked = ranked[:topK]
	}
	result.Tools = ranked
	result.Filtered = true
	result.TopK = topK
	result.Truncated = len(ranked) < total
	result.Notice = filteredToolsNotice(len(ranked), total)
	return result, nil
}

func (g *Gateway) ListMCPServerToolSet(ctx context.Context) (ListToolsResult, error) {
	if err := ctx.Err(); err != nil {
		return ListToolsResult{}, err
	}
	if g == nil || g.registry == nil {
		return ListToolsResult{}, ErrServiceDependency
	}
	definitions, err := g.registry.ListByKind(ctx, toolres.KindMCPServer)
	if err != nil {
		return ListToolsResult{}, err
	}
	tools := make([]ToolDescriptor, 0, len(definitions))
	for _, definition := range definitions {
		if !definition.Enabled {
			continue
		}
		tools = append(tools, descriptorsFromMCPServerCatalog(definition)...)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].ToolID < tools[j].ToolID })
	return ListToolsResult{Tools: tools, TotalAvailable: len(tools)}, nil
}

func (g *Gateway) configuredToolIDs(ctx context.Context, req ListToolsRequest) ([]string, error) {
	profile, err := g.configuredProfile(ctx, req.ProfileID, req.AgentID)
	if err != nil {
		return nil, err
	}
	if profile.ID.IsZero() && listRequiresProfileBinding(req) {
		return []string{nonexistentConfiguredToolID}, nil
	}
	if !listRequiresProfileBinding(req) && len(req.ToolIDs) > 0 {
		return req.ToolIDs, nil
	}
	if profile.ID.IsZero() {
		return nil, nil
	}
	out := make([]string, 0, len(profile.ToolRefs))
	for _, ref := range profile.ToolRefs {
		if id := strings.TrimSpace(string(ref.ToolID)); id != "" {
			out = append(out, id)
		}
	}
	out = appendSkillLoaderToolIDs(out)
	return out, nil
}

const nonexistentConfiguredToolID = "\x00__no_configured_tools__"

func listRequiresProfileBinding(req ListToolsRequest) bool {
	return strings.TrimSpace(req.AgentID) != "" || strings.TrimSpace(req.ProfileID) != ""
}

func (g *Gateway) configuredProfile(ctx context.Context, profileID string, agentID string) (agentcore.AgentProfile, error) {
	profileID = strings.TrimSpace(profileID)
	agentID = strings.TrimSpace(agentID)
	if profileID == "" && agentID == "" {
		return agentcore.AgentProfile{}, nil
	}
	if g == nil || g.profiles == nil {
		return agentcore.AgentProfile{}, nil
	}
	if profileID == "" {
		resolved, err := profileIDForAgent(ctx, g.profiles, agentID)
		if err != nil {
			return agentcore.AgentProfile{}, err
		}
		profileID = resolved
	}
	if profileID == "" {
		return agentcore.AgentProfile{}, nil
	}
	return g.profiles.GetProfile(ctx, agentcore.AgentProfileID(profileID))
}

func profileIDForAgent(ctx context.Context, profiles AgentProfileToolSource, agentID string) (string, error) {
	agentID = strings.TrimSpace(agentID)
	page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		IncludeAI:                 true,
		IncludeHumans:             true,
		IncludePersonalAssistants: true,
	})
	if err != nil {
		return "", err
	}
	humanProfileID := humanProfileIDForViewer(agentID)
	for _, entry := range page.Entries {
		if strings.EqualFold(entry.AgentProfileID.String(), agentID) ||
			strings.EqualFold(entry.AgentInstanceID.String(), agentID) {
			return entry.AgentProfileID.String(), nil
		}
		if humanProfileID != "" && strings.EqualFold(entry.AgentProfileID.String(), humanProfileID) {
			return entry.AgentProfileID.String(), nil
		}
	}
	return "", nil
}

func humanProfileIDForViewer(viewerID string) string {
	viewerID = strings.ToLower(strings.TrimSpace(viewerID))
	if viewerID == "" || strings.HasPrefix(viewerID, "human-") {
		return viewerID
	}
	var b strings.Builder
	lastDash := false
	for _, r := range viewerID {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return ""
	}
	return "human-" + out
}

func (g *Gateway) CallTool(ctx context.Context, req CallToolRequest) (CallToolResult, error) {
	if g == nil || g.router == nil {
		return CallToolResult{}, ErrServiceDependency
	}
	if err := ctx.Err(); err != nil {
		return CallToolResult{}, err
	}
	authority, err := g.resolveResponsibilityExecution(ctx, req.RunID, req.ResponsibilityContext)
	if err != nil {
		return CallToolResult{}, err
	}
	if authority != nil {
		if req.ExecutionEnvelope != nil && !sameExecutionEnvelopeIdentity(req.ExecutionEnvelope, authority.Envelope) {
			return CallToolResult{}, ErrPermissionDenied
		}
		if !executionEnvelopeClaimsMatch(req.ExecutionEnvelopeIDClaim, req.ExecutionEnvelopeDigestClaim, authority.Envelope) {
			return CallToolResult{}, ErrPermissionDenied
		}
		req.ResponsibilityContext = agentcore.CloneResponsibilityContext(authority.Context)
		req.ExecutionEnvelope = agentcore.CloneExecutionEnvelope(authority.Envelope)
		if err := bindResponsibilityCallIdentity(&req, authority); err != nil {
			return CallToolResult{}, err
		}
	}
	if enriched, err := g.enrichCallWithProfileBinding(ctx, req); err != nil {
		return CallToolResult{}, err
	} else {
		req = enriched
	}
	var attempt ToolCallAttempt
	if g.lifecycle != nil {
		started, err := g.lifecycle.StartToolCall(ctx, g.toolCallLifecycleStart(ctx, req))
		if err != nil {
			return CallToolResult{}, fmt.Errorf("start MCP tool call lifecycle: %w", err)
		}
		if started == nil {
			return CallToolResult{}, fmt.Errorf("start MCP tool call lifecycle: %w", ErrServiceDependency)
		}
		attempt = started
	}

	result, callErr := g.router.CallTool(ctx, req)
	if attempt == nil {
		return result, callErr
	}
	finalizeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if callErr != nil {
		if err := attempt.FailToolCall(finalizeCtx, toolCallFailure(callErr)); err != nil {
			return result, errors.Join(callErr, fmt.Errorf("fail MCP tool call lifecycle: %w", err))
		}
		return result, callErr
	}
	if err := attempt.CompleteToolCall(finalizeCtx, result); err != nil {
		return result, fmt.Errorf("%w: %w", ErrToolCallOutcomeUnrecorded, err)
	}
	return result, nil
}

func (g *Gateway) resolveResponsibilityExecution(ctx context.Context, runID string, claimed *agentcore.ResponsibilityContext) (*ResponsibilityExecutionAuthority, error) {
	runID = strings.TrimSpace(runID)
	if g == nil || g.responsibilityAuthority == nil {
		if err := validateResponsibilityExecutionIdentity(runID, claimed); err != nil {
			return nil, err
		}
		if claimed == nil {
			return nil, nil
		}
		return &ResponsibilityExecutionAuthority{
			RunID: runID, AgentProfileID: strings.TrimSpace(claimed.AgentProfileID), Context: agentcore.CloneResponsibilityContext(claimed),
		}, nil
	}
	if runID == "" {
		if claimed != nil {
			return nil, ErrInvalidRequest
		}
		return nil, nil
	}
	authoritative, err := g.responsibilityAuthority.ResolveResponsibilityExecution(ctx, runID)
	if err != nil {
		return nil, err
	}
	if authoritative == nil {
		if claimed != nil {
			return nil, ErrPermissionDenied
		}
		return nil, nil
	}
	if err := validateResponsibilityExecutionAuthority(runID, authoritative); err != nil {
		return nil, err
	}
	if claimed != nil && !sameResponsibilityExecutionIdentity(claimed, authoritative.Context) {
		return nil, ErrPermissionDenied
	}
	return cloneResponsibilityExecutionAuthority(authoritative), nil
}

func validateResponsibilityExecutionAuthority(runID string, authority *ResponsibilityExecutionAuthority) error {
	if authority == nil || strings.TrimSpace(authority.RunID) != strings.TrimSpace(runID) ||
		strings.TrimSpace(authority.TenantID) == "" || strings.TrimSpace(authority.AgentID) == "" ||
		strings.TrimSpace(authority.AgentProfileID) == "" || (authority.Context == nil && authority.Envelope == nil) {
		return ErrInvalidRequest
	}
	if authority.Context != nil {
		if err := validateResponsibilityExecutionIdentity(runID, authority.Context); err != nil {
			return err
		}
		if strings.TrimSpace(authority.Context.AgentProfileID) != strings.TrimSpace(authority.AgentProfileID) {
			return ErrInvalidRequest
		}
	}
	if authority.Envelope != nil {
		if err := authority.Envelope.Verify(); err != nil {
			return ErrInvalidRequest
		}
		if !authority.Envelope.ExpiresAt.IsZero() && !authority.Envelope.ExpiresAt.After(time.Now().UTC()) {
			return ErrInvalidRequest
		}
		if strings.TrimSpace(authority.Envelope.RunID) != strings.TrimSpace(runID) ||
			strings.TrimSpace(authority.Envelope.TenantID) != strings.TrimSpace(authority.TenantID) ||
			strings.TrimSpace(authority.Envelope.AgentID) != strings.TrimSpace(authority.AgentID) ||
			strings.TrimSpace(authority.Envelope.AgentProfileID) != strings.TrimSpace(authority.AgentProfileID) {
			return ErrInvalidRequest
		}
	}
	return nil
}

func bindResponsibilityListIdentity(req *ListToolsRequest, authority *ResponsibilityExecutionAuthority) error {
	if req == nil || authority == nil {
		return nil
	}
	if err := requireResponsibilityIdentity(&req.AgentID, authority.AgentID); err != nil {
		return err
	}
	return bindResponsibilityIdentity(&req.ProfileID, authority.AgentProfileID)
}

func bindResponsibilityCallIdentity(req *CallToolRequest, authority *ResponsibilityExecutionAuthority) error {
	if req == nil || authority == nil {
		return nil
	}
	if err := requireResponsibilityIdentity(&req.TenantID, authority.TenantID); err != nil {
		return err
	}
	if err := requireResponsibilityIdentity(&req.AgentID, authority.AgentID); err != nil {
		return err
	}
	principalID := authority.AgentID
	// Ordinary personal-assistant runs execute for a user. The server-owned
	// envelope binds that user separately from the technical Agent identity.
	// Responsibility tasks continue to execute as the assigned Agent.
	if authority.Context == nil && authority.Envelope != nil && strings.TrimSpace(authority.Envelope.PrincipalID) != "" {
		principalID = authority.Envelope.PrincipalID
		if strings.TrimSpace(req.PrincipalKind) != string(authority.Envelope.PrincipalKind) {
			return ErrPermissionDenied
		}
	}
	if err := requireResponsibilityIdentity(&req.PrincipalID, principalID); err != nil {
		return err
	}
	return bindResponsibilityIdentity(&req.ProfileID, authority.AgentProfileID)
}

func requireResponsibilityIdentity(target *string, authoritative string) error {
	authoritative = strings.TrimSpace(authoritative)
	if authoritative == "" {
		return nil
	}
	claimed := strings.TrimSpace(*target)
	if claimed == "" || claimed != authoritative {
		return ErrPermissionDenied
	}
	*target = authoritative
	return nil
}

func bindResponsibilityIdentity(target *string, authoritative string) error {
	authoritative = strings.TrimSpace(authoritative)
	if authoritative == "" {
		return nil
	}
	claimed := strings.TrimSpace(*target)
	if claimed != "" && claimed != authoritative {
		return ErrPermissionDenied
	}
	*target = authoritative
	return nil
}

func cloneResponsibilityExecutionAuthority(value *ResponsibilityExecutionAuthority) *ResponsibilityExecutionAuthority {
	if value == nil {
		return nil
	}
	clone := *value
	clone.Context = agentcore.CloneResponsibilityContext(value.Context)
	clone.Envelope = agentcore.CloneExecutionEnvelope(value.Envelope)
	return &clone
}

func sameExecutionEnvelopeIdentity(claimed, authoritative *agentcore.ExecutionEnvelope) bool {
	if claimed == nil || authoritative == nil {
		return claimed == authoritative
	}
	return strings.TrimSpace(claimed.EnvelopeID) == strings.TrimSpace(authoritative.EnvelopeID) &&
		strings.TrimSpace(claimed.EnvelopeDigest) == strings.TrimSpace(authoritative.EnvelopeDigest)
}

func executionEnvelopeClaimsMatch(id, digest string, authoritative *agentcore.ExecutionEnvelope) bool {
	if strings.TrimSpace(id) == "" && strings.TrimSpace(digest) == "" {
		return true
	}
	if authoritative == nil {
		return false
	}
	return (strings.TrimSpace(id) == "" || strings.TrimSpace(id) == strings.TrimSpace(authoritative.EnvelopeID)) &&
		(strings.TrimSpace(digest) == "" || strings.TrimSpace(digest) == strings.TrimSpace(authoritative.EnvelopeDigest))
}

func filterExecutionEnvelopeTools(tools []ToolDescriptor, envelope *agentcore.ExecutionEnvelope) []ToolDescriptor {
	if envelope == nil {
		return tools
	}
	filtered := make([]ToolDescriptor, 0, len(tools))
	for _, tool := range tools {
		if envelope.AllowsTool(tool.ToolID, tool.RequiredPermissions) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func sameResponsibilityExecutionIdentity(claimed, authoritative *agentcore.ResponsibilityContext) bool {
	if claimed == nil || authoritative == nil {
		return claimed == authoritative
	}
	if strings.TrimSpace(claimed.ContextID) != strings.TrimSpace(authoritative.ContextID) ||
		strings.TrimSpace(claimed.ResponsibilityID) != strings.TrimSpace(authoritative.ResponsibilityID) ||
		claimed.ResponsibilityVersion != authoritative.ResponsibilityVersion ||
		strings.TrimSpace(claimed.PolicyID) != strings.TrimSpace(authoritative.PolicyID) ||
		claimed.PolicyVersion != authoritative.PolicyVersion {
		return false
	}
	claimedProfile := strings.TrimSpace(claimed.AgentProfileID)
	return claimedProfile == "" || claimedProfile == strings.TrimSpace(authoritative.AgentProfileID)
}

func (g *Gateway) toolCallLifecycleStart(ctx context.Context, req CallToolRequest) ToolCallLifecycleStart {
	start := ToolCallLifecycleStart{
		TenantID:              strings.TrimSpace(req.TenantID),
		RunID:                 strings.TrimSpace(req.RunID),
		ToolID:                strings.TrimSpace(req.ToolID),
		PrincipalID:           strings.TrimSpace(req.PrincipalID),
		AgentID:               strings.TrimSpace(req.AgentID),
		ProfileID:             strings.TrimSpace(req.ProfileID),
		Arguments:             cloneArguments(req.Arguments),
		CorrelationID:         auditCorrelationID(req.Arguments),
		ResponsibilityContext: agentcore.CloneResponsibilityContext(req.ResponsibilityContext),
		Maintenance:           req.Maintenance,
	}
	definition, downstreamToolName, source, err := g.router.resolveTool(ctx, start.ToolID)
	if err != nil {
		return start
	}
	start.Source = source
	start.DownstreamToolName = downstreamToolName
	if source == ToolSourceExternalMCP {
		start.ServerToolID = strings.TrimSpace(string(definition.ID))
	}
	return start
}

func (g *Gateway) enrichCallWithProfileBinding(ctx context.Context, req CallToolRequest) (CallToolRequest, error) {
	if req.Maintenance && isMemoryDiaryMaintenanceToolID(req.ToolID) {
		if !validDiaryMaintenanceIdentity(req) {
			return req, ErrPermissionDenied
		}
		return req, nil
	}
	profile, err := g.configuredProfile(ctx, req.ProfileID, req.AgentID)
	if err != nil {
		return req, err
	}
	if profile.ID.IsZero() && callRequiresProfileBinding(req) {
		return req, ErrToolNotFound
	}
	if profile.ID.IsZero() {
		return req, nil
	}
	if !g.personalAssistantPrincipalOwnsProfile(ctx, profile, req) {
		return req, ErrPermissionDenied
	}
	req.ProfileID = strings.TrimSpace(profile.ID.String())
	toolID := strings.TrimSpace(req.ToolID)
	if toolID == "" {
		return req, nil
	}
	allowed := false
	permissions := append([]string(nil), req.Permissions...)
	if isSkillToolID(toolID) {
		allowed = true
		if required, ok := g.permissionsForSystemMCPInternalTool(ctx, toolID); ok && !req.PermissionsAuthoritative {
			permissions = appendMissingPermissions(permissions, required)
		}
		req.SkillBindings = appendMissingSkillBindings(req.SkillBindings, runtimeSkillBindingsFromProfile(profile))
	}
	for _, ref := range profile.ToolRefs {
		refID := strings.TrimSpace(string(ref.ToolID))
		if refID == "" {
			continue
		}
		if refID == string(toolstd.SystemMCPServer) {
			if !requiresExplicitToolBinding(toolID) {
				if required, ok := g.permissionsForSystemMCPInternalTool(ctx, toolID); ok {
					allowed = true
					if !req.PermissionsAuthoritative {
						permissions = appendMissingPermissions(permissions, required)
					}
				}
			}
		}
		if refID == toolID || strings.HasPrefix(toolID, refID+".") {
			allowed = true
			if !req.PermissionsAuthoritative {
				permissions = appendMissingPermissions(permissions, []string{ref.PermissionScope})
			}
		}
	}
	if !allowed {
		return req, ErrToolNotFound
	}
	req.Permissions = permissions
	if !req.PermissionsAuthoritative {
		req.MCPAuthorizations = appendMissingMCPAuthorizations(req.MCPAuthorizations, profile.MCPAuthorizations)
	}
	return req, nil
}

func (g *Gateway) personalAssistantPrincipalOwnsProfile(ctx context.Context, profile agentcore.AgentProfile, req CallToolRequest) bool {
	if !profile.IsPersonalAssistant() {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(req.TenantID), strings.TrimSpace(profile.PersonalAssistantTenantID)) ||
		agentcore.ExecutionPrincipalKind(strings.TrimSpace(req.PrincipalKind)) != agentcore.ExecutionPrincipalUser {
		return false
	}
	ownerID := strings.TrimSpace(profile.PersonalAssistantOwnerID)
	principalID := strings.TrimSpace(req.PrincipalID)
	if ownerID == "" || principalID == "" {
		return false
	}
	if strings.EqualFold(ownerID, principalID) {
		return true
	}
	principal, err := g.profiles.GetProfile(ctx, agentcore.AgentProfileID(principalID))
	if err != nil || !principal.Enabled || principal.AgentKind != agentcore.AgentKindHuman {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(principal.ID.String()), ownerID) ||
		strings.EqualFold(strings.TrimSpace(principal.UserAccount.Username), ownerID)
}

func callRequiresProfileBinding(req CallToolRequest) bool {
	return strings.TrimSpace(req.AgentID) != "" || strings.TrimSpace(req.ProfileID) != ""
}

func requiresExplicitToolBinding(toolID string) bool {
	return strings.TrimSpace(toolID) == string(toolstd.PersonalAssistantShellExec) ||
		strings.TrimSpace(toolID) == string(toolstd.EmailSend) ||
		strings.TrimSpace(toolID) == string(toolstd.MemoryDiaryWrite) ||
		strings.TrimSpace(toolID) == string(toolstd.RSILearningPlan) ||
		toolstd.IsPersonalBrowserTool(strings.TrimSpace(toolID)) ||
		isMemoryDiaryMaintenanceToolID(toolID)
}

func appendSkillLoaderToolIDs(current []string) []string {
	for _, id := range []string{
		string(toolstd.SkillList),
		string(toolstd.SkillLoad),
		string(toolstd.SkillResourceList),
		string(toolstd.SkillResourceLoad),
	} {
		if !containsString(current, id) {
			current = append(current, id)
		}
	}
	return current
}

func runtimeSkillBindingsFromProfile(profile agentcore.AgentProfile) []agentcore.RuntimeSkillBinding {
	if len(profile.SkillRefs) == 0 {
		return nil
	}
	out := make([]agentcore.RuntimeSkillBinding, 0, len(profile.SkillRefs))
	for _, ref := range profile.SkillRefs {
		id := strings.TrimSpace(string(ref.SkillID))
		if id == "" {
			continue
		}
		out = append(out, agentcore.RuntimeSkillBinding{
			SkillID: id,
			Alias:   strings.TrimSpace(ref.Alias),
		})
	}
	return out
}

func appendMissingSkillBindings(current []agentcore.RuntimeSkillBinding, extras []agentcore.RuntimeSkillBinding) []agentcore.RuntimeSkillBinding {
	seen := map[string]bool{}
	for _, binding := range current {
		if strings.TrimSpace(binding.SkillID) != "" {
			seen[strings.TrimSpace(binding.SkillID)] = true
		}
	}
	for _, binding := range extras {
		id := strings.TrimSpace(binding.SkillID)
		if id == "" || seen[id] {
			continue
		}
		current = append(current, binding)
		seen[id] = true
	}
	return current
}

func appendMissingMCPAuthorizations(current []agentcore.MCPAuthorization, extras []agentcore.MCPAuthorization) []agentcore.MCPAuthorization {
	seen := map[string]bool{}
	for _, item := range current {
		if id := strings.TrimSpace(string(item.ServerToolID)); id != "" {
			seen[strings.ToLower(id)] = true
		}
	}
	for _, item := range extras {
		id := strings.TrimSpace(string(item.ServerToolID))
		if id == "" || strings.TrimSpace(item.CredentialValue) == "" {
			continue
		}
		key := strings.ToLower(id)
		if seen[key] {
			continue
		}
		current = append(current, item)
		seen[key] = true
	}
	return current
}

func (g *Gateway) permissionsForSystemMCPInternalTool(ctx context.Context, toolID string) ([]string, bool) {
	if g == nil || g.registry == nil {
		return nil, false
	}
	definition, err := g.registry.Get(ctx, agentcore.ToolID(toolID))
	if err != nil || definition.Kind != toolres.KindInternalServiceTool || !definition.Enabled {
		return nil, false
	}
	return requiredPermissions(definition.Permissions), true
}

func descriptorFromInternalTool(definition toolres.ToolDefinition) ToolDescriptor {
	return ToolDescriptor{
		ToolID:              string(definition.ID),
		Name:                definition.Name,
		Description:         definition.Description,
		Source:              ToolSourceInternal,
		InputSchema:         cloneSchema(definition.InputSchema),
		OutputSchema:        cloneSchema(definition.OutputSchema),
		RequiredPermissions: requiredPermissions(definition.Permissions),
		RiskLevel:           string(definition.RiskLevel),
		Enabled:             definition.Enabled,
	}
}

func descriptorsFromExternalMCP(definition toolres.ToolDefinition) []ToolDescriptor {
	if definition.MCP == nil {
		return nil
	}
	tools := make([]ToolDescriptor, 0, len(definition.MCP.ExposedTools)+len(definition.MCP.ExposedToolDetails))
	seen := map[string]bool{}
	for _, exposed := range definition.MCP.ExposedToolDetails {
		name := strings.TrimSpace(exposed.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		tools = append(tools, descriptorFromExternalMCPTool(definition, name, strings.TrimSpace(exposed.Description), exposed.InputSchema, exposed.OutputSchema))
	}
	for _, exposed := range definition.MCP.ExposedTools {
		exposed = strings.TrimSpace(exposed)
		if exposed == "" || seen[exposed] {
			continue
		}
		seen[exposed] = true
		tools = append(tools, descriptorFromExternalMCPTool(definition, exposed, "", nil, nil))
	}
	return tools
}

func descriptorsFromMCPServerCatalog(definition toolres.ToolDefinition) []ToolDescriptor {
	if definition.MCP == nil {
		return nil
	}
	tools := make([]ToolDescriptor, 0, len(definition.MCP.ExposedTools)+len(definition.MCP.ExposedToolDetails))
	seen := map[string]bool{}
	for _, exposed := range definition.MCP.ExposedToolDetails {
		name := strings.TrimSpace(exposed.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		tools = append(tools, descriptorFromMCPServerCatalogTool(definition, name, strings.TrimSpace(exposed.Description), exposed.InputSchema, exposed.OutputSchema))
	}
	for _, exposed := range definition.MCP.ExposedTools {
		exposed = strings.TrimSpace(exposed)
		if exposed == "" || seen[exposed] {
			continue
		}
		seen[exposed] = true
		tools = append(tools, descriptorFromMCPServerCatalogTool(definition, exposed, "", nil, nil))
	}
	return tools
}

func descriptorFromExternalMCPTool(definition toolres.ToolDefinition, exposed string, description string, inputSchema map[string]any, outputSchema map[string]any) ToolDescriptor {
	return ToolDescriptor{
		ToolID:              externalGatewayToolID(string(definition.ID), exposed),
		Name:                fmt.Sprintf("%s: %s", definition.Name, exposed),
		Description:         firstNonBlank(description, definition.Description),
		Source:              ToolSourceExternalMCP,
		ServerToolID:        string(definition.ID),
		DownstreamToolName:  exposed,
		InputSchema:         firstSchema(inputSchema, genericExternalMCPInputSchema()),
		OutputSchema:        firstSchema(outputSchema, genericExternalMCPOutputSchema()),
		RequiredPermissions: requiredPermissions(definition.Permissions),
		RiskLevel:           string(definition.RiskLevel),
		Enabled:             definition.Enabled,
	}
}

func descriptorFromMCPServerCatalogTool(definition toolres.ToolDefinition, exposed string, description string, inputSchema map[string]any, outputSchema map[string]any) ToolDescriptor {
	toolID := externalGatewayToolID(string(definition.ID), exposed)
	if isSystemMCPServer(definition) {
		toolID = strings.TrimSpace(exposed)
	}
	return ToolDescriptor{
		ToolID:              toolID,
		Name:                fmt.Sprintf("%s: %s", definition.Name, exposed),
		Description:         firstNonBlank(description, definition.Description),
		Source:              ToolSourceExternalMCP,
		ServerToolID:        string(definition.ID),
		DownstreamToolName:  exposed,
		InputSchema:         firstSchema(inputSchema, genericExternalMCPInputSchema()),
		OutputSchema:        firstSchema(outputSchema, genericExternalMCPOutputSchema()),
		RequiredPermissions: requiredPermissions(definition.Permissions),
		RiskLevel:           string(definition.RiskLevel),
		Enabled:             definition.Enabled,
	}
}

func externalGatewayToolID(serverID string, downstreamToolName string) string {
	return strings.TrimSpace(serverID) + "." + strings.TrimSpace(downstreamToolName)
}

func requiredPermissions(declarations []toolres.PermissionDeclaration) []string {
	out := make([]string, 0, len(declarations))
	for _, declaration := range declarations {
		if strings.TrimSpace(declaration.Scope) == "" {
			continue
		}
		out = append(out, declaration.Scope)
	}
	return out
}

func appendMissingPermissions(current []string, scopes []string) []string {
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || containsString(current, scope) {
			continue
		}
		current = append(current, scope)
	}
	return current
}

func genericExternalMCPInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"description":          "Arguments forwarded to the registered downstream MCP tool.",
	}
}

func genericExternalMCPOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"description":          "Result returned by the registered downstream MCP tool.",
	}
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

func firstSchema(values ...map[string]any) map[string]any {
	for _, value := range values {
		if value != nil {
			return cloneSchema(value)
		}
	}
	return nil
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func filterConfiguredTools(tools []ToolDescriptor, toolIDs []string) []ToolDescriptor {
	if len(toolIDs) == 0 {
		return tools
	}
	allowed := map[string]bool{}
	for _, id := range toolIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			allowed[id] = true
		}
	}
	if len(allowed) == 0 {
		return tools
	}
	out := make([]ToolDescriptor, 0, len(tools))
	for _, tool := range tools {
		if allowed[tool.ToolID] || allowed[tool.ServerToolID] || (allowed[string(toolstd.SystemMCPServer)] && tool.Source == ToolSourceInternal && !requiresExplicitToolBinding(tool.ToolID)) {
			out = append(out, tool)
		}
	}
	return out
}

func filterResponsibilityTools(tools []ToolDescriptor, responsibility *agentcore.ResponsibilityContext) []ToolDescriptor {
	if responsibility == nil {
		return tools
	}
	out := make([]ToolDescriptor, 0, len(tools))
	for _, descriptor := range tools {
		if responsibilityAuthorityAllowsTool(responsibility, descriptor.ToolID, descriptor.ServerToolID, descriptor.RequiredPermissions) {
			out = append(out, descriptor)
		}
	}
	return out
}

func isSystemMCPServer(definition toolres.ToolDefinition) bool {
	return definition.ID == toolstd.SystemMCPServer
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func shouldReturnAllTools(req ListToolsRequest, total int) bool {
	mode := strings.TrimSpace(strings.ToLower(req.Mode))
	if mode == "" || mode == "all" {
		return true
	}
	if total < 10 {
		return true
	}
	return strings.TrimSpace(req.Query) == ""
}

func filteredToolsNotice(returned, total int) string {
	if returned == 0 {
		return "No tools matched the current query. This is not the full tool list; retry with mode=all to inspect all available tools."
	}
	return fmt.Sprintf("Returned %d of %d available tools using BM25 preselection; this is not all available tools. Retry with mode=all if no returned tool fits.", returned, total)
}

var bm25TokenRE = regexp.MustCompile(`[a-zA-Z0-9_]+`)

func bm25RankTools(tools []ToolDescriptor, query string) []ToolDescriptor {
	queryTerms := tokenizeBM25(query)
	if len(queryTerms) == 0 {
		return nil
	}
	docs := make([][]string, len(tools))
	docFreq := map[string]int{}
	totalLength := 0
	for i, tool := range tools {
		docs[i] = tokenizeBM25(toolBM25Text(tool))
		totalLength += len(docs[i])
		seen := map[string]bool{}
		for _, token := range docs[i] {
			if seen[token] {
				continue
			}
			seen[token] = true
			docFreq[token]++
		}
	}
	avgLength := 0.0
	if len(docs) > 0 {
		avgLength = float64(totalLength) / float64(len(docs))
	}
	type scoredTool struct {
		tool  ToolDescriptor
		score float64
	}
	scored := make([]scoredTool, 0, len(tools))
	for i, tool := range tools {
		score := bm25Score(docs[i], queryTerms, docFreq, len(tools), avgLength)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredTool{tool: tool, score: score})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].tool.ToolID < scored[j].tool.ToolID
		}
		return scored[i].score > scored[j].score
	})
	out := make([]ToolDescriptor, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.tool)
	}
	return out
}

func toolBM25Text(tool ToolDescriptor) string {
	parts := []string{
		tool.ToolID,
		tool.Name,
		tool.Description,
		string(tool.Source),
		tool.ServerToolID,
		tool.DownstreamToolName,
		tool.RiskLevel,
		strings.Join(tool.RequiredPermissions, " "),
	}
	return strings.Join(parts, " ")
}

func tokenizeBM25(text string) []string {
	matches := bm25TokenRE.FindAllString(strings.ToLower(text), -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		for _, token := range strings.Split(match, "_") {
			token = strings.TrimSpace(token)
			if token != "" {
				out = append(out, token)
			}
		}
	}
	return out
}

func bm25Score(doc []string, queryTerms []string, docFreq map[string]int, docCount int, avgLength float64) float64 {
	if len(doc) == 0 || len(queryTerms) == 0 || docCount == 0 {
		return 0
	}
	tf := map[string]int{}
	for _, token := range doc {
		tf[token]++
	}
	const k1 = 1.5
	const b = 0.75
	var score float64
	docLength := float64(len(doc))
	if avgLength <= 0 {
		avgLength = docLength
	}
	seenQuery := map[string]bool{}
	for _, term := range queryTerms {
		if seenQuery[term] {
			continue
		}
		seenQuery[term] = true
		frequency := tf[term]
		if frequency == 0 {
			continue
		}
		df := docFreq[term]
		idf := math.Log(1 + (float64(docCount-df)+0.5)/(float64(df)+0.5))
		numerator := float64(frequency) * (k1 + 1)
		denominator := float64(frequency) + k1*(1-b+b*(docLength/avgLength))
		score += idf * numerator / denominator
	}
	return score
}

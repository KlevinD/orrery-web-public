package assembly

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/toolbridge"
	"orrery_v2/internal/resource/tool"
)

type EffectiveResourceBinderOptions struct {
	ToolResolver  ToolResolver
	SkillResolver SkillResolver
}

type EffectiveResourceBinder struct {
	tools  ToolResolver
	skills SkillResolver
}

type EffectiveResourceBindRequest struct {
	Profile     agentcore.AgentProfile
	AgentID     agentcore.AgentID
	Permissions []string
}

type EffectiveResourceBinding struct {
	Tools  []agentcore.ToolBinding
	Skills []agentcore.SkillBinding
}

func NewEffectiveResourceBinder(opts EffectiveResourceBinderOptions) EffectiveResourceBinder {
	return EffectiveResourceBinder{tools: opts.ToolResolver, skills: opts.SkillResolver}
}

func (b EffectiveResourceBinder) Bind(ctx context.Context, req EffectiveResourceBindRequest) (EffectiveResourceBinding, error) {
	if err := ctx.Err(); err != nil {
		return EffectiveResourceBinding{}, err
	}
	skills, requiredTools, err := b.bindSkills(ctx, req.Profile)
	if err != nil {
		return EffectiveResourceBinding{}, err
	}
	toolRefs := mergeToolRefs(req.Profile.ToolRefs, requiredTools)
	tools, err := b.bindTools(ctx, req.Profile, req.AgentID, req.Permissions, toolRefs)
	if err != nil {
		return EffectiveResourceBinding{}, err
	}
	return EffectiveResourceBinding{Tools: tools, Skills: skills}, nil
}

func (b EffectiveResourceBinder) bindTools(ctx context.Context, profile agentcore.AgentProfile, agentID agentcore.AgentID, permissions []string, refs []agentcore.ToolRef) ([]agentcore.ToolBinding, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if b.tools == nil {
		return nil, newError(CodeToolBindingDenied, profile.ID, "tool_refs", "tool resolver is not configured", nil)
	}
	out := make([]agentcore.ToolBinding, 0, len(refs))
	for _, ref := range refs {
		refPermissions := append([]string(nil), permissions...)
		if scope := strings.TrimSpace(ref.PermissionScope); scope != "" {
			refPermissions = append(refPermissions, scope)
		}
		resolved, err := b.tools.Resolve(ctx, ToolResolveRequest{
			Ref:       ref,
			Principal: toolPrincipal(agentID, refPermissions),
		})
		if err != nil {
			return nil, newError(CodeToolBindingDenied, profile.ID, "tool_refs", "tool binding denied", err)
		}
		secretEnv := map[string]string{}
		for _, secret := range resolved.SecretBindingRefs {
			if secret.Env != "" {
				secretEnv[secret.Env] = secret.SecretName
			}
		}
		out = append(out, runtimeToolBindingsForResolvedTool(resolved, secretEnv)...)
	}
	return out, nil
}

func runtimeToolBindingsForResolvedTool(resolved tool.ResolvedTool, secretEnv map[string]string) []agentcore.ToolBinding {
	if resolved.Definition.Kind == tool.KindMCPServer && resolved.Definition.MCP != nil {
		return runtimeToolBindingsForMCPServer(resolved, secretEnv)
	}
	return []agentcore.ToolBinding{{
		ToolID:             resolved.Ref.ToolID,
		Alias:              resolved.Ref.Alias,
		Version:            resolved.Definition.Version,
		Name:               resolved.Definition.Name,
		Description:        resolved.Definition.Description,
		GrantedPermissions: append([]string(nil), resolved.GrantedPermissions...),
		SecretEnv:          cloneStringMap(secretEnv),
		Summary:            resolved.Definition.Description,
		InputSchema:        cloneAnyMap(resolved.Definition.InputSchema),
		OutputSchema:       cloneAnyMap(resolved.Definition.OutputSchema),
	}}
}

func runtimeToolBindingsForMCPServer(resolved tool.ResolvedTool, secretEnv map[string]string) []agentcore.ToolBinding {
	definition := resolved.Definition
	if requested := requestedMCPDownstreamToolName(resolved.Ref.ToolID, definition.ID); requested != "" {
		if binding, ok := runtimeToolBindingForMCPTool(resolved, secretEnv, requested); ok {
			return []agentcore.ToolBinding{binding}
		}
		return nil
	}
	seen := map[string]bool{}
	out := make([]agentcore.ToolBinding, 0, len(definition.MCP.ExposedToolDetails)+len(definition.MCP.ExposedTools))
	appendTool := func(name string, description string, inputSchema map[string]any, outputSchema map[string]any) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, newMCPRuntimeToolBinding(resolved, secretEnv, name, description, inputSchema, outputSchema))
	}
	for _, exposed := range definition.MCP.ExposedToolDetails {
		appendTool(exposed.Name, exposed.Description, exposed.InputSchema, exposed.OutputSchema)
	}
	for _, exposed := range definition.MCP.ExposedTools {
		appendTool(exposed, "", nil, nil)
	}
	return out
}

func runtimeToolBindingForMCPTool(resolved tool.ResolvedTool, secretEnv map[string]string, name string) (agentcore.ToolBinding, bool) {
	definition := resolved.Definition
	name = strings.TrimSpace(name)
	if name == "" || definition.MCP == nil {
		return agentcore.ToolBinding{}, false
	}
	for _, exposed := range definition.MCP.ExposedToolDetails {
		if strings.TrimSpace(exposed.Name) == name {
			return newMCPRuntimeToolBinding(resolved, secretEnv, name, exposed.Description, exposed.InputSchema, exposed.OutputSchema), true
		}
	}
	for _, exposed := range definition.MCP.ExposedTools {
		if strings.TrimSpace(exposed) == name {
			return newMCPRuntimeToolBinding(resolved, secretEnv, name, "", nil, nil), true
		}
	}
	return agentcore.ToolBinding{}, false
}

func newMCPRuntimeToolBinding(resolved tool.ResolvedTool, secretEnv map[string]string, name string, description string, inputSchema map[string]any, outputSchema map[string]any) agentcore.ToolBinding {
	definition := resolved.Definition
	toolID := agentcore.ToolID(strings.TrimSpace(string(definition.ID)) + "." + name)
	return agentcore.ToolBinding{
		ToolID:             toolID,
		Alias:              safeRuntimeToolAlias(string(toolID)),
		Version:            definition.Version,
		Name:               definition.Name + ": " + name,
		Description:        firstNonBlank(description, definition.Description),
		Source:             string(tool.KindMCPServer),
		ServerToolID:       string(definition.ID),
		DownstreamToolName: name,
		GrantedPermissions: append([]string(nil), resolved.GrantedPermissions...),
		SecretEnv:          cloneStringMap(secretEnv),
		Summary:            firstNonBlank(description, definition.Description),
		InputSchema:        cloneAnyMap(inputSchema),
		OutputSchema:       cloneAnyMap(outputSchema),
	}
}

func requestedMCPDownstreamToolName(refID agentcore.ToolID, serverID agentcore.ToolID) string {
	ref := strings.TrimSpace(string(refID))
	server := strings.TrimSpace(string(serverID))
	prefix := server + "."
	if ref == "" || server == "" || !strings.HasPrefix(ref, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(ref, prefix))
}

func safeRuntimeToolAlias(name string) string {
	return toolbridge.NormalizeToolName(name)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (b EffectiveResourceBinder) bindSkills(ctx context.Context, profile agentcore.AgentProfile) ([]agentcore.SkillBinding, []agentcore.ToolRef, error) {
	if len(profile.SkillRefs) == 0 {
		return nil, nil, nil
	}
	if b.skills == nil {
		return nil, nil, newError(CodeSkillVersionMissing, profile.ID, "skill_refs", "skill resolver is not configured", nil)
	}
	out := make([]agentcore.SkillBinding, 0, len(profile.SkillRefs))
	requiredTools := []agentcore.ToolRef{}
	for _, ref := range profile.SkillRefs {
		resolved, err := b.skills.Resolve(ctx, SkillResolveRequest{Ref: ref})
		if err != nil {
			return nil, nil, newError(CodeSkillVersionMissing, profile.ID, "skill_refs", "skill binding failed", err)
		}
		packageDigest := ""
		if resolved.Definition.PackageSnapshot != nil {
			packageDigest = resolved.Definition.PackageSnapshot.Digest
		}
		out = append(out, agentcore.SkillBinding{
			SkillID:       string(resolved.Ref.SkillID),
			Alias:         resolved.Ref.Alias,
			Version:       resolved.MatchedVersion,
			Name:          resolved.Definition.Name,
			Summary:       resolved.Definition.Description,
			PackageDigest: packageDigest,
		})
		requiredTools = append(requiredTools, resolved.Definition.RequiredTools...)
	}
	return out, requiredTools, nil
}

func mergeToolRefs(profileRefs, requiredRefs []agentcore.ToolRef) []agentcore.ToolRef {
	if len(profileRefs) == 0 && len(requiredRefs) == 0 {
		return nil
	}
	out := make([]agentcore.ToolRef, 0, len(profileRefs)+len(requiredRefs))
	seen := map[string]struct{}{}
	for _, ref := range profileRefs {
		if ref.IsZero() {
			continue
		}
		key := resourceRefKey(ref.ToolID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	for _, ref := range requiredRefs {
		if ref.IsZero() {
			continue
		}
		key := resourceRefKey(ref.ToolID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func resourceRefKey(id agentcore.ToolID) string {
	return strings.ToLower(strings.TrimSpace(string(id)))
}

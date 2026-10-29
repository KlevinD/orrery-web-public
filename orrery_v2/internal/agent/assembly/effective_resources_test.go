package assembly

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/resource/skill"
	"orrery_v2/internal/resource/tool"
)

func TestEffectiveResourceBinderIncludesSkillRequiredTools(t *testing.T) {
	profile := validProfile()
	profile.ToolRefs = []agentcore.ToolRef{{ToolID: "tool-shell", PermissionScope: "shell.exec"}}
	profile.SkillRefs = []agentcore.SkillRef{{SkillID: "skill-release"}}
	toolResolver := &recordingToolResolver{definitions: map[agentcore.ToolID]tool.ToolDefinition{
		"tool-shell": {
			ID:          "tool-shell",
			Kind:        tool.KindCLITool,
			Name:        "shell",
			Description: "Run shell commands",
			Version:     "1.0.0",
			Enabled:     true,
		},
		"mcp-github": {
			ID:          "mcp-github",
			Kind:        tool.KindMCPServer,
			Name:        "GitHub MCP",
			Description: "Use GitHub through MCP",
			Version:     "1.0.0",
			Enabled:     true,
			MCP:         &tool.MCPServerDefinition{ServerName: "github", ExposedTools: []string{"issues:search"}},
		},
	}}
	skillResolver := fixedSkillResolver{resolved: skill.ResolvedSkill{
		Ref:            profile.SkillRefs[0],
		MatchedVersion: "2.0.0",
		Definition: skill.SkillDefinition{
			ID:          "skill-release",
			Name:        "release",
			Version:     "2.0.0",
			Description: "Ship releases with evidence",
			Enabled:     true,
			RequiredTools: []agentcore.ToolRef{
				{ToolID: "mcp-github", Alias: "github", PermissionScope: "repo.write"},
			},
		},
	}}
	binder := NewEffectiveResourceBinder(EffectiveResourceBinderOptions{
		ToolResolver:  toolResolver,
		SkillResolver: skillResolver,
	})

	resolved, err := binder.Bind(context.Background(), EffectiveResourceBindRequest{
		Profile:     profile,
		AgentID:     "agent-release",
		Permissions: []string{"mission.write"},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if len(resolved.Skills) != 1 || resolved.Skills[0].SkillID != "skill-release" {
		t.Fatalf("skills = %+v", resolved.Skills)
	}
	if len(resolved.Tools) != 2 {
		t.Fatalf("tools = %+v", resolved.Tools)
	}
	if resolved.Tools[0].ToolID != "tool-shell" || resolved.Tools[1].ToolID != "mcp-github.issues:search" {
		t.Fatalf("tool order = %+v", resolved.Tools)
	}
	if len(toolResolver.requests) != 2 {
		t.Fatalf("tool resolve requests = %+v", toolResolver.requests)
	}
	if !hasPermission(toolResolver.requests[1].Principal.Permissions, "repo.write") {
		t.Fatalf("required tool permissions = %+v", toolResolver.requests[1].Principal.Permissions)
	}
}

func TestEffectiveResourceBinderDeduplicatesProfileAndSkillTools(t *testing.T) {
	profile := validProfile()
	profile.ToolRefs = []agentcore.ToolRef{{ToolID: "tool-shell", Alias: "profile-shell"}}
	profile.SkillRefs = []agentcore.SkillRef{{SkillID: "skill-shell"}}
	toolResolver := &recordingToolResolver{definitions: map[agentcore.ToolID]tool.ToolDefinition{
		"tool-shell": {
			ID:          "tool-shell",
			Kind:        tool.KindCLITool,
			Name:        "shell",
			Description: "Run shell commands",
			Version:     "1.0.0",
			Enabled:     true,
		},
	}}
	binder := NewEffectiveResourceBinder(EffectiveResourceBinderOptions{
		ToolResolver: toolResolver,
		SkillResolver: fixedSkillResolver{resolved: skill.ResolvedSkill{
			Ref:            profile.SkillRefs[0],
			MatchedVersion: "1.0.0",
			Definition: skill.SkillDefinition{
				ID:            "skill-shell",
				Name:          "shell skill",
				Version:       "1.0.0",
				Enabled:       true,
				RequiredTools: []agentcore.ToolRef{{ToolID: "tool-shell", Alias: "skill-shell"}},
			},
		}},
	})

	resolved, err := binder.Bind(context.Background(), EffectiveResourceBindRequest{
		Profile: profile,
		AgentID: "agent-release",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if len(toolResolver.requests) != 1 {
		t.Fatalf("expected one tool resolution, got %+v", toolResolver.requests)
	}
	if len(resolved.Tools) != 1 || resolved.Tools[0].Alias != "profile-shell" {
		t.Fatalf("profile binding should win over skill required tool: %+v", resolved.Tools)
	}
}

func TestEffectiveResourceBinderExpandsMCPServerToolsForRuntime(t *testing.T) {
	profile := validProfile()
	profile.ToolRefs = []agentcore.ToolRef{{ToolID: "mcp-yunji"}}
	profile.SkillRefs = []agentcore.SkillRef{}
	toolResolver := &recordingToolResolver{definitions: map[agentcore.ToolID]tool.ToolDefinition{
		"mcp-yunji": {
			ID:          "mcp-yunji",
			Kind:        tool.KindMCPServer,
			Name:        "Yunji MCP",
			Description: "Use Yunji recruiting tools through MCP",
			Version:     "1.0.0",
			Enabled:     true,
			MCP: &tool.MCPServerDefinition{
				ServerName:   "yunji-zhaopin",
				ExposedTools: []string{"candidate:list"},
				ExposedToolDetails: []tool.MCPExposedToolDefinition{{
					Name:        "candidate:advance",
					Description: "Advance candidate stage",
					InputSchema: map[string]any{
						"type":       "object",
						"properties": map[string]any{"candidateId": map[string]any{"type": "string"}},
						"required":   []any{"candidateId"},
					},
				}},
			},
		},
	}}
	binder := NewEffectiveResourceBinder(EffectiveResourceBinderOptions{
		ToolResolver: toolResolver,
	})

	resolved, err := binder.Bind(context.Background(), EffectiveResourceBindRequest{
		Profile: profile,
		AgentID: "agent-yunji",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if got, want := toolIDs(resolved.Tools), []agentcore.ToolID{"mcp-yunji.candidate:advance", "mcp-yunji.candidate:list"}; !sameToolIDs(got, want) {
		t.Fatalf("runtime mcp tool bindings = %v, want %v", got, want)
	}
	if resolved.Tools[0].Name != "Yunji MCP: candidate:advance" || resolved.Tools[0].Description != "Advance candidate stage" {
		t.Fatalf("expanded tool metadata = %+v", resolved.Tools[0])
	}
	if resolved.Tools[0].InputSchema["type"] != "object" {
		t.Fatalf("expanded tool schema = %+v", resolved.Tools[0].InputSchema)
	}
}

func TestEffectiveResourceBinderBindsSingleMCPDownstreamToolRef(t *testing.T) {
	profile := validProfile()
	profile.ToolRefs = []agentcore.ToolRef{{ToolID: "mcp-yunji.candidate:advance"}}
	profile.SkillRefs = []agentcore.SkillRef{}
	toolResolver := &recordingToolResolver{definitions: map[agentcore.ToolID]tool.ToolDefinition{
		"mcp-yunji": {
			ID:          "mcp-yunji",
			Kind:        tool.KindMCPServer,
			Name:        "Yunji MCP",
			Description: "Use Yunji recruiting tools through MCP",
			Version:     "1.0.0",
			Enabled:     true,
			MCP: &tool.MCPServerDefinition{
				ServerName:   "yunji-zhaopin",
				ExposedTools: []string{"candidate:list"},
				ExposedToolDetails: []tool.MCPExposedToolDefinition{{
					Name:        "candidate:advance",
					Description: "Advance candidate stage",
				}},
			},
		},
	}}
	toolResolver.resolvedByRef = map[agentcore.ToolID]agentcore.ToolID{
		"mcp-yunji.candidate:advance": "mcp-yunji",
	}
	binder := NewEffectiveResourceBinder(EffectiveResourceBinderOptions{
		ToolResolver: toolResolver,
	})

	resolved, err := binder.Bind(context.Background(), EffectiveResourceBindRequest{
		Profile: profile,
		AgentID: "agent-yunji",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if got, want := toolIDs(resolved.Tools), []agentcore.ToolID{"mcp-yunji.candidate:advance"}; !sameToolIDs(got, want) {
		t.Fatalf("runtime mcp tool bindings = %v, want %v", got, want)
	}
	if resolved.Tools[0].ServerToolID != "mcp-yunji" || resolved.Tools[0].DownstreamToolName != "candidate:advance" {
		t.Fatalf("single mcp downstream metadata = %+v", resolved.Tools[0])
	}
}

type recordingToolResolver struct {
	definitions   map[agentcore.ToolID]tool.ToolDefinition
	resolvedByRef map[agentcore.ToolID]agentcore.ToolID
	requests      []ToolResolveRequest
}

func (r *recordingToolResolver) Resolve(_ context.Context, req ToolResolveRequest) (tool.ResolvedTool, error) {
	r.requests = append(r.requests, req)
	definitionID := req.Ref.ToolID
	if mapped := r.resolvedByRef[req.Ref.ToolID]; mapped != "" {
		definitionID = mapped
	}
	definition := r.definitions[definitionID]
	return tool.ResolvedTool{
		Ref:                req.Ref,
		Definition:         definition,
		GrantedPermissions: append([]string(nil), req.Principal.Permissions...),
		SecretBindingRefs:  secretBindingRefsForTest(definition),
	}, nil
}

type fixedSkillResolver struct {
	resolved skill.ResolvedSkill
	err      error
}

func (r fixedSkillResolver) Resolve(context.Context, SkillResolveRequest) (skill.ResolvedSkill, error) {
	return r.resolved, r.err
}

func hasPermission(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func toolIDs(bindings []agentcore.ToolBinding) []agentcore.ToolID {
	out := make([]agentcore.ToolID, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, binding.ToolID)
	}
	return out
}

func sameToolIDs(got []agentcore.ToolID, want []agentcore.ToolID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func secretBindingRefsForTest(definition tool.ToolDefinition) []tool.SecretBindingRef {
	refs := make([]tool.SecretBindingRef, 0, len(definition.SecretRefs))
	for _, ref := range definition.SecretRefs {
		refs = append(refs, tool.SecretBindingRef{SecretName: ref.Name, Env: ref.Env})
	}
	return refs
}

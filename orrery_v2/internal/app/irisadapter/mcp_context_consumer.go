package irisadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	contextgraph "orrery_v2/internal/app/contextgraph"
)

const (
	EnterpriseGraphBuildContextBundleToolID = "enterprise_graph.build_context_bundle"
	EnterpriseGraphReadPermission           = "enterprise_graph.read"
)

type MCPToolCaller interface {
	CallTool(context.Context, MCPToolCallRequest) (MCPToolCallResult, error)
}

type MCPToolCallRequest struct {
	TenantID    string         `json:"tenant_id,omitempty"`
	ToolID      string         `json:"tool_id"`
	PrincipalID string         `json:"principal_id,omitempty"`
	AgentID     string         `json:"agent_id,omitempty"`
	ProfileID   string         `json:"profile_id,omitempty"`
	Permissions []string       `json:"permissions,omitempty"`
	Arguments   map[string]any `json:"arguments,omitempty"`
}

type MCPToolCallResult struct {
	Output map[string]any `json:"output,omitempty"`
}

type MCPContextConsumer struct {
	caller  MCPToolCaller
	adapter Adapter
}

type BuildSnapshotRequest struct {
	TenantID     string             `json:"tenant_id"`
	PrincipalID  string             `json:"principal_id,omitempty"`
	AgentID      string             `json:"agent_id,omitempty"`
	ProfileID    string             `json:"profile_id,omitempty"`
	Purpose      string             `json:"purpose"`
	ActorRef     contextgraph.Ref   `json:"actor_ref"`
	SubjectRefs  []contextgraph.Ref `json:"subject_refs"`
	IncludeKinds []string           `json:"include_kinds,omitempty"`
	MaxDepth     int                `json:"max_depth,omitempty"`
	TokenBudget  int                `json:"token_budget,omitempty"`
}

func NewMCPContextConsumer(caller MCPToolCaller) MCPContextConsumer {
	return MCPContextConsumer{caller: caller, adapter: NewAdapter()}
}

func (c MCPContextConsumer) BuildSnapshot(ctx context.Context, req BuildSnapshotRequest) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if c.caller == nil {
		return Snapshot{}, fmt.Errorf("mcp context consumer requires tool caller")
	}
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.Purpose) == "" || strings.TrimSpace(req.ActorRef.ID) == "" || len(req.SubjectRefs) == 0 {
		return Snapshot{}, fmt.Errorf("tenant_id, purpose, actor_ref, and subject_refs are required")
	}
	result, err := c.caller.CallTool(ctx, MCPToolCallRequest{
		TenantID:    strings.TrimSpace(req.TenantID),
		ToolID:      EnterpriseGraphBuildContextBundleToolID,
		PrincipalID: strings.TrimSpace(req.PrincipalID),
		AgentID:     strings.TrimSpace(req.AgentID),
		ProfileID:   strings.TrimSpace(req.ProfileID),
		Permissions: []string{EnterpriseGraphReadPermission},
		Arguments:   buildContextBundleArguments(req),
	})
	if err != nil {
		return Snapshot{}, err
	}
	bundle, err := contextBundleFromOutput(result.Output)
	if err != nil {
		return Snapshot{}, err
	}
	return c.adapter.ContextBundleToSnapshot(bundle), nil
}

func buildContextBundleArguments(req BuildSnapshotRequest) map[string]any {
	args := map[string]any{
		"tenant_id":    strings.TrimSpace(req.TenantID),
		"consumer":     "iris",
		"purpose":      strings.TrimSpace(req.Purpose),
		"actor_ref":    refArgument(req.ActorRef),
		"subject_refs": refArguments(req.SubjectRefs),
	}
	if len(req.IncludeKinds) > 0 {
		args["include_kinds"] = append([]string(nil), req.IncludeKinds...)
	}
	if req.MaxDepth > 0 {
		args["max_depth"] = req.MaxDepth
	}
	if req.TokenBudget > 0 {
		args["token_budget"] = req.TokenBudget
	}
	return args
}

func contextBundleFromOutput(output map[string]any) (contextgraph.ContextBundle, error) {
	if len(output) == 0 {
		return contextgraph.ContextBundle{}, fmt.Errorf("enterprise graph context bundle output is empty")
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return contextgraph.ContextBundle{}, err
	}
	var bundle contextgraph.ContextBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return contextgraph.ContextBundle{}, err
	}
	if strings.TrimSpace(bundle.BundleID) == "" {
		return contextgraph.ContextBundle{}, fmt.Errorf("enterprise graph context bundle output missing bundle_id")
	}
	return bundle, nil
}

func refArgument(ref contextgraph.Ref) map[string]any {
	return map[string]any{
		"kind": strings.TrimSpace(ref.Kind),
		"id":   strings.TrimSpace(ref.ID),
	}
}

func refArguments(refs []contextgraph.Ref) []map[string]any {
	out := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.ID) == "" {
			continue
		}
		out = append(out, refArgument(ref))
	}
	return out
}

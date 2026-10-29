package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
	toolres "orrery_v2/internal/resource/tool"
)

const mcpCapabilityProjectionTenantID = "system"

type mcpCapabilityToolProjection struct {
	Name         string
	Description  string
	InputSchema  map[string]any
	OutputSchema map[string]any
}

func (p productionAPI) projectMCPServerCapabilities(ctx context.Context, toolID string) error {
	toolID = strings.TrimSpace(toolID)
	if p.contextGraphCore == nil || toolID == "" {
		return nil
	}
	definition, err := toolres.NewPlatformRegistry(p.contextGraphCore).Get(ctx, agentcore.ToolID(toolID))
	if err != nil {
		if isMissingContextGraphTableError(err) {
			return nil
		}
		return err
	}
	if definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
		return nil
	}
	toolProjections := mcpCapabilityToolProjections(definition.MCP)
	if err := clearMCPServerCapabilityProjection(ctx, p.contextGraphCore, string(definition.ID), mcpProjectedToolIDs(string(definition.ID), toolProjections)); err != nil {
		if isMissingContextGraphTableError(err) {
			return nil
		}
		return err
	}
	if err := saveMCPServerCapabilityProjection(ctx, contextgraph.NewPlatformStore(p.contextGraphCore), definition, toolProjections); err != nil {
		if isMissingContextGraphTableError(err) {
			return nil
		}
		return err
	}
	return nil
}

func saveMCPServerCapabilityProjection(ctx context.Context, store *contextgraph.PlatformStore, definition toolres.ToolDefinition, toolProjections []mcpCapabilityToolProjection) error {
	if definition.MCP == nil {
		return nil
	}
	now := time.Now().UTC()
	serverID := strings.TrimSpace(string(definition.ID))
	accessRef := systemResourceOntologySourceRef(serverID)
	evidenceID := "mcp-capability-projected:" + serverID
	provenance := contextgraph.Ref{Kind: "resource.mcp", ID: serverID}
	evidenceRefs := []contextgraph.Ref{{Kind: "evidence", ID: evidenceID}}
	if err := store.SaveEvidence(ctx, contextgraph.Evidence{
		ID:              evidenceID,
		TenantID:        mcpCapabilityProjectionTenantID,
		SourceRef:       provenance,
		SourceAccessRef: accessRef,
		Summary:         "mcp_capability_projection",
		Confidence:      1,
		ObservedAt:      now,
	}); err != nil {
		return err
	}
	if err := store.SaveObject(ctx, contextgraph.Object{
		ID:           serverID,
		TenantID:     mcpCapabilityProjectionTenantID,
		ObjectType:   "mcp_server",
		CanonicalKey: "mcp_server:" + serverID,
		Label:        firstMCPProjectionString(definition.MCP.ServerName, definition.Name, serverID),
		Properties: map[string]any{
			"server_name": definition.MCP.ServerName,
			"transport":   string(definition.MCP.Transport),
		},
		Confidence:     1,
		Status:         enabledContextGraphStatus(definition.Enabled),
		Provenance:     provenance,
		EvidenceRefs:   evidenceRefs,
		OwnerAccessRef: accessRef,
		UpdatedAt:      now,
	}); err != nil {
		return err
	}
	if err := saveMCPProjectionFact(ctx, store, now, accessRef, evidenceRefs, provenance, "mcp_server", serverID, "server_name", definition.MCP.ServerName); err != nil {
		return err
	}
	if err := saveMCPProjectionFact(ctx, store, now, accessRef, evidenceRefs, provenance, "mcp_server", serverID, "transport", string(definition.MCP.Transport)); err != nil {
		return err
	}
	for _, projectedTool := range toolProjections {
		toolID := serverID + "." + projectedTool.Name
		if err := store.SaveObject(ctx, contextgraph.Object{
			ID:           toolID,
			TenantID:     mcpCapabilityProjectionTenantID,
			ObjectType:   "tool",
			CanonicalKey: "tool:" + toolID,
			Label:        projectedTool.Name,
			Properties: map[string]any{
				"name":             projectedTool.Name,
				"description":      projectedTool.Description,
				"provider_tool_id": serverID,
				"provider":         "mcp",
				"risk_level":       string(definition.RiskLevel),
				"input_schema":     projectedTool.InputSchema,
				"output_schema":    projectedTool.OutputSchema,
			},
			Confidence:     1,
			Status:         enabledContextGraphStatus(definition.Enabled),
			Provenance:     provenance,
			EvidenceRefs:   evidenceRefs,
			OwnerAccessRef: accessRef,
			UpdatedAt:      now,
		}); err != nil {
			return err
		}
		if err := saveMCPProjectionFact(ctx, store, now, accessRef, evidenceRefs, provenance, "tool", toolID, "name", projectedTool.Name); err != nil {
			return err
		}
		if err := saveMCPProjectionFact(ctx, store, now, accessRef, evidenceRefs, provenance, "tool", toolID, "risk_level", string(definition.RiskLevel)); err != nil {
			return err
		}
		if err := saveMCPProjectionFact(ctx, store, now, accessRef, evidenceRefs, provenance, "tool", toolID, "provider", "mcp"); err != nil {
			return err
		}
		if err := saveMCPProjectionFact(ctx, store, now, accessRef, evidenceRefs, provenance, "tool", toolID, "provider_tool_id", serverID); err != nil {
			return err
		}
		if projectedTool.Description != "" {
			if err := saveMCPProjectionFact(ctx, store, now, accessRef, evidenceRefs, provenance, "tool", toolID, "description", projectedTool.Description); err != nil {
				return err
			}
		}
		if len(projectedTool.InputSchema) > 0 {
			if err := saveMCPProjectionFact(ctx, store, now, accessRef, evidenceRefs, provenance, "tool", toolID, "input_schema", projectedTool.InputSchema); err != nil {
				return err
			}
		}
		if len(projectedTool.OutputSchema) > 0 {
			if err := saveMCPProjectionFact(ctx, store, now, accessRef, evidenceRefs, provenance, "tool", toolID, "output_schema", projectedTool.OutputSchema); err != nil {
				return err
			}
		}
		if err := store.SaveLink(ctx, contextgraph.Link{
			ID:           "tool_backed_by_mcp_server:" + toolID + ":" + serverID,
			TenantID:     mcpCapabilityProjectionTenantID,
			LinkType:     "tool_backed_by_mcp_server",
			FromObjectID: toolID,
			ToObjectID:   serverID,
			Properties: map[string]any{
				"provider": "mcp",
			},
			Confidence:      1,
			Status:          enabledContextGraphStatus(definition.Enabled),
			Provenance:      provenance,
			EvidenceRefs:    evidenceRefs,
			SourceAccessRef: accessRef,
			UpdatedAt:       now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func clearMCPServerCapabilityProjection(ctx context.Context, core *db.Store, serverID string, currentToolIDs map[string]bool) error {
	if core == nil || strings.TrimSpace(serverID) == "" {
		return nil
	}
	return core.WithTx(ctx, func(tx db.Tx) error {
		rows, err := tx.QueryContext(ctx, `
SELECT from_object_id
FROM context_graph_links
WHERE tenant_id = ? AND link_type = ? AND to_object_id = ?`,
			mcpCapabilityProjectionTenantID,
			"tool_backed_by_mcp_server",
			serverID,
		)
		if err != nil {
			return fmt.Errorf("list mcp capability projection tools: %w", err)
		}
		defer rows.Close()
		toolIDs := make([]string, 0)
		for rows.Next() {
			var toolID string
			if err := rows.Scan(&toolID); err != nil {
				return fmt.Errorf("scan mcp capability projection tool: %w", err)
			}
			if strings.TrimSpace(toolID) != "" {
				toolIDs = append(toolIDs, toolID)
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("list mcp capability projection tools: %w", err)
		}
		for _, toolID := range toolIDs {
			if err := clearMCPProjectedToolFacts(ctx, tx, toolID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
DELETE FROM context_graph_links
WHERE tenant_id = ? AND link_type = ? AND to_object_id = ?`,
			mcpCapabilityProjectionTenantID,
			"tool_backed_by_mcp_server",
			serverID,
		); err != nil {
			return fmt.Errorf("clear mcp capability projection links: %w", err)
		}
		for _, toolID := range toolIDs {
			if currentToolIDs[toolID] {
				continue
			}
			if err := deleteMCPProjectedToolIfUnreferenced(ctx, tx, toolID); err != nil {
				return err
			}
		}
		return nil
	})
}

func clearMCPProjectedToolFacts(ctx context.Context, tx db.Tx, toolID string) error {
	if _, err := tx.ExecContext(ctx, `
DELETE FROM context_graph_facts
WHERE tenant_id = ? AND subject_kind = ? AND subject_id = ? AND predicate IN (?, ?, ?, ?, ?, ?, ?)`,
		mcpCapabilityProjectionTenantID,
		"tool",
		toolID,
		"name",
		"risk_level",
		"description",
		"provider",
		"provider_tool_id",
		"input_schema",
		"output_schema",
	); err != nil {
		return fmt.Errorf("clear mcp capability facts: %w", err)
	}
	return nil
}

func deleteMCPProjectedToolIfUnreferenced(ctx context.Context, tx db.Tx, toolID string) error {
	var canonicalKey string
	err := tx.QueryRowContext(ctx, `
SELECT canonical_key
FROM context_graph_objects
WHERE tenant_id = ? AND object_type = ? AND id = ?`,
		mcpCapabilityProjectionTenantID,
		"tool",
		toolID,
	).Scan(&canonicalKey)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find mcp capability projection tool object: %w", err)
	}
	if canonicalKey != "tool:"+toolID {
		return nil
	}
	var linkCount int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM context_graph_links
WHERE tenant_id = ? AND (from_object_id = ? OR to_object_id = ?)`,
		mcpCapabilityProjectionTenantID,
		toolID,
		toolID,
	).Scan(&linkCount); err != nil {
		return fmt.Errorf("count mcp capability references: %w", err)
	}
	if linkCount > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM context_graph_objects
WHERE tenant_id = ? AND object_type = ? AND id = ?`,
		mcpCapabilityProjectionTenantID,
		"tool",
		toolID,
	); err != nil {
		return fmt.Errorf("clear mcp capability object: %w", err)
	}
	return nil
}

func saveMCPProjectionFact(ctx context.Context, store *contextgraph.PlatformStore, observedAt time.Time, accessRef contextgraph.AccessRef, evidenceRefs []contextgraph.Ref, provenance contextgraph.Ref, subjectKind, subjectID, predicate string, value any) error {
	if isEmptyMCPProjectionFactValue(value) {
		return nil
	}
	return store.SaveFact(ctx, contextgraph.Fact{
		ID:              fmt.Sprintf("%s:%s:%s", subjectKind, subjectID, predicate),
		TenantID:        mcpCapabilityProjectionTenantID,
		Subject:         contextgraph.Ref{Kind: subjectKind, ID: subjectID},
		Predicate:       predicate,
		Value:           value,
		Confidence:      1,
		Status:          "active",
		Provenance:      provenance,
		EvidenceRefs:    evidenceRefs,
		SourceAccessRef: accessRef,
		UpdatedAt:       observedAt,
	})
}

func mcpCapabilityToolProjections(definition *toolres.MCPServerDefinition) []mcpCapabilityToolProjection {
	if definition == nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]mcpCapabilityToolProjection, 0, len(definition.ExposedToolDetails)+len(definition.ExposedTools))
	for _, detail := range definition.ExposedToolDetails {
		name := strings.TrimSpace(detail.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, mcpCapabilityToolProjection{
			Name:         name,
			Description:  strings.TrimSpace(detail.Description),
			InputSchema:  detail.InputSchema,
			OutputSchema: detail.OutputSchema,
		})
	}
	for _, exposedTool := range definition.ExposedTools {
		name := strings.TrimSpace(exposedTool)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, mcpCapabilityToolProjection{Name: name})
	}
	return out
}

func mcpProjectedToolIDs(serverID string, tools []mcpCapabilityToolProjection) map[string]bool {
	out := make(map[string]bool, len(tools))
	serverID = strings.TrimSpace(serverID)
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if serverID == "" || name == "" {
			continue
		}
		out[serverID+"."+name] = true
	}
	return out
}

func enabledContextGraphStatus(enabled bool) string {
	if !enabled {
		return "inactive"
	}
	return "active"
}

func firstMCPProjectionString(candidates ...string) string {
	for _, candidate := range candidates {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func isEmptyMCPProjectionFactValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	default:
		return false
	}
}

func isMissingContextGraphTableError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "context_graph_") &&
		(strings.Contains(message, "no such table") ||
			strings.Contains(message, "does not exist") ||
			strings.Contains(message, "undefined table"))
}

package main

import (
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/config"
	toolres "orrery_v2/internal/resource/tool"
)

const (
	defaultLegalMCPServerToolID    agentcore.ToolID = "mcp.ieado_legal"
	defaultLegalMCPPermissionScope                  = "legal.mcp"
	defaultLegalMCPDefaultURL                       = "http://127.0.0.1:8092/mcp"
)

func defaultLegalMCPServerDefinition(now time.Time, configs ...config.Config) toolres.ToolDefinition {
	tools := defaultLegalMCPExposedTools()
	details := make([]toolres.MCPExposedToolDefinition, 0, len(tools))
	for _, item := range tools {
		details = append(details, toolres.MCPExposedToolDefinition{
			Name:        item.name,
			Description: item.description,
		})
	}
	exposed := make([]string, 0, len(tools))
	for _, item := range tools {
		exposed = append(exposed, item.name)
	}
	return toolres.ToolDefinition{
		ID:          defaultLegalMCPServerToolID,
		Kind:        toolres.KindMCPServer,
		Name:        "Orrery Legal MCP",
		Description: "External Legal MCP server that exposes the compact legal-agent capability surface for Orrery Legal Agents.",
		Version:     "1.0.0",
		Enabled:     true,
		RiskLevel:   toolres.RiskHigh,
		Tags:        []string{"ieado-legal", "legal", "mcp", "agents", "pageindex", "rag"},
		Permissions: []toolres.PermissionDeclaration{{
			Scope:       defaultLegalMCPPermissionScope,
			Description: "Allows Orrery Legal Agents to call the governed Legal MCP server and its compact legal tool surface.",
			Risk:        toolres.RiskHigh,
			Required:    true,
		}},
		MCP: &toolres.MCPServerDefinition{
			ServerName:         "ieado-legal",
			Transport:          toolres.TransportStreamHTTP,
			URL:                defaultLegalMCPURL(configs...),
			ExposedTools:       exposed,
			ExposedToolDetails: details,
			HealthCheck:        toolres.HealthCheckPolicy{Mode: toolres.HealthCheckStartup, Timeout: 5 * time.Second},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func defaultLegalMCPURL(configs ...config.Config) string {
	if len(configs) > 0 {
		if value := strings.TrimSpace(configs[0].Integrations.LegalMCPURL); value != "" {
			return value
		}
	}
	return defaultLegalMCPDefaultURL
}

type defaultLegalMCPTool struct {
	name        string
	description string
}

func defaultLegalMCPExposedTools() []defaultLegalMCPTool {
	return []defaultLegalMCPTool{
		{name: "legal_agent_capability_layer_summary", description: "Read Legal MCP capability boundaries, layers, and agent-facing operating model."},
		{name: "legal_agent_toolbox", description: "Read the compact legal-agent toolbox and action catalog."},
		{name: "legal_enterprise_search", description: "Search the isolated Legal and Policy corpus through the Legal MCP retrieval layer."},
		{name: "legal_source_manage", description: "Manage governed legal knowledge sources, ingestion requests, and source records."},
		{name: "legal_intake_run", description: "Run legal intake triage, routing, assignment, progress, and activity operations."},
		{name: "legal_contract_review_run", description: "Run evidence-first contract review, clause risk, comparison, and readiness workflows."},
		{name: "legal_compliance_risk_run", description: "Run compliance scans, risk registers, mitigations, and report generation workflows."},
		{name: "legal_policy_intelligence_summary", description: "Read legal policy intelligence source, report, risk, and review status."},
		{name: "legal_policy_pipeline_status", description: "Read isolated legal policy pipeline and index status."},
		{name: "legal_human_review_run", description: "Create and record legal human review requests and decisions."},
		{name: "legal_audit_trail", description: "Append or inspect legal audit, evidence-chain, snapshot, and matter timeline records."},
		{name: "legal_operations_summary", description: "Read legal operations analytics and queue summaries."},
		{name: "legal_connector_handoff_record", description: "Record controlled external connector handoffs without executing external systems."},
	}
}

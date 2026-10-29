package main

import (
	"os"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolres "orrery_v2/internal/resource/tool"
)

const (
	defaultWebsiteMarketingMCPServerToolID       agentcore.ToolID = "mcp.marketing"
	defaultWebsiteMarketingLeadCaptureTool                        = "marketing_lead_capture_create"
	defaultWebsiteMarketingLeadCapturePermission                  = "marketing.lead.capture"
	defaultWebsiteMarketingDatabaseSecretRef                      = "mcp_marketing_database_url"
)

func defaultWebsiteMarketingMCPServerDefinition(now time.Time) (toolres.ToolDefinition, bool) {
	command := strings.TrimSpace(os.Getenv("ORRERY_WEBSITE_MARKETING_MCP_COMMAND"))
	entrypoint := strings.TrimSpace(os.Getenv("ORRERY_WEBSITE_MARKETING_MCP_ENTRYPOINT"))
	if command == "" || entrypoint == "" {
		return toolres.ToolDefinition{}, false
	}

	secret := toolres.SecretRef{Name: defaultWebsiteMarketingDatabaseSecretRef, Env: "MARKETING_MCP_DATABASE_URL"}
	return toolres.ToolDefinition{
		ID:          defaultWebsiteMarketingMCPServerToolID,
		Kind:        toolres.KindMCPServer,
		Name:        "Minvia Website Marketing Lead Capture",
		Description: "Restricted Marketing MCP surface for consented Minvia website enquiries.",
		Version:     "1.0.0",
		Enabled:     true,
		RiskLevel:   toolres.RiskMedium,
		Permissions: []toolres.PermissionDeclaration{{
			Scope:       defaultWebsiteMarketingLeadCapturePermission,
			Description: "Allows the locked website lead adapter to capture a consented enquiry.",
			Risk:        toolres.RiskMedium,
			Required:    true,
		}},
		SecretRefs: []toolres.SecretRef{secret},
		Tags:       []string{"website", "marketing", "lead", "mcp", "restricted"},
		MCP: &toolres.MCPServerDefinition{
			ServerName:    "marketing",
			Transport:     toolres.TransportStdio,
			Command:       command,
			Args:          []string{entrypoint},
			EnvSecretRefs: []toolres.SecretRef{secret},
			ExposedTools:  []string{defaultWebsiteMarketingLeadCaptureTool},
			ExposedToolDetails: []toolres.MCPExposedToolDefinition{{
				Name:        defaultWebsiteMarketingLeadCaptureTool,
				Description: "Capture a consented Minvia website enquiry as a Marketing lead.",
			}},
			CallTimeoutSeconds: 15,
			HealthCheck:        toolres.HealthCheckPolicy{Mode: toolres.HealthCheckStartup, Timeout: 5 * time.Second},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, true
}

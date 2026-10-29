package main

import (
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolres "orrery_v2/internal/resource/tool"
)

const (
	defaultServerBrowserMCPServerToolID  agentcore.ToolID = "mcp.server_browser"
	defaultServerBrowserNavigateTool                      = "navigate_page"
	defaultServerBrowserScreenshotTool                    = "take_screenshot"
	defaultServerBrowserPermission                        = "browser.server.read"
	defaultServerBrowserMCPVersion                        = "1.7.0"
	defaultServerBrowserMCPCommandEnv                     = "ORRERY_SERVER_BROWSER_MCP_COMMAND"
	defaultServerBrowserCDPURLCommandEnv                  = "ORRERY_SERVER_BROWSER_CDP_URL"
)

func defaultServerBrowserMCPServerDefinition(now time.Time) (toolres.ToolDefinition, bool) {
	command := strings.TrimSpace(os.Getenv(defaultServerBrowserMCPCommandEnv))
	cdpURL, validCDPURL := normalizeServerBrowserCDPURL(os.Getenv(defaultServerBrowserCDPURLCommandEnv))
	if command == "" || !validCDPURL {
		return toolres.ToolDefinition{}, false
	}

	return toolres.ToolDefinition{
		ID:          defaultServerBrowserMCPServerToolID,
		Kind:        toolres.KindMCPServer,
		Name:        "Orrery Server Browser",
		Description: "Restricted navigation and screenshot access to the server-side noVNC Chrome session.",
		Version:     defaultServerBrowserMCPVersion,
		Enabled:     true,
		RiskLevel:   toolres.RiskMedium,
		Permissions: []toolres.PermissionDeclaration{{
			Scope:       defaultServerBrowserPermission,
			Description: "Allows navigation and screenshots in the shared server-side Chrome session.",
			Risk:        toolres.RiskMedium,
			Required:    true,
		}},
		Tags: []string{"browser", "chrome", "novnc", "mcp", "restricted", toolres.TagMCPToolSurfaceLocked},
		MCP: &toolres.MCPServerDefinition{
			ServerName: "orrery-server-browser",
			Transport:  toolres.TransportStdio,
			Command:    command,
			Args: []string{
				"--browser-url=" + cdpURL,
				"--no-usage-statistics",
				"--no-performance-crux",
				"--no-category-emulation",
				"--no-category-performance",
				"--no-category-network",
				"--screenshot-format=jpeg",
				"--screenshot-quality=80",
				"--screenshot-max-width=1440",
				"--screenshot-max-height=1000",
			},
			ExposedTools: []string{
				defaultServerBrowserNavigateTool,
				defaultServerBrowserScreenshotTool,
			},
			ExposedToolDetails: []toolres.MCPExposedToolDefinition{
				{
					Name:        defaultServerBrowserNavigateTool,
					Description: "Navigate the current server browser tab to a URL. Script injection and browser interaction are not exposed.",
					InputSchema: map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"url": map[string]any{"type": "string", "format": "uri", "maxLength": 4096},
						},
						"required": []any{"url"},
					},
				},
				{
					Name:        defaultServerBrowserScreenshotTool,
					Description: "Capture the current server browser viewport as an image for visual inspection.",
					InputSchema: map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"fullPage": map[string]any{"type": "boolean", "description": "Capture the full page instead of only the viewport."},
						},
					},
				},
			},
			CallTimeoutSeconds: 45,
			HealthCheck:        toolres.HealthCheckPolicy{Mode: toolres.HealthCheckStartup, Timeout: 10 * time.Second},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, true
}

func normalizeServerBrowserCDPURL(value string) (string, bool) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", false
	}
	hostname := strings.TrimSpace(parsed.Hostname())
	loopback := strings.EqualFold(hostname, "localhost")
	if ip := net.ParseIP(hostname); ip != nil {
		loopback = ip.IsLoopback()
	}
	if !loopback {
		return "", false
	}
	return strings.TrimRight(value, "/"), true
}

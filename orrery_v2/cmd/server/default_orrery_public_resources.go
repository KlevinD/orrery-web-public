package main

import (
	"os"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolres "orrery_v2/internal/resource/tool"
)

const (
	defaultOrreryPublicMCPServerToolID                agentcore.ToolID = "mcp.orrery-public-readonly"
	defaultOrreryPublicMCPPermissionScope                              = "knowledge.orrery_public.read"
	defaultOrreryPublicMCPDefaultURL                                   = "http://127.0.0.1:8093/mcp"
	defaultOrreryPublicMCPAPIKeyRef                                    = "secret://mcp/orrery-public-readonly/api-key"
	defaultOrreryCustomerMCPServerToolID              agentcore.ToolID = "mcp.orrery-customer-readonly"
	defaultOrreryCustomerMCPPermissionScope                            = "knowledge.orrery_customer.read"
	defaultOrreryCustomerMCPDefaultURL                                 = "http://127.0.0.1:8096/mcp"
	defaultOrreryCustomerMCPAPIKeyRef                                  = "secret://mcp/orrery-customer-readonly/api-key"
	defaultWebsiteKnowledgeCurationMCPPermissionScope                  = "knowledge.website_gap.propose"
	defaultWebsiteKnowledgeGapListToolID              agentcore.ToolID = "website_knowledge_gap_list"
	defaultWebsiteDistillationCandidatesListToolID    agentcore.ToolID = "website_distillation_candidates_list"
	defaultWebsiteFAQCatalogListToolID                agentcore.ToolID = "website_faq_catalog_list"
	defaultWebsiteFAQMetricsListToolID                agentcore.ToolID = "website_faq_metrics_list"
	defaultWebsiteKnowledgeProposalCreateToolID       agentcore.ToolID = "website_knowledge_proposal_create"
)

func defaultOrreryPublicMCPServerDefinition(now time.Time) toolres.ToolDefinition {
	details := defaultOrreryPublicMCPExposedToolDetails()
	exposed := make([]string, 0, len(details))
	for _, detail := range details {
		exposed = append(exposed, detail.Name)
	}
	return toolres.ToolDefinition{
		ID:          defaultOrreryPublicMCPServerToolID,
		Kind:        toolres.KindMCPServer,
		Name:        "Orrery Public Knowledge",
		Description: "Read-only access to approved public Orrery product knowledge.",
		Version:     "1.0.0",
		Enabled:     true,
		RiskLevel:   toolres.RiskLow,
		Tags:        []string{"orrery", "public", "knowledge", "mcp", "pageindex", "readonly"},
		Permissions: []toolres.PermissionDeclaration{{
			Scope:       defaultOrreryPublicMCPPermissionScope,
			Description: "Allows an agent to read approved public Orrery product knowledge.",
			Risk:        toolres.RiskLow,
			Required:    true,
		}},
		SecretRefs: []toolres.SecretRef{{Name: defaultOrreryPublicMCPAPIKeyRef}},
		MCP: &toolres.MCPServerDefinition{
			ServerName:         "orrery-public-readonly",
			Transport:          toolres.TransportStreamHTTP,
			URL:                defaultOrreryPublicMCPURL(),
			CallTimeoutSeconds: 60,
			Headers: map[string]string{
				"Authorization": "Bearer ${" + defaultOrreryPublicMCPAPIKeyRef + "}",
			},
			Auth: toolres.MCPAuthPolicy{
				Required:                    true,
				Mode:                        toolres.MCPAuthModeGlobal,
				HeaderName:                  "Authorization",
				DefaultCredentialConfigured: true,
			},
			ExposedTools:       exposed,
			ExposedToolDetails: details,
			HealthCheck:        toolres.HealthCheckPolicy{Mode: toolres.HealthCheckStartup, Timeout: 5 * time.Second},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func defaultWebsiteKnowledgeCurationToolDefinitions(now time.Time) []toolres.ToolDefinition {
	permission := []toolres.PermissionDeclaration{{
		Scope: defaultWebsiteKnowledgeCurationMCPPermissionScope, Description: "Allows aggregate gap review and proposal drafting only.", Risk: toolres.RiskLow, Required: true,
	}}
	return []toolres.ToolDefinition{
		{
			ID: defaultWebsiteKnowledgeGapListToolID, Kind: toolres.KindInternalServiceTool,
			Name: "List website knowledge gaps", Description: "Read only aggregated and sanitized website knowledge gaps.",
			Version: "1.0.0", Enabled: true, RiskLevel: toolres.RiskLow, Permissions: permission,
			InputSchema: objectSchema(map[string]any{"minimum_count": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 3}}, nil),
			Tags:        []string{"website", "knowledge", "curation", "aggregate", "readonly"}, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: defaultWebsiteDistillationCandidatesListToolID, Kind: toolres.KindInternalServiceTool,
			Name: "List website answer distillation candidates", Description: "Read only source-backed, sanitized answer candidate clusters without raw conversations or visitor identities.",
			Version: "1.0.0", Enabled: true, RiskLevel: toolres.RiskLow, Permissions: permission,
			InputSchema: objectSchema(map[string]any{"minimum_count": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 5}}, nil),
			Tags:        []string{"website", "knowledge", "distillation", "aggregate", "readonly"}, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: defaultWebsiteFAQCatalogListToolID, Kind: toolres.KindInternalServiceTool,
			Name: "List active website FAQ catalog", Description: "Read only the active governed website FAQ rules and revision.",
			Version: "1.0.0", Enabled: true, RiskLevel: toolres.RiskLow, Permissions: permission,
			InputSchema: objectSchema(map[string]any{}, nil),
			Tags:        []string{"website", "faq", "catalog", "curation", "readonly"}, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: defaultWebsiteFAQMetricsListToolID, Kind: toolres.KindInternalServiceTool,
			Name: "List website FAQ metrics", Description: "Read only aggregated seven-day FAQ intent metrics without raw visitor messages.",
			Version: "1.0.0", Enabled: true, RiskLevel: toolres.RiskLow, Permissions: permission,
			InputSchema: objectSchema(map[string]any{}, nil),
			Tags:        []string{"website", "faq", "curation", "aggregate", "readonly"}, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: defaultWebsiteKnowledgeProposalCreateToolID, Kind: toolres.KindInternalServiceTool,
			Name: "Create website knowledge proposal", Description: "Create a reviewable draft without approval or publication authority.",
			Version: "1.0.0", Enabled: true, RiskLevel: toolres.RiskLow, Permissions: permission,
			InputSchema: objectSchema(map[string]any{
				"proposal_kind": map[string]any{"type": "string", "enum": []string{"knowledge_document", "faq_entry", "faq_alias", "faq_suggestion_order", "faq_demotion"}, "default": "knowledge_document"},
				"gap_id":        map[string]any{"type": "string"}, "target_knowledge_key": map[string]any{"type": "string"},
				"draft": map[string]any{"type": "string"}, "source_refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"rationale": map[string]any{"type": "string"}, "faq_revision": websiteFAQRevisionInputSchema(),
			}, []string{"gap_id", "target_knowledge_key", "draft", "source_refs", "rationale"}),
			Tags: []string{"website", "knowledge", "curation", "proposal", "draft-only"}, CreatedAt: now, UpdatedAt: now,
		},
	}
}

func websiteFAQRevisionInputSchema() map[string]any {
	entry := objectSchema(map[string]any{
		"locale":             map[string]any{"type": "string", "enum": []string{"zh-CN", "en-US"}},
		"canonical_question": map[string]any{"type": "string"},
		"exact":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"patterns":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"answer":             map[string]any{"type": "string"},
		"sources":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"class":              map[string]any{"type": "string", "enum": []string{"conversational", "factual", "refusal", "clarification", "lead_prompt"}},
		"action":             map[string]any{"type": "string"},
	}, []string{"locale", "canonical_question", "answer", "sources", "class"})
	return objectSchema(map[string]any{
		"logical_intent_id":   map[string]any{"type": "string"},
		"entries":             map[string]any{"type": "array", "minItems": 2, "maxItems": 2, "items": entry},
		"suggestion_eligible": map[string]any{"type": "boolean"},
		"suggestion_rank":     map[string]any{"type": "integer", "minimum": 0, "maximum": 1000},
	}, []string{"logical_intent_id", "entries", "suggestion_eligible", "suggestion_rank"})
}

func defaultOrreryCustomerMCPServerDefinition(now time.Time) toolres.ToolDefinition {
	details := defaultOrreryPublicMCPExposedToolDetails()
	exposed := make([]string, 0, len(details))
	for _, detail := range details {
		exposed = append(exposed, detail.Name)
	}
	return toolres.ToolDefinition{
		ID: defaultOrreryCustomerMCPServerToolID, Kind: toolres.KindMCPServer,
		Name: "Orrery Customer Knowledge", Description: "Read-only access to approved public and customer-facing Orrery knowledge.",
		Version: "1.0.0", Enabled: true, RiskLevel: toolres.RiskLow,
		Tags: []string{"orrery", "customer", "knowledge", "mcp", "pageindex", "readonly"},
		Permissions: []toolres.PermissionDeclaration{{
			Scope: defaultOrreryCustomerMCPPermissionScope, Description: "Allows a website receptionist to read approved customer-facing Orrery knowledge.", Risk: toolres.RiskLow, Required: true,
		}},
		SecretRefs: []toolres.SecretRef{{Name: defaultOrreryCustomerMCPAPIKeyRef}},
		MCP: &toolres.MCPServerDefinition{
			ServerName: "orrery-customer-readonly", Transport: toolres.TransportStreamHTTP, URL: websiteCustomerMCPURL(),
			CallTimeoutSeconds: 60,
			Headers:            map[string]string{"Authorization": "Bearer ${" + defaultOrreryCustomerMCPAPIKeyRef + "}"},
			Auth:               toolres.MCPAuthPolicy{Required: true, Mode: toolres.MCPAuthModeGlobal, HeaderName: "Authorization", DefaultCredentialConfigured: true},
			ExposedTools:       exposed, ExposedToolDetails: details,
			HealthCheck: toolres.HealthCheckPolicy{Mode: toolres.HealthCheckStartup, Timeout: 5 * time.Second},
		},
		CreatedAt: now, UpdatedAt: now,
	}
}

func defaultOrreryPublicMCPURL() string {
	if value := strings.TrimSpace(os.Getenv("ORRERY_PUBLIC_MCP_URL")); value != "" {
		return value
	}
	return defaultOrreryPublicMCPDefaultURL
}

func websiteCustomerMCPURL() string {
	if value := strings.TrimSpace(os.Getenv("ORRERY_WEBSITE_CUSTOMER_MCP_URL")); value != "" {
		return value
	}
	return defaultOrreryCustomerMCPDefaultURL
}

func defaultOrreryPublicMCPExposedToolDetails() []toolres.MCPExposedToolDefinition {
	return []toolres.MCPExposedToolDefinition{
		{
			Name:        "search_documents",
			Description: "Search approved public Orrery product knowledge.",
			InputSchema: objectSchema(map[string]any{
				"query": map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 6, "default": 6},
			}, []string{"query"}),
		},
		{
			Name:        "get_document",
			Description: "Get metadata for an approved public Orrery document.",
			InputSchema: objectSchema(map[string]any{"document_id": map[string]any{"type": "string"}}, []string{"document_id"}),
		},
		{
			Name:        "get_document_structure",
			Description: "Get the tree structure of an approved public Orrery document.",
			InputSchema: objectSchema(map[string]any{"document_id": map[string]any{"type": "string"}}, []string{"document_id"}),
		},
		{
			Name:        "get_page_content",
			Description: "Read selected content from an approved public Orrery document.",
			InputSchema: objectSchema(map[string]any{
				"document_id": map[string]any{"type": "string"},
				"pages":       map[string]any{"type": "string"},
			}, []string{"document_id", "pages"}),
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

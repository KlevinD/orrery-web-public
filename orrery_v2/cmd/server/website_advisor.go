package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	websitechannel "orrery_v2/internal/gateway/channel/website"
	gwcore "orrery_v2/internal/gateway/core"
	"orrery_v2/internal/gateway/protocol"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	mcptransport "orrery_v2/internal/transport/mcp"
)

const websiteMarketingLeadCaptureToolID = "mcp.marketing.marketing_lead_capture_create"

const (
	websitePublicKnowledgeSearchToolID   = "mcp.orrery-public-readonly.search_documents"
	websiteCustomerKnowledgeSearchToolID = "mcp.orrery-customer-readonly.search_documents"
)

type productionWebsiteMarketingLeadCapture struct {
	gateway productionMCPGatewayCaller
}

type productionWebsiteKnowledgeSearch struct {
	gateway productionMCPGatewayCaller
}

type productionWebsiteKnowledgeCuration struct {
	store     websiteKnowledgeCurationStore
	catalog   *websitechannel.WebsiteFAQCatalog
	publisher websiteLowRiskProposalPublisher
	now       func() time.Time
}

type websiteLowRiskProposalPublisher interface {
	PublishEligibleProposal(context.Context, websitechannel.WebsiteKnowledgeProposal, time.Time) (websitechannel.WebsiteKnowledgeProposal, error)
}

type websiteKnowledgeCurationStore interface {
	ListKnowledgeGaps(context.Context, int, time.Time) ([]websitechannel.WebsiteGapAggregate, error)
	ListWebsiteFAQMetrics(context.Context, time.Time) ([]websitechannel.WebsiteFAQMetric, error)
	ListDistillationCandidateClusters(context.Context, int, time.Time) ([]websitechannel.WebsiteDistillationCandidateCluster, error)
	CreateProposal(context.Context, websitechannel.WebsiteKnowledgeProposalInput) (websitechannel.WebsiteKnowledgeProposal, error)
}

func newProductionWebsiteKnowledgeCuration(store *websitechannel.SQLWebsiteGapStore, catalog *websitechannel.WebsiteFAQCatalog, now func() time.Time) productionWebsiteKnowledgeCuration {
	return productionWebsiteKnowledgeCuration{
		store: store, catalog: catalog, now: now,
		publisher: websitechannel.WebsiteLowRiskPublisher{Store: store, Candidates: store, Catalog: catalog},
	}
}

const websiteKnowledgeCuratorDefaultMinimumCount = 3

func newProductionWebsiteKnowledgeRuntime(ctx context.Context, core *db.Store) (*websitechannel.SQLWebsiteGapStore, *websitechannel.WebsiteFAQCatalog, error) {
	store := websitechannel.NewSQLWebsiteGapStore(core)
	catalog := websitechannel.NewWebsiteFAQCatalog(nil)
	if err := catalog.Reload(ctx, store); err != nil {
		return nil, nil, fmt.Errorf("load website FAQ catalog: %w", err)
	}
	return store, catalog, nil
}

func (c productionWebsiteKnowledgeCuration) ExecuteWebsiteKnowledgeTool(ctx context.Context, toolID string, request mcptransport.WebsiteKnowledgeToolRequest) (mcptransport.WebsiteKnowledgeToolResult, error) {
	if c.store == nil {
		return mcptransport.WebsiteKnowledgeToolResult{}, fmt.Errorf("website knowledge governance is not configured")
	}
	now := time.Now().UTC()
	if c.now != nil {
		now = c.now().UTC()
	}
	switch strings.TrimSpace(toolID) {
	case string(defaultWebsiteDistillationCandidatesListToolID):
		minimum := request.MinimumCount
		if minimum < 1 {
			minimum = 5
		}
		if minimum > 100 {
			minimum = 100
		}
		candidates, err := c.store.ListDistillationCandidateClusters(ctx, minimum, now)
		if err != nil {
			return mcptransport.WebsiteKnowledgeToolResult{}, err
		}
		if candidates == nil {
			candidates = []websitechannel.WebsiteDistillationCandidateCluster{}
		}
		return mcptransport.WebsiteKnowledgeToolResult{Output: map[string]any{"window": "7d", "candidates": candidates}}, nil
	case string(defaultWebsiteFAQCatalogListToolID):
		if c.catalog == nil {
			return mcptransport.WebsiteKnowledgeToolResult{}, fmt.Errorf("website FAQ catalog is not configured")
		}
		return mcptransport.WebsiteKnowledgeToolResult{Output: map[string]any{"revision": c.catalog.Revision(), "entries": c.catalog.Entries()}}, nil
	case string(defaultWebsiteKnowledgeGapListToolID):
		minimum := request.MinimumCount
		if minimum < 1 {
			minimum = websiteKnowledgeCuratorDefaultMinimumCount
		}
		if minimum > 100 {
			minimum = 100
		}
		gaps, err := c.store.ListKnowledgeGaps(ctx, minimum, now)
		if err != nil {
			return mcptransport.WebsiteKnowledgeToolResult{}, err
		}
		return mcptransport.WebsiteKnowledgeToolResult{Output: map[string]any{"gaps": gaps}}, nil
	case string(defaultWebsiteFAQMetricsListToolID):
		metrics, err := c.store.ListWebsiteFAQMetrics(ctx, now.Add(-7*24*time.Hour))
		if err != nil {
			return mcptransport.WebsiteKnowledgeToolResult{}, err
		}
		if metrics == nil {
			metrics = []websitechannel.WebsiteFAQMetric{}
		}
		return mcptransport.WebsiteKnowledgeToolResult{Output: map[string]any{"window": "7d", "metrics": metrics}}, nil
	case string(defaultWebsiteKnowledgeProposalCreateToolID):
		var faqRevision *websitechannel.WebsiteFAQRevision
		if request.FAQRevision != nil {
			encoded, err := json.Marshal(request.FAQRevision)
			if err != nil {
				return mcptransport.WebsiteKnowledgeToolResult{}, err
			}
			faqRevision = &websitechannel.WebsiteFAQRevision{}
			if err := json.Unmarshal(encoded, faqRevision); err != nil {
				return mcptransport.WebsiteKnowledgeToolResult{}, err
			}
		}
		proposal, err := c.store.CreateProposal(ctx, websitechannel.WebsiteKnowledgeProposalInput{
			Kind: websitechannel.WebsiteKnowledgeProposalKind(request.ProposalKind), GapID: request.GapID,
			TargetKnowledgeKey: request.TargetKnowledgeKey, Draft: request.Draft, SourceRefs: request.SourceRefs,
			Rationale: request.Rationale, CreatedBy: request.PrincipalID, FAQRevision: faqRevision, Now: now,
		})
		if err != nil {
			return mcptransport.WebsiteKnowledgeToolResult{}, err
		}
		if c.publisher != nil && (proposal.ProposalKind == websitechannel.ProposalKindFAQAlias || proposal.ProposalKind == websitechannel.ProposalKindFAQSuggestionOrder) {
			proposal, err = c.publisher.PublishEligibleProposal(ctx, proposal, now)
			if err != nil {
				return mcptransport.WebsiteKnowledgeToolResult{}, err
			}
		}
		return mcptransport.WebsiteKnowledgeToolResult{Output: map[string]any{"proposal": proposal}}, nil
	default:
		return mcptransport.WebsiteKnowledgeToolResult{}, fmt.Errorf("website knowledge tool is not allowed")
	}
}

func newProductionWebsiteAdvisor(_ *platform.Runtime, core *db.Store, gateway *gwcore.Gateway, mcpGateway productionMCPGatewayCaller, gapStore websitechannel.WebsiteKnowledgeGapStore, faqCatalog *websitechannel.WebsiteFAQCatalog) (*websitechannel.Handler, error) {
	options := websitechannel.Options{
		AllowedOrigins:  websiteAdvisorAllowedOrigins(),
		PublicOnly:      true,
		HistoryStore:    websitechannel.NewSQLWebsiteHistoryStore(core),
		MarketingLeads:  productionWebsiteMarketingLeadCapture{gateway: mcpGateway},
		KnowledgeSearch: productionWebsiteKnowledgeSearch{gateway: mcpGateway},
		GapStore:        gapStore,
		FAQCatalog:      faqCatalog,
	}
	if distillationStore, ok := gapStore.(websitechannel.WebsiteDistillationStore); ok {
		options.DistillationStore = distillationStore
	}
	if strings.TrimSpace(os.Getenv("ORRERY_WEBSITE_IDENTITY_ASSERTION_KEY")) != "" {
		ownerHMACKey, err := websiteAdvisorOwnerHMACKey()
		if err != nil {
			return nil, err
		}
		assertionKey, err := websiteIdentityAssertionKey()
		if err != nil {
			return nil, err
		}
		identityAssertions, err := websitechannel.NewWebsiteIdentityAssertionVerifier(assertionKey, nil)
		if err != nil {
			return nil, err
		}
		options.IdentityAssertions = identityAssertions
		options.PublicOnly = false
		humanVerificationAssertions, err := websitechannel.NewWebsiteHumanVerificationAssertionVerifier(assertionKey, nil)
		if err != nil {
			return nil, err
		}
		options.HumanVerificationAssertions = humanVerificationAssertions
		options.OwnerHMACKey = ownerHMACKey
	}
	return websitechannel.NewHandlerChecked(gateway, options)
}

func (s productionWebsiteKnowledgeSearch) Search(ctx context.Context, tier protocol.AdvisorTier, query string) (websitechannel.WebsiteKnowledgeSearchResult, error) {
	if s.gateway == nil {
		return websitechannel.WebsiteKnowledgeSearchResult{}, fmt.Errorf("website knowledge search is not configured")
	}
	toolID := websitePublicKnowledgeSearchToolID
	permission := defaultOrreryPublicMCPPermissionScope
	if tier == protocol.AdvisorTierCustomer {
		toolID = websiteCustomerKnowledgeSearchToolID
		permission = defaultOrreryCustomerMCPPermissionScope
	}
	result, err := s.gateway.CallTool(ctx, mcptransport.CallToolRequest{
		ToolID: toolID, PrincipalID: "website-knowledge-search",
		Permissions: []string{permission},
		Arguments:   map[string]any{"query": strings.TrimSpace(query), "limit": 5},
	})
	if err != nil {
		return websitechannel.WebsiteKnowledgeSearchResult{}, err
	}
	resolution, ok := result.Output["resolution"].(string)
	if !ok {
		if results, rawResults := result.Output["results"].([]any); rawResults {
			return rawPageIndexSearchResult(results, tier), nil
		}
		return websitechannel.WebsiteKnowledgeSearchResult{}, fmt.Errorf("website knowledge search returned an invalid resolution")
	}
	return websitechannel.WebsiteKnowledgeSearchResult{
		Resolution: websitechannel.WebsiteSearchResolution(strings.TrimSpace(resolution)),
		Answer:     outputString(result.Output, "answer"),
		SourceURI:  outputString(result.Output, "source_uri"),
		Revision:   outputString(result.Output, "approval_revision"),
		Score:      outputCandidateScore(result.Output),
	}, nil
}

func rawPageIndexSearchResult(results []any, tier protocol.AdvisorTier) websitechannel.WebsiteKnowledgeSearchResult {
	result := websitechannel.WebsiteKnowledgeSearchResult{Resolution: websitechannel.WebsiteSearchNeedsClarification}
	if len(results) == 0 {
		return result
	}
	candidate, ok := results[0].(map[string]any)
	if !ok {
		return result
	}
	source := outputString(candidate, "source_uri")
	if source == "" {
		source = outputString(candidate, "original_file_url")
	}
	if strings.HasPrefix(source, "minviabrain://08-Wiki/Public/Orrery/") ||
		(tier == protocol.AdvisorTierCustomer && strings.HasPrefix(source, "minviabrain://08-Wiki/Customer/Orrery/")) {
		result.SourceURI = source
	}
	result.Revision = outputString(candidate, "original_checksum")
	result.Score, _ = candidate["score"].(float64)
	content := outputString(candidate, "content")
	if result.SourceURI != "" && result.Revision != "" && content != "" {
		result.Resolution = websitechannel.WebsiteSearchDirectAnswer
		result.Answer = content
		if title := outputString(candidate, "title"); title != "" {
			result.Answer = title + "\n\n" + content
		}
	}
	return result
}

func outputCandidateScore(output map[string]any) float64 {
	candidates, ok := output["candidates"].([]any)
	if !ok || len(candidates) == 0 {
		return 0
	}
	candidate, ok := candidates[0].(map[string]any)
	if !ok {
		return 0
	}
	score, _ := candidate["score"].(float64)
	return score
}

func outputString(output map[string]any, key string) string {
	value, _ := output[key].(string)
	return strings.TrimSpace(value)
}

func (a productionWebsiteMarketingLeadCapture) CaptureWebsiteLead(ctx context.Context, input websitechannel.MarketingLeadCaptureInput) (websitechannel.MarketingLeadReceipt, error) {
	if a.gateway == nil {
		return websitechannel.MarketingLeadReceipt{}, fmt.Errorf("website marketing lead capture is not configured")
	}
	arguments := map[string]any{
		"idempotency_key": input.IdempotencyKey,
		"enquiry":         input.Enquiry,
		"consent_version": input.ConsentVersion,
		"consent_at":      input.ConsentAt.UTC().Format(time.RFC3339Nano),
	}
	for key, value := range map[string]string{
		"contact_name": input.ContactName, "company": input.Company, "role": input.Role,
		"email": input.Email, "phone": input.Phone, "interest": input.Interest,
		"deployment_preference": input.DeploymentPreference, "locale": input.Locale,
		"source_page_path": input.SourcePagePath, "conversation_id": input.ConversationID,
		"user_subject_ref": input.UserSubjectRef,
	} {
		if value = strings.TrimSpace(value); value != "" {
			arguments[key] = value
		}
	}
	result, err := a.gateway.CallTool(ctx, mcptransport.CallToolRequest{
		ToolID: websiteMarketingLeadCaptureToolID, PrincipalID: "website-lead-adapter",
		Permissions: []string{"marketing.operate", "marketing.lead.capture"}, Arguments: arguments,
	})
	if err != nil {
		return websitechannel.MarketingLeadReceipt{}, err
	}
	leadID, leadOK := result.Output["lead_id"].(string)
	status, statusOK := result.Output["status"].(string)
	source, sourceOK := result.Output["source"].(string)
	createdAtValue, createdOK := result.Output["created_at"].(string)
	createdAt, parseErr := time.Parse(time.RFC3339Nano, createdAtValue)
	if !leadOK || strings.TrimSpace(leadID) == "" || !statusOK || strings.TrimSpace(status) == "" || !sourceOK || source != "minvia_website_advisor" || !createdOK || parseErr != nil {
		return websitechannel.MarketingLeadReceipt{}, fmt.Errorf("marketing lead capture returned an invalid receipt")
	}
	return websitechannel.MarketingLeadReceipt{
		LeadID: leadID, Status: status, Source: source, CreatedAt: createdAt,
	}, nil
}

func websiteAdvisorAllowedOrigins() []string {
	if raw := strings.TrimSpace(os.Getenv("ORRERY_WEBSITE_ADVISOR_ALLOWED_ORIGINS")); raw != "" {
		parts := strings.Split(raw, ",")
		origins := make([]string, 0, len(parts))
		for _, part := range parts {
			if origin := strings.TrimSpace(part); origin != "" {
				origins = append(origins, origin)
			}
		}
		return origins
	}
	return []string{
		"http://127.0.0.1:4174",
		"http://localhost:4174",
		"https://minvia.cn",
		"https://www.minvia.cn",
	}
}

func websiteAdvisorOwnerHMACKey() ([]byte, error) {
	key := []byte(strings.TrimSpace(os.Getenv("ORRERY_WEBSITE_ADVISOR_OWNER_HMAC_KEY")))
	if len(key) < 32 {
		return nil, fmt.Errorf("ORRERY_WEBSITE_ADVISOR_OWNER_HMAC_KEY must contain at least 32 bytes")
	}
	return key, nil
}

func websiteIdentityAssertionKey() ([]byte, error) {
	key := []byte(strings.TrimSpace(os.Getenv("ORRERY_WEBSITE_IDENTITY_ASSERTION_KEY")))
	if len(key) < 32 {
		return nil, fmt.Errorf("ORRERY_WEBSITE_IDENTITY_ASSERTION_KEY must contain at least 32 bytes")
	}
	return key, nil
}

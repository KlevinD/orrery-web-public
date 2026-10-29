package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	websitechannel "orrery_v2/internal/gateway/channel/website"
	"orrery_v2/internal/gateway/protocol"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type fakeWebsiteKnowledgeCurationStore struct {
	metricsSince       time.Time
	candidatesMinimum  int
	candidatesObserved time.Time
	deletedBefore      time.Time
	createdInput       websitechannel.WebsiteKnowledgeProposalInput
	createdProposal    websitechannel.WebsiteKnowledgeProposal
	emptyInputs        bool
	readErr            error
}

func (f *fakeWebsiteKnowledgeCurationStore) ListKnowledgeGaps(context.Context, int, time.Time) ([]websitechannel.WebsiteGapAggregate, error) {
	return nil, nil
}

func (f *fakeWebsiteKnowledgeCurationStore) ListWebsiteFAQMetrics(_ context.Context, since time.Time) ([]websitechannel.WebsiteFAQMetric, error) {
	f.metricsSince = since
	if f.emptyInputs || f.readErr != nil {
		return nil, f.readErr
	}
	return []websitechannel.WebsiteFAQMetric{{IntentID: "orrery", Route: "standard_answer", Outcome: websitechannel.GapOutcomeResolved, ConversationCount: 7}}, nil
}

func (f *fakeWebsiteKnowledgeCurationStore) CreateProposal(_ context.Context, input websitechannel.WebsiteKnowledgeProposalInput) (websitechannel.WebsiteKnowledgeProposal, error) {
	f.createdInput = input
	return f.createdProposal, nil
}

func (f *fakeWebsiteKnowledgeCurationStore) ListDistillationCandidateClusters(_ context.Context, minimum int, now time.Time) ([]websitechannel.WebsiteDistillationCandidateCluster, error) {
	f.candidatesMinimum = minimum
	f.candidatesObserved = now
	if f.emptyInputs || f.readErr != nil {
		return nil, f.readErr
	}
	return []websitechannel.WebsiteDistillationCandidateCluster{{ClusterID: "wdc_test", ConversationCount: 5}}, nil
}

func (f *fakeWebsiteKnowledgeCurationStore) DeleteExpiredDistillationCandidates(_ context.Context, cutoff time.Time) (int, error) {
	f.deletedBefore = cutoff
	return 2, nil
}

type fakeWebsiteLowRiskPublisher struct {
	calls    int
	received websitechannel.WebsiteKnowledgeProposal
}

func (f *fakeWebsiteLowRiskPublisher) PublishEligibleProposal(_ context.Context, proposal websitechannel.WebsiteKnowledgeProposal, _ time.Time) (websitechannel.WebsiteKnowledgeProposal, error) {
	f.calls++
	f.received = proposal
	proposal.Status = websitechannel.ProposalStatusPublished
	return proposal, nil
}

func TestProductionServerMountsAnonymousWebsiteAdvisorWithoutOpeningOtherAPIs(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", persistentProductionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	t.Setenv("ORRERY_WEBSITE_ADVISOR_ALLOWED_ORIGINS", "https://www.minvia.cn")
	t.Setenv("ORRERY_WEBSITE_ADVISOR_OWNER_HMAC_KEY", "test-hmac-key-"+"0123456789abcdef"+"!!")
	t.Setenv("ORRERY_WEBSITE_IDENTITY_ASSERTION_KEY", "test-assertion-key-"+"0123456789abc")
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler: %v", err)
	}
	defer cleanup()

	request := httptest.NewRequest(http.MethodPost, "/api/advisor/sessions", nil)
	request.Header.Set("Origin", "https://www.minvia.cn")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, request)
	if rec.Code != http.StatusCreated {
		t.Fatalf("advisor session status = %d body=%s", rec.Code, rec.Body.String())
	}
	var session struct {
		SessionID   string `json:"session_id"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&session); err != nil {
		t.Fatalf("decode advisor session: %v", err)
	}
	if session.SessionID == "" || session.AccessToken == "" {
		t.Fatalf("advisor session = %+v", session)
	}

	protected := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, protected, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("settings status = %d body=%s", protected.Code, protected.Body.String())
	}
}

func TestProductionServerAllowsVerifiedWebsiteCustomerWhenIdentityAssertionsConfigured(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", persistentProductionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	t.Setenv("ORRERY_WEBSITE_ADVISOR_ALLOWED_ORIGINS", "https://www.minvia.cn")
	t.Setenv("ORRERY_WEBSITE_ADVISOR_OWNER_HMAC_KEY", "test-hmac-key-"+"0123456789abcdef"+"!!")
	assertionKey := "test-assertion-key-" + "0123456789abc"
	t.Setenv("ORRERY_WEBSITE_IDENTITY_ASSERTION_KEY", assertionKey)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler: %v", err)
	}
	defer cleanup()

	request := httptest.NewRequest(http.MethodGet, "/api/advisor/account", nil)
	request.Header.Set("Origin", "https://www.minvia.cn")
	request.Header.Set("X-Website-Identity", signProductionWebsiteClaims(t, assertionKey, time.Now().UTC()))
	recorder := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("verified customer account status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWebsiteAdvisorAllowedOriginsUseExplicitOverride(t *testing.T) {
	t.Setenv("ORRERY_WEBSITE_ADVISOR_ALLOWED_ORIGINS", " https://minvia.cn, https://www.minvia.cn ,,http://127.0.0.1:4174 ")
	origins := websiteAdvisorAllowedOrigins()
	if len(origins) != 3 || origins[0] != "https://minvia.cn" || origins[2] != "http://127.0.0.1:4174" {
		t.Fatalf("origins = %+v", origins)
	}
}

func TestWebsiteAdvisorOwnerHMACKeyRequiresThirtyTwoBytes(t *testing.T) {
	t.Setenv("ORRERY_WEBSITE_ADVISOR_OWNER_HMAC_KEY", "short")
	if _, err := websiteAdvisorOwnerHMACKey(); err == nil {
		t.Fatal("short key was accepted")
	}
	t.Setenv("ORRERY_WEBSITE_ADVISOR_OWNER_HMAC_KEY", "test-hmac-key-"+"0123456789abcdef"+"!!")
	key, err := websiteAdvisorOwnerHMACKey()
	if err != nil || len(key) != 32 {
		t.Fatalf("key length=%d err=%v", len(key), err)
	}
}

func TestWebsiteIdentityAssertionKeyRequiresThirtyTwoBytes(t *testing.T) {
	t.Setenv("ORRERY_WEBSITE_IDENTITY_ASSERTION_KEY", "short")
	if _, err := websiteIdentityAssertionKey(); err == nil {
		t.Fatal("short assertion key was accepted")
	}
	t.Setenv("ORRERY_WEBSITE_IDENTITY_ASSERTION_KEY", "test-assertion-key-"+"0123456789abc")
	key, err := websiteIdentityAssertionKey()
	if err != nil || len(key) != 32 {
		t.Fatalf("key length=%d err=%v", len(key), err)
	}
}

func signProductionWebsiteClaims(t *testing.T, key string, now time.Time) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal assertion header: %v", err)
	}
	claims, err := json.Marshal(map[string]any{
		"aud": "orrery-website-channel", "exp": now.Add(4 * time.Minute).Unix(), "iat": now.Unix(),
		"iss": "minvia-website-auth", "nonce": "production-customer-test", "sub": "customer-test", "tier": "customer",
	})
	if err != nil {
		t.Fatalf("marshal assertion claims: %v", err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claims)
	unsigned := encodedHeader + "." + encodedClaims
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestWebsiteMarketingLeadAdapterCallsOnlyFixedCaptureToolWithoutTenantBinding(t *testing.T) {
	gateway := &serverTestMCPGatewayCaller{result: mcptransport.CallToolResult{Output: map[string]any{
		"lead_id": "lead_123", "status": "new", "source": "minvia_website_advisor", "created_at": "2026-07-26T09:00:00Z",
	}}}
	adapter := productionWebsiteMarketingLeadCapture{gateway: gateway}
	consentAt := time.Date(2026, 7, 26, 8, 59, 0, 0, time.UTC)
	receipt, err := adapter.CaptureWebsiteLead(context.Background(), websitechannel.MarketingLeadCaptureInput{
		IdempotencyKey: "submission-1", ContactName: "User One", Company: "Minvia",
		Role: "Operations", Email: "user@example.com", Phone: "13800000000",
		Interest: "Orrery", DeploymentPreference: "private", Enquiry: "Request a demo",
		Locale: "zh-CN", SourcePagePath: "/products/orrery", ConversationID: "webconv_123",
		UserSubjectRef: "v1_owner", Source: "minvia_website_advisor",
		ConsentVersion: "website-lead-v1", ConsentAt: consentAt,
	})
	if err != nil {
		t.Fatalf("CaptureWebsiteLead: %v", err)
	}
	if receipt.LeadID != "lead_123" || receipt.Status != "new" || receipt.Source != "minvia_website_advisor" || !receipt.CreatedAt.Equal(time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("receipt = %+v", receipt)
	}
	request := gateway.req
	if request.ToolID != "mcp.marketing.marketing_lead_capture_create" || request.PrincipalID != "website-lead-adapter" {
		t.Fatalf("fixed identity = %+v", request)
	}
	if request.TenantID != "" || request.AgentID != "" || request.ProfileID != "" {
		t.Fatalf("tenant or agent binding crossed boundary = %+v", request)
	}
	if len(request.Permissions) != 2 || request.Permissions[0] != "marketing.operate" || request.Permissions[1] != "marketing.lead.capture" {
		t.Fatalf("permissions = %+v", request.Permissions)
	}
	if request.Arguments["idempotency_key"] != "submission-1" || request.Arguments["consent_at"] != consentAt.Format(time.RFC3339Nano) || request.Arguments["source"] != nil {
		t.Fatalf("arguments = %+v", request.Arguments)
	}
}

func TestWebsiteKnowledgeSearchAdapterUsesCustomerToolForCustomerTier(t *testing.T) {
	gateway := &serverTestMCPGatewayCaller{result: mcptransport.CallToolResult{Output: map[string]any{
		"resolution":        "direct_answer",
		"answer":            "Approved customer answer",
		"source_uri":        "minviabrain://08-Wiki/Customer/Orrery/answer.md",
		"approval_revision": "approval-customer-1",
	}}}
	adapter := productionWebsiteKnowledgeSearch{gateway: gateway}

	result, err := adapter.Search(context.Background(), protocol.AdvisorTierCustomer, "How does it work?")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Resolution != websitechannel.WebsiteSearchDirectAnswer || result.Answer != "Approved customer answer" || result.Revision != "approval-customer-1" {
		t.Fatalf("result = %+v", result)
	}
	request := gateway.req
	if request.ToolID != "mcp.orrery-customer-readonly.search_documents" || request.PrincipalID != "website-knowledge-search" {
		t.Fatalf("fixed search identity = %+v", request)
	}
	if request.TenantID != "" || request.AgentID != "" || request.ProfileID != "" {
		t.Fatalf("search crossed tenant or agent boundary = %+v", request)
	}
	if len(request.Permissions) != 1 || request.Permissions[0] != defaultOrreryCustomerMCPPermissionScope || request.Arguments["limit"] != 5 {
		t.Fatalf("search scope = %+v", request)
	}
}

func TestWebsiteKnowledgeSearchAdapterReturnsCustomerPageIndexExcerpt(t *testing.T) {
	gateway := &serverTestMCPGatewayCaller{result: mcptransport.CallToolResult{Output: map[string]any{
		"results": []any{map[string]any{
			"title":             "Website login boundary",
			"content":           "官网客户登录与 Orrery 租户身份必须保持独立。",
			"original_file_url": "minviabrain://08-Wiki/Customer/Orrery/website-login-boundary.md",
			"original_checksum": "sha256:customer-revision",
		}},
	}}}

	result, err := (productionWebsiteKnowledgeSearch{gateway: gateway}).Search(
		context.Background(), protocol.AdvisorTierCustomer, "官网客户登录边界",
	)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Resolution != websitechannel.WebsiteSearchDirectAnswer ||
		result.Answer != "Website login boundary\n\n官网客户登录与 Orrery 租户身份必须保持独立。" ||
		result.SourceURI != "minviabrain://08-Wiki/Customer/Orrery/website-login-boundary.md" ||
		result.Revision != "sha256:customer-revision" {
		t.Fatalf("result = %+v", result)
	}
}

func TestWebsiteKnowledgeSearchAdapterReturnsPublicPageIndexExcerpt(t *testing.T) {
	gateway := &serverTestMCPGatewayCaller{result: mcptransport.CallToolResult{Output: map[string]any{
		"results": []any{map[string]any{
			"document_id":       "doc_public",
			"title":             "Orrery enterprise integration",
			"content":           "Tools are registered and explicitly bound to agent profiles and missions.",
			"source_uri":        "minviabrain://08-Wiki/Public/Orrery/integration-smoke.md",
			"original_checksum": "sha256:public-revision",
		}},
	}}}

	result, err := (productionWebsiteKnowledgeSearch{gateway: gateway}).Search(
		context.Background(), protocol.AdvisorTierPublic, "Design an ERP integration",
	)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Resolution != websitechannel.WebsiteSearchDirectAnswer ||
		result.Answer != "Orrery enterprise integration\n\nTools are registered and explicitly bound to agent profiles and missions." ||
		result.SourceURI != "minviabrain://08-Wiki/Public/Orrery/integration-smoke.md" ||
		result.Revision != "sha256:public-revision" {
		t.Fatalf("result = %+v", result)
	}
}

func TestWebsiteKnowledgeSearchAdapterRejectsCustomerExcerptForPublicTier(t *testing.T) {
	gateway := &serverTestMCPGatewayCaller{result: mcptransport.CallToolResult{Output: map[string]any{
		"results": []any{map[string]any{
			"content":           "Customer-only content.",
			"source_uri":        "minviabrain://08-Wiki/Customer/Orrery/account-and-advisor.md",
			"original_checksum": "sha256:customer-revision",
		}},
	}}}

	result, err := (productionWebsiteKnowledgeSearch{gateway: gateway}).Search(
		context.Background(), protocol.AdvisorTierPublic, "Customer-only question",
	)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Resolution != websitechannel.WebsiteSearchNeedsClarification || result.Answer != "" || result.SourceURI != "" {
		t.Fatalf("customer excerpt crossed public boundary: %+v", result)
	}
}

func TestDefaultPublicKnowledgeMCPAllowsColdRetrieval(t *testing.T) {
	definition := defaultOrreryPublicMCPServerDefinition(time.Now())
	if definition.MCP == nil || definition.MCP.CallTimeoutSeconds != 60 {
		t.Fatalf("public MCP timeout = %+v", definition.MCP)
	}
}

func TestWebsiteKnowledgeCurationMetricsUsesFixedSevenDayAggregateWindow(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	store := &fakeWebsiteKnowledgeCurationStore{}
	adapter := productionWebsiteKnowledgeCuration{store: store, now: func() time.Time { return now }}
	result, err := adapter.ExecuteWebsiteKnowledgeTool(context.Background(), string(defaultWebsiteFAQMetricsListToolID), mcptransport.WebsiteKnowledgeToolRequest{PrincipalID: "website-knowledge-curator"})
	if err != nil {
		t.Fatal(err)
	}
	metrics, ok := result.Output["metrics"].([]websitechannel.WebsiteFAQMetric)
	if !ok || len(metrics) != 1 || metrics[0].IntentID != "orrery" || result.Output["window"] != "7d" {
		t.Fatalf("result = %+v", result)
	}
	if !store.metricsSince.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Fatalf("metrics since = %s", store.metricsSince)
	}
}

func TestWebsiteKnowledgeCandidateAndFAQCatalogToolsExposeOnlySanitizedMaintenanceInputs(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	store := &fakeWebsiteKnowledgeCurationStore{}
	catalog := websitechannel.NewWebsiteFAQCatalog(nil)
	adapter := productionWebsiteKnowledgeCuration{store: store, catalog: catalog, now: func() time.Time { return now }}

	candidates, err := adapter.ExecuteWebsiteKnowledgeTool(context.Background(), string(defaultWebsiteDistillationCandidatesListToolID), mcptransport.WebsiteKnowledgeToolRequest{MinimumCount: 5})
	if err != nil {
		t.Fatal(err)
	}
	clusters, ok := candidates.Output["candidates"].([]websitechannel.WebsiteDistillationCandidateCluster)
	if !ok || len(clusters) != 1 || clusters[0].ClusterID != "wdc_test" || store.candidatesMinimum != 5 || !store.candidatesObserved.Equal(now) {
		t.Fatalf("candidate output=%+v store=%+v", candidates.Output, store)
	}
	if !store.deletedBefore.IsZero() {
		t.Fatalf("candidate read deleted data before %s", store.deletedBefore)
	}

	faq, err := adapter.ExecuteWebsiteKnowledgeTool(context.Background(), string(defaultWebsiteFAQCatalogListToolID), mcptransport.WebsiteKnowledgeToolRequest{})
	if err != nil {
		t.Fatal(err)
	}
	entries, ok := faq.Output["entries"].([]websitechannel.WebsiteFAQCatalogEntry)
	if !ok || len(entries) == 0 || faq.Output["revision"] == "" {
		t.Fatalf("faq output=%+v", faq.Output)
	}
}

func TestProductionWebsiteKnowledgeCurationAutoPublishesOnlyLowRiskRules(t *testing.T) {
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	store := &fakeWebsiteKnowledgeCurationStore{createdProposal: websitechannel.WebsiteKnowledgeProposal{
		ProposalID: "wkp_alias", ProposalKind: websitechannel.ProposalKindFAQAlias, Status: websitechannel.ProposalStatusDraft,
	}}
	publisher := &fakeWebsiteLowRiskPublisher{}
	adapter := productionWebsiteKnowledgeCuration{store: store, publisher: publisher, now: func() time.Time { return now }}
	result, err := adapter.ExecuteWebsiteKnowledgeTool(context.Background(), string(defaultWebsiteKnowledgeProposalCreateToolID), mcptransport.WebsiteKnowledgeToolRequest{
		ProposalKind: string(websitechannel.ProposalKindFAQAlias), GapID: "wdc_test", TargetKnowledgeKey: "08-Wiki/Public/Orrery/faq/orrery.md",
		Draft: "alias", SourceRefs: []string{websiteDistillationTestSourceForServer}, Rationale: "evidence", PrincipalID: "website-knowledge-curator",
	})
	if err != nil || publisher.calls != 1 || result.Output["proposal"].(websitechannel.WebsiteKnowledgeProposal).Status != websitechannel.ProposalStatusPublished {
		t.Fatalf("result=%+v calls=%d err=%v", result, publisher.calls, err)
	}

	store.createdProposal = websitechannel.WebsiteKnowledgeProposal{ProposalID: "wkp_factual", ProposalKind: websitechannel.ProposalKindFAQEntry, Status: websitechannel.ProposalStatusDraft}
	result, err = adapter.ExecuteWebsiteKnowledgeTool(context.Background(), string(defaultWebsiteKnowledgeProposalCreateToolID), mcptransport.WebsiteKnowledgeToolRequest{
		ProposalKind: string(websitechannel.ProposalKindFAQEntry), GapID: "wkg_test", TargetKnowledgeKey: "08-Wiki/Public/Orrery/faq/new.md",
		Draft: "factual", SourceRefs: []string{websiteDistillationTestSourceForServer}, Rationale: "needs review", PrincipalID: "website-knowledge-curator",
	})
	if err != nil || publisher.calls != 1 || result.Output["proposal"].(websitechannel.WebsiteKnowledgeProposal).Status != websitechannel.ProposalStatusDraft {
		t.Fatalf("factual result=%+v calls=%d err=%v", result, publisher.calls, err)
	}
}

const websiteDistillationTestSourceForServer = "minviabrain://08-Wiki/Public/Orrery/overview.md"

func TestWebsiteMaintenanceReadsDistinguishEmptyInputsFromErrors(t *testing.T) {
	for toolID, field := range map[string]string{
		string(defaultWebsiteDistillationCandidatesListToolID): "candidates",
		string(defaultWebsiteFAQMetricsListToolID):             "metrics",
	} {
		t.Run(field, func(t *testing.T) {
			store := &fakeWebsiteKnowledgeCurationStore{emptyInputs: true}
			adapter := productionWebsiteKnowledgeCuration{store: store}
			result, err := adapter.ExecuteWebsiteKnowledgeTool(context.Background(), toolID, mcptransport.WebsiteKnowledgeToolRequest{})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(result.Output[field])
			if err != nil || string(encoded) != "[]" || !store.deletedBefore.IsZero() {
				t.Fatalf("empty read=%s error=%v store=%+v", encoded, err, store)
			}
			store.readErr = errors.New("input store unavailable")
			_, err = adapter.ExecuteWebsiteKnowledgeTool(context.Background(), toolID, mcptransport.WebsiteKnowledgeToolRequest{})
			if !errors.Is(err, store.readErr) {
				t.Fatalf("read failure must not become an empty success: %v", err)
			}
		})
	}
}

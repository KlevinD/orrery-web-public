package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/gateway/channel/website"
)

type fakeWebsiteKnowledgeGovernance struct {
	proposalInput  website.WebsiteKnowledgeProposalInput
	approvedBy     string
	publishedBy    string
	rulesSaved     int
	proposalStatus website.ProposalStatus
	proposalLimit  int
}

func (f *fakeWebsiteKnowledgeGovernance) RecordGap(context.Context, website.WebsiteGapEvent) (website.WebsiteGapAggregate, error) {
	return website.WebsiteGapAggregate{}, nil
}
func (f *fakeWebsiteKnowledgeGovernance) ListKnowledgeGaps(context.Context, int, time.Time) ([]website.WebsiteGapAggregate, error) {
	return []website.WebsiteGapAggregate{{GapID: "wkg_1", TopicFingerprint: strings.Repeat("a", 64), ConversationCount: 5, Candidate: true}}, nil
}
func (f *fakeWebsiteKnowledgeGovernance) SaveRuleRevision(_ context.Context, _ website.AutomaticRuleRevision) error {
	f.rulesSaved++
	return nil
}
func (f *fakeWebsiteKnowledgeGovernance) EvaluateRuleRevision(_ context.Context, id string, _ bool, _, _ float64, now time.Time) (website.AutomaticRuleRevision, error) {
	return website.AutomaticRuleRevision{RevisionID: id, RolledBackAt: now, RollbackReason: "source_validity_regression"}, nil
}
func (f *fakeWebsiteKnowledgeGovernance) CreateProposal(_ context.Context, input website.WebsiteKnowledgeProposalInput) (website.WebsiteKnowledgeProposal, error) {
	f.proposalInput = input
	return website.NewWebsiteKnowledgeProposal(input)
}
func (f *fakeWebsiteKnowledgeGovernance) ApproveProposal(_ context.Context, id, hash, actor string, now time.Time) (website.WebsiteKnowledgeProposal, error) {
	f.approvedBy = actor
	return website.WebsiteKnowledgeProposal{ProposalID: id, RevisionHash: hash, Status: website.ProposalStatusApproved, ApprovedBy: actor, ApprovedAt: now}, nil
}
func (f *fakeWebsiteKnowledgeGovernance) PublishProposal(_ context.Context, id, hash, actor string, now time.Time) (website.WebsiteKnowledgeProposal, error) {
	f.publishedBy = actor
	return website.WebsiteKnowledgeProposal{ProposalID: id, RevisionHash: hash, Status: website.ProposalStatusPublished, PublishedBy: actor, PublishedAt: now}, nil
}
func (f *fakeWebsiteKnowledgeGovernance) ListWebsiteKnowledgeProposals(_ context.Context, status website.ProposalStatus, limit int) ([]website.WebsiteKnowledgeProposal, error) {
	f.proposalStatus, f.proposalLimit = status, limit
	return []website.WebsiteKnowledgeProposal{{ProposalID: "wkp_1", ProposalKind: website.ProposalKindFAQEntry, RevisionHash: "hash-1", Status: status}}, nil
}
func (f *fakeWebsiteKnowledgeGovernance) GetWebsiteKnowledgeProposal(_ context.Context, id string) (website.WebsiteKnowledgeProposal, error) {
	return website.WebsiteKnowledgeProposal{ProposalID: id, ProposalKind: website.ProposalKindFAQEntry, RevisionHash: "hash-1", Status: website.ProposalStatusDraft}, nil
}
func (f *fakeWebsiteKnowledgeGovernance) ListWebsiteKnowledgeReceipts(_ context.Context, id string) ([]website.WebsiteKnowledgeReceipt, error) {
	return []website.WebsiteKnowledgeReceipt{{ReceiptID: "app_1", Kind: website.WebsiteKnowledgeReceiptApproval, ProposalID: id, RevisionHash: "hash-1", ActorID: "reviewer"}}, nil
}
func (f *fakeWebsiteKnowledgeGovernance) ListWebsiteFAQMetrics(context.Context, time.Time) ([]website.WebsiteFAQMetric, error) {
	return []website.WebsiteFAQMetric{{IntentID: "orrery", Route: "standard_answer", Outcome: website.GapOutcomeResolved, ConversationCount: 12}}, nil
}
func (f *fakeWebsiteKnowledgeGovernance) ListPublishedFAQRevisions(context.Context) ([]website.WebsiteFAQRevision, error) {
	return nil, nil
}

func TestWebsiteKnowledgeGovernanceRoutesUseExistingAuthScopesAndServerActor(t *testing.T) {
	service := &fakeWebsiteKnowledgeGovernance{}
	router := newTestRouter(RouterOptions{WebsiteKnowledgeGovernance: service})

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/website-knowledge/gaps", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	list := authedRequest(http.MethodGet, "/api/website-knowledge/gaps?minimum_count=3", "")
	list.Header.Set("X-Permission-Scope", "knowledge.website_gap.propose")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, list)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"conversation_count":5`) {
		t.Fatalf("list=%d %s", recorder.Code, recorder.Body.String())
	}

	create := authedRequest(http.MethodPost, "/api/website-knowledge/proposals", `{"gap_id":"wkg_1","target_knowledge_key":"08-Wiki/Public/Orrery/faq.md","draft":"Reviewed answer","source_refs":["minviabrain://08-Wiki/Public/Orrery/overview.md"],"rationale":"Repeated demand"}`)
	create.Header.Set("X-Permission-Scope", "knowledge.website_gap.propose")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, create)
	if recorder.Code != http.StatusCreated || service.proposalInput.CreatedBy != "agent-1" {
		t.Fatalf("create=%d actor=%q body=%s", recorder.Code, service.proposalInput.CreatedBy, recorder.Body.String())
	}

	curatorApprove := authedRequest(http.MethodPost, "/api/website-knowledge/proposals/wkp_1/approve", `{"revision_hash":"hash-1"}`)
	curatorApprove.Header.Set("X-Permission-Scope", "knowledge.website_gap.propose")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, curatorApprove)
	if recorder.Code != http.StatusForbidden || service.approvedBy != "" {
		t.Fatalf("curator approve=%d actor=%q body=%s", recorder.Code, service.approvedBy, recorder.Body.String())
	}

	approve := authedRequest(http.MethodPost, "/api/website-knowledge/proposals/wkp_1/approve", `{"revision_hash":"hash-1"}`)
	approve.Header.Set("X-Permission-Scope", "approvals.decide")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, approve)
	if recorder.Code != http.StatusOK || service.approvedBy != "agent-1" {
		t.Fatalf("approve=%d actor=%q body=%s", recorder.Code, service.approvedBy, recorder.Body.String())
	}
}

func TestWebsiteKnowledgeGovernanceRejectsCallerAuthorityFields(t *testing.T) {
	service := &fakeWebsiteKnowledgeGovernance{}
	router := newTestRouter(RouterOptions{WebsiteKnowledgeGovernance: service})
	request := authedRequest(http.MethodPost, "/api/website-knowledge/proposals", `{"gap_id":"wkg_1","target_knowledge_key":"08-Wiki/Public/Orrery/faq.md","draft":"Answer","source_refs":["minviabrain://08-Wiki/Public/Orrery/overview.md"],"rationale":"Demand","created_by":"browser","tenant_id":"tenant-1"}`)
	request.Header.Set("X-Permission-Scope", "knowledge.website_gap.propose")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || service.proposalInput.CreatedBy != "" {
		t.Fatalf("authority fields status=%d input=%+v body=%s", recorder.Code, service.proposalInput, recorder.Body.String())
	}
}

func TestWebsiteKnowledgeGovernanceCapabilitiesSeparateCuratorReviewAndPublish(t *testing.T) {
	capabilities := defaultPermissionCapabilities()
	assertCapability(t, capabilities, http.MethodGet, "/api/website-knowledge/gaps", "knowledge.website_gap.propose")
	assertCapability(t, capabilities, http.MethodGet, "/api/website-knowledge/faqs", "knowledge.read")
	assertCapability(t, capabilities, http.MethodGet, "/api/website-knowledge/metrics", "knowledge.read")
	assertCapability(t, capabilities, http.MethodGet, "/api/website-knowledge/proposals", "knowledge.read")
	assertCapability(t, capabilities, http.MethodGet, "/api/website-knowledge/proposals/{proposal_id}", "knowledge.read")
	assertCapability(t, capabilities, http.MethodGet, "/api/website-knowledge/proposals/{proposal_id}/receipts", "knowledge.read")
	assertCapability(t, capabilities, http.MethodPost, "/api/website-knowledge/proposals", "knowledge.website_gap.propose")
	assertCapability(t, capabilities, http.MethodPost, "/api/website-knowledge/proposals/{proposal_id}/approve", "approvals.decide")
	assertCapability(t, capabilities, http.MethodPost, "/api/website-knowledge/proposals/{proposal_id}/publish", "knowledge.write")
}

func TestWebsiteKnowledgeGovernanceOperatorReadsArePermissionBoundAndBounded(t *testing.T) {
	service := &fakeWebsiteKnowledgeGovernance{}
	router := newTestRouter(RouterOptions{
		WebsiteKnowledgeGovernance: service,
		WebsiteFAQCatalog:          website.NewWebsiteFAQCatalog(nil),
	})

	curator := authedRequest(http.MethodGet, "/api/website-knowledge/proposals?status=draft&limit=200", "")
	curator.Header.Set("X-Permission-Scope", "knowledge.website_gap.propose")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, curator)
	if recorder.Code != http.StatusForbidden || service.proposalLimit != 0 {
		t.Fatalf("curator=%d limit=%d body=%s", recorder.Code, service.proposalLimit, recorder.Body.String())
	}

	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/api/website-knowledge/faqs", want: `"logical_intent_id":"orrery"`},
		{path: "/api/website-knowledge/metrics", want: `"conversation_count":12`},
		{path: "/api/website-knowledge/proposals?status=draft&limit=200", want: `"proposal_id":"wkp_1"`},
		{path: "/api/website-knowledge/proposals/wkp_1", want: `"revision_hash":"hash-1"`},
		{path: "/api/website-knowledge/proposals/wkp_1/receipts", want: `"kind":"approval"`},
	} {
		request := authedRequest(http.MethodGet, test.path, "")
		request.Header.Set("X-Permission-Scope", "knowledge.read")
		recorder = httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), test.want) {
			t.Fatalf("GET %s = %d %s", test.path, recorder.Code, recorder.Body.String())
		}
		if strings.Contains(strings.ToLower(recorder.Body.String()), `"tenant_id"`) {
			t.Fatalf("GET %s leaked tenant data: %s", test.path, recorder.Body.String())
		}
	}
	if service.proposalStatus != website.ProposalStatusDraft || service.proposalLimit != 100 {
		t.Fatalf("proposal query status=%q limit=%d", service.proposalStatus, service.proposalLimit)
	}
}

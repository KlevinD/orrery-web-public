package website

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/gateway/protocol"
)

type searchResponse struct {
	result WebsiteKnowledgeSearchResult
	err    error
}

type fakeWebsiteKnowledgeSearch struct {
	mu        sync.Mutex
	responses []searchResponse
	calls     []protocol.AdvisorTier
}

func (f *fakeWebsiteKnowledgeSearch) Search(_ context.Context, tier protocol.AdvisorTier, _ string) (WebsiteKnowledgeSearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, tier)
	if len(f.responses) == 0 {
		return WebsiteKnowledgeSearchResult{Resolution: WebsiteSearchNeedsClarification}, nil
	}
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response.result, response.err
}

func (f *fakeWebsiteKnowledgeSearch) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestAnonymousSearchReturnsSourceBackedDirectAnswerBeforeAgent(t *testing.T) {
	search := &fakeWebsiteKnowledgeSearch{responses: []searchResponse{{result: WebsiteKnowledgeSearchResult{
		Resolution: WebsiteSearchDirectAnswer,
		Answer:     "企业版价格需要由销售根据范围确认。",
		SourceURI:  "minviabrain://08-Wiki/Public/Orrery/pricing.md",
		Revision:   "approval-public-1",
	}}}}
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{
		AllowedOrigins:  []string{"https://www.minvia.cn"},
		KnowledgeSearch: search,
	})
	session := createSession(t, handler, "https://www.minvia.cn")

	recorder := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"企业版是怎么定价的？"}`)
	completed := completedPayload(t, recorder.Body.String())

	if gateway.inputCount() != 0 || search.callCount() != 1 {
		t.Fatalf("search/agent calls = %d/%d", search.callCount(), gateway.inputCount())
	}
	if completed["route"] != "knowledge_search" || completed["resolution"] != "resolved" || completed["human_verification_required"] != false {
		t.Fatalf("completion = %+v body=%s", completed, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "minviabrain://08-Wiki/Public/Orrery/pricing.md") {
		t.Fatalf("source missing: %s", recorder.Body.String())
	}
}

func TestAnonymousMissClarifiesOnceThenRequiresHumanVerification(t *testing.T) {
	search := &fakeWebsiteKnowledgeSearch{}
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{
		AllowedOrigins:  []string{"https://www.minvia.cn"},
		KnowledgeSearch: search,
	})
	session := createSession(t, handler, "https://www.minvia.cn")

	first := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"我们有一个特殊流程怎么接？"}`)
	firstCompleted := completedPayload(t, first.Body.String())
	if firstCompleted["route"] != "clarification" || firstCompleted["resolution"] != "needs_clarification" || firstCompleted["human_verification_required"] != false {
		t.Fatalf("first completion = %+v body=%s", firstCompleted, first.Body.String())
	}

	second := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"是跨系统审批和回写。"}`)
	secondCompleted := completedPayload(t, second.Body.String())
	if secondCompleted["route"] != "human_verification" || secondCompleted["resolution"] != "unresolved" || secondCompleted["human_verification_required"] != true {
		t.Fatalf("second completion = %+v body=%s", secondCompleted, second.Body.String())
	}
	if gateway.inputCount() != 0 {
		t.Fatalf("anonymous agent calls before verification = %d", gateway.inputCount())
	}
}

func TestAnonymousDetailedMissRequiresVerificationWithoutRedundantClarification(t *testing.T) {
	search := &fakeWebsiteKnowledgeSearch{}
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{
		AllowedOrigins:  []string{"https://www.minvia.cn"},
		KnowledgeSearch: search,
	})
	session := createSession(t, handler, "https://www.minvia.cn")
	question := "我们是一家300人制造企业，ERP使用用友，审批在飞书，客户数据在自建CRM。希望把销售线索进入、资格判断、报价审批、合同归档交给数字员工推进，但必须本地部署，业务数据不能离开内网，关键报价和合同必须人工确认。Orrery如何接入这些系统、组织知识库和权限审计？请给出建议的试点范围、实施步骤、验收指标和已知边界。"

	recorder := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"`+question+`"}`)
	completed := completedPayload(t, recorder.Body.String())
	if completed["route"] != "human_verification" || completed["resolution"] != "unresolved" || completed["human_verification_required"] != true {
		t.Fatalf("detailed miss should skip redundant clarification: %+v body=%s", completed, recorder.Body.String())
	}
	if gateway.inputCount() != 0 || search.callCount() != 1 {
		t.Fatalf("search/agent calls = %d/%d", search.callCount(), gateway.inputCount())
	}
}

func TestConversationalAnswerResetsAnonymousUnresolvedCount(t *testing.T) {
	search := &fakeWebsiteKnowledgeSearch{}
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{
		AllowedOrigins:  []string{"https://www.minvia.cn"},
		KnowledgeSearch: search,
	})
	session := createSession(t, handler, "https://www.minvia.cn")

	first := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"test","language":"zh-CN"}`)
	if completedPayload(t, first.Body.String())["route"] != "clarification" {
		t.Fatalf("first response = %s", first.Body.String())
	}

	greeting := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"你好啊","language":"zh-CN"}`)
	if completedPayload(t, greeting.Body.String())["route"] != "standard_answer" {
		t.Fatalf("greeting response = %s", greeting.Body.String())
	}

	third := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"另一个未知短句","language":"zh-CN"}`)
	completed := completedPayload(t, third.Body.String())
	if completed["route"] != "clarification" || completed["human_verification_required"] != false {
		t.Fatalf("third response = %+v body=%s", completed, third.Body.String())
	}
	if gateway.inputCount() != 0 {
		t.Fatalf("agent calls before verification = %d", gateway.inputCount())
	}
}

func TestVerifiedCustomerSearchReturnsCustomerAnswerBeforeAgent(t *testing.T) {
	now := fixedWebsiteAssertionTime()
	assertionKey := []byte("website-assertion-key-0123456789abcdef")
	verifier, err := NewWebsiteIdentityAssertionVerifier(assertionKey, func() time.Time { return now })
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	search := &fakeWebsiteKnowledgeSearch{responses: []searchResponse{{result: WebsiteKnowledgeSearchResult{
		Resolution: WebsiteSearchDirectAnswer,
		Answer:     "官网客户登录与 Orrery 租户身份必须保持独立。",
		SourceURI:  "minviabrain://08-Wiki/Customer/Orrery/website-login-boundary.md",
		Revision:   "customer-revision-1",
	}}}}
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, IdentityAssertions: verifier,
		OwnerHMACKey: []byte("website-owner-hmac-key-0123456789abcdef"), HistoryStore: NewMemoryWebsiteHistoryStore(), Now: func() time.Time { return now },
		KnowledgeSearch: search,
	})
	assertion := signWebsiteClaims(t, assertionKey, validWebsiteClaims(now, protocol.AdvisorTierCustomer, "customer-search-route"))
	session := createSessionWithAssertion(t, handler, "https://www.minvia.cn", assertion)

	recorder := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"请设计 SAP 跨系统审批和回写方案"}`)
	completed := completedPayload(t, recorder.Body.String())

	if search.callCount() != 1 || gateway.inputCount() != 0 {
		t.Fatalf("customer routing search=%d agent=%d", search.callCount(), gateway.inputCount())
	}
	if completed["route"] != "knowledge_search" || completed["resolution"] != "resolved" ||
		!strings.Contains(recorder.Body.String(), "minviabrain://08-Wiki/Customer/Orrery/website-login-boundary.md") {
		t.Fatalf("completion = %+v body=%s", completed, recorder.Body.String())
	}
}

func TestSearchTransportFailureDoesNotAdvanceUnresolvedState(t *testing.T) {
	search := &fakeWebsiteKnowledgeSearch{responses: []searchResponse{
		{err: errors.New("transport unavailable")},
		{result: WebsiteKnowledgeSearchResult{Resolution: WebsiteSearchNeedsClarification}},
	}}
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins:  []string{"https://www.minvia.cn"},
		KnowledgeSearch: search,
	})
	session := createSession(t, handler, "https://www.minvia.cn")

	failed := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"复杂集成一"}`)
	if !strings.Contains(failed.Body.String(), `"code":"advisor_search_unavailable"`) {
		t.Fatalf("transport failure = %s", failed.Body.String())
	}
	second := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"复杂集成二"}`)
	completed := completedPayload(t, second.Body.String())
	if completed["route"] != "clarification" || completed["human_verification_required"] != false {
		t.Fatalf("transport failure advanced state: %+v", completed)
	}
}

func TestDirectSearchWithoutCanonicalSourceIsUnresolved(t *testing.T) {
	search := &fakeWebsiteKnowledgeSearch{responses: []searchResponse{{result: WebsiteKnowledgeSearchResult{
		Resolution: WebsiteSearchDirectAnswer,
		Answer:     "Untrusted answer",
		SourceURI:  "https://untrusted.example/answer",
		Revision:   "approval-1",
	}}}}
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins:  []string{"https://www.minvia.cn"},
		KnowledgeSearch: search,
	})
	session := createSession(t, handler, "https://www.minvia.cn")

	recorder := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"未收录的问题"}`)
	completed := completedPayload(t, recorder.Body.String())
	if completed["route"] != "clarification" || strings.Contains(recorder.Body.String(), "Untrusted answer") {
		t.Fatalf("invalid source was accepted: %+v body=%s", completed, recorder.Body.String())
	}
}

func completedPayload(t *testing.T, body string) map[string]any {
	t.Helper()
	var completed map[string]any
	for _, frame := range strings.Split(body, "\n\n") {
		line := strings.TrimSpace(frame)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
			t.Fatalf("decode SSE: %v", err)
		}
		if payload["type"] == "turn_completed" {
			completed = payload
		}
	}
	if completed == nil {
		t.Fatalf("turn completion missing: %s", body)
	}
	return completed
}

func fixedWebsiteAssertionTime() time.Time {
	return time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
}

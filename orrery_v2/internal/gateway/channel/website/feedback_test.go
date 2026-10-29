package website

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/gateway/protocol"
)

type recordingWebsiteGapStore struct {
	mu     sync.Mutex
	events []WebsiteGapEvent
}

func (s *recordingWebsiteGapStore) RecordGap(_ context.Context, event WebsiteGapEvent) (WebsiteGapAggregate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return WebsiteGapAggregate{GapID: gapID(event.TopicFingerprint, event.Tier), TopicFingerprint: event.TopicFingerprint, Tier: event.Tier}, nil
}

func (s *recordingWebsiteGapStore) ListKnowledgeGaps(context.Context, int, time.Time) ([]WebsiteGapAggregate, error) {
	return nil, nil
}

func (s *recordingWebsiteGapStore) snapshot() []WebsiteGapEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]WebsiteGapEvent(nil), s.events...)
}

func TestAdvisorRecordsSanitizedMissAndAcceptsBoundedFeedback(t *testing.T) {
	gapStore := &recordingWebsiteGapStore{}
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, KnowledgeSearch: &fakeWebsiteKnowledgeSearch{}, GapStore: gapStore,
	})
	session := createSession(t, handler, "https://www.minvia.cn")
	rawQuestion := "复杂部署，联系 secret@example.com，token sk-private"
	answer := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"`+rawQuestion+`"}`)
	if answer.Code != http.StatusOK {
		t.Fatalf("answer=%d %s", answer.Code, answer.Body.String())
	}

	feedback := postAdvisorFeedback(handler, session, "unresolved")
	if feedback.Code != http.StatusAccepted || !strings.Contains(feedback.Body.String(), `"human_verification_required":true`) {
		t.Fatalf("feedback=%d %s", feedback.Code, feedback.Body.String())
	}
	events := gapStore.snapshot()
	if len(events) != 2 || events[1].Feedback != GapFeedbackUnresolved || events[1].Outcome != GapOutcomeUnresolved {
		t.Fatalf("events=%+v", events)
	}
	serialized, _ := json.Marshal(events)
	for _, forbidden := range []string{rawQuestion, "secret@example.com", "sk-private", session.SessionID} {
		if strings.Contains(string(serialized), forbidden) {
			t.Fatalf("gap event leaked %q: %s", forbidden, serialized)
		}
	}
}

func TestAdvisorFeedbackRequiresCurrentSessionCapabilityAndLastTurn(t *testing.T) {
	handler := NewHandler(&fakeGateway{}, Options{AllowedOrigins: []string{"https://www.minvia.cn"}, GapStore: &recordingWebsiteGapStore{}})
	session := createSession(t, handler, "https://www.minvia.cn")

	withoutTurn := postAdvisorFeedback(handler, session, "resolved")
	if withoutTurn.Code != http.StatusConflict {
		t.Fatalf("feedback without turn=%d %s", withoutTurn.Code, withoutTurn.Body.String())
	}
	wrong := session
	wrong.AccessToken = "wrong"
	unauthorized := postAdvisorFeedback(handler, wrong, "unresolved")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token=%d %s", unauthorized.Code, unauthorized.Body.String())
	}
	invalid := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/feedback", strings.NewReader(`{"outcome":"maybe"}`))
	req.Header.Set("Origin", "https://www.minvia.cn")
	req.Header.Set("Authorization", "Bearer "+session.AccessToken)
	handler.ServeHTTP(invalid, req)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid outcome=%d %s", invalid.Code, invalid.Body.String())
	}
	extra := httptest.NewRecorder()
	extraRequest := httptest.NewRequest(http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/feedback", strings.NewReader(`{"outcome":"resolved","tenant_id":"forged"}`))
	extraRequest.Header.Set("Origin", "https://www.minvia.cn")
	extraRequest.Header.Set("Authorization", "Bearer "+session.AccessToken)
	handler.ServeHTTP(extra, extraRequest)
	if extra.Code != http.StatusBadRequest {
		t.Fatalf("extra authority field=%d %s", extra.Code, extra.Body.String())
	}
}

func TestAdvisorStandardAnswerCanBeRatedAndKeepsMatchedIntent(t *testing.T) {
	gapStore := &recordingWebsiteGapStore{}
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, GapStore: gapStore,
	})
	session := createSession(t, handler, "https://www.minvia.cn")
	answer := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"我想预约演示","language":"zh-CN"}`)
	if answer.Code != http.StatusOK {
		t.Fatalf("answer=%d %s", answer.Code, answer.Body.String())
	}

	feedback := postAdvisorFeedback(handler, session, "unresolved")
	if feedback.Code != http.StatusAccepted {
		t.Fatalf("feedback=%d %s", feedback.Code, feedback.Body.String())
	}
	events := gapStore.snapshot()
	if len(events) != 2 || events[0].Route != "standard_answer" || events[0].MatchedIntent != "demo.zh-CN" || events[1].Feedback != GapFeedbackUnresolved {
		t.Fatalf("events=%+v", events)
	}
}

func TestAdvisorUnresolvedStandardAnswerRequiresPublicVerificationThenTakeover(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	humanKey := []byte("website-human-proof-key-0123456789abcdef")
	humanVerifier, err := NewWebsiteHumanVerificationAssertionVerifier(humanKey, func() time.Time { return now })
	if err != nil {
		t.Fatalf("new human verifier: %v", err)
	}
	gapStore := &recordingWebsiteGapStore{}
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{
		AllowedOrigins:              []string{"https://www.minvia.cn"},
		GapStore:                    gapStore,
		HumanVerificationAssertions: humanVerifier,
		Now:                         func() time.Time { return now },
	})
	session := createSession(t, handler, "https://www.minvia.cn")

	answer := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 如何控制权限？","language":"zh-CN"}`)
	if completedPayload(t, answer.Body.String())["route"] != "standard_answer" || gateway.inputCount() != 0 {
		t.Fatalf("initial fixed answer = %s agent=%d", answer.Body.String(), gateway.inputCount())
	}
	feedback := postAdvisorFeedback(handler, session, "unresolved")
	if feedback.Code != http.StatusAccepted ||
		!strings.Contains(feedback.Body.String(), `"human_verification_required":true`) ||
		!strings.Contains(feedback.Body.String(), `"takeover_required":true`) {
		t.Fatalf("public feedback = %d %s", feedback.Code, feedback.Body.String())
	}
	unverifiedRetry := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 如何控制权限？","language":"zh-CN"}`)
	if completedPayload(t, unverifiedRetry.Body.String())["route"] != "standard_answer" || gateway.inputCount() != 0 {
		t.Fatalf("unverified retry consumed takeover = %s agent=%d", unverifiedRetry.Body.String(), gateway.inputCount())
	}

	different := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"什么是 Task Contract？","language":"zh-CN"}`)
	if completedPayload(t, different.Body.String())["route"] != "standard_answer" || gateway.inputCount() != 0 {
		t.Fatalf("different fixed intent was bypassed = %s agent=%d", different.Body.String(), gateway.inputCount())
	}
	proof := signHumanVerificationClaims(t, humanKey, map[string]any{
		"iss": "minvia-website-auth", "aud": "orrery-website-human-verification",
		"sid": session.SessionID, "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix(), "nonce": "fixed-answer-takeover",
	})
	if verified := verifyHuman(t, handler, session, proof); verified.Code != http.StatusNoContent {
		t.Fatalf("verification = %d %s", verified.Code, verified.Body.String())
	}

	takeover := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 如何控制权限？","language":"zh-CN"}`)
	completed := completedPayload(t, takeover.Body.String())
	if completed["route"] != "public_agent" || gateway.inputCount() != 1 {
		t.Fatalf("public takeover = %+v calls=%d body=%s", completed, gateway.inputCount(), takeover.Body.String())
	}
	again := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 如何控制权限？","language":"zh-CN"}`)
	if completedPayload(t, again.Body.String())["route"] != "standard_answer" || gateway.inputCount() != 1 {
		t.Fatalf("public bypass was not one-shot = %s calls=%d", again.Body.String(), gateway.inputCount())
	}
}

func TestAdvisorUnresolvedStandardAnswerRequiresCustomerTakeoverWithoutVerification(t *testing.T) {
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	assertionKey := []byte("website-assertion-key-0123456789abcdef")
	identityVerifier, err := NewWebsiteIdentityAssertionVerifier(assertionKey, func() time.Time { return now })
	if err != nil {
		t.Fatalf("new identity verifier: %v", err)
	}
	gapStore := &recordingWebsiteGapStore{}
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{
		AllowedOrigins:     []string{"https://www.minvia.cn"},
		GapStore:           gapStore,
		IdentityAssertions: identityVerifier,
		OwnerHMACKey:       []byte("website-owner-hmac-key-0123456789abcdef"),
		HistoryStore:       NewMemoryWebsiteHistoryStore(),
		Now:                func() time.Time { return now },
	})
	assertion := signWebsiteClaims(t, assertionKey, validWebsiteClaims(now, protocol.AdvisorTierCustomer, "fixed-answer-customer"))
	session := createSessionWithAssertion(t, handler, "https://www.minvia.cn", assertion)

	answer := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 如何控制权限？","language":"zh-CN"}`)
	if completedPayload(t, answer.Body.String())["route"] != "standard_answer" || gateway.inputCount() != 0 {
		t.Fatalf("initial fixed answer = %s agent=%d", answer.Body.String(), gateway.inputCount())
	}
	feedback := postAdvisorFeedback(handler, session, "unresolved")
	if feedback.Code != http.StatusAccepted ||
		strings.Contains(feedback.Body.String(), `"human_verification_required":true`) ||
		!strings.Contains(feedback.Body.String(), `"takeover_required":true`) {
		t.Fatalf("customer feedback = %d %s", feedback.Code, feedback.Body.String())
	}

	different := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"什么是 Task Contract？","language":"zh-CN"}`)
	if completedPayload(t, different.Body.String())["route"] != "standard_answer" || gateway.inputCount() != 0 {
		t.Fatalf("different fixed intent was bypassed = %s agent=%d", different.Body.String(), gateway.inputCount())
	}
	takeover := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 如何控制权限？","language":"zh-CN"}`)
	completed := completedPayload(t, takeover.Body.String())
	if completed["route"] != "customer_agent" || gateway.inputCount() != 1 || gateway.lastInput().AdvisorTier != protocol.AdvisorTierCustomer {
		t.Fatalf("customer takeover = %+v calls=%d input=%+v", completed, gateway.inputCount(), gateway.lastInput())
	}
	again := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 如何控制权限？","language":"zh-CN"}`)
	if completedPayload(t, again.Body.String())["route"] != "standard_answer" || gateway.inputCount() != 1 {
		t.Fatalf("customer bypass was not one-shot = %s calls=%d", again.Body.String(), gateway.inputCount())
	}
}

func postAdvisorFeedback(handler http.Handler, session SessionResponse, outcome string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/feedback", strings.NewReader(`{"outcome":"`+outcome+`"}`))
	request.Header.Set("Origin", "https://www.minvia.cn")
	request.Header.Set("Authorization", "Bearer "+session.AccessToken)
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	return recorder
}

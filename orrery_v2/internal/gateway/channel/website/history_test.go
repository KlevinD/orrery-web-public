package website

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/gateway/protocol"
)

type fakeWebsiteBearerAuthenticator struct {
	accounts map[string]WebsiteAccount
}

func (f fakeWebsiteBearerAuthenticator) AuthenticateWebsiteBearer(_ context.Context, token string) (WebsiteAccount, error) {
	account, ok := f.accounts[token]
	if !ok {
		return WebsiteAccount{}, errors.New("unauthorized")
	}
	return account, nil
}

func TestWebsiteAccountHistoryIsOwnedAndTenantNeutral(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	store := NewMemoryWebsiteHistoryStore()
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{
		AllowedOrigins:   []string{"https://www.minvia.cn"},
		Now:              func() time.Time { return now },
		BearerAuth:       historyTestAuthenticator(),
		HistoryStore:     store,
		OwnerHMACKey:     []byte("history-test-owner-key-at-least-32-bytes"),
		HistoryRetention: 30 * 24 * time.Hour,
	})
	session := createOwnedSession(t, handler, "owner-a-token")
	message := sendOwnedMessage(handler, session, "owner-a-token", `{"message":"Orrery 如何连接 SAP 并保留审计证据？","client_message_id":"owned-message-1","language":"zh-CN","page_path":"/zh-CN/orrery"}`)
	if message.Code != http.StatusOK {
		t.Fatalf("message = %d %s", message.Code, message.Body.String())
	}

	account := advisorRequest(handler, http.MethodGet, "/api/advisor/account", "owner-a-token", "", "")
	if account.Code != http.StatusOK || !strings.Contains(account.Body.String(), "Owner A") || !strings.Contains(account.Body.String(), "owner-a@example.com") {
		t.Fatalf("account = %d %s", account.Code, account.Body.String())
	}
	list := advisorRequest(handler, http.MethodGet, "/api/advisor/conversations", "owner-a-token", "", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	var listed struct {
		Conversations []WebsiteConversationSummary `json:"conversations"`
	}
	if err := json.NewDecoder(list.Body).Decode(&listed); err != nil || len(listed.Conversations) != 1 {
		t.Fatalf("listed = %+v err=%v", listed, err)
	}
	conversationID := listed.Conversations[0].ConversationID
	if strings.Contains(list.Body.String(), "tenant") || strings.Contains(conversationID, "owner-a") {
		t.Fatalf("history leaked tenant/owner identity: %s", list.Body.String())
	}

	other := advisorRequest(handler, http.MethodGet, "/api/advisor/conversations/"+conversationID, "owner-b-token", "", "")
	if other.Code != http.StatusNotFound {
		t.Fatalf("cross-owner get = %d %s", other.Code, other.Body.String())
	}
	owned := advisorRequest(handler, http.MethodGet, "/api/advisor/conversations/"+conversationID, "owner-a-token", "", "")
	if owned.Code != http.StatusOK || strings.Count(owned.Body.String(), `"role"`) != 2 {
		t.Fatalf("owned conversation = %d %s", owned.Code, owned.Body.String())
	}
	input := gateway.lastInput()
	if input.Identity == nil || input.Identity.DisplayName != "Website customer" || !strings.HasPrefix(input.Identity.ExternalUserID, "v1_") || input.Identity.ExternalUserID == session.SessionID {
		t.Fatalf("gateway identity = %+v", input.Identity)
	}
	if input.AdvisorTier != protocol.AdvisorTierCustomer || input.Identity.AccountID != "" || input.Identity.ExternalTenantID != "" {
		t.Fatalf("gateway customer authority = tier=%q identity=%+v", input.AdvisorTier, input.Identity)
	}
	for key, value := range input.Telemetry {
		if strings.Contains(strings.ToLower(key), "tenant") || strings.Contains(strings.ToLower(value), "tenant") {
			t.Fatalf("tenant leaked into telemetry: %+v", input.Telemetry)
		}
	}

	deletedByOther := advisorRequest(handler, http.MethodDelete, "/api/advisor/conversations/"+conversationID, "owner-b-token", "", "")
	if deletedByOther.Code != http.StatusNotFound {
		t.Fatalf("cross-owner delete = %d", deletedByOther.Code)
	}
	deleted := advisorRequest(handler, http.MethodDelete, "/api/advisor/conversations/"+conversationID, "owner-a-token", "", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("owned delete = %d %s", deleted.Code, deleted.Body.String())
	}
}

func TestWebsiteAnonymousHistoryRequiresExplicitAdoption(t *testing.T) {
	store := NewMemoryWebsiteHistoryStore()
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, BearerAuth: historyTestAuthenticator(),
		HistoryStore: store, OwnerHMACKey: []byte("history-test-owner-key-at-least-32-bytes"),
	})
	session := createSession(t, handler, "https://www.minvia.cn")
	if rec := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"你好","client_message_id":"anonymous-turn-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("anonymous message = %d %s", rec.Code, rec.Body.String())
	}
	before := advisorRequest(handler, http.MethodGet, "/api/advisor/conversations", "owner-a-token", "", "")
	if before.Code != http.StatusOK || !strings.Contains(before.Body.String(), `"conversations":[]`) {
		t.Fatalf("history before adoption = %d %s", before.Code, before.Body.String())
	}
	adopt := advisorRequest(handler, http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/adopt", "owner-a-token", session.AccessToken, "{}")
	if adopt.Code != http.StatusOK {
		t.Fatalf("adopt = %d %s", adopt.Code, adopt.Body.String())
	}
	after := advisorRequest(handler, http.MethodGet, "/api/advisor/conversations", "owner-a-token", "", "")
	if after.Code != http.StatusOK || strings.Contains(after.Body.String(), `"conversations":[]`) {
		t.Fatalf("history after adoption = %d %s", after.Code, after.Body.String())
	}
}

func TestWebsiteHistoryExpiresAfterThirtyDays(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	store := NewMemoryWebsiteHistoryStore()
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, Now: func() time.Time { return now },
		BearerAuth: historyTestAuthenticator(), HistoryStore: store,
		OwnerHMACKey: []byte("history-test-owner-key-at-least-32-bytes"), HistoryRetention: 30 * 24 * time.Hour,
	})
	session := createOwnedSession(t, handler, "owner-a-token")
	if rec := sendOwnedMessage(handler, session, "owner-a-token", `{"message":"你好","client_message_id":"expiry-turn-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("message = %d %s", rec.Code, rec.Body.String())
	}
	now = now.Add(30*24*time.Hour + time.Second)
	list := advisorRequest(handler, http.MethodGet, "/api/advisor/conversations", "owner-a-token", "", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"conversations":[]`) {
		t.Fatalf("expired list = %d %s", list.Code, list.Body.String())
	}
	if removed, err := store.CleanupExpired(context.Background(), now); err != nil || removed != 1 {
		t.Fatalf("cleanup removed=%d err=%v", removed, err)
	}
}

func TestWebsiteHistoryCanResumeAcrossDevices(t *testing.T) {
	store := NewMemoryWebsiteHistoryStore()
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, BearerAuth: historyTestAuthenticator(),
		HistoryStore: store, OwnerHMACKey: []byte("history-test-owner-key-at-least-32-bytes"),
	})
	firstSession := createOwnedSession(t, handler, "owner-a-token")
	_ = sendOwnedMessage(handler, firstSession, "owner-a-token", `{"message":"你好","client_message_id":"resume-turn-1"}`)
	list := advisorRequest(handler, http.MethodGet, "/api/advisor/conversations", "owner-a-token", "", "")
	var listed struct {
		Conversations []WebsiteConversationSummary `json:"conversations"`
	}
	_ = json.NewDecoder(list.Body).Decode(&listed)
	if len(listed.Conversations) != 1 {
		t.Fatalf("conversations = %+v", listed.Conversations)
	}

	resume := advisorRequest(handler, http.MethodPost, "/api/advisor/conversations/"+listed.Conversations[0].ConversationID+"/resume", "owner-a-token", "", "{}")
	if resume.Code != http.StatusCreated {
		t.Fatalf("resume = %d %s", resume.Code, resume.Body.String())
	}
	var secondSession SessionResponse
	if err := json.NewDecoder(resume.Body).Decode(&secondSession); err != nil {
		t.Fatalf("decode resume: %v", err)
	}
	_ = sendOwnedMessage(handler, secondSession, "owner-a-token", `{"message":"继续介绍部署边界","client_message_id":"resume-turn-2"}`)
	conversation := advisorRequest(handler, http.MethodGet, "/api/advisor/conversations/"+listed.Conversations[0].ConversationID, "owner-a-token", "", "")
	if conversation.Code != http.StatusOK || strings.Count(conversation.Body.String(), `"role"`) != 4 {
		t.Fatalf("resumed conversation = %d %s", conversation.Code, conversation.Body.String())
	}
}

func TestWebsiteHistoryRequiresDedicatedScope(t *testing.T) {
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, BearerAuth: historyTestAuthenticator(),
		HistoryStore: NewMemoryWebsiteHistoryStore(), OwnerHMACKey: []byte("history-test-owner-key-at-least-32-bytes"),
	})
	rec := advisorRequest(handler, http.MethodGet, "/api/advisor/account", "wrong-scope-token", "", "")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "advisor_scope_forbidden") {
		t.Fatalf("scope response = %d %s", rec.Code, rec.Body.String())
	}
}

func historyTestAuthenticator() fakeWebsiteBearerAuthenticator {
	return fakeWebsiteBearerAuthenticator{accounts: map[string]WebsiteAccount{
		"owner-a-token":     {Issuer: "https://orrery.example/hydra", Subject: "owner-a", DisplayName: "Owner A", Email: "owner-a@example.com", Scopes: []string{"orrery:website-advisor"}},
		"owner-b-token":     {Issuer: "https://orrery.example/hydra", Subject: "owner-b", DisplayName: "Owner B", Email: "owner-b@example.com", Scopes: []string{"orrery:website-advisor"}},
		"wrong-scope-token": {Issuer: "https://orrery.example/hydra", Subject: "owner-c", DisplayName: "Owner C", Scopes: []string{"openid"}},
	}}
}

func createOwnedSession(t *testing.T, handler http.Handler, oauthToken string) SessionResponse {
	t.Helper()
	rec := advisorRequest(handler, http.MethodPost, "/api/advisor/sessions", oauthToken, "", "{}")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create owned session = %d %s", rec.Code, rec.Body.String())
	}
	var session SessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	return session
}

func sendOwnedMessage(handler http.Handler, session SessionResponse, oauthToken, body string) *httptest.ResponseRecorder {
	return advisorRequest(handler, http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/messages", oauthToken, session.AccessToken, body)
}

func advisorRequest(handler http.Handler, method, path, oauthToken, advisorToken, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Origin", "https://www.minvia.cn")
	if oauthToken != "" {
		req.Header.Set("Authorization", "Bearer "+oauthToken)
	}
	if advisorToken != "" {
		req.Header.Set("X-Advisor-Session-Token", advisorToken)
	}
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	return rec
}

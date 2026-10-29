package website

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerRequiresClientMessageID(t *testing.T) {
	handler := NewHandler(&fakeGateway{}, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/messages", strings.NewReader(`{"message":"你好"}`))
	req.Header.Set("Origin", "https://www.minvia.cn")
	req.Header.Set("Authorization", "Bearer "+session.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "advisor_client_message_id_required") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerReplaysCompletedDeterministicTurn(t *testing.T) {
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")
	body := `{"message":"你好","client_message_id":"message-replay-1"}`

	first := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", body)
	second := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", body)

	if first.Code != http.StatusOK || second.Code != http.StatusOK || first.Body.String() != second.Body.String() {
		t.Fatalf("first = %d %q second = %d %q", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	if gateway.inputCount() != 0 {
		t.Fatalf("gateway calls = %d", gateway.inputCount())
	}
}

func TestHandlerRejectsClientMessageIDReuseWithDifferentContent(t *testing.T) {
	handler := NewHandler(&fakeGateway{}, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")
	first := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"你好","client_message_id":"message-conflict-1"}`)
	second := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"再见","client_message_id":"message-conflict-1"}`)

	if first.Code != http.StatusOK || second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "advisor_idempotency_conflict") {
		t.Fatalf("first = %d second = %d %s", first.Code, second.Code, second.Body.String())
	}
}

func TestHandlerConcurrentDuplicateWaitsAndUsesOneGatewaySubmission(t *testing.T) {
	gateway := &fakeGateway{block: make(chan struct{})}
	handler := NewHandler(gateway, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")
	server := httptest.NewServer(handler)
	defer server.Close()
	body := `{"message":"Orrery 如何连接 SAP 并保留审计证据？","client_message_id":"message-concurrent-duplicate"}`

	statuses := make(chan int, 2)
	send := func() {
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/advisor/sessions/"+session.SessionID+"/messages", strings.NewReader(body))
		req.Header.Set("Origin", "https://www.minvia.cn")
		req.Header.Set("Authorization", "Bearer "+session.AccessToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			statuses <- 0
			return
		}
		_ = resp.Body.Close()
		statuses <- resp.StatusCode
	}

	go send()
	waitForGatewayInput(t, gateway)
	go send()
	time.Sleep(20 * time.Millisecond)
	if gateway.inputCount() != 1 {
		t.Fatalf("gateway calls before release = %d", gateway.inputCount())
	}
	close(gateway.block)
	for range 2 {
		if status := <-statuses; status != http.StatusOK {
			t.Fatalf("status = %d", status)
		}
	}
	if gateway.inputCount() != 1 {
		t.Fatalf("gateway calls = %d", gateway.inputCount())
	}
}

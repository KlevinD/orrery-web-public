package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/gateway/channel"

	dingchatbot "github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	dingclient "github.com/open-dingtalk/dingtalk-stream-sdk-go/client"
)

func markSDKClientConnected(t *testing.T, client *SDKClient) {
	t.Helper()
	client.mu.Lock()
	client.stream = dingclient.NewStreamClient()
	client.healthy = true
	client.mu.Unlock()
}

func TestDingTalkBindingIdentityRequiresProactiveDeliveryStaffID(t *testing.T) {
	data := &dingchatbot.BotCallbackDataModel{SenderId: "encrypted-sender", SenderStaffId: " staff-1 "}
	if got := dingTalkProactiveRecipientID(data); got != "staff-1" {
		t.Fatalf("staff recipient = %q", got)
	}
	data.SenderStaffId = ""
	if got := dingTalkProactiveRecipientID(data); got != "" {
		t.Fatalf("encrypted sender unexpectedly became proactive recipient: %q", got)
	}
}

func TestSDKClientSendsProactiveDirectMessageAndCachesToken(t *testing.T) {
	var tokenCalls atomic.Int32
	var sendCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case dingTalkTokenPath:
			tokenCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "access-token", "expireIn": 7200})
		case dingTalkDirectSendPath:
			sendCalls.Add(1)
			if got := r.Header.Get("x-acs-dingtalk-access-token"); got != "access-token" {
				t.Fatalf("access token header = %q", got)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["robotCode"] != "robot-code" || body["msgKey"] != "sampleText" {
				t.Fatalf("body = %+v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"processQueryKey": "receipt-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewSDKClientWithHTTP(nil, server.Client(), true)
	client.options = Options{ClientID: "client", ClientSecret: "secret", RobotCode: "robot-code", APIBaseURL: server.URL}
	markSDKClientConnected(t, client)
	for i := 0; i < 2; i++ {
		receipt, err := client.SendDirect(context.Background(), "staff-1", "", "hello", "text")
		if err != nil || receipt != "receipt-1" {
			t.Fatalf("SendDirect receipt=%q err=%v", receipt, err)
		}
	}
	if tokenCalls.Load() != 1 || sendCalls.Load() != 2 {
		t.Fatalf("token calls=%d send calls=%d", tokenCalls.Load(), sendCalls.Load())
	}
}

func TestSDKClientChunksLongProactiveMessagesAndReturnsEveryReceipt(t *testing.T) {
	var sendCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case dingTalkTokenPath:
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "access-token", "expireIn": 7200})
		case dingTalkDirectSendPath:
			call := sendCalls.Add(1)
			var body struct {
				MsgParam string `json:"msgParam"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			var params struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(body.MsgParam), &params); err != nil {
				t.Fatalf("decode msgParam: %v", err)
			}
			if count := len([]rune(params.Content)); count == 0 || count > dingTalkTextLimit {
				t.Fatalf("chunk length = %d", count)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"processQueryKey": fmt.Sprintf("receipt-%d", call)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewSDKClientWithHTTP(nil, server.Client(), true)
	client.options = Options{ClientID: "client", ClientSecret: "secret", RobotCode: "robot-code", APIBaseURL: server.URL}
	markSDKClientConnected(t, client)
	receipt, err := client.SendDirect(context.Background(), "staff-1", "", strings.Repeat("界", dingTalkTextLimit+1), "text")
	if err != nil || receipt != "receipt-1,receipt-2" || sendCalls.Load() != 2 {
		t.Fatalf("SendDirect receipt=%q calls=%d err=%v", receipt, sendCalls.Load(), err)
	}
}

func TestSDKClientChunksLongWebhookReplies(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Markdown struct {
				Text string `json:"text"`
			} `json:"markdown"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if count := len([]rune(body.Markdown.Text)); count == 0 || count > dingTalkMarkdownLimit {
			t.Fatalf("chunk length = %d", count)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
	}))
	defer server.Close()
	client := NewSDKClientWithHTTP(nil, server.Client(), true)
	markSDKClientConnected(t, client)
	if err := client.SendReply(context.Background(), OutboundMessage{
		SessionWebhook: server.URL + "/v1.0/robot/sessionWebhook/send?token=test",
		Text:           strings.Repeat("界", dingTalkMarkdownLimit+1),
	}); err != nil {
		t.Fatalf("SendReply: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("webhook calls = %d", calls.Load())
	}
}

func TestSDKClientRejectsAccessTokenWithoutBoundedExpiry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != dingTalkTokenPath {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "access-token"})
	}))
	defer server.Close()

	client := NewSDKClientWithHTTP(nil, server.Client(), true)
	_, _, err := client.fetchToken(context.Background(), Options{ClientID: "client", ClientSecret: "secret", APIBaseURL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "invalid expiry") {
		t.Fatalf("fetchToken error = %v", err)
	}
}

func TestSDKClientRejectsUntrustedSessionWebhook(t *testing.T) {
	client := NewSDKClient(nil)
	markSDKClientConnected(t, client)
	err := client.SendReply(context.Background(), OutboundMessage{
		SessionWebhook: "http://169.254.169.254/latest/meta-data",
		Text:           "hello",
	})
	if err == nil {
		t.Fatal("SendReply should reject a non-DingTalk webhook")
	}
}

func TestSDKClientOnlyAcceptsOfficialSecureStreamEndpoints(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		wantError bool
	}{
		{name: "official", endpoint: "wss://wss-open-connection.dingtalk.com/connect"},
		{name: "official tls port", endpoint: "wss://wss-open-connection.dingtalk.com:443/connect"},
		{name: "insecure", endpoint: "ws://wss-open-connection.dingtalk.com/connect", wantError: true},
		{name: "loopback", endpoint: "wss://127.0.0.1/connect", wantError: true},
		{name: "lookalike", endpoint: "wss://dingtalk.com.attacker.example/connect", wantError: true},
		{name: "base domain", endpoint: "wss://dingtalk.com/connect", wantError: true},
		{name: "query", endpoint: "wss://wss-open-connection.dingtalk.com/connect?ticket=attacker", wantError: true},
		{name: "userinfo", endpoint: "wss://user@wss-open-connection.dingtalk.com/connect", wantError: true},
		{name: "non tls port", endpoint: "wss://wss-open-connection.dingtalk.com:8443/connect", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDingTalkStreamEndpoint(test.endpoint, false)
			if test.wantError && err == nil {
				t.Fatalf("endpoint %q was accepted", test.endpoint)
			}
			if !test.wantError && err != nil {
				t.Fatalf("endpoint %q was rejected: %v", test.endpoint, err)
			}
		})
	}
}

func TestSDKClientSendsReplyOnlyToValidatedWebhook(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1.0/robot/sessionWebhook/send" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
	}))
	defer server.Close()
	client := NewSDKClientWithHTTP(nil, server.Client(), true)
	markSDKClientConnected(t, client)
	if err := client.SendReply(context.Background(), OutboundMessage{SessionWebhook: server.URL + "/v1.0/robot/sessionWebhook/send?token=test", Text: "hello"}); err != nil {
		t.Fatalf("SendReply: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestSDKClientRejectsWebhookErrorInSuccessfulHTTPResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 310000, "errmsg": "invalid webhook token"})
	}))
	defer server.Close()

	client := NewSDKClientWithHTTP(nil, server.Client(), true)
	markSDKClientConnected(t, client)
	err := client.SendReply(context.Background(), OutboundMessage{
		SessionWebhook: server.URL + "/v1.0/robot/sessionWebhook/send?token=test",
		Text:           "hello",
	})
	if err == nil || !strings.Contains(err.Error(), "310000") {
		t.Fatalf("SendReply accepted provider error: %v", err)
	}
}

func TestSDKClientRejectsWebhookResponseWithoutExplicitSuccess(t *testing.T) {
	for _, body := range []string{"", `{}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			client := NewSDKClientWithHTTP(nil, server.Client(), true)
			markSDKClientConnected(t, client)
			err := client.SendReply(context.Background(), OutboundMessage{
				SessionWebhook: server.URL + "/v1.0/robot/sessionWebhook/send?token=test", Text: "hello",
			})
			if err == nil {
				t.Fatal("SendReply accepted a response without an explicit success code")
			}
		})
	}
}

func TestSDKClientRejectsOversizedWebhookResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0}` + strings.Repeat(" ", dingTalkMaxResponse)))
	}))
	defer server.Close()
	client := NewSDKClientWithHTTP(nil, server.Client(), true)
	markSDKClientConnected(t, client)
	err := client.SendReply(context.Background(), OutboundMessage{
		SessionWebhook: server.URL + "/v1.0/robot/sessionWebhook/send?token=test", Text: "hello",
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestSDKClientClassifiesInvalidRecipientAsPermanent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case dingTalkTokenPath:
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "access-token", "expireIn": 7200})
		case dingTalkDirectSendPath:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "invalidParameter.userIds", "message": "invalid user id", "requestid": "req-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewSDKClientWithHTTP(nil, server.Client(), true)
	client.options = Options{ClientID: "client", ClientSecret: "secret", RobotCode: "robot-code", APIBaseURL: server.URL}
	markSDKClientConnected(t, client)
	_, err := client.SendDirect(context.Background(), "missing-user", "", "hello", "text")
	if !channel.IsPermanentDeliveryError(err) {
		t.Fatalf("error = %v, want permanent delivery error", err)
	}
}

func TestSDKClientRefreshesTokenAfterUnauthorizedSend(t *testing.T) {
	var tokenCalls atomic.Int32
	var sendCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == dingTalkTokenPath {
			call := tokenCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "token-" + string(rune('0'+call)), "expireIn": 7200})
			return
		}
		if r.URL.Path == dingTalkDirectSendPath {
			if sendCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"processQueryKey": "receipt-2"})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := NewSDKClientWithHTTP(nil, &http.Client{Timeout: time.Second}, true)
	client.options = Options{ClientID: "client", ClientSecret: "secret", RobotCode: "client", APIBaseURL: server.URL}
	markSDKClientConnected(t, client)
	receipt, err := client.SendDirect(context.Background(), "staff-1", "", "hello", "text")
	if err != nil || receipt != "receipt-2" {
		t.Fatalf("SendDirect receipt=%q err=%v", receipt, err)
	}
	if tokenCalls.Load() != 2 || sendCalls.Load() != 2 {
		t.Fatalf("token calls=%d send calls=%d", tokenCalls.Load(), sendCalls.Load())
	}
}

func TestSDKClientRefreshesTokenAfterBusinessAuthenticationError(t *testing.T) {
	var tokenCalls atomic.Int32
	var sendCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case dingTalkTokenPath:
			call := tokenCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": fmt.Sprintf("token-%d", call), "expireIn": 7200})
		case dingTalkDirectSendPath:
			call := sendCalls.Add(1)
			if call == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": "InvalidAuthentication", "message": "access token expired"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"processQueryKey": "process-after-refresh"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewSDKClientWithHTTP(nil, server.Client(), true)
	client.options = Options{ClientID: "client", ClientSecret: "secret", RobotCode: "client", APIBaseURL: server.URL}
	markSDKClientConnected(t, client)
	receipt, err := client.SendDirect(context.Background(), "staff-a", "", "hello", "text")
	if err != nil || receipt != "process-after-refresh" {
		t.Fatalf("SendDirect receipt=%q error=%v", receipt, err)
	}
	if tokenCalls.Load() != 2 || sendCalls.Load() != 2 {
		t.Fatalf("token calls=%d send calls=%d", tokenCalls.Load(), sendCalls.Load())
	}
}

func TestSDKClientDoesNotTreatRequestIDAsDeliveryReceipt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case dingTalkTokenPath:
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "access-token", "expireIn": 7200})
		case dingTalkDirectSendPath:
			_ = json.NewEncoder(w).Encode(map[string]any{"requestId": "request-only"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewSDKClientWithHTTP(nil, server.Client(), true)
	client.options = Options{ClientID: "client", ClientSecret: "secret", RobotCode: "robot-code", APIBaseURL: server.URL}
	markSDKClientConnected(t, client)
	if receipt, err := client.SendDirect(context.Background(), "staff-1", "", "hello", "text"); err == nil || receipt != "" {
		t.Fatalf("request id was accepted as delivery receipt: receipt=%q err=%v", receipt, err)
	}
}

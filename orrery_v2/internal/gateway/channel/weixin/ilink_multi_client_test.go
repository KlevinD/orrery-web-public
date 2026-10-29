package weixin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIlinkBotMultiClientRoutesProactiveSendByAccount(t *testing.T) {
	recipients := make(chan string, 2)
	newAccount := func(token string) (*IlinkBotClient, *httptest.Server) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path != "/sendmessage" {
				http.NotFound(w, req)
				return
			}
			if req.Header.Get("Authorization") != "Bearer "+token {
				t.Errorf("authorization = %q", req.Header.Get("Authorization"))
			}
			var body struct {
				Message struct {
					ToUserID string `json:"to_user_id"`
				} `json:"msg"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode request: %v", err)
			}
			recipients <- body.Message.ToUserID
			writeIlinkJSON(w, http.StatusOK, map[string]any{"ret": 0})
		}))
		client := &IlinkBotClient{
			httpClient: server.Client(), allowInsecureLoopback: true,
			options: Options{BotToken: token, IlinkBaseURL: server.URL, SendMessagePath: "sendmessage", RequestTimeoutSeconds: 2}.WithDefaults(),
		}
		return client, server
	}
	accountA, serverA := newAccount("token-a")
	defer serverA.Close()
	accountB, serverB := newAccount("token-b")
	defer serverB.Close()
	multi := &IlinkBotMultiClient{clients: map[string]*IlinkBotClient{"bot-a": accountA, "bot-b": accountB}}

	if err := multi.SendReply(context.Background(), OutboundMessage{AccountID: "bot-b", ChatID: "user-b", Text: "hello"}); err != nil {
		t.Fatalf("SendReply: %v", err)
	}
	if got := <-recipients; got != "user-b" {
		t.Fatalf("recipient = %q", got)
	}
	select {
	case unexpected := <-recipients:
		t.Fatalf("message was sent through more than one account: %q", unexpected)
	default:
	}
}

func TestIlinkBotMultiClientRejectsMissingAccountRoute(t *testing.T) {
	multi := NewIlinkBotMultiClient()
	if err := multi.SendReply(context.Background(), OutboundMessage{ChatID: "user-a", Text: "hello"}); err == nil || !strings.Contains(err.Error(), "account id is required") {
		t.Fatalf("missing account error = %v", err)
	}
	if err := multi.SendReply(context.Background(), OutboundMessage{AccountID: "missing", ChatID: "user-a", Text: "hello"}); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("unknown account error = %v", err)
	}
}

func TestIlinkBotMultiClientValidatesAccountsConcurrently(t *testing.T) {
	var arrived atomic.Int32
	allArrived := make(chan struct{})
	var closeOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/getupdates" {
			http.NotFound(w, req)
			return
		}
		if arrived.Add(1) == 2 {
			closeOnce.Do(func() { close(allArrived) })
		}
		select {
		case <-allArrived:
		case <-time.After(2 * time.Second):
			t.Error("account validation was serialized")
		}
		writeIlinkJSON(w, http.StatusOK, map[string]any{"ret": 0, "msgs": []any{}, "get_updates_buf": "cursor"})
	}))
	defer server.Close()

	client := NewIlinkBotMultiClient()
	client.newClient = func() *IlinkBotClient {
		account := NewIlinkBotClient()
		account.allowInsecureLoopback = true
		return account
	}
	opts := Options{
		IlinkBaseURL: server.URL, GetUpdatesPath: "getupdates", SendMessagePath: "sendmessage",
		StatePath: filepath.Join(t.TempDir(), "state.json"), PollIntervalSeconds: 300, RequestTimeoutSeconds: 5,
		InboundEvents: newWeixinInboundStore(t),
		Accounts: []AccountOptions{
			{TenantID: "tenant-a", UserID: "user-a", BotID: "bot-a", BotToken: "token-a", IlinkBaseURL: server.URL},
			{TenantID: "tenant-a", UserID: "user-b", BotID: "bot-b", BotToken: "token-b", IlinkBaseURL: server.URL},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Start(ctx, opts, nil, func(context.Context, InboundMessage) ([]OutboundMessage, error) { return nil, nil }); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if arrived.Load() != 2 || !client.GatewayConnectionHealthy() {
		t.Fatalf("arrived=%d healthy=%v", arrived.Load(), client.GatewayConnectionHealthy())
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := client.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

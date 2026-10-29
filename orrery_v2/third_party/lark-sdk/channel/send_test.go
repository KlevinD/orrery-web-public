package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/channel/types"
)

type MockHttpClient struct {
	DoFunc func(req *http.Request) (*http.Response, error)
}

type capturedMessageRequest struct {
	MsgType string `json:"msg_type"`
	Content string `json:"content"`
	UUID    string `json:"uuid"`
}

type capturedInteractiveCard struct {
	Schema string `json:"schema"`
	Config struct {
		WidthMode string `json:"width_mode"`
	} `json:"config"`
	Header *struct {
		Template string `json:"template"`
		Title    struct {
			Tag     string `json:"tag"`
			Content string `json:"content"`
		} `json:"title"`
	} `json:"header"`
	Body struct {
		Elements []struct {
			Tag     string `json:"tag"`
			Content string `json:"content"`
		} `json:"elements"`
	} `json:"body"`
}

func (m *MockHttpClient) Do(req *http.Request) (*http.Response, error) {
	if m.DoFunc != nil {
		return m.DoFunc(req)
	}
	return nil, nil
}

func TestSend_Text(t *testing.T) {
	var sentUUID string
	mockHttp := &MockHttpClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/im/v1/messages") {
				var body struct {
					UUID string `json:"uuid"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatalf("decode message request: %v", err)
				}
				sentUUID = body.UUID
			}
			// Mock send message response
			respBody := `{"code":0,"msg":"success","data":{"message_id":"om_123456"}}`
			header := make(http.Header)
			header.Set("Content-Type", "application/json")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(respBody)),
				Header:     header,
			}, nil
		},
	}

	client := lark.NewClient("appId", "appSecret", lark.WithHttpClient(mockHttp))
	ch := NewChannel(client, nil)

	res, err := ch.Send(context.Background(), &types.SendInput{
		UserID:         "u_123",
		Text:           "hello",
		IdempotencyKey: "delivery-123",
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res.MessageID != "om_123456" {
		t.Errorf("expected message_id om_123456, got %s", res.MessageID)
	}
	if sentUUID != chunkIdempotencyKey("delivery-123", 0) {
		t.Errorf("message uuid = %q", sentUUID)
	}
}

func TestSend_InteractiveMarkdownCard(t *testing.T) {
	var request capturedMessageRequest
	mockHTTP := &MockHttpClient{DoFunc: func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "tenant_access_token") {
			return successfulTenantTokenResponse(), nil
		}
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			t.Fatalf("decode message request: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"success","data":{"message_id":"om_card_1"}}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	}}
	client := lark.NewClient("appId", "appSecret", lark.WithHttpClient(mockHTTP))
	ch := NewChannel(client, nil)

	result, err := ch.Send(context.Background(), &types.SendInput{
		UserID:   "u_123",
		MsgType:  "interactive",
		Title:    "Daily News",
		Markdown: "# Brief",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if result.MessageID != "om_card_1" {
		t.Fatalf("message id = %q", result.MessageID)
	}
	if request.MsgType != "interactive" {
		t.Fatalf("msg_type = %q, want interactive", request.MsgType)
	}
	var card capturedInteractiveCard
	if err := json.Unmarshal([]byte(request.Content), &card); err != nil {
		t.Fatalf("decode interactive card: %v", err)
	}
	if card.Schema != "2.0" || card.Config.WidthMode != "fill" {
		t.Fatalf("card schema/config = %+v", card)
	}
	if card.Header == nil || card.Header.Template != "blue" || card.Header.Title.Tag != "plain_text" || card.Header.Title.Content != "Daily News" {
		t.Fatalf("card header = %+v", card.Header)
	}
	if len(card.Body.Elements) != 1 || card.Body.Elements[0].Tag != "markdown" || card.Body.Elements[0].Content != "# Brief" {
		t.Fatalf("card body = %+v", card.Body)
	}
}

func TestSend_MarkdownDefaultsToPost(t *testing.T) {
	var request capturedMessageRequest
	mockHTTP := &MockHttpClient{DoFunc: func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "tenant_access_token") {
			return successfulTenantTokenResponse(), nil
		}
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			t.Fatalf("decode message request: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"success","data":{"message_id":"om_post_1"}}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	}}
	client := lark.NewClient("appId", "appSecret", lark.WithHttpClient(mockHTTP))
	ch := NewChannel(client, nil)

	if _, err := ch.Send(context.Background(), &types.SendInput{UserID: "u_123", Markdown: "# Brief"}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if request.MsgType != "post" {
		t.Fatalf("msg_type = %q, want post", request.MsgType)
	}
}

func TestSend_InteractiveMarkdownCardChunks(t *testing.T) {
	var requests []capturedMessageRequest
	mockHTTP := &MockHttpClient{DoFunc: func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "tenant_access_token") {
			return successfulTenantTokenResponse(), nil
		}
		var request capturedMessageRequest
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			t.Fatalf("decode message request: %v", err)
		}
		requests = append(requests, request)
		messageID := fmt.Sprintf("om_card_%d", len(requests))
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"code":0,"msg":"success","data":{"message_id":"` + messageID + `"}}`,
			)),
			Header: http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	}}
	client := lark.NewClient("appId", "appSecret", lark.WithHttpClient(mockHTTP))
	outboundConfig := types.DefaultChannelConfig().Outbound
	outboundConfig.TextChunkLimit = 20
	ch := NewChannel(client, nil, types.WithOutboundConfig(outboundConfig))

	result, err := ch.Send(context.Background(), &types.SendInput{
		UserID:         "u_123",
		MsgType:        "interactive",
		Title:          "Daily News",
		Markdown:       strings.Repeat("Feishu card content. ", 5),
		IdempotencyKey: "delivery-card-chunks",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if len(requests) < 2 {
		t.Fatalf("request count = %d, want multiple chunks", len(requests))
	}
	if len(result.ChunkIDs) != len(requests) {
		t.Fatalf("chunk IDs = %v, requests = %d", result.ChunkIDs, len(requests))
	}
	seenUUIDs := make(map[string]struct{}, len(requests))
	for i, request := range requests {
		if request.MsgType != "interactive" {
			t.Fatalf("request %d msg_type = %q", i, request.MsgType)
		}
		var card capturedInteractiveCard
		if err := json.Unmarshal([]byte(request.Content), &card); err != nil {
			t.Fatalf("decode card %d: %v", i, err)
		}
		if card.Schema != "2.0" || len(card.Body.Elements) != 1 || card.Body.Elements[0].Tag != "markdown" {
			t.Fatalf("card %d = %+v", i, card)
		}
		if request.UUID == "" {
			t.Fatalf("request %d has empty uuid", i)
		}
		if _, duplicate := seenUUIDs[request.UUID]; duplicate {
			t.Fatalf("request %d reused uuid %q", i, request.UUID)
		}
		seenUUIDs[request.UUID] = struct{}{}
	}
}

func TestSend_InteractiveMarkdownCardOmitsEmptyHeader(t *testing.T) {
	cardJSON, err := interactiveMarkdownCard("  ", "body")
	if err != nil {
		t.Fatalf("interactiveMarkdownCard failed: %v", err)
	}
	var card capturedInteractiveCard
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatalf("decode card: %v", err)
	}
	if card.Header != nil {
		t.Fatalf("header = %+v, want omitted", card.Header)
	}
}

func successfulTenantTokenResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"success","tenant_access_token":"t-token","expire":7200}`)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func TestSendRejectsSuccessfulResponseWithoutMessageReceipt(t *testing.T) {
	mockHTTP := &MockHttpClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"success","data":{}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}
	client := lark.NewClient("appId", "appSecret", lark.WithHttpClient(mockHTTP))
	ch := NewChannel(client, nil)

	if result, err := ch.Send(context.Background(), &types.SendInput{UserID: "u_123", Text: "hello"}); err == nil || result != nil {
		t.Fatalf("successful response without receipt was accepted: result=%+v err=%v", result, err)
	}
}

func TestSend_NoUserIDOrChatID(t *testing.T) {
	client := lark.NewClient("appId", "appSecret")
	ch := NewChannel(client, nil)

	_, err := ch.Send(context.Background(), &types.SendInput{
		Text: "hello",
	})

	if err == nil || !strings.Contains(err.Error(), "ReceiveID, ChatID, or UserID must be provided") {
		t.Errorf("expected error 'ReceiveID, ChatID, or UserID must be provided', got %v", err)
	}
}

func TestStartRejectsMissingWebSocketClient(t *testing.T) {
	client := lark.NewClient("appId", "appSecret")
	ch := NewChannel(client, nil)

	if err := ch.Start(context.Background()); err == nil {
		t.Fatal("Start reported success without a WebSocket client")
	}
}

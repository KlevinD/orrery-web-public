package feishu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkchannel "github.com/larksuite/oapi-sdk-go/v3/channel"
)

type feishuTestHTTPClient struct {
	do func(*http.Request) (*http.Response, error)
}

func (c feishuTestHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return c.do(req)
}

type feishuMessageRequest struct {
	MsgType string `json:"msg_type"`
	Content string `json:"content"`
}

type feishuInteractiveCard struct {
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

func TestSDKClientSendDirectUsesInteractiveCardForMarkdown(t *testing.T) {
	var request feishuMessageRequest
	httpClient := feishuTestHTTPClient{do: func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "tenant_access_token") {
			return successfulFeishuTenantTokenResponse(), nil
		}
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			t.Fatalf("decode Feishu request: %v", err)
		}
		return successfulFeishuMessageResponse("om_card_1"), nil
	}}
	apiClient := lark.NewClient("app-id", "app-secret", lark.WithHttpClient(httpClient))
	client := &SDKClient{channel: larkchannel.NewChannel(apiClient, nil), started: true}

	receipt, err := client.SendDirect(
		context.Background(),
		"oc_group_1",
		"每日新闻简报",
		"# 核心新闻\n\nAI Agent 发布新版本。",
		"markdown",
		"delivery-1",
	)
	if err != nil {
		t.Fatalf("SendDirect failed: %v", err)
	}
	if receipt != "om_card_1" {
		t.Fatalf("receipt = %q", receipt)
	}
	if request.MsgType != "interactive" {
		t.Fatalf("msg_type = %q, want interactive", request.MsgType)
	}
	var card feishuInteractiveCard
	if err := json.Unmarshal([]byte(request.Content), &card); err != nil {
		t.Fatalf("decode interactive card: %v", err)
	}
	if card.Schema != "2.0" || card.Config.WidthMode != "fill" {
		t.Fatalf("card schema/config = %+v", card)
	}
	if card.Header == nil || card.Header.Template != "blue" || card.Header.Title.Tag != "plain_text" || card.Header.Title.Content != "每日新闻简报" {
		t.Fatalf("card header = %+v", card.Header)
	}
	if len(card.Body.Elements) != 1 || card.Body.Elements[0].Tag != "markdown" || !strings.Contains(card.Body.Elements[0].Content, "AI Agent") {
		t.Fatalf("card body = %+v", card.Body)
	}
}

func TestSDKClientSendDirectKeepsTextMessage(t *testing.T) {
	var request feishuMessageRequest
	httpClient := feishuTestHTTPClient{do: func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "tenant_access_token") {
			return successfulFeishuTenantTokenResponse(), nil
		}
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			t.Fatalf("decode Feishu request: %v", err)
		}
		return successfulFeishuMessageResponse("om_text_1"), nil
	}}
	apiClient := lark.NewClient("app-id", "app-secret", lark.WithHttpClient(httpClient))
	client := &SDKClient{channel: larkchannel.NewChannel(apiClient, nil), started: true}

	if _, err := client.SendDirect(context.Background(), "oc_group_1", "Status", "completed", "text", "delivery-2"); err != nil {
		t.Fatalf("SendDirect failed: %v", err)
	}
	if request.MsgType != "text" {
		t.Fatalf("msg_type = %q, want text", request.MsgType)
	}
	var content map[string]string
	if err := json.Unmarshal([]byte(request.Content), &content); err != nil {
		t.Fatalf("decode text content: %v", err)
	}
	if content["text"] != "completed" {
		t.Fatalf("text content = %+v", content)
	}
}

func successfulFeishuMessageResponse(messageID string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(
			`{"code":0,"msg":"success","data":{"message_id":"` + messageID + `","chat_id":"oc_group_1"}}`,
		)),
		Header: http.Header{"Content-Type": []string{"application/json"}},
	}
}

func successfulFeishuTenantTokenResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"success","tenant_access_token":"t-token","expire":7200}`)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

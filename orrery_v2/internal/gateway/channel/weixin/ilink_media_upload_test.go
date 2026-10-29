package weixin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"orrery_v2/internal/gateway/protocol"
)

func TestIlinkOutboundImageEncryptedUploadAndBoundReply(t *testing.T) {
	data := testPNG(t)
	var key []byte
	sent := false
	uploaded := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ilink/bot/getuploadurl":
			var p map[string]any
			_ = json.NewDecoder(r.Body).Decode(&p)
			if p["to_user_id"] != "owner-weixin" || p["media_type"] != float64(1) || p["rawsize"] != float64(len(data)) {
				t.Error("wrong upload owner or metadata")
			}
			key, _ = hex.DecodeString(p["aeskey"].(string))
			_, _ = io.WriteString(w, `{"ret":0,"upload_param":"signed"}`)
		case "/upload":
			if r.Header.Get("Authorization") != "" || r.URL.Query().Get("encrypted_query_param") != "signed" {
				t.Error("CDN credential isolation failed")
			}
			raw, _ := io.ReadAll(r.Body)
			plain, err := decryptIlinkImage(raw, key)
			if err != nil || !bytes.Equal(plain, data) {
				t.Error("invalid upload encryption")
			}
			uploaded = true
			w.Header().Set("x-encrypted-param", "download-signed")
		case "/sendmessage":
			var p struct {
				Msg struct {
					To       string `json:"to_user_id"`
					Context  string `json:"context_token"`
					ClientID string `json:"client_id"`
					Items    []struct {
						Type  int `json:"type"`
						Image struct {
							Media struct {
								Key   string `json:"aes_key"`
								Param string `json:"encrypt_query_param"`
							} `json:"media"`
						} `json:"image_item"`
					} `json:"item_list"`
				} `json:"msg"`
			}
			if json.NewDecoder(r.Body).Decode(&p) != nil || p.Msg.To != "owner-weixin" || p.Msg.Context != "owner-context" || p.Msg.ClientID != "stable-image-id" || len(p.Msg.Items) != 1 || p.Msg.Items[0].Type != 2 {
				t.Error("wrong image delivery")
			}
			decoded, _ := base64.StdEncoding.DecodeString(p.Msg.Items[0].Image.Media.Key)
			if string(decoded) != hex.EncodeToString(key) || p.Msg.Items[0].Image.Media.Param != "download-signed" {
				t.Error("wrong image media reference")
			}
			sent = true
			_, _ = io.WriteString(w, `{"ret":0}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := &IlinkBotClient{httpClient: server.Client(), allowInsecureLoopback: true, options: Options{IlinkBaseURL: server.URL, IlinkCDNBaseURL: server.URL}.WithDefaults()}
	receipt, err := client.SendWithReceipt(context.Background(), OutboundMessage{ChatID: "owner-weixin", ContextToken: "owner-context", ClientID: "stable-image-id", Image: data})
	if err != nil || receipt != "stable-image-id" || !uploaded || !sent {
		t.Fatalf("receipt=%s err=%v uploaded=%v sent=%v", receipt, err, uploaded, sent)
	}
}

func TestRenderGeneratedImageWithoutLeakingPrivateMarkdown(t *testing.T) {
	data := testPNG(t)
	markdown := "![Generated image](/api/private/download)"
	events := []protocol.Event{{Type: protocol.EventAssistantTextDelta, Text: "Done\n" + markdown}, {Type: protocol.EventAssistantImage, Attachment: &protocol.Attachment{Type: "image", MIMEType: "image/png", Content: base64.StdEncoding.EncodeToString(data), Metadata: map[string]any{"markdown": markdown}}}}
	replies := RenderReplies("owner", events, Options{})
	if len(replies) != 2 || replies[0].Text != "Done" || !bytes.Equal(replies[1].Image, data) {
		t.Fatalf("unexpected replies: %d", len(replies))
	}
	if replies := RenderReplies("owner", events[1:], Options{}); len(replies) != 1 || len(replies[0].Image) == 0 {
		t.Fatal("image-only response lost")
	}
}

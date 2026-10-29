package weixin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIlinkVoicePlatformTranscriptPreservesModalityInDurablePayload(t *testing.T) {
	var provider ilinkWeixinMessage
	if err := json.Unmarshal([]byte(`{"item_list":[{"type":3,"voice_item":{"text":"同意","media":{"full_url":"https://novac2c.cdn.weixin.qq.com/voice"}}}]}`), &provider); err != nil {
		t.Fatal(err)
	}
	inbound := inboundFromWeixinMessage(provider)
	raw, err := json.Marshal(inbound)
	if err != nil {
		t.Fatal(err)
	}
	var recovered InboundMessage
	if err := json.Unmarshal(raw, &recovered); err != nil {
		t.Fatal(err)
	}
	input := BuildSubmitInput(recovered)
	if input.Message != "同意" || input.InputModality != "voice" || input.TranscriptSource != "platform" || len(input.Attachments) != 0 {
		t.Fatalf("input=%+v", input)
	}
	recovered.Text = "/new"
	if BuildSubmitInput(recovered).Telemetry["weixin_new_session"] != "false" {
		t.Fatal("voice triggered a session command")
	}
}

func TestIlinkVoiceDownloadsAndDecryptsSILKWithoutCredentials(t *testing.T) {
	key := []byte("0123456789abcdef")
	data := []byte("\x02#!SILK_V3 private voice")
	encrypted := encryptTestIlinkImage(t, data, key)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Error("credentials sent to CDN")
		}
		_, _ = w.Write(encrypted)
	}))
	defer server.Close()
	message := inboundFromWeixinMessage(ilinkWeixinMessage{ItemList: []ilinkWeixinItem{{Type: 3, VoiceItem: &ilinkVoiceItem{EncodeType: 6, Media: &ilinkCDNMedia{FullURL: server.URL, AESKey: base64.StdEncoding.EncodeToString(key)}}}}})
	client := &IlinkBotClient{httpClient: server.Client(), allowInsecureLoopback: true, options: Options{IlinkCDNBaseURL: server.URL}.WithDefaults()}
	resolved, err := client.resolveInboundAttachments(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Attachments) != 1 || resolved.Attachments[0].MIMEType != "audio/silk" || resolved.Attachments[0].FullURL != "" || resolved.Attachments[0].AESKey != "" {
		t.Fatal("invalid resolved voice")
	}
	decoded, err := base64.StdEncoding.DecodeString(resolved.Attachments[0].ContentBase64)
	if err != nil || !bytes.Equal(decoded, data) {
		t.Fatal("voice changed during decryption")
	}
	input := BuildSubmitInput(resolved)
	if input.InputModality != "voice" || input.Attachments[0].Type != "audio" {
		t.Fatalf("input=%+v", input)
	}
}

func TestIlinkMissingVoiceMediaStillReachesUserFacingFailure(t *testing.T) {
	input := BuildSubmitInput(inboundFromWeixinMessage(ilinkWeixinMessage{ItemList: []ilinkWeixinItem{{Type: 3, VoiceItem: &ilinkVoiceItem{}}}}))
	if input.InputModality != "voice" {
		t.Fatal("empty voice was dropped")
	}
}

func TestIlinkMixedTextAndVoiceRetainsBothParts(t *testing.T) {
	message := inboundFromWeixinMessage(ilinkWeixinMessage{ItemList: []ilinkWeixinItem{
		{TextItem: &ilinkTextItem{Text: "先解释一下"}},
		{VoiceItem: &ilinkVoiceItem{Text: "这是什么审批"}},
	}})
	if message.Text != "先解释一下\n这是什么审批" || message.InputModality != "voice" {
		t.Fatalf("message=%+v", message)
	}
}

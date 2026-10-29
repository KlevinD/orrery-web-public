package weixin

import (
	"bytes"
	"context"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gwcore "orrery_v2/internal/gateway/core"
)

func TestIlinkImageMessageIsParsedDownloadedAndMappedToGatewayAttachment(t *testing.T) {
	imageBytes := testPNG(t)
	key := []byte("0123456789abcdef")
	ciphertext := encryptTestIlinkImage(t, imageBytes, key)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected CDN request method=%s authorization=%q", req.Method, req.Header.Get("Authorization"))
		}
		_, _ = w.Write(ciphertext)
	}))
	defer server.Close()

	raw := `{
		"message_id":"image-message",
		"from_user_id":"weixin-user",
		"to_user_id":"weixin-bot",
		"context_token":"context-token",
		"item_list":[{"type":2,"image_item":{"aeskey":"` + hex.EncodeToString(key) + `","media":{"full_url":"` + server.URL + `/image","encrypt_query_param":"signed"}}}]
	}`
	var providerMessage ilinkWeixinMessage
	if err := json.Unmarshal([]byte(raw), &providerMessage); err != nil {
		t.Fatal(err)
	}
	inbound := inboundFromWeixinMessage(providerMessage)
	if inbound.Text != "" || len(inbound.Attachments) != 1 || inbound.Attachments[0].FullURL == "" {
		t.Fatalf("parsed inbound = %+v", inbound)
	}
	client := &IlinkBotClient{
		httpClient:            server.Client(),
		allowInsecureLoopback: true,
		options:               Options{IlinkCDNBaseURL: server.URL}.WithDefaults(),
	}
	resolved, err := client.resolveInboundAttachments(context.Background(), inbound)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Attachments) != 1 || resolved.Attachments[0].MIMEType != "image/png" || resolved.Attachments[0].FullURL != "" || resolved.Attachments[0].AESKeyHex != "" {
		t.Fatalf("resolved attachment = %+v", resolved.Attachments)
	}
	decoded, err := base64.StdEncoding.DecodeString(resolved.Attachments[0].ContentBase64)
	if err != nil || !bytes.Equal(decoded, imageBytes) {
		t.Fatalf("decoded image mismatch: error=%v bytes=%d", err, len(decoded))
	}
	input := BuildSubmitInput(resolved)
	if input.Message != "" || len(input.Attachments) != 1 || input.Attachments[0].MIMEType != "image/png" || input.Attachments[0].Metadata["content_encoding"] != "base64" {
		t.Fatalf("gateway input = %+v", input)
	}
}

func TestIlinkImageSecretsRemainInDurablePayloadButDecryptedContentDoesNot(t *testing.T) {
	payload, err := json.Marshal(ilinkInboundJobPayload{Message: InboundMessage{Attachments: []InboundAttachment{{
		Type: "image", FullURL: "https://novac2c.cdn.weixin.qq.com/c2c/download?token=provider", AESKey: "provider-key", ContentBase64: "decrypted-content",
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if !strings.Contains(text, "provider-key") || strings.Contains(text, "decrypted-content") {
		t.Fatalf("durable image payload = %s", text)
	}
}

func TestIlinkImageDownloadRejectsNonWeixinHost(t *testing.T) {
	_, err := ilinkImageDownloadURL(Options{}.WithDefaults(), InboundAttachment{FullURL: "https://example.com/image.png"}, false)
	if err == nil || !strings.Contains(err.Error(), "official") {
		t.Fatalf("non-Weixin CDN error = %v", err)
	}
	_, err = ilinkImageDownloadURL(Options{IlinkCDNBaseURL: "https://example.com/c2c"}.WithDefaults(), InboundAttachment{EncryptQueryParam: "signed"}, false)
	if err == nil || !strings.Contains(err.Error(), "official") {
		t.Fatalf("non-Weixin CDN base error = %v", err)
	}
	fallback, err := ilinkImageDownloadURL(Options{}.WithDefaults(), InboundAttachment{
		FullURL: "https://example.com/image.png", EncryptQueryParam: "signed",
	}, false)
	if err != nil || !strings.HasPrefix(fallback, defaultIlinkCDNBaseURL+"/download?") || !strings.Contains(fallback, "encrypted_query_param=signed") {
		t.Fatalf("safe CDN fallback = %q err=%v", fallback, err)
	}
}

func TestIlinkImageAESKeyAcceptsProviderFormats(t *testing.T) {
	want := []byte("0123456789abcdef")
	for name, attachment := range map[string]InboundAttachment{
		"image item hex":   {AESKeyHex: hex.EncodeToString(want)},
		"media raw base64": {AESKey: base64.StdEncoding.EncodeToString(want)},
		"media hex base64": {AESKey: base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(want)))},
	} {
		t.Run(name, func(t *testing.T) {
			got, encrypted, err := ilinkImageAESKey(attachment)
			if err != nil || !encrypted || !bytes.Equal(got, want) {
				t.Fatalf("key = %x encrypted=%v err=%v", got, encrypted, err)
			}
		})
	}
}

func TestProcessInboundAcceptsImageWithoutText(t *testing.T) {
	runner := &fakeRunner{}
	handle := &Handle{gateway: gwcore.NewGateway(runner), options: Options{}, active: map[string]struct{}{}}
	replies, err := handle.ProcessInbound(context.Background(), InboundMessage{
		AccountID: "bot-a", MessageID: "image-message", ChatID: "user-a",
		Attachments: []InboundAttachment{{Type: "image", Name: "photo.png", MIMEType: "image/png", Bytes: 1, ContentBase64: "AA=="}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 1 || len(runner.inputs[0].Attachments) != 1 || len(replies) != 1 {
		t.Fatalf("image-only turn inputs=%+v replies=%+v", runner.inputs, replies)
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func encryptTestIlinkImage(t *testing.T, plaintext, key []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(append([]byte(nil), plaintext...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	ciphertext := make([]byte, len(padded))
	for offset := 0; offset < len(padded); offset += aes.BlockSize {
		block.Encrypt(ciphertext[offset:offset+aes.BlockSize], padded[offset:offset+aes.BlockSize])
	}
	return ciphertext
}

package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/agent/runtime/llmprofile"
)

func testWAV() []byte {
	data := make([]byte, 48)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 24000)
	binary.LittleEndian.PutUint32(data[28:], 48000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], 4)
	return data
}

func testGateway(server *httptest.Server) *Gateway {
	return NewGateway(Config{BaseURL: server.URL + "/v1", TranscriptionProfileID: "stt", SpeechProfileID: "tts", Voices: []string{"configured-voice"},
		Client: server.Client(), Secrets: llmprofile.StaticSecretResolver{Values: map[string]string{"gateway-secret": "private-key"}},
		Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{
			{ProfileID: "chat", Enabled: true, GlobalDefault: true, Model: "never-chat"},
			{ProfileID: "stt", Enabled: true, BaseURL: server.URL + "/v1/", Model: "configured-stt", APIKeyRef: "gateway-secret"},
			{ProfileID: "tts", Enabled: true, BaseURL: server.URL + "/v1", Model: "configured-tts", APIKeyRef: "gateway-secret"},
		}},
	})
}

func testTranscription() TranscriptionRequest {
	return TranscriptionRequest{Scope: Scope{TenantID: "tenant-a", OwnerID: "owner-a", SourceID: "event-a"}, Data: testWAV(), MIMEType: "audio/wav", Language: "zh"}
}

func TestGatewayTranscriptionUsesExplicitProfileAndPrivateScopedIdempotency(t *testing.T) {
	var keys []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/audio/transcriptions" || req.Method != "POST" || req.Header.Get("Authorization") != "Bearer private-key" {
			t.Error("invalid gateway routing")
		}
		keys = append(keys, req.Header.Get("Idempotency-Key"))
		if err := req.ParseMultipartForm(MaxAudioBytes); err != nil {
			t.Error(err)
			return
		}
		defer req.MultipartForm.RemoveAll()
		if req.FormValue("model") != "configured-stt" || req.FormValue("language") != "zh" || req.FormValue("response_format") != "json" {
			t.Error("invalid transcription parameters")
		}
		file, header, err := req.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if header.Filename != "voice.wav" || !bytes.Equal(data, testWAV()) {
			t.Error("invalid audio file")
		}
		_, _ = io.WriteString(w, `{"text":" 帮我解释这项审批 "}`)
	}))
	defer server.Close()
	gateway := testGateway(server)
	for _, owner := range []string{"owner-a", "owner-a", "owner-b"} {
		input := testTranscription()
		input.Scope.OwnerID = owner
		result, err := gateway.Transcribe(context.Background(), input)
		if err != nil || result.Text != "帮我解释这项审批" || result.Source != "model_gateway" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	if keys[0] == "" || keys[0] != keys[1] || keys[0] == keys[2] || strings.Contains(keys[0], "owner") {
		t.Fatalf("invalid scoped keys: %v", keys)
	}
}

func TestGatewayStrictProfilesNeverFallBackToChat(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP request") }))
	defer server.Close()
	for _, scenario := range []string{"missing", "disabled", "ambiguous", "wrong-gateway", "missing-secret", "http"} {
		t.Run(scenario, func(t *testing.T) {
			g := testGateway(server)
			profiles := g.config.Profiles.(llmprofile.StaticProfileProvider)
			switch scenario {
			case "missing":
				g.config.TranscriptionProfileID = "unknown"
			case "disabled":
				profiles.Profiles[1].Enabled = false
			case "ambiguous":
				profiles.Profiles = append(profiles.Profiles, profiles.Profiles[1])
			case "wrong-gateway":
				profiles.Profiles[1].BaseURL = "https://vendor.example/v1"
			case "missing-secret":
				g.config.Secrets = nil
			case "http":
				g.config.BaseURL = strings.Replace(server.URL, "https:", "http:", 1)
			}
			g.config.Profiles = profiles
			if _, err := g.Transcribe(context.Background(), testTranscription()); err == nil {
				t.Fatal("expected configuration failure")
			}
		})
	}
}

func TestGatewayRejectsInvalidAudioBeforeSending(t *testing.T) {
	for _, scenario := range []string{"identity", "spoofed", "oversize", "silk"} {
		t.Run(scenario, func(t *testing.T) {
			g := NewGateway(Config{})
			input := testTranscription()
			switch scenario {
			case "identity":
				input.Scope.OwnerID = ""
			case "spoofed":
				input.Data = []byte("this is not a WAV file")
			case "oversize":
				input.Data = make([]byte, MaxAudioBytes+1)
			case "silk":
				input.Data = []byte("\x02#!SILK_V3 raw bytes")
				input.MIMEType = "audio/silk"
			}
			if _, err := g.Transcribe(context.Background(), input); err == nil || errors.Is(err, ErrNotConfigured) {
				t.Fatalf("validation error=%v", err)
			}
		})
	}
}

func TestGatewayErrorsAreBoundedAndRedacted(t *testing.T) {
	for _, scenario := range []string{"provider", "redirect", "empty", "invalid-json", "oversize", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				switch scenario {
				case "provider":
					w.WriteHeader(500)
					_, _ = io.WriteString(w, "private-key private-transcript")
				case "redirect":
					w.Header().Set("Location", "/leaked")
					w.WriteHeader(307)
				case "empty":
					_, _ = io.WriteString(w, `{"text":" "}`)
				case "invalid-json":
					_, _ = io.WriteString(w, "private-transcript")
				case "oversize":
					w.Header().Set("Content-Length", "1048577")
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			_, err := testGateway(server).Transcribe(ctx, testTranscription())
			if err == nil || strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), server.URL) {
				t.Fatalf("unsafe error=%v", err)
			}
			if calls > 1 {
				t.Fatal("redirect followed or request retried")
			}
			if scenario == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestGatewaySpeechReturnsValidatedAudio(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if req.URL.Path != "/v1/audio/speech" || body["model"] != "configured-tts" || body["input"] != "这项审批需要你确认。" || body["voice"] != "configured-voice" || body["response_format"] != "wav" {
			t.Error("invalid speech request")
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(testWAV())
	}))
	defer server.Close()
	g := testGateway(server)
	input := SpeechRequest{Scope: testTranscription().Scope, Text: "这项审批需要你确认。", Voice: "configured-voice", Format: "wav"}
	result, err := g.Synthesize(context.Background(), input)
	if err != nil || result.MIMEType != "audio/wav" || !bytes.Equal(result.Data, testWAV()) {
		t.Fatalf("speech err=%v", err)
	}
	input.Voice = "unknown"
	if _, err := g.Synthesize(context.Background(), input); err == nil {
		t.Fatal("unknown voice accepted")
	}
}

func TestGatewaySpeechRejectsJSONDisguisedAsAudio(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"error":"private-key"}`) }))
	defer server.Close()
	_, err := testGateway(server).Synthesize(context.Background(), SpeechRequest{Scope: testTranscription().Scope, Text: "test", Voice: "configured-voice", Format: "wav"})
	if !errors.Is(err, ErrInvalidAudio) {
		t.Fatalf("err=%v", err)
	}
}

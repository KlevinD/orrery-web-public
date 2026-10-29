package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/speech"
)

type testPersonalAssistantSpeech struct {
	calls  int
	viewer discussionsession.ViewerRef
	err    error
}

func (s *testPersonalAssistantSpeech) Synthesize(_ context.Context, viewer discussionsession.ViewerRef, _ speech.SynthesizeInput) (speech.Audio, error) {
	s.calls++
	s.viewer = viewer
	return speech.Audio{Data: []byte("ID3speech"), MIMEType: "audio/mpeg"}, s.err
}

func (s *testPersonalAssistantSpeech) Transcribe(_ context.Context, viewer discussionsession.ViewerRef, _ speech.TranscribeInput) (speech.Transcript, error) {
	s.calls++
	s.viewer = viewer
	return speech.Transcript{Text: "test", InputModality: "voice", ApprovalConfirmationRequired: true}, s.err
}

func TestPersonalAssistantSpeechNativeRoutes(t *testing.T) {
	for _, operation := range []string{"speech", "transcriptions"} {
		service := &testPersonalAssistantSpeech{}
		handler := newTestRouter(RouterOptions{PersonalAssistantSpeech: service})
		body := `{"conversation_id":"c","message_id":"m"}`
		if operation == "transcriptions" {
			body = `{"conversation_id":"c","attachment_id":"a"}`
		}
		req := authedRequest(http.MethodPost, "/api/profile/personal-assistant/audio/"+operation, body)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		if out.Code != http.StatusOK || service.calls != 1 || service.viewer.TenantID != "system" || service.viewer.OwnerID != "agent-1" {
			t.Fatalf("status=%d body=%s viewer=%+v", out.Code, out.Body.String(), service.viewer)
		}
		if out.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("private speech is cacheable")
		}
		if operation == "speech" && out.Header().Get("Content-Type") != "audio/mpeg" {
			t.Fatal("speech was not binary audio")
		}
		if operation == "transcriptions" && !strings.Contains(out.Body.String(), `"approval_confirmation_required":true`) {
			t.Fatal("voice provenance missing")
		}
	}
}

func TestPersonalAssistantSpeechRejectsUntrustedPayloadAndAnonymousCalls(t *testing.T) {
	service := &testPersonalAssistantSpeech{}
	handler := newTestRouter(RouterOptions{PersonalAssistantSpeech: service})
	path := "/api/profile/personal-assistant/audio/speech"
	for _, body := range []string{`{"text":"arbitrary text","owner_id":"someone-else"}`, `{} {}`, strings.Repeat("x", maxPersonalAssistantRequestBytes+1)} {
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, authedRequest(http.MethodPost, path, body))
		if out.Code != http.StatusBadRequest || service.calls != 0 {
			t.Fatalf("status=%d calls=%d", out.Code, service.calls)
		}
	}
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
	if out.Code != http.StatusUnauthorized || service.calls != 0 {
		t.Fatalf("status=%d calls=%d", out.Code, service.calls)
	}
}

func TestPersonalAssistantSpeechConfigurationFailureIsExplicit(t *testing.T) {
	handler := newTestRouter(RouterOptions{PersonalAssistantSpeech: &testPersonalAssistantSpeech{err: speech.ErrNotConfigured}})
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, authedRequest(http.MethodPost, "/api/profile/personal-assistant/audio/speech", `{"conversation_id":"c","message_id":"m"}`))
	if out.Code != http.StatusServiceUnavailable || !strings.Contains(out.Body.String(), "语音服务尚未配置完成") {
		t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
	}
}

func TestPersonalAssistantSpeechAllowlistIsExact(t *testing.T) {
	for _, path := range []string{"/api/profile/personal-assistant/audio/speech", "/api/profile/personal-assistant/audio/transcriptions"} {
		if !authenticatedProfileRouteAllowed(authedRequest(http.MethodPost, path, "")) {
			t.Fatalf("missing route: %s", path)
		}
		if authenticatedProfileRouteAllowed(authedRequest(http.MethodGet, path, "")) || authenticatedProfileRouteAllowed(authedRequest(http.MethodPost, path+"/extra", "")) {
			t.Fatalf("speech authorization too broad: %s", path)
		}
	}
}

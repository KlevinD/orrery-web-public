package website

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHumanVerificationUnlocksPublicAgentForThirtyMinutes(t *testing.T) {
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	key := []byte("website-human-proof-key-0123456789abcdef")
	verifier, err := NewWebsiteHumanVerificationAssertionVerifier(key, func() time.Time { return now })
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	search := &fakeWebsiteKnowledgeSearch{}
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, KnowledgeSearch: search,
		HumanVerificationAssertions: verifier, Now: func() time.Time { return now },
		IdleTTL: time.Hour, MaxTTL: 2 * time.Hour,
	})
	session := createSession(t, handler, "https://www.minvia.cn")
	sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"复杂问题一"}`)
	sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"复杂问题二"}`)

	proof := signHumanVerificationClaims(t, key, map[string]any{
		"iss": "minvia-website-auth", "aud": "orrery-website-human-verification",
		"sid": session.SessionID, "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix(), "nonce": "human-proof-1",
	})
	verified := verifyHuman(t, handler, session, proof)
	if verified.Code != http.StatusNoContent {
		t.Fatalf("verification = %d %s", verified.Code, verified.Body.String())
	}

	answer := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"复杂问题三"}`)
	completed := completedPayload(t, answer.Body.String())
	if completed["route"] != "public_agent" || gateway.inputCount() != 1 {
		t.Fatalf("verified route = %+v agent=%d body=%s", completed, gateway.inputCount(), answer.Body.String())
	}

	now = now.Add(31 * time.Minute)
	afterExpiry := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"复杂问题四"}`)
	if completedPayload(t, afterExpiry.Body.String())["human_verification_required"] != true || gateway.inputCount() != 1 {
		t.Fatalf("expired verification remained active: %s", afterExpiry.Body.String())
	}
}

func TestHumanVerificationRejectsWrongSessionAndReplay(t *testing.T) {
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	key := []byte("website-human-proof-key-0123456789abcdef")
	verifier, _ := NewWebsiteHumanVerificationAssertionVerifier(key, func() time.Time { return now })
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins:              []string{"https://www.minvia.cn"},
		HumanVerificationAssertions: verifier, Now: func() time.Time { return now },
	})
	session := createSession(t, handler, "https://www.minvia.cn")
	wrong := signHumanVerificationClaims(t, key, map[string]any{
		"iss": "minvia-website-auth", "aud": "orrery-website-human-verification",
		"sid": "adv_other", "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix(), "nonce": "human-wrong",
	})
	if recorder := verifyHuman(t, handler, session, wrong); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong session accepted: %d %s", recorder.Code, recorder.Body.String())
	}
	proof := signHumanVerificationClaims(t, key, map[string]any{
		"iss": "minvia-website-auth", "aud": "orrery-website-human-verification",
		"sid": session.SessionID, "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix(), "nonce": "human-replay",
	})
	if recorder := verifyHuman(t, handler, session, proof); recorder.Code != http.StatusNoContent {
		t.Fatalf("first proof failed: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := verifyHuman(t, handler, session, proof); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("replayed proof accepted: %d %s", recorder.Code, recorder.Body.String())
	}
}

func verifyHuman(t *testing.T, handler http.Handler, session SessionResponse, proof string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"session_id": session.SessionID})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/advisor/human-verification", bytes.NewReader(body))
	request.Header.Set("Origin", "https://www.minvia.cn")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Advisor-Session-Token", session.AccessToken)
	request.Header.Set("X-Website-Human-Verification", proof)
	handler.ServeHTTP(recorder, request)
	return recorder
}

func signHumanVerificationClaims(t *testing.T, key []byte, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

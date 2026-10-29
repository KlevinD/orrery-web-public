package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestWorkforceAvatarUploadReadAndReset(t *testing.T) {
	store := NewLocalWorkforceAvatarStore(t.TempDir())
	handler := newTestRouter(RouterOptions{
		Operations:       &fakeOperationsService{},
		WorkforceAvatars: store,
	})
	avatar := testWorkforceAvatarPNG(t)

	upload := workforceAvatarUploadRequest(t, "/api/workforce/members/agent-one/avatar", "avatar.png", avatar)
	uploadRecorder := httptest.NewRecorder()
	handler.ServeHTTP(uploadRecorder, upload)
	if uploadRecorder.Code != http.StatusOK {
		t.Fatalf("upload status = %d body = %s", uploadRecorder.Code, uploadRecorder.Body.String())
	}

	read := authedRequest(http.MethodGet, "/api/workforce/members/agent-one/avatar", "")
	readRecorder := httptest.NewRecorder()
	handler.ServeHTTP(readRecorder, read)
	if readRecorder.Code != http.StatusOK {
		t.Fatalf("read status = %d body = %s", readRecorder.Code, readRecorder.Body.String())
	}
	if got := readRecorder.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content type = %q", got)
	}
	if !bytes.Equal(readRecorder.Body.Bytes(), avatar) {
		t.Fatal("read avatar does not match uploaded bytes")
	}

	reset := authedRequest(http.MethodDelete, "/api/workforce/members/agent-one/avatar", "")
	resetRecorder := httptest.NewRecorder()
	handler.ServeHTTP(resetRecorder, reset)
	if resetRecorder.Code != http.StatusNoContent {
		t.Fatalf("reset status = %d body = %s", resetRecorder.Code, resetRecorder.Body.String())
	}

	missingRecorder := httptest.NewRecorder()
	handler.ServeHTTP(missingRecorder, authedRequest(http.MethodGet, "/api/workforce/members/agent-one/avatar", ""))
	if missingRecorder.Code != http.StatusNoContent {
		t.Fatalf("missing status = %d body = %s", missingRecorder.Code, missingRecorder.Body.String())
	}
	if missingRecorder.Body.Len() != 0 {
		t.Fatalf("missing avatar body = %q", missingRecorder.Body.String())
	}
}

func TestWorkforceAvatarDefaultsCanBeListedAndSelected(t *testing.T) {
	store := NewLocalWorkforceAvatarStore(t.TempDir())
	handler := newTestRouter(RouterOptions{
		Operations:       &fakeOperationsService{},
		WorkforceAvatars: store,
	})

	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, authedRequest(http.MethodGet, "/api/workforce/avatar-defaults", ""))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listRecorder.Code, listRecorder.Body.String())
	}
	var defaults []WorkforceAvatarDefaultDTO
	if err := json.NewDecoder(listRecorder.Body).Decode(&defaults); err != nil {
		t.Fatalf("decode defaults: %v", err)
	}
	if len(defaults) != 8 {
		t.Fatalf("default avatar catalog has %d items, want 8", len(defaults))
	}
	for _, item := range defaults {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, authedRequest(http.MethodGet, item.AvatarURL, ""))
		if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "image/png" || recorder.Header().Get("ETag") != `"`+item.ETag+`"` || recorder.Header().Get("Cache-Control") != "private, max-age=3600" {
			t.Fatalf("default %s response: status=%d headers=%v", item.ID, recorder.Code, recorder.Header())
		}
		if _, err := png.Decode(bytes.NewReader(recorder.Body.Bytes())); err != nil {
			t.Fatalf("default %s HTTP content is not a PNG: %v", item.ID, err)
		}
	}
	selectedID := defaults[0].ID
	if selectedID == "" || defaults[0].AvatarURL == "" || defaults[0].ContentType == "" {
		t.Fatalf("default avatar metadata is incomplete: %+v", defaults[0])
	}

	payload, err := json.Marshal(workforceAvatarDefaultSelectionRequest{DefaultID: selectedID})
	if err != nil {
		t.Fatalf("encode selection: %v", err)
	}
	selectRequest := authedRequest(http.MethodPut, "/api/workforce/members/agent-one/avatar/default", string(payload))
	selectRequest.Header.Set("Content-Type", "application/json")
	selectRecorder := httptest.NewRecorder()
	handler.ServeHTTP(selectRecorder, selectRequest)
	if selectRecorder.Code != http.StatusOK {
		t.Fatalf("select status = %d body = %s", selectRecorder.Code, selectRecorder.Body.String())
	}
	var selected WorkforceAvatarDTO
	if err := json.NewDecoder(selectRecorder.Body).Decode(&selected); err != nil {
		t.Fatalf("decode selected avatar: %v", err)
	}
	if selected.AgentProfileID != "agent-one" || selected.ContentType != defaults[0].ContentType || selected.ETag != defaults[0].ETag {
		t.Fatalf("selected avatar = %+v, catalog item = %+v", selected, defaults[0])
	}

	readRecorder := httptest.NewRecorder()
	handler.ServeHTTP(readRecorder, authedRequest(http.MethodGet, "/api/workforce/members/agent-one/avatar", ""))
	if readRecorder.Code != http.StatusOK {
		t.Fatalf("read selected status = %d body = %s", readRecorder.Code, readRecorder.Body.String())
	}
	defaultAvatar, err := store.OpenDefault(context.Background(), selectedID)
	if err != nil {
		t.Fatalf("open selected default: %v", err)
	}
	if !bytes.Equal(readRecorder.Body.Bytes(), defaultAvatar.Bytes) {
		t.Fatal("selected avatar bytes do not match the built-in default")
	}

	unknownDefault := authedRequest(http.MethodGet, "/api/workforce/avatar-defaults/does-not-exist", "")
	unknownRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unknownRecorder, unknownDefault)
	if unknownRecorder.Code != http.StatusNotFound {
		t.Fatalf("unknown default status = %d body = %s", unknownRecorder.Code, unknownRecorder.Body.String())
	}
}

func TestDefaultWorkforceAvatarsAreCompleteAndOptimized(t *testing.T) {
	profileIDs := []string{
		"website-knowledge-curator",
		"A000014",
		"A000015",
		"A000013",
		"A000006",
		"A000011",
		"A000012",
		"A000010",
	}

	for _, profileID := range profileIDs {
		t.Run(profileID, func(t *testing.T) {
			avatar, err := openDefaultWorkforceAvatar(profileID)
			if err != nil {
				t.Fatalf("open default avatar: %v", err)
			}
			if avatar.Info.ContentType != "image/png" {
				t.Fatalf("content type = %q", avatar.Info.ContentType)
			}
			if avatar.Info.Size > 100*1024 {
				t.Fatalf("avatar is not optimized: %d bytes", avatar.Info.Size)
			}
			config, err := png.DecodeConfig(bytes.NewReader(avatar.Bytes))
			if err != nil {
				t.Fatalf("decode PNG: %v", err)
			}
			if config.Width != 512 || config.Height != 512 {
				t.Fatalf("dimensions = %dx%d", config.Width, config.Height)
			}
		})
	}
}

func TestLegacyDefaultWorkforceAvatarIDResolvesCanonicalAsset(t *testing.T) {
	legacy, err := openDefaultWorkforceAvatar("lead-agent")
	if err != nil {
		t.Fatalf("open legacy default avatar: %v", err)
	}
	canonical, err := openDefaultWorkforceAvatar("A000010")
	if err != nil {
		t.Fatalf("open canonical default avatar: %v", err)
	}
	if !bytes.Equal(legacy.Bytes, canonical.Bytes) {
		t.Fatal("legacy default avatar does not resolve to the canonical asset")
	}
}

func TestRetiredLegalAgentAvatarsAreNotDefaultOptions(t *testing.T) {
	for _, profileID := range []string{
		"legal-contract-review-agent",
		"legal-compliance-risk-agent",
		"legal-orchestrator-agent",
		"legal-knowledge-agent",
	} {
		if _, err := openDefaultWorkforceAvatar(profileID); !errors.Is(err, errWorkforceAvatarNotFound) {
			t.Fatalf("retired default avatar %s should not be available: %v", profileID, err)
		}
	}
}

func TestWorkforceAvatarUploadRejectsNonImageContent(t *testing.T) {
	handler := newTestRouter(RouterOptions{
		Operations:       &fakeOperationsService{},
		WorkforceAvatars: NewLocalWorkforceAvatarStore(t.TempDir()),
	})
	req := workforceAvatarUploadRequest(t, "/api/workforce/members/agent-one/avatar", "avatar.png", []byte("not an image"))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkforceAvatarUploadRejectsUnknownProfile(t *testing.T) {
	handler := newTestRouter(RouterOptions{
		Operations:       &fakeOperationsService{},
		WorkforceAvatars: NewLocalWorkforceAvatarStore(t.TempDir()),
		AgentProfiles:    missingWorkforceAvatarProfileDirectory{},
	})
	req := workforceAvatarUploadRequest(t, "/api/workforce/members/missing-agent/avatar", "avatar.png", testWorkforceAvatarPNG(t))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkforceAvatarRoutesHidePersonalAssistantProfile(t *testing.T) {
	store := NewLocalWorkforceAvatarStore(t.TempDir())
	if _, err := store.Save(context.Background(), "private-assistant", "image/png", testWorkforceAvatarPNG(t)); err != nil {
		t.Fatalf("seed avatar: %v", err)
	}
	handler := newTestRouter(RouterOptions{
		Operations:       &fakeOperationsService{},
		WorkforceAvatars: store,
		AgentProfiles:    personalAssistantWorkforceAvatarProfileDirectory{},
	})

	requests := []*http.Request{
		authedRequest(http.MethodGet, "/api/workforce/members/private-assistant/avatar", ""),
		workforceAvatarUploadRequest(t, "/api/workforce/members/private-assistant/avatar", "avatar.png", testWorkforceAvatarPNG(t)),
		authedRequest(http.MethodDelete, "/api/workforce/members/private-assistant/avatar", ""),
	}
	for _, req := range requests {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d body = %s", req.Method, recorder.Code, recorder.Body.String())
		}
	}
}

func TestLocalWorkforceAvatarStoreReplacesPreviousContent(t *testing.T) {
	store := NewLocalWorkforceAvatarStore(t.TempDir())
	first := testWorkforceAvatarPNG(t)
	secondImage := image.NewRGBA(image.Rect(0, 0, 3, 3))
	secondImage.Set(1, 1, color.RGBA{R: 12, G: 220, B: 140, A: 255})
	var second bytes.Buffer
	if err := png.Encode(&second, secondImage); err != nil {
		t.Fatalf("encode second avatar: %v", err)
	}

	if _, err := store.Save(context.Background(), "agent-one", "image/png", first); err != nil {
		t.Fatalf("save first avatar: %v", err)
	}
	if _, err := store.Save(context.Background(), "agent-one", "image/png", second.Bytes()); err != nil {
		t.Fatalf("save second avatar: %v", err)
	}
	stored, err := store.Open(context.Background(), "agent-one")
	if err != nil {
		t.Fatalf("open avatar: %v", err)
	}
	if !bytes.Equal(stored.Bytes, second.Bytes()) {
		t.Fatal("store did not return replacement avatar")
	}
}

func workforceAvatarUploadRequest(t *testing.T, path, fileName string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("create avatar form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write avatar form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close avatar multipart writer: %v", err)
	}
	req := authedHTTPTestRequest(http.MethodPut, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func testWorkforceAvatarPNG(t *testing.T) []byte {
	t.Helper()
	avatar := image.NewRGBA(image.Rect(0, 0, 2, 2))
	avatar.Set(0, 0, color.RGBA{R: 86, G: 84, B: 225, A: 255})
	var content bytes.Buffer
	if err := png.Encode(&content, avatar); err != nil {
		t.Fatalf("encode avatar: %v", err)
	}
	return content.Bytes()
}

type missingWorkforceAvatarProfileDirectory struct{}

func (missingWorkforceAvatarProfileDirectory) GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return agentcore.AgentProfile{}, agentcore.ErrNotFound
}

func (missingWorkforceAvatarProfileDirectory) QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return agentcore.AgentDirectoryPage{}, nil
}

type personalAssistantWorkforceAvatarProfileDirectory struct{}

func (personalAssistantWorkforceAvatarProfileDirectory) GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return agentcore.AgentProfile{ID: "private-assistant", PersonalAssistantOwnerID: "owner-1"}, nil
}

func (personalAssistantWorkforceAvatarProfileDirectory) QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return agentcore.AgentDirectoryPage{}, nil
}

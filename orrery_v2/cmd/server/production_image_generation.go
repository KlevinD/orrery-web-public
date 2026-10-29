package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/discussionsession"
	toolstd "orrery_v2/internal/resource/tool/std"
	httptransport "orrery_v2/internal/transport/http"
)

const maxGeneratedImageBytes = 12 << 20

type imageGenerationSettings interface {
	GetSettings(context.Context) (httptransport.SettingsDTO, error)
}
type imageGenerationAttachments interface {
	ListAttachments(context.Context, discussionsession.ViewerRef, string) (discussionsession.AttachmentPageDTO, error)
	UploadAttachment(context.Context, discussionsession.ViewerRef, discussionsession.UploadAttachmentCommand) (discussionsession.AttachmentDTO, error)
}

type productionImageGenerator struct {
	repo         *discussionsession.Repository
	discussions  imageGenerationAttachments
	settings     imageGenerationSettings
	secrets      httptransport.SettingsSecretResolver
	root         string
	client       *http.Client
	publicOrigin string
}

func imageGenerationFailure(reason string) runtimecore.RuntimeToolCallResult {
	return runtimecore.RuntimeToolCallResult{Output: map[string]any{
		"status": "failed", "tool": "image_generate", "error_code": reason, "retryable": false,
		"error":         "Image generation did not complete. Do not claim an image was generated or delivered.",
		"recovery_hint": "Do not retry automatically or switch providers. A submitted request may have been billed. Explain the failure and ask the user before a new generation.",
	}}
}

func (g *productionImageGenerator) result(a discussionsession.AttachmentDTO) runtimecore.RuntimeToolCallResult {
	// Absolute product URLs survive runtime local-filesystem path redaction.
	a.DownloadURL = strings.TrimRight(g.publicOrigin, "/") + a.DownloadURL
	return runtimecore.RuntimeToolCallResult{Output: map[string]any{
		"status": "completed", "tool": "image_generate", "attachment_id": a.AttachmentID,
		"conversation_id": a.ConversationID, "download_url": a.DownloadURL,
		"markdown":        "![Generated image](" + a.DownloadURL + ")",
		"delivery_status": "stored_private_attachment",
	}}
}

func (g *productionImageGenerator) call(ctx context.Context, call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding, bound bool) runtimecore.RuntimeToolCallResult {
	if g == nil || g.repo == nil || g.discussions == nil || g.settings == nil || g.secrets == nil || g.root == "" {
		return imageGenerationFailure("image_service_unavailable")
	}
	origin, originErr := url.Parse(g.publicOrigin)
	if originErr != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || strings.Trim(origin.Path, "/") != "" {
		return imageGenerationFailure("public_origin_unavailable")
	}
	viewer := runtimeDiscussionPrincipal(call)
	if !bound || binding.ToolID != toolstd.ImageGenerate || !runtimePermissionScopesContain(binding.GrantedPermissions, toolstd.PermissionImageGenerate) || viewer.ViewerKind != discussionsession.ViewerKindUser || viewer.TenantID == "" || viewer.TenantID != call.TenantID {
		return imageGenerationFailure("permission_denied")
	}
	if marker := call.ProviderSpecific[discussionsession.PersonalAssistantReadOnlyReviewProviderKey]; marker == "1" || strings.EqualFold(marker, "true") {
		return imageGenerationFailure("read_only_turn")
	}
	conversationID := strings.TrimSpace(call.ProviderSpecific["discussion_id"])
	conversation, err := g.repo.GetConversation(ctx, conversationID)
	if err != nil || conversation.ConversationKind != discussionsession.ConversationKindPersonalAssistant || conversation.TenantID != viewer.TenantID || conversation.CreatedBy != viewer.ViewerID || conversation.OwnerID == "" || call.AgentID.String() != discussionsession.PersonalAssistantAgentID(conversation.TenantID, conversation.OwnerID) {
		return imageGenerationFailure("private_owner_required")
	}
	run, err := g.repo.GetRun(ctx, call.RunID.String())
	if err != nil || run.ConversationID != conversationID || run.TurnID == "" || firstNonBlank(run.LogicalAgentID, run.AgentID) != call.AgentID.String() || !run.CompletedAt.IsZero() {
		return imageGenerationFailure("invalid_runtime_context")
	}
	prompt, ok := call.Input["prompt"].(string)
	if !ok || strings.TrimSpace(prompt) == "" || len([]rune(prompt)) > 4000 {
		return imageGenerationFailure("invalid_prompt")
	}
	size := "1024x1024"
	if v, exists := call.Input["size"]; exists {
		var valid bool
		size, valid = v.(string)
		if !valid {
			return imageGenerationFailure("invalid_size")
		}
	}
	if size != "1024x1024" && size != "1024x1536" && size != "1536x1024" {
		return imageGenerationFailure("invalid_size")
	}
	for key := range call.Input {
		if key != "prompt" && key != "size" {
			return imageGenerationFailure("invalid_arguments")
		}
	}
	settings, err := g.settings.GetSettings(ctx)
	if err != nil {
		return imageGenerationFailure("settings_unavailable")
	}
	if !settings.ImageGeneration.Enabled {
		return imageGenerationFailure("image_generation_disabled")
	}
	if _, _, valid := httptransport.ValidateImageGenerationSettings(settings.ImageGeneration); !valid {
		return imageGenerationFailure("invalid_configuration")
	}
	token, err := g.secrets.ResolveSecret(ctx, settings.ImageGeneration.APIKeyRef)
	if err != nil || strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return imageGenerationFailure("credential_unavailable")
	}
	page, err := g.discussions.ListAttachments(ctx, viewer, conversationID)
	if err != nil {
		return imageGenerationFailure("private_attachment_access_denied")
	}
	key := sha256.Sum256([]byte(viewer.TenantID + "\x00" + conversationID + "\x00" + run.TurnID))
	id := hex.EncodeToString(key[:])
	fileName := "generated-" + id + ".png"
	for _, a := range page.Attachments {
		if a.FileName == fileName && a.UploadedBy == viewer.ViewerID {
			return g.result(a)
		}
	}
	if os.MkdirAll(g.root, 0700) != nil {
		return imageGenerationFailure("generation_claim_unavailable")
	}
	// An exclusive durable claim survives retries and restarts, including an ambiguous
	// upstream timeout. Never remove it automatically: another POST can incur a charge.
	claim, err := os.OpenFile(filepath.Join(g.root, id+".claim"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return imageGenerationFailure("generation_already_attempted")
	}
	if err := claim.Sync(); err != nil {
		claim.Close()
		return imageGenerationFailure("generation_claim_unavailable")
	}
	claim.Close()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	data, err := generateImage(ctx, settings.ImageGeneration, token, prompt, size, g.client)
	if err != nil {
		return imageGenerationFailure(err.Error())
	}
	a, err := g.discussions.UploadAttachment(ctx, viewer, discussionsession.UploadAttachmentCommand{ConversationID: conversationID, FileName: fileName, ContentType: "image/png", SizeBytes: int64(len(data)), Reader: bytes.NewReader(data), IdempotencyKey: "image-generation-" + id})
	if err != nil {
		return imageGenerationFailure("generated_image_storage_failed")
	}
	return g.result(a)
}

func generateImage(ctx context.Context, settings httptransport.SettingsImageGenerationDTO, token, prompt, size string, client *http.Client) ([]byte, error) {
	if _, _, valid := httptransport.ValidateImageGenerationSettings(settings); !valid {
		return nil, errors.New("invalid_configuration")
	}
	body, _ := json.Marshal(map[string]any{"model": settings.Model, "prompt": prompt, "n": 1, "size": size, "output_format": "png", "quality": "high"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(settings.BaseURL, "/")+"/images/generations", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid_configuration")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = httptransport.NewImageGenerationClient()
		defer client.CloseIdleConnections()
	}
	copy := *client
	copy.Jar = nil
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := copy.Do(req)
	if err != nil {
		return nil, errors.New("generation_outcome_unknown")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("generation_http_%d", resp.StatusCode)
	}
	const maxResponse = (maxGeneratedImageBytes * 4 / 3) + (64 << 10)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return nil, errors.New("invalid_generation_response")
	}
	var response struct {
		Data []struct {
			Base64 string `json:"b64_json"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Data) != 1 || response.Data[0].Base64 == "" {
		return nil, errors.New("invalid_generation_response")
	}
	data, err := base64.StdEncoding.DecodeString(response.Data[0].Base64)
	if err != nil || len(data) > maxGeneratedImageBytes {
		return nil, errors.New("invalid_generated_image")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "png" || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 {
		return nil, errors.New("invalid_generated_image")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, errors.New("invalid_generated_image")
	}
	return data, nil
}

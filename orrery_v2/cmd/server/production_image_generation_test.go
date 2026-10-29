package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform/testkit"
	toolstd "orrery_v2/internal/resource/tool/std"
	httptransport "orrery_v2/internal/transport/http"
)

type imageTransport func(*http.Request) (*http.Response, error)

func (f imageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type imageTestSettings struct{ enabled bool }

func (s imageTestSettings) GetSettings(context.Context) (httptransport.SettingsDTO, error) {
	return httptransport.SettingsDTO{ImageGeneration: httptransport.SettingsImageGenerationDTO{Enabled: s.enabled, Protocol: "openai-compatible", BaseURL: "https://gateway.example/v1", Model: "gpt-image-2", APIKeyRef: "secret://image/default"}}, nil
}

type imageTestSecrets struct{}

func (imageTestSecrets) ResolveSecret(context.Context, string) (string, error) {
	return "test-secret", nil
}

func generatedTestPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestGenerateImageProtocolAndFailures(t *testing.T) {
	settings, _ := (imageTestSettings{true}).GetSettings(context.Background())
	valid, _ := json.Marshal(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(generatedTestPNG(t))}}})
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		success bool
	}{
		{"success", 200, string(valid), true}, {"provider_failure", 500, `{"error":"test-secret"}`, false},
		{"url_only", 200, `{"data":[{"url":"http://127.0.0.1/private"}]}`, false},
		{"bad_base64", 200, `{"data":[{"b64_json":"invalid"}]}`, false},
		{"empty", 200, `{"data":[]}`, false}, {"redirect", 302, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "POST" || r.URL.String() != "https://gateway.example/v1/images/generations" || r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Error("incorrect image request")
				}
				var payload map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["model"] != "gpt-image-2" || payload["n"] != float64(1) || payload["prompt"] != "a green square" {
					t.Error("incorrect image payload")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": []string{"https://other.example"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			_, err := generateImage(context.Background(), settings.ImageGeneration, "test-secret", "a green square", "1024x1024", client)
			if (err == nil) != tc.success || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
			if err != nil && strings.Contains(err.Error(), "test-secret") {
				t.Fatal("leaked provider body")
			}
		})
	}
}

func TestPersonalAssistantImageAuthorizationAndDurableDeduplication(t *testing.T) {
	for _, variant := range []string{"success", "wrong_owner", "wrong_tenant", "wrong_agent", "group", "unbound", "missing_scope", "read_only", "disabled", "invalid_run", "unknown_argument", "upstream_failure"} {
		t.Run(variant, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			repo := discussionsession.NewRepository(env.DBRouter)
			agentID := discussionsession.PersonalAssistantAgentID("tenant-image", "owner-image")
			conversation := discussionsession.ConversationRecord{ConversationID: "image-conversation", TenantID: "tenant-image", OwnerID: "owner-image", CreatedBy: "human-image", ConversationKind: discussionsession.ConversationKindPersonalAssistant}
			if variant == "group" {
				conversation.ConversationKind = "group"
			}
			if err := repo.SaveConversation(ctx, conversation); err != nil {
				t.Fatal(err)
			}
			if err := repo.SaveRun(ctx, discussionsession.RunRecord{RunID: "image-run", ConversationID: conversation.ConversationID, TurnID: "image-turn", AgentID: agentID, LogicalAgentID: agentID, Status: "running"}); err != nil {
				t.Fatal(err)
			}
			service := discussionsession.NewService(discussionsession.ServiceOptions{Repository: repo, AttachmentStore: discussionsession.NewLocalAttachmentStore(t.TempDir())})
			requests := 0
			client := &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				status := 200
				payload := `{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(generatedTestPNG(t)) + `"}]}`
				if variant == "upstream_failure" {
					status = 500
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(payload))}, nil
			})}
			g := &productionImageGenerator{repo: repo, discussions: service, settings: imageTestSettings{variant != "disabled"}, secrets: imageTestSecrets{}, root: t.TempDir(), client: client, publicOrigin: "https://orrery.example"}
			call := runtimecore.RuntimeToolCallRequest{TenantID: "tenant-image", AgentID: agentcore.AgentID(agentID), RunID: "image-run", Name: "image_generate", CallID: "image-call", ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "human-image", TenantID: "tenant-image"}, ProviderSpecific: map[string]string{"discussion_id": conversation.ConversationID}, Input: map[string]any{"prompt": "a green square"}}
			binding := agentcore.RuntimeToolBinding{ToolID: toolstd.ImageGenerate, GrantedPermissions: []string{toolstd.PermissionImageGenerate}}
			switch variant {
			case "wrong_owner":
				call.ExecutionPrincipal.ID = "someone-else"
			case "wrong_tenant":
				call.TenantID = "other"
			case "wrong_agent":
				call.AgentID = "other"
			case "missing_scope":
				binding.GrantedPermissions = nil
			case "read_only":
				call.ProviderSpecific[discussionsession.PersonalAssistantReadOnlyReviewProviderKey] = "true"
			case "invalid_run":
				call.RunID = "absent"
			case "unknown_argument":
				call.Input["base_url"] = "https://other.example"
			}
			result := g.call(ctx, call, binding, variant != "unbound")
			if variant == "success" {
				if link, _ := result.Output["download_url"].(string); !strings.HasPrefix(link, "https://orrery.example/api/discussions/") {
					t.Fatalf("image link is not an absolute product URL: %s", link)
				}
				if result.Output["status"] != "completed" {
					t.Fatalf("result=%v", result.Output)
				}
				if _, err := service.DownloadAttachment(ctx, discussionsession.ViewerRef{ViewerID: "someone-else", TenantID: "tenant-image", ViewerKind: discussionsession.ViewerKindUser}, conversation.ConversationID, result.Output["attachment_id"].(string)); err == nil {
					t.Fatal("another user downloaded image")
				}
				restarted := *g
				again := restarted.call(ctx, call, binding, true)
				if again.Output["attachment_id"] != result.Output["attachment_id"] || requests != 1 {
					t.Fatalf("duplicate generation: %v requests=%d", again.Output, requests)
				}
			} else {
				if result.Output["status"] != "failed" || result.Output["retryable"] != false {
					t.Fatalf("result=%v", result.Output)
				}
				if variant == "upstream_failure" {
					restarted := *g
					again := restarted.call(ctx, call, binding, true)
					if requests != 1 || again.Output["error_code"] != "generation_already_attempted" {
						t.Fatal("retried ambiguous generation")
					}
				} else if requests != 0 {
					t.Fatal("unauthorized provider request")
				}
			}
		})
	}
}

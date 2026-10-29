package einomodel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	llmprofile "orrery_v2/internal/agent/runtime/llmprofile"
	profilemodelruntime "orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/app/improvement"
)

func TestGeneratorUsesConfiguredModelAndStrictCandidateProtocol(t *testing.T) {
	for name, response := range map[string]string{
		"valid":           `{"diagnosis":"fix general behavior","edits":[{"target":"tool:selector","content":"new source"}]}`,
		"self evaluation": `{"diagnosis":"fix","edits":[],"passed":true}`,
		"trailing object": `{"diagnosis":"fix","edits":[]} {"passed":true}`,
		"markdown":        "```json\n{}\n```",
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-key" || r.URL.Path != "/v1/chat/completions" {
					t.Error("configured endpoint/credentials were not used")
				}
				var body struct {
					Model    string `json:"model"`
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Model != "frozen-model" || len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "repair_failures") {
					t.Error("model request does not bind the admitted identity/evidence")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": response}}}})
			}))
			defer server.Close()
			models := profilemodelruntime.NewProfileModelFactory(profilemodelruntime.ProfileModelFactoryConfig{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "frozen-profile", Model: "frozen-model", BaseURL: server.URL, APIKey: "test-key", Enabled: true}}}})
			g := &Generator{Models: models, Descriptor: improvement.GeneratorIdentity{ProfileID: "frozen-profile", Model: "frozen-model", Endpoint: server.URL, Version: PromptVersion, PromptSHA256: PromptSHA256()}}
			proposal, err := g.Generate(context.Background(), improvement.GenerationInput{Summary: "task failure"})
			if name == "valid" {
				if err != nil || len(proposal.Edits) != 1 {
					t.Fatalf("valid candidate rejected: %+v %v", proposal, err)
				}
			} else if !errors.Is(err, improvement.ErrInvalid) {
				t.Fatalf("model-owned authority accepted: %v", err)
			}
		})
	}
}

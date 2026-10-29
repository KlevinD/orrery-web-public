package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/decision"
)

func question() decision.Question {
	return decision.Question{Type: "choice", Instructions: "Classify", Criteria: map[string]string{"a": "relevant", "b": "unrelated"}}
}

func TestAdapterUsesSelectedProfileAndBothProviderProtocols(t *testing.T) {
	for _, provider := range []string{"custom_openai", "custom-openai-responses"} {
		t.Run(provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer selected-key" {
					t.Error("wrong credential")
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["model"] != "selected-model" {
					t.Errorf("wrong model %v", body)
				}
				content := `{"decisions":[{"choice":"a","confidence":0.8,"answer_confidence":0.7}]}`
				if provider == "custom_openai" {
					if r.URL.Path != "/v1/chat/completions" {
						t.Error(r.URL.Path)
					}
					json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content}}}})
				} else {
					if r.URL.Path != "/v1/responses" {
						t.Error(r.URL.Path)
					}
					json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": content}}}}})
				}
			}))
			defer server.Close()
			profiles := &llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "selected", Provider: provider, BaseURL: server.URL + "/v1", APIKeyRef: "secret://selected", Model: "selected-model", Enabled: true, GlobalDefault: true}}}
			a := Adapter{Profiles: profiles, Secrets: llmprofile.StaticSecretResolver{Values: map[string]string{"secret://selected": "selected-key"}}}
			r, err := a.Resolve(context.Background(), decision.Profile{LLMProfileID: "platform-default"})
			if err != nil {
				t.Fatal(err)
			}
			got, err := r.Judge.Choose(context.Background(), []string{"evidence"}, question())
			if err != nil || len(got) != 1 || got[0].Choice != "a" {
				t.Fatalf("%+v %v", got, err)
			}
			if r.Metadata.Model != "selected-model" || r.Metadata.LLMProfileID != "selected" || r.Metadata.ConfidenceKind != "self_reported" {
				t.Fatalf("metadata %+v", r.Metadata)
			}
			profiles.Profiles[0].Model = "next-model"
			next, err := a.Resolve(context.Background(), decision.Profile{})
			if err != nil || next.Metadata.Model != "next-model" || next.Metadata.CacheKey == r.Metadata.CacheKey {
				t.Fatal("default model was copied or cached")
			}
			if _, err := a.Resolve(context.Background(), decision.Profile{LLMProfileID: "missing"}); err == nil {
				t.Fatal("missing explicit profile fell back")
			}
		})
	}
}

func TestLLMResultsRequireCompleteBoundedChoiceContract(t *testing.T) {
	for _, text := range []string{`{"decisions":[{"choice":"a"}]}`, `{"decisions":[{"choice":"unknown","confidence":0.5,"answer_confidence":0.5}]}`, `{"decisions":[{"choice":"a","confidence":1.1,"answer_confidence":0.5}]}`, `{"decisions":[]}`, `{"decisions":[{"choice":"a","confidence":0.5,"answer_confidence":0.5}]} trailing`, `{"decisions":[{"choice":"a","confidence":0.5,"answer_confidence":0.5}],"execute":"do something"}`} {
		if _, err := parse(text, 1, question()); !errors.Is(err, decision.ErrResponse) {
			t.Fatalf("accepted %s", text)
		}
	}
}

func TestAdapterNeverFollowsCredentialRedirectsOrExposesProviderErrors(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		io.WriteString(w, "PRIVATE_PROVIDER_BODY")
	}))
	defer server.Close()
	a := Adapter{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "p", Provider: "custom_openai", BaseURL: server.URL, APIKey: "private-key", Model: "m", Enabled: true}}}}
	r, err := a.Resolve(context.Background(), decision.Profile{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Judge.Choose(context.Background(), []string{"evidence"}, question())
	if err == nil || strings.Contains(err.Error(), "PRIVATE") || called {
		t.Fatalf("unsafe redirect/error %v called=%v", err, called)
	}
}

package agentkernel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/agent/runtime/session"
)

func TestResponsesUsageSurvivesKernelEventProjectionAndCheckpoint(t *testing.T) {
	for _, details := range []string{"", `,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}`, `,"input_tokens_details":{"cached_tokens":800},"output_tokens_details":{"reasoning_tokens":40}`} {
		t.Run(details, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":1000,"output_tokens":100,"total_tokens":1100%s}}`, details)
			}))
			defer server.Close()
			factory := profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{Enabled: true, Provider: "custom-openai-responses", BaseURL: server.URL + "/v1", APIKey: "fixture"}}}})
			chat, err := factory.ModelForRequest(context.Background(), profilemodel.ModelRequest{ManagedContext: true, StreamResponses: true})
			if err != nil {
				t.Fatal(err)
			}
			runner, input := openRunner(t, chat, func(config *Config, _ *agentcore.RuntimeRequest) { checkpointConfig(t, config) })
			projection := session.NewResponseProjection(agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{Model: "test"}}, agentcore.RuntimeKindManaged, time.Now())
			_, err = runner.Run(context.Background(), input, func(event agentcore.SessionEvent) error {
				if event.Kind == agentcore.EventUsageDelta {
					return projection.Fold(agentcore.SessionEnvelope{Event: event})
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			u := projection.Response(time.Now()).Usage["test"]
			if u.CachedInputTokensKnown != (details != "") || u.ReasoningTokensKnown != (details != "") || u.InputTokens != 1000 || u.OutputTokens != 100 || u.CacheReadTokens != 0 {
				t.Fatalf("incorrect usage: %+v", u)
			}
			if u.CachedInputTokens != int64(runner.checkpoint.Usage.CachedInputTokens) || u.ReasoningTokens != int64(runner.checkpoint.Usage.ReasoningTokens) {
				t.Fatal("event and checkpoint accounting disagree")
			}
			if len(details) > 0 && details != `,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}` && (u.CachedInputTokens != 800 || u.ReasoningTokens != 40) {
				t.Fatal("cached/reasoning subset lost")
			}
		})
	}
}

package rsillm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/app/resourceadmin"
	decisionllm "orrery_v2/internal/decision/llm"
)

func TestDiagnoserSendsFullEvidenceAndRejectsInvalidOrActionOutput(t *testing.T) {
	for _, protocol := range []string{"custom_openai", "custom_openai_responses"} {
		for _, content := range []string{`{"classification":"skill_defect","reason":"Missing mapping"}`, `{"classification":"skill_defect","reason":"Missing mapping","publish":true}`, `{"classification":"environment","reason":"x"} {}`} {
			t.Run(protocol+content, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					raw, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(raw), "COMPLETE_EVIDENCE_TAIL") || r.Header.Get("Authorization") != "Bearer key" {
						t.Error("full evidence/auth missing")
					}
					if strings.Contains(protocol, "responses") {
						json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": content}}}}})
					} else {
						json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content}}}})
					}
				}))
				defer server.Close()
				d := Diagnoser{Adapter: decisionllm.Adapter{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "main", Enabled: true, GlobalDefault: true, Provider: protocol, Model: "model", BaseURL: server.URL + "/v1", APIKey: "key"}}}}}
				result, metadata, err := d.Diagnose(context.Background(), resourceadmin.RSILearningSample{Evidence: json.RawMessage(`{"source":"` + strings.Repeat("x", 1000) + `COMPLETE_EVIDENCE_TAIL"}`)})
				if content == `{"classification":"skill_defect","reason":"Missing mapping"}` {
					if err != nil || result.Classification != "skill_defect" || metadata.LLMProfileID != "main" || metadata.CacheKey != "" {
						t.Fatalf("result: %+v %v", result, err)
					}
				} else if err == nil {
					t.Fatal("invalid model response accepted")
				}
				if calls != 1 {
					t.Fatal("unexpected model retry")
				}
			})
		}
	}
}

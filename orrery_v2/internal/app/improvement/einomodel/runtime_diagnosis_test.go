package einomodel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/app/improvement"
)

func TestRuntimeDiagnoserStrictResponseAndHostModelIdentity(t *testing.T) {
	for _, answer := range []string{
		`{"summary":"运行终态没有业务验收，需要核对同次结果。","evidence_ids":["offered"],"affected_kinds":["workflow"]}`,
		`{"summary":"invented","evidence_ids":["offered"],"affected_kinds":["workflow"],"release":true}`,
		"```json\n{}\n```", `{} {}`, `{"diagnosis_case_id":"authority"}`,
	} {
		t.Run(answer, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["model"] != "frozen-model" {
					t.Error("wrong model")
				}
				raw, _ := json.Marshal(body)
				if !strings.Contains(string(raw), "supplied host-captured metadata") || !strings.Contains(string(raw), "offered") {
					t.Error("missing grounded input")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": answer}}}})
			}))
			defer server.Close()
			d := &RuntimeDiagnoser{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "selected", Model: "frozen-model", BaseURL: server.URL, APIKey: "test-key", Enabled: true}}}, ProfileID: "selected"}
			got, err := d.DiagnoseRuntime(context.Background(), improvement.RuntimeDiagnosisInput{Protocol: improvement.RuntimeDiagnosisProtocol, EvidenceIDs: []string{"offered"}})
			if strings.Contains(answer, "运行终态") {
				if err != nil || got.ProfileID != "selected" || got.Model != "frozen-model" || got.PromptSHA256 != improvement.Digest(runtimeDiagnosisInstruction) || got.Diagnosis.EvidenceIDs[0] != "offered" {
					t.Fatal(got, err)
				}
			} else if !errors.Is(err, improvement.ErrInvalid) {
				t.Fatal("non-strict model response accepted", err)
			}
			if calls != 1 {
				t.Fatal("model called more than once")
			}
		})
	}
}

func TestRuntimeDiagnoserExplicitProfileNeverFallsBack(t *testing.T) {
	d := &RuntimeDiagnoser{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "other", Model: "other-model", Enabled: true, BaseURL: "http://127.0.0.1:1"}}}, ProfileID: "missing"}
	if _, err := d.DiagnoseRuntime(context.Background(), improvement.RuntimeDiagnosisInput{}); !errors.Is(err, improvement.ErrUnavailable) {
		t.Fatal("explicit missing selection fell back", err)
	}
}

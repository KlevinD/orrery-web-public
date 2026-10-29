package website

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/gateway/protocol"
)

func TestNewWebsiteDistillationCandidateSanitizesPublicContent(t *testing.T) {
	candidate, err := NewWebsiteDistillationCandidate(WebsiteDistillationCandidateInput{
		ConversationID: "conversation-public-1",
		Question:       "请联系 visitor@example.com 或 13800138000，介绍 Orrery 私有部署",
		Answer:         "Orrery 支持私有化和本地部署。",
		Locale:         "zh-CN",
		PagePath:       "/zh-CN/products?campaign=secret",
		Tier:           protocol.AdvisorTierPublic,
		SourceIDs:      []string{overviewSource, "https://invalid.example/private", overviewSource},
		SourceRevision: "public-rev-1",
		ModelRevision:  "minimax-m3",
		PromptRevision: "max-public-v1",
		OccurredAt:     time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(candidate)
	for _, forbidden := range []string{"visitor@example.com", "13800138000", "conversation-public-1", "campaign=secret", "invalid.example"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("candidate leaked %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(candidate.Question, "[redacted]") || candidate.PageCategory != "/zh-CN/products" {
		t.Fatalf("sanitized candidate = %+v", candidate)
	}
	if len(candidate.SourceIDs) != 1 || candidate.SourceIDs[0] != overviewSource || candidate.ClusterID == "" || candidate.CandidateID == "" {
		t.Fatalf("candidate identity/sources = %+v", candidate)
	}
}

func TestNewWebsiteDistillationCandidateRejectsUnsafeOrNonPublicContent(t *testing.T) {
	base := WebsiteDistillationCandidateInput{
		ConversationID: "conversation-public-1", Question: "Orrery 如何部署？", Answer: "使用已审核的部署方式。",
		Locale: "zh-CN", Tier: protocol.AdvisorTierPublic, SourceIDs: []string{overviewSource}, OccurredAt: time.Now(),
	}
	for name, mutate := range map[string]func(*WebsiteDistillationCandidateInput){
		"credential": func(input *WebsiteDistillationCandidateInput) { input.Question = "api-key=example-value" },
		"private ip": func(input *WebsiteDistillationCandidateInput) { input.Answer = "连接 192.168.1.20" },
		"customer":   func(input *WebsiteDistillationCandidateInput) { input.Tier = protocol.AdvisorTierCustomer },
		"no source":  func(input *WebsiteDistillationCandidateInput) { input.SourceIDs = nil },
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			mutate(&input)
			if _, err := NewWebsiteDistillationCandidate(input); err == nil {
				t.Fatalf("unsafe candidate accepted: %+v", input)
			}
		})
	}
}

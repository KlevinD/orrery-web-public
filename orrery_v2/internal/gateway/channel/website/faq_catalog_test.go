package website

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeWebsiteFAQRevisionStore struct {
	revisions []WebsiteFAQRevision
	err       error
}

func (f fakeWebsiteFAQRevisionStore) ListPublishedFAQRevisions(context.Context) ([]WebsiteFAQRevision, error) {
	return f.revisions, f.err
}

func TestFAQCatalogReturnsSixDiversePublicSuggestions(t *testing.T) {
	catalog := NewWebsiteFAQCatalog(nil)
	items := catalog.Suggestions("zh-CN", 6)
	if len(items) != 6 {
		t.Fatalf("suggestions = %d, want 6", len(items))
	}
	wantIDs := []string{"orrery", "suitable-scenarios", "integration", "deployment", "permissions-approvals", "demo"}
	for index, wantID := range wantIDs {
		if items[index].ID != wantID || strings.TrimSpace(items[index].Question) == "" {
			t.Fatalf("suggestion[%d] = %+v", index, items[index])
		}
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"minviabrain://", "patterns", "sources", "answer", "text"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("suggestions leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestFAQCatalogReturnsLocalizedSuggestions(t *testing.T) {
	items := NewWebsiteFAQCatalog(nil).Suggestions("en-US", 6)
	if len(items) != 6 || items[0].ID != "orrery" || items[0].Question != "what is orrery" {
		t.Fatalf("english suggestions = %+v", items)
	}
}

func TestFAQCatalogReloadsValidPublishedRevision(t *testing.T) {
	catalog := NewWebsiteFAQCatalog(nil)
	revision := WebsiteFAQRevision{
		RevisionHash:       "published-hash-1",
		LogicalIntentID:    "orrery",
		SuggestionEligible: true,
		SuggestionRank:     1,
		Entries: []WebsiteFAQRevisionEntry{
			{Locale: "zh-CN", CanonicalQuestion: "Orrery 可以做什么", Exact: []string{"orrery 可以做什么"}, Answer: "已审核的新回答。", Sources: []string{overviewSource}, Class: standardAnswerFactual},
			{Locale: "en-US", CanonicalQuestion: "what can orrery do", Exact: []string{"what can orrery do"}, Answer: "Reviewed new answer.", Sources: []string{overviewSource}, Class: standardAnswerFactual},
		},
	}
	if err := catalog.Reload(context.Background(), fakeWebsiteFAQRevisionStore{revisions: []WebsiteFAQRevision{revision}}); err != nil {
		t.Fatal(err)
	}
	answer, ok := catalog.Match("Orrery 可以做什么？", "zh-CN")
	if !ok || answer.Text != "已审核的新回答。" {
		t.Fatalf("answer = %+v matched=%v", answer, ok)
	}
	if catalog.Revision() != "published-hash-1" {
		t.Fatalf("revision = %q", catalog.Revision())
	}
	if items := catalog.Suggestions("zh-CN", 1); len(items) != 1 || items[0].Question != "Orrery 可以做什么" {
		t.Fatalf("suggestions = %+v", items)
	}
}

func TestFAQCatalogKeepsLastValidSnapshotWhenReloadFails(t *testing.T) {
	catalog := NewWebsiteFAQCatalog(nil)
	before := catalog.Revision()
	invalid := WebsiteFAQRevision{
		RevisionHash:       "invalid-hash",
		LogicalIntentID:    "orrery",
		SuggestionEligible: true,
		Entries: []WebsiteFAQRevisionEntry{
			{Locale: "zh-CN", CanonicalQuestion: "Orrery", Patterns: []string{"orrery"}, Answer: "invalid", Sources: []string{overviewSource}, Class: standardAnswerFactual},
			{Locale: "en-US", CanonicalQuestion: "Orrery", Answer: "invalid", Sources: []string{overviewSource}, Class: standardAnswerFactual},
		},
	}
	if err := catalog.Reload(context.Background(), fakeWebsiteFAQRevisionStore{revisions: []WebsiteFAQRevision{invalid}}); err == nil {
		t.Fatal("invalid overlay was accepted")
	}
	if catalog.Revision() != before {
		t.Fatalf("revision changed from %q to %q", before, catalog.Revision())
	}
	if answer, ok := catalog.Match("Orrery 是什么？", "zh-CN"); !ok || answer.IntentID != "orrery.zh-CN" {
		t.Fatalf("baseline unavailable after failed reload: %+v matched=%v", answer, ok)
	}
}

func TestFAQCatalogKeepsSnapshotWhenStoreFails(t *testing.T) {
	catalog := NewWebsiteFAQCatalog(nil)
	before := catalog.Revision()
	if err := catalog.Reload(context.Background(), fakeWebsiteFAQRevisionStore{err: errors.New("store unavailable")}); err == nil {
		t.Fatal("store failure was ignored")
	}
	if catalog.Revision() != before {
		t.Fatalf("revision changed from %q to %q", before, catalog.Revision())
	}
}

func TestFAQSuggestionRouteIsPublicAndReturnsNoAnswerData(t *testing.T) {
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"},
		FAQCatalog:     NewWebsiteFAQCatalog(nil),
	})
	request := httptest.NewRequest(http.MethodGet, "/api/advisor/faq-suggestions?language=zh-CN", nil)
	request.Header.Set("Origin", "https://www.minvia.cn")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	var payload struct {
		Items    []WebsiteFAQSuggestion `json:"items"`
		Revision string                 `json:"revision"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 6 || payload.Revision == "" {
		t.Fatalf("payload = %+v", payload)
	}
	for _, forbidden := range []string{"minviabrain://", "patterns", "sources", "answer"} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}
}

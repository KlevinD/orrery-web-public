package responsibility

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPPeopleResponsibilitySourceResolvesPeopleResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v2/subjects/subject-1/responsibilities" || request.Header.Get("Authorization") != "Bearer token-1" {
			t.Fatalf("request path=%q authorization=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"subject_id":"subject-1","items":[{"id":"responsibility-1","code":"DELIVERY","title":"Deliver","description":"Ship it","valid_from":"2026-08-01T00:00:00Z","valid_to":null,"revision":2,"sources":[{"binding_id":"binding-1","id":"binding-1","responsibility_id":"responsibility-1","target_type":"position","target_id":"position-1","valid_from":"2026-08-01T00:00:00Z","valid_to":null,"revision":3}]}],"as_of":"2026-08-08T00:00:00Z","source_revision":"5:2"}`))
	}))
	defer server.Close()

	source, err := NewHTTPPeopleResponsibilitySource(server.URL, "token-1", server.Client())
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	resolution, err := source.ResolveResponsibilities(context.Background(), "subject-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolution.SubjectID != "subject-1" || resolution.SourceRevision != "5:2" || len(resolution.Items) != 1 || len(resolution.Items[0].Sources) != 1 {
		t.Fatalf("resolution = %+v", resolution)
	}
	if resolution.Items[0].Sources[0].TargetType != "position" {
		t.Fatalf("source binding = %+v", resolution.Items[0].Sources[0])
	}
}

func TestHTTPPeopleResponsibilitySourceRejectsMismatchedSubject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"subject_id":"other","items":[],"as_of":"2026-08-08T00:00:00Z","source_revision":"5:2"}`))
	}))
	defer server.Close()

	source, err := NewHTTPPeopleResponsibilitySource(server.URL, "token-1", server.Client())
	if err != nil {
		t.Fatalf("new source: %v", err)
	}
	if _, err := source.ResolveResponsibilities(context.Background(), "subject-1"); err == nil {
		t.Fatal("resolve succeeded with a mismatched subject")
	}
}

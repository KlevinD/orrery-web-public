package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCandidateTransportUsesIdentifiedClientAndBoundsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.UserAgent(), "orrery-") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.Copy(w, strings.NewReader(strings.Repeat("x", 3<<20)))
	}))
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: candidateTransport{base: http.DefaultTransport}}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || len(body) != 2<<20 || request.Header.Get("User-Agent") != "" {
		t.Fatalf("model transport contract failed: status=%d bytes=%d error=%v", response.StatusCode, len(body), err)
	}
}

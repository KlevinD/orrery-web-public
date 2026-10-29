package laya

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testQuestion() Question {
	return Question{Type: "choice", Instructions: "Choose.", Criteria: map[string]string{"a": "one", "b": "two"}}
}

func TestClientContractAndFailureBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
		wantErr        bool
	}{
		{"valid", `{"results":[{"answers":{"judgment":{"type":"choice","choice":"a","confidence":0.7,"answer_confidence":0.8,"probabilities":{"a":0.8,"b":0.2}}}}]}`, 200, false},
		{"unknown_choice", `{"results":[{"answers":{"judgment":{"type":"choice","choice":"c"}}}]}`, 200, true},
		{"missing_result", `{"results":[]}`, 200, true},
		{"invalid_probability", `{"results":[{"answers":{"judgment":{"type":"choice","choice":"a","confidence":1.4,"probabilities":{"a":0.8,"b":0.2}}}}]}`, 200, true},
		{"auth_failure", `private-credential-detail`, 401, true},
		{"server_failure", `private-customer-detail`, 503, true},
		{"oversized", strings.Repeat("x", 65537), 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/systemone/batch" || r.Header.Get("Authorization") != "Bearer local-test-key" {
					t.Error("wrong API contract")
				}
				var body struct {
					Model     string
					States    []string
					BatchSize int `json:"batch_size"`
					Questions map[string]Question
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "multilingual" || body.BatchSize != 2 || len(body.States) != 1 || body.Questions["judgment"].Type != "choice" {
					t.Error("incorrect multilingual batch payload")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer srv.Close()
			c, err := NewClient(srv.URL, "local-test-key", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			result, err := c.Choose(context.Background(), []string{"hello"}, testQuestion())
			if (err != nil) != tc.wantErr {
				t.Fatalf("result %v, err %v", result, err)
			}
			if err != nil && (strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "local-test-key")) {
				t.Fatal("sensitive error detail escaped")
			}
		})
	}
}

func TestClientRejectsOversizeRedirectAndTimeout(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/v1/systemone/batch" {
			http.Redirect(w, r, "/capture", http.StatusTemporaryRedirect)
		}
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "test-key", time.Second)
	defer c.Close()
	if _, err := c.Choose(context.Background(), []string{strings.Repeat("x", MaxRequestBytes)}, testQuestion()); !errors.Is(err, ErrBudget) || calls.Load() != 0 {
		t.Fatal("oversized input reached model")
	}
	if _, err := c.Choose(context.Background(), []string{"hello"}, testQuestion()); err == nil || calls.Load() != 1 {
		t.Fatal("followed credential-bearing redirect")
	}
	for _, endpoint := range []string{"http://example.com", "https://127.0.0.1", "http://user:pass@127.0.0.1", "http://127.0.0.1/?key=x"} {
		if _, err := NewClient(endpoint, "key", time.Second); err == nil {
			t.Fatalf("accepted endpoint %s", endpoint)
		}
	}
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer slow.Close()
	defer close(release)
	short, _ := NewClient(slow.URL, "key", 20*time.Millisecond)
	defer short.Close()
	started := time.Now()
	if _, err := short.Choose(context.Background(), []string{"hello"}, testQuestion()); err == nil || time.Since(started) > time.Second {
		t.Fatal("timeout did not bound inference")
	}
}

func TestReadKeyFile(t *testing.T) {
	for _, content := range []string{"plain-key\n", "LAYA_API_KEY=env-key\nLAYA_THREADS=2\n"} {
		path := filepath.Join(t.TempDir(), "key")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		key, err := ReadKeyFile(path)
		if err != nil || !strings.HasSuffix(key, "key") {
			t.Fatalf("key parse failed: %v", err)
		}
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/platform/db"
)

func TestRSISamplesCLIReadsScopedEvidenceAndRetriesFailedClassification(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbURL := "file://" + filepath.ToSlash(filepath.Join(root, "rsi.db"))
	core, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: dbURL})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	samples := resourceadmin.NewPlatformRSILearningSampleStore(core)
	scope, _ := resourceadmin.RSILearningScope("tenant", "agent")
	sample := resourceadmin.RSILearningSample{Scope: scope, EvidenceRef: "run:call", Tool: "query_order", ErrorCode: "invalid_arguments", Summary: "Required order_id was omitted. " + strings.Repeat("x", 300) + "PRIVATE_SUMMARY_TAIL", Evidence: json.RawMessage(`{"output":{"private":"FULL_PRIVATE_EVIDENCE"}}`)}
	observation, err := samples.Save(ctx, sample)
	if err != nil {
		t.Fatal(err)
	}
	base := []string{"rsi-samples", "--db", dbURL, "--tenant", "tenant", "--agent", "agent"}
	var out bytes.Buffer
	if err := run(ctx, base, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"status":"pending"`) || strings.Contains(out.String(), "PRIVATE") {
		t.Fatalf("default output leaked evidence: %s", &out)
	}
	out.Reset()
	if err := run(ctx, append(append([]string{}, base...), "--id", observation.ID, "--include-evidence"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "FULL_PRIVATE_EVIDENCE") || !strings.Contains(out.String(), "PRIVATE_SUMMARY_TAIL") {
		t.Fatal("explicit evidence read omitted original data")
	}
	out.Reset()
	wrongScope := append([]string{}, base...)
	wrongScope[len(wrongScope)-1] = "other-agent"
	if err := run(ctx, append(wrongScope, "--id", observation.ID), &out); !errors.Is(err, resourceadmin.ErrNotFound) {
		t.Fatalf("cross-agent access: %v", err)
	}
	keyFile := filepath.Join(root, "key")
	if err := os.WriteFile(keyFile, []byte("test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var badRequest atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/systemone/batch" || r.Header.Get("Authorization") != "Bearer test-key" || bytes.Contains(body, []byte("PRIVATE")) {
			badRequest.Store(true)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"results":[{"answers":{"judgment":{"type":"choice","choice":"tool_usage","confidence":0.5,"answer_confidence":0.8,"probabilities":{"tool_usage":0.8,"environment":0.05,"skill_defect":0.05,"insufficient":0.1}}}}]}`)
	}))
	defer server.Close()
	retry := append(append([]string{}, base...), "--id", observation.ID, "--retry", "--key-file", keyFile, "--url", server.URL)
	out.Reset()
	if err := run(ctx, retry, &out); err == nil || !strings.Contains(out.String(), `"status":"fallback"`) {
		t.Fatalf("failed attempt not retained: %s %v", &out, err)
	}
	out.Reset()
	if err := run(ctx, retry, &out); err != nil {
		t.Fatal(err)
	}
	if badRequest.Load() || !strings.Contains(out.String(), `"status":"assessed"`) {
		t.Fatalf("retry or model input: %s", &out)
	}
	got, err := samples.Get(ctx, scope, observation.ID)
	if err != nil || got.Assessment.Decisions[0].Choice != "tool_usage" || !bytes.Equal(got.Sample.Evidence, sample.Evidence) {
		t.Fatalf("retry changed evidence: %+v %v", got, err)
	}
}

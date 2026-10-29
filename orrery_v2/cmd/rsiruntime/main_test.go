package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/improvement"
)

func TestRuntimeCLIRejectsUnknownInputAndUnboundExecutor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	for _, raw := range []string{`{"unknown":true}`, `{} {}`, strings.Repeat(" ", (4<<20)+1)} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		var request improvement.RuntimeRequest
		if err := readJSON(path, &request); err == nil {
			t.Fatal("invalid or oversized runtime input accepted")
		}
	}
	t.Setenv("ORRERY_RSI_AUTH_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("authority", 4))))
	if err := os.WriteFile(path, []byte(`{"executor_sha256":"wrong"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"authorize", "--input", path, "--issuer", "operator"}, io.Discard); err == nil || !strings.Contains(err.Error(), "executor") {
		t.Fatalf("authorized a different executable: %v", err)
	}
}

func TestRuntimeCLIAuthorizationBindsExactPreparedPlan(t *testing.T) {
	t.Setenv("ORRERY_RSI_AUTH_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("authority", 4))))
	digest, err := executorDigest()
	if err != nil {
		t.Fatal(err)
	}
	plan := improvement.RuntimePlan{RuntimeRequest: improvement.RuntimeRequest{TenantID: "tenant", CaseID: "case", RuntimeID: "runtime", ExecutorSHA256: digest, ObservationBudget: 6}}
	raw, _ := json.Marshal(plan)
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := run(context.Background(), []string{"authorize", "--input", path, "--issuer", "operator", "--ttl", "30m"}, &output); err != nil {
		t.Fatal(err)
	}
	var grant improvement.RuntimeGrant
	if err := json.Unmarshal([]byte(output.String()), &grant); err != nil || grant.PlanHash != improvement.Digest(plan) || grant.Issuer != "operator" || !grant.ExpiresAt.After(time.Now()) {
		t.Fatalf("unbound grant: %+v %v", grant, err)
	}
}

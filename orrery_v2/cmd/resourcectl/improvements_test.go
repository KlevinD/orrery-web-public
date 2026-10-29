package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestImprovementsCLIRequiresScopeAndOnlyReads(t *testing.T) {
	ctx := context.Background()
	url := "file://" + filepath.Join(t.TempDir(), "core.db")
	core, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: url})
	if err != nil {
		t.Fatal(err)
	}
	store := improvement.NewStore(core)
	c, err := store.Observe(ctx, improvement.Observation{ID: "result", TenantID: "tenant-a", Task: improvement.ObjectRef{Kind: "task", ID: "task-a"}, Run: improvement.ObjectRef{Kind: "agent_run", ID: "run-a"}, Trigger: "task_result", Summary: "需要流程评估", EvidenceIDs: []string{"run-a"}, SourceAccessRef: graphprotocol.AccessRef{Kind: "task", ID: "task-a", TenantID: "tenant-a"}})
	if err != nil {
		t.Fatal(err)
	}
	c, err = store.Claim(ctx, c.TenantID, c.ID, "learner-a", c.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token := c.Lease.Token
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"improvements", "--db", url}, {"improvements", "--db", url, "--tenant", "tenant-a", "--limit", "101"}, {"improvements", "--db", url, "--tenant", "tenant-a", "--apply"}} {
		if err := run(ctx, args, &bytes.Buffer{}); err == nil {
			t.Fatalf("invalid command accepted: %+v", args)
		}
	}
	var out bytes.Buffer
	if err := run(ctx, []string{"improvements", "--db", url, "--tenant", "tenant-a", "--id", c.ID}, &out); err != nil {
		t.Fatal(err)
	}
	var got improvement.Case
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != c.ID || got.Revision != c.Revision || got.State != improvement.Observed || got.Lease != nil || strings.Contains(out.String(), token) {
		t.Fatalf("unexpected inspection output: %s", out.String())
	}
	out.Reset()
	if err := run(ctx, []string{"improvements", "--db", url, "--tenant", "tenant-b"}, &out); err != nil {
		t.Fatal(err)
	}
	var page improvement.Page
	if err := json.Unmarshal(out.Bytes(), &page); err != nil || len(page.Items) != 0 {
		t.Fatalf("foreign scope result: %s %v", out.String(), err)
	}
}

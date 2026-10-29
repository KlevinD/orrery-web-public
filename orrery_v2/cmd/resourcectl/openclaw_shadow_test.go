package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenClawShadowRequiresExplicitMutationConfirmation(t *testing.T) {
	err := run(context.Background(), []string{"openclaw-shadow", "apply", "--db", "memory://test", "--jobs", "jobs.json", "--base-dir", t.TempDir()}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "requires explicit --apply") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenClawProfileReconcileRequiresExplicitMutationConfirmationWithoutJobsExport(t *testing.T) {
	err := run(context.Background(), []string{"openclaw-shadow", "reconcile-profiles", "--db", "memory://test", "--base-dir", t.TempDir()}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "requires explicit --apply") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenClawShadowRejectsUnknownActionAndMissingFlags(t *testing.T) {
	if err := run(context.Background(), []string{"openclaw-shadow", "launch"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown action error = %v", err)
	}
	if err := run(context.Background(), []string{"openclaw-shadow", "plan"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing flags error = %v", err)
	}
}

func TestReadOpenClawJobsReadsStdinForDash(t *testing.T) {
	raw, err := readOpenClawJobs("-", strings.NewReader(`{"jobs":[]}`))
	if err != nil {
		t.Fatalf("read jobs: %v", err)
	}
	if string(raw) != `{"jobs":[]}` {
		t.Fatalf("raw = %q", raw)
	}
}

func TestReadOpenClawJobsReadsFileForPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	if err := os.WriteFile(path, []byte(`{"jobs":[]}`), 0o600); err != nil {
		t.Fatalf("write jobs: %v", err)
	}
	raw, err := readOpenClawJobs(path, strings.NewReader(`{"jobs":[{"id":"stdin"}]}`))
	if err != nil {
		t.Fatalf("read jobs: %v", err)
	}
	if string(raw) != `{"jobs":[]}` {
		t.Fatalf("raw = %q", raw)
	}
}

func TestOpenClawShadowOutputReportsRetiredTaskCount(t *testing.T) {
	var out bytes.Buffer
	if err := writeOpenClawShadowOutput(&out, openClawShadowOutput{RetiredTasks: 1}); err != nil {
		t.Fatalf("write output: %v", err)
	}
	if !strings.Contains(out.String(), `"retired_tasks": 1`) {
		t.Fatalf("output = %s", out.String())
	}
}

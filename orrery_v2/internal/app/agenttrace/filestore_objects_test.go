package agenttrace

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/filestore"
)

func TestFilestoreObjectsAbortTraceTreatsMissingManifestAsAlreadyRemoved(t *testing.T) {
	objects := NewFilestoreObjects(filestore.NewLocal(t.TempDir()))
	err := objects.AbortTrace(context.Background(), TraceRecord{
		TenantID: "tenant-1", RunID: "run-missing", TraceVersion: 2,
		TraceRef: "discussion-conversations/missing/runtime-trace.json",
	}, time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("AbortTrace missing manifest: %v", err)
	}
}

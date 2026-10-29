package agenttrace

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"
)

type missingTraceObjects struct {
	*memoryTraceObjects
	missing string
}

func (s missingTraceObjects) ReadTrace(ctx context.Context, ref string) ([]ArchiveEntry, error) {
	if ref == s.missing {
		return nil, fs.ErrNotExist
	}
	return s.memoryTraceObjects.ReadTrace(ctx, ref)
}
func TestMissingTraceDoesNotBlockHealthyArchive(t *testing.T) {
	f := newRetentionFixture()
	broken := f.seedHot("completed", f.now.Add(-31*24*time.Hour))
	healthy := f.seedHot("completed", f.now.Add(-31*24*time.Hour))
	f.service.objects = missingTraceObjects{f.objects, broken.TraceRef}
	report, err := f.service.Run(context.Background(), 100)
	if !errors.Is(err, fs.ErrNotExist) || report.Archived != 1 || report.Missing != 1 || report.Failed != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if f.repo.records[0].Status != TraceMissing || f.repo.records[0].TerminalStatus != "completed" {
		t.Fatalf("metadata lost: %+v", f.repo.records[0])
	}
	if f.repo.records[1].Status != TraceArchived || f.objects.exists(healthy.TraceRef) {
		t.Fatal("healthy archive did not complete")
	}
}

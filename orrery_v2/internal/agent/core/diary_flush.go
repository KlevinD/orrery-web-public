package core

import "context"

const DedicatedLogicalMemoryCaptureProviderKey = "dedicated_logical_memory_capture"

type DiaryFlushStatus string

const (
	DiaryFlushSkipped   DiaryFlushStatus = "skipped"
	DiaryFlushNoContent DiaryFlushStatus = "no_content"
	DiaryFlushWritten   DiaryFlushStatus = "written"
	DiaryFlushFailed    DiaryFlushStatus = "failed"
	DiaryFlushExhausted DiaryFlushStatus = "exhausted"
)

type DiaryFlushOutcome struct {
	Status          DiaryFlushStatus
	Trigger         string
	Reason          string
	Path            string
	RecordID        string
	ContextRevision string
	Attempts        int
	Error           string
}

type DiaryFlushRequest struct {
	Runtime      SessionRuntime
	Request      RuntimeRequest
	Response     RuntimeResponse
	Capabilities Capabilities
}

type DiaryFlushCoordinator interface {
	PrepareRuntimeRequest(ctx context.Context, req RuntimeRequest) RuntimeRequest
	FlushTerminal(ctx context.Context, req DiaryFlushRequest) DiaryFlushOutcome
}

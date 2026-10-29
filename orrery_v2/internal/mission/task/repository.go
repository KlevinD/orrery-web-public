package task

import (
	"context"

	"orrery_v2/internal/platform/uow"
)

type Repository interface {
	GetTask(ctx context.Context, tx uow.Tx, id TaskID) (TaskRecord, error)
	SaveTask(ctx context.Context, tx uow.Tx, record TaskRecord) error
	FindSubmissionByIdempotencyKey(ctx context.Context, tx uow.Tx, taskID TaskID, key string) (TaskSubmission, bool, error)
	SaveSubmission(ctx context.Context, tx uow.Tx, submission TaskSubmission, deliverables []DeliverableSubmission, evidence []EvidenceSubmission) error
	LatestSubmission(ctx context.Context, tx uow.Tx, taskID TaskID) (TaskSubmission, bool, error)
	ListDeliverableSubmissions(ctx context.Context, tx uow.Tx, submissionID TaskSubmissionID) ([]DeliverableSubmission, error)
	ListEvidenceSubmissions(ctx context.Context, tx uow.Tx, submissionID TaskSubmissionID) ([]EvidenceSubmission, error)
	FindOpenReworkRequest(ctx context.Context, tx uow.Tx, taskID TaskID) (ReworkRequest, bool, error)
	SaveReworkRequest(ctx context.Context, tx uow.Tx, request ReworkRequest) error
	FindQAResultByIdempotencyKey(ctx context.Context, tx uow.Tx, taskID TaskID, key string) (QAResultRecord, bool, error)
	SaveQAResult(ctx context.Context, tx uow.Tx, result QAResultRecord) error
	SaveAcceptanceReport(ctx context.Context, tx uow.Tx, report AcceptanceReport) error
}

type EventSink interface {
	AppendTaskEvent(ctx context.Context, tx uow.Tx, event TaskEvent) error
}

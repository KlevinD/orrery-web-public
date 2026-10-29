package actioncontract

import "context"

type Repository interface {
	SaveActionType(ctx context.Context, actionType ActionType) error
	GetActionType(ctx context.Context, id ActionTypeID) (ActionType, error)
	ListActionTypes(ctx context.Context) ([]ActionType, error)
	SaveActionRun(ctx context.Context, run ActionRun) error
	GetActionRun(ctx context.Context, id ActionRunID) (ActionRun, error)
	ListActionRuns(ctx context.Context, query ActionRunQuery) (ActionRunPage, error)
	SaveWritebackReceipt(ctx context.Context, receipt WritebackReceipt) error
	ListWritebackReceipts(ctx context.Context, runID ActionRunID) ([]WritebackReceipt, error)
}

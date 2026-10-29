package operations_test

import (
	"context"

	"orrery_v2/internal/app/operations"
)

type fakeWorkforceAccountProjector struct {
	personID string
	result   operations.WorkforceAccountProjectionResult
	err      error
}

func (p *fakeWorkforceAccountProjector) ProjectLatest(_ context.Context, personID string) (operations.WorkforceAccountProjectionResult, error) {
	p.personID = personID
	return p.result, p.err
}

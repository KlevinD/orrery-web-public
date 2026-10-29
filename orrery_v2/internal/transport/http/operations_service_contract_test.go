package httptransport

import (
	"testing"

	"orrery_v2/internal/app/operations"
)

func TestOperationsServiceInterfaceMatchesApplicationService(t *testing.T) {
	var _ OperationsService = (*operations.Service)(nil)
}

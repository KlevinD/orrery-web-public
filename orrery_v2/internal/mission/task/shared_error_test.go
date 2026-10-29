package task

import (
	"testing"

	contracterrors "orrery_v2/internal/contract/errors"
)

func TestTaskValidationErrorsConvertToSharedContractError(t *testing.T) {
	err := validationError(CodeMissingDeliverable, "deliverables", "deliverable is required")
	contractErr := err.ContractError()

	if contractErr.Code != contracterrors.CodeMissingDeliverable || contractErr.Field != "deliverables" {
		t.Fatalf("unexpected shared contract error: %#v", contractErr)
	}
}

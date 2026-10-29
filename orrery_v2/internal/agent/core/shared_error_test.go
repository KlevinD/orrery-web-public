package core

import (
	"testing"

	contracterrors "orrery_v2/internal/contract/errors"
)

func TestAgentValidationErrorsConvertToSharedContractError(t *testing.T) {
	err := validationError(CodeInvalidRef, "tool_refs[0].tool_id", "tool_id is required")
	contractErr := err.ContractError()

	if contractErr.Code != contracterrors.CodeInvalidRef || contractErr.Field != "tool_refs[0].tool_id" {
		t.Fatalf("unexpected shared contract error: %#v", contractErr)
	}
}

package kernelcompat

import (
	"errors"
	"fmt"
	"testing"
)

func TestExecutionErrorCodeReturnsFixedCodeThroughWrapping(t *testing.T) {
	err := NewExecutionError("model_request_failed", errors.New("provider endpoint=https://private.invalid key=secret"))
	err = fmt.Errorf("adapter context: %w", err)

	code, ok := ExecutionErrorCode(err)
	if !ok || code != "model_request_failed" {
		t.Fatalf("ExecutionErrorCode() = (%q, %t), want (model_request_failed, true)", code, ok)
	}
	if got := err.Error(); got != "adapter context: managed kernel execution failed: model_request_failed" {
		t.Fatalf("Error() = %q, want bounded diagnostic", got)
	}
}

func TestExecutionErrorCodeDoesNotClassifyUnknownErrors(t *testing.T) {
	if code, ok := ExecutionErrorCode(errors.New("provider endpoint=https://private.invalid key=secret")); ok || code != "" {
		t.Fatalf("ExecutionErrorCode(unknown) = (%q, %t), want (\"\", false)", code, ok)
	}
	if code, ok := ExecutionErrorCode(NewExecutionError("provider_details", errors.New("private"))); ok || code != "" {
		t.Fatalf("ExecutionErrorCode(unknown adapter code) = (%q, %t), want (\"\", false)", code, ok)
	}
}

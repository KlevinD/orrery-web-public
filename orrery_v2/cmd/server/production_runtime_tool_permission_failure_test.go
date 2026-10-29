package main

import (
	"fmt"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	mcptransport "orrery_v2/internal/transport/mcp"
	"testing"
)

func TestRuntimePermissionFailureDoesNotRecommendAutomaticRetry(t *testing.T) {
	for _, cause := range []error{mcptransport.ErrPermissionDenied, fmt.Errorf("wrapped: %w", mcptransport.ErrPermissionDenied)} {
		result := runtimeToolFailureResult(runtimecore.RuntimeToolCallRequest{Name: "knowledge_search"}, cause, runtimeToolFailureCode(cause))
		if result.Output["retryable"] != false || result.Output["error_code"] != "permission_denied" {
			t.Fatalf("permanent denial treated as transient: %+v", result.Output)
		}
	}
}

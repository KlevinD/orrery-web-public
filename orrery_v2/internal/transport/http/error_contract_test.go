package httptransport

import "testing"

func TestStableErrorCodesIncludeStage10Contract(t *testing.T) {
	want := []string{
		"invalid_request",
		"unauthorized",
		"forbidden",
		"not_found",
		"conflict",
		"failed_precondition",
		"version_conflict",
		"idempotency_conflict",
		"rate_limited",
		"resource_unavailable",
		"app_rule_denied",
		"runtime_unavailable",
		"internal_error",
	}
	got := StableErrorCodes()
	for _, code := range want {
		if !got[code] {
			t.Fatalf("stable error code %q missing from registry %#v", code, got)
		}
	}
}

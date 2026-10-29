package httptransport

import (
	"os"
	"strings"
	"testing"
)

func TestRouterInterfaceContractsDeclareDemoRoutesForbiddenForDevelopment(t *testing.T) {
	data, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	content := string(data)
	required := []string{
		"DEVELOPMENT RULE: Do not satisfy these HTTP service interfaces with demo handlers or demo API facades.",
		"Implement the missing backend service, storage, runtime adapter, or return an explicit service dependency error.",
	}
	for _, marker := range required {
		if !strings.Contains(content, marker) {
			t.Fatalf("routes.go is missing demo interface guardrail marker %q", marker)
		}
	}
}

package browser

import (
	"strings"
	"testing"
)

func TestFixedBrowserOperationsRejectExecutableAndOutOfBoundsInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"evaluate", map[string]any{"script": "document.cookie"}},
		{"snapshot", map[string]any{"tabId": float64(7), "method": "Runtime.evaluate"}},
		{"snapshot", map[string]any{"tabId": float64(-1)}},
		{"snapshot", map[string]any{"tabId": float64(1.5)}},
		{"tabs", nil},
		{"navigate", map[string]any{"tabId": float64(7), "operationId": "operation-123", "url": "file:///D:/secret"}},
		{"navigate", map[string]any{"tabId": float64(7), "operationId": "operation-123", "url": "https://user:password@example.com"}},
		{"navigate", map[string]any{"tabId": float64(7), "operationId": "operation-123", "url": "https://chromewebstore.google.com/detail/test"}},
		{"fill", map[string]any{"tabId": float64(7), "operationId": "operation-123", "snapshotId": "snapshot-123", "ref": "e1", "text": strings.Repeat("a", 10001)}},
		{"press", map[string]any{"tabId": float64(7), "operationId": "operation-123", "snapshotId": "snapshot-123", "ref": "e1", "key": "F12"}},
	} {
		if ValidateArguments(tc.name, tc.args) == nil {
			t.Fatalf("accepted %s: %#v", tc.name, tc.args)
		}
	}
	for name, args := range map[string]map[string]any{"tabs": {}, "snapshot": {"tabId": float64(7)}, "navigate": {"tabId": float64(7), "operationId": "operation-123", "url": "https://jobs.example.com/role/7"}} {
		if err := ValidateArguments(name, args); err != nil {
			t.Fatalf("valid %s: %v", name, err)
		}
	}
}

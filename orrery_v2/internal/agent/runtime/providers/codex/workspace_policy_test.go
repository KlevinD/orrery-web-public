package codex

import (
	"path/filepath"
	"testing"
)

func TestThreadOpenPreservesHostWritableRootsAndMCP(t *testing.T) {
	root := t.TempDir()
	s := &NativeSession{workspace: NativeWorkspace{Directory: filepath.Join(root, "agents", "steward"), Sandbox: "workspace-write", WritableRoots: []string{root}}}
	params := s.threadOpenParams()
	config := params["config"].(map[string]any)
	roots := config["sandbox_workspace_write"].(map[string]any)["writable_roots"].([]string)
	if len(roots) != 1 || roots[0] != root || params["cwd"] != s.workspace.Directory || params["sandbox"] != "workspace-write" {
		t.Fatalf("host workspace policy was lost: %#v", params)
	}
	roots[0] = "changed"
	if s.workspace.WritableRoots[0] != root {
		t.Fatal("wire roots alias host policy")
	}
	s.workspace.Sandbox = "read-only"
	if _, exists := s.threadOpenParams()["config"]; exists {
		t.Fatal("read-only session gained writable roots")
	}
}

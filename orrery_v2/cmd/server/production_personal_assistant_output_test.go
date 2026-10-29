package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	mcptransport "orrery_v2/internal/transport/mcp"
)

func shellOutputFixtureCommand(stderr bool) string {
	if runtime.GOOS == "windows" {
		if stderr {
			return "type document.txt & type error.txt 1>&2"
		}
		return "type document.txt"
	}
	if stderr {
		return "cat document.txt; cat error.txt >&2"
	}
	return "cat document.txt"
}

func assertSavedShellOutput(t *testing.T, output map[string]any, stream, original string) {
	t.Helper()
	preview := output[stream].(string)
	if output[stream+"_bytes"] != int64(len(original)) || output[stream+"_returned_bytes"] != len(preview) {
		t.Fatal("incorrect stream byte counts")
	}
	if !utf8.ValidString(preview) || preview != original[:len(preview)] {
		t.Fatal("preview damaged the original text")
	}
	if output[stream+"_truncated"] != true {
		t.Fatal("missing stream truncation")
	}
	ref := output[stream+"_output"].(map[string]any)
	saved, err := os.ReadFile(ref["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(saved)
	if string(saved) != original || ref["size_bytes"] != int64(len(original)) || ref["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatal("complete original output was not saved")
	}
	offset := ref["next_byte_offset"].(int)
	if offset != len(preview) || preview+string(saved[offset:]) != original {
		t.Fatal("continuation skips or duplicates bytes")
	}
	if !pathWithin(output["workspace"].(string), ref["path"].(string)) {
		t.Fatal("output escaped workspace")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(ref["path"].(string))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("saved stream is not private")
		}
	}
}

func TestPersonalAssistantShellOutputChineseContinuation(t *testing.T) {
	root := t.TempDir()
	original := "X" + strings.Repeat("中文内容🙂", 7000) + "UNREAD_TAIL"
	if err := os.WriteFile(filepath.Join(root, "document.txt"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := (productionPersonalAssistantShellExecutor{}).ExecutePersonalAssistantShell(context.Background(), "Shell", mcptransport.PersonalAssistantShellRequest{
		WorkspaceRoot: root, WorkingDirectory: root, Command: shellOutputFixtureCommand(false), TimeoutMs: 5000,
		MaxOutputLen: 300000,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSavedShellOutput(t, result.Output, "stdout", original)
	if len(result.Output["stdout"].(string)) > 65536 || result.Output["stderr_truncated"] != false {
		t.Fatal("preview cap was not enforced independently")
	}
	raw, err := json.Marshal(result.Output)
	if err != nil || len(raw) > personalAssistantShellReceiptBytes {
		t.Fatal("oversized receipt")
	}
	t.Logf("complete_bytes=%d preview_bytes=%d JSON_receipt_bytes=%d full_output_preserved=true", len(original), len(result.Output["stdout"].(string)), len(raw))
}

func TestPersonalAssistantShellOutputEscapedStreamsFitReceipt(t *testing.T) {
	root := t.TempDir()
	original := strings.Repeat("\x01\t\n\\\"", 20000)
	for _, name := range []string{"document.txt", "error.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := (productionPersonalAssistantShellExecutor{}).ExecutePersonalAssistantShell(context.Background(), "Shell", mcptransport.PersonalAssistantShellRequest{
		WorkspaceRoot: root, WorkingDirectory: root, Command: shellOutputFixtureCommand(true), TimeoutMs: 5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSavedShellOutput(t, result.Output, "stdout", original)
	assertSavedShellOutput(t, result.Output, "stderr", original)
	raw, err := json.Marshal(result.Output)
	if err != nil || len(raw) > personalAssistantShellReceiptBytes {
		t.Fatal("escaped receipt exceeds budget")
	}
	t.Logf("stdout_bytes=%d stderr_bytes=%d JSON_receipt_bytes=%d", len(original), len(original), len(raw))
}

func TestPersonalAssistantShellOutputCaptureBoundsMemory(t *testing.T) {
	root := t.TempDir()
	store, err := newPersonalAssistantOutputStore(root, root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()
	capture := newPersonalAssistantOutputCapture(store, "stdout", 7)
	defer capture.close()
	block := bytes.Repeat([]byte("x"), 8192)
	for i := 0; i < 256; i++ {
		if _, err := capture.Write(block); err != nil {
			t.Fatal(err)
		}
		if len(capture.preview) > 7+utf8.UTFMax {
			t.Fatal("capture retained full output in memory")
		}
	}
	if err := capture.close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(capture.path)
	if err != nil || info.Size() != 256*8192 {
		t.Fatal("spooled bytes missing")
	}
}

func TestPersonalAssistantShellOutputSmallCommandsDoNotCreateArtifacts(t *testing.T) {
	root := t.TempDir()
	_, err := (productionPersonalAssistantShellExecutor{}).ExecutePersonalAssistantShell(context.Background(), "Shell", mcptransport.PersonalAssistantShellRequest{
		WorkspaceRoot: root, WorkingDirectory: root, Command: testShellCommand("pwd"), TimeoutMs: 5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("small output created unnecessary saved files")
	}
}

func TestPersonalAssistantShellOutputStorageFailureIsNotComplete(t *testing.T) {
	root := t.TempDir()
	store, err := newPersonalAssistantOutputStore(root, root)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.root.Close()
	stdout := newPersonalAssistantOutputCapture(store, "stdout", 4)
	stderr := newPersonalAssistantOutputCapture(store, "stderr", 4)
	if _, err := stdout.Write([]byte("more output than the preview")); err == nil {
		t.Fatal("storage failure was ignored")
	}
	if _, err := personalAssistantShellOutput(map[string]any{}, stdout, stderr, 4); err == nil {
		t.Fatal("failed output capture became a complete result")
	}
}

func TestPersonalAssistantShellOutputRejectsSymlinkOutsideWorkspace(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(root, "outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := (productionPersonalAssistantShellExecutor{}).ExecutePersonalAssistantShell(context.Background(), "Shell", mcptransport.PersonalAssistantShellRequest{
		WorkspaceRoot: root, WorkingDirectory: root, Cwd: link, Command: testShellCommand("pwd"), TimeoutMs: 5000,
	})
	if err == nil {
		t.Fatal("output scope followed a symlink outside the workspace")
	}
}

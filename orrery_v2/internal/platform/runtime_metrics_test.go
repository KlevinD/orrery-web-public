package platform_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform"
)

func TestRuntimeStartsAndStopsConfiguredMetricsServer(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	configPath := filepath.Join(root, "orrery.yaml")
	if err := os.WriteFile(configPath, []byte(`
paths:
  base_dir: .
database:
  urls:
    core: file://./data/core.db
    audit: file://./data/audit.db
    events: file://./data/events.db
    jobs: file://./data/jobs.db
    runtime: file://./data/runtime.db
    observability: file://./data/observability.db
filestore:
  backend: local
  root: ./artifacts
logging:
  console: false
  file: ./logs/platform.jsonl
metrics:
  addr: `+addr+`
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		_ = rt.Close()
		t.Fatalf("get /metrics: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "orrery_build_info") {
		_ = rt.Close()
		t.Fatalf("/metrics status=%d body=%s", resp.StatusCode, string(body))
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	client := http.Client{Timeout: 100 * time.Millisecond}
	if resp, err := client.Get("http://" + addr + "/metrics"); err == nil {
		_ = resp.Body.Close()
		t.Fatalf("metrics server still accepted requests after Runtime.Close")
	}
}

func TestRuntimeReloadComponentsKeepsMetricsServerWhenAddressUnchanged(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAMLWithMetrics(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms", addr)
	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	writeRuntimeYAMLWithMetrics(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "1ms", addr)
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err != nil {
		t.Fatalf("ReloadComponents returned error: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("metrics server stopped after unchanged-address reload: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", resp.StatusCode)
	}
}

func TestRuntimeReloadComponentsStopsMetricsServerWhenAddressDisabled(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAMLWithMetrics(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms", addr)
	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	writeRuntimeYAML(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "1ms")
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err != nil {
		t.Fatalf("ReloadComponents returned error: %v", err)
	}

	client := http.Client{Timeout: 100 * time.Millisecond}
	if resp, err := client.Get("http://" + addr + "/metrics"); err == nil {
		_ = resp.Body.Close()
		t.Fatalf("metrics server still accepted requests after metrics.addr was disabled")
	}
}

func TestRuntimeReloadComponentsKeepsOldMetricsServerWhenNewAddressCannotBind(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	oldListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen old: %v", err)
	}
	oldAddr := oldListener.Addr().String()
	if err := oldListener.Close(); err != nil {
		t.Fatalf("close old listener: %v", err)
	}
	blockedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen blocked: %v", err)
	}
	defer blockedListener.Close()
	blockedAddr := blockedListener.Addr().String()

	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAMLWithMetrics(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms", oldAddr)
	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	writeRuntimeYAMLWithMetrics(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "1ms", blockedAddr)
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err == nil {
		t.Fatalf("ReloadComponents returned nil error for blocked metrics addr")
	}

	resp, err := http.Get("http://" + oldAddr + "/metrics")
	if err != nil {
		t.Fatalf("old metrics server stopped after failed reload: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("old /metrics status = %d, want 200", resp.StatusCode)
	}
	if rt.Config.Metrics.Addr != blockedAddr {
		t.Fatalf("config snapshot addr = %q, want blocked addr retained for diagnostics", rt.Config.Metrics.Addr)
	}
}

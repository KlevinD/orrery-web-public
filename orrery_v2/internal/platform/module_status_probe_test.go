package platform_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
)

func TestBusinessModuleRegistersStatusProbeWithoutMetricsTypes(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{Environment: "test"}
	cfg.DatabaseURLs = map[string]string{
		"core":          "memory://status-probe-core",
		"events":        "memory://status-probe-events",
		"jobs":          "memory://status-probe-jobs",
		"runtime":       "memory://status-probe-runtime",
		"observability": "memory://status-probe-observability",
	}
	cfg.FileStore.Backend = "local"
	cfg.FileStore.Root = t.TempDir()
	cfg.Logging.Console = true

	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		return registry.RegisterStatusProbe("business", "workers", "core", func(ctx context.Context) (map[string]int64, error) {
			return map[string]int64{"blocked": 1, "ready": 2}, nil
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	rec := httptest.NewRecorder()
	platform.NewMetricsHandler(rt).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	text := string(body)
	for _, want := range []string{
		`orrery_business_workers{role="core",status="blocked"} 1`,
		`orrery_business_workers{role="core",status="ready"} 2`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics output missing %q: %s", want, text)
		}
	}
}

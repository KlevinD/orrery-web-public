package platform_test

import (
	"context"
	"path/filepath"
	"testing"

	"orrery_v2/internal/platform"
)

func TestBusinessModuleReadsSecretWithoutSecretTypes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	t.Setenv("ORRERY_SECRET_BUSINESS_TOKEN", "token-runtime")

	resolved := make(chan string, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		value, err := registrar.ReadSecret(ctx, "business_token")
		if err != nil {
			return err
		}
		resolved <- value.Plaintext()
		return nil
	})

	rt, err := platform.OpenFromYAMLWithModules(ctx, configPath, module)
	if err != nil {
		t.Fatalf("OpenFromYAMLWithModules returned error: %v", err)
	}
	defer rt.Close()

	select {
	case got := <-resolved:
		if got != "token-runtime" {
			t.Fatalf("module resolved secret = %q, want token-runtime", got)
		}
	default:
		t.Fatalf("module did not resolve secret through registrar")
	}
}

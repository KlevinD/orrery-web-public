package platform_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
)

func TestRuntimeExposesControlledSecretProvider(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	t.Setenv("ORRERY_SECRET_OPENAI_API_KEY", "sk-runtime")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	value, err := rt.Secrets.Get(ctx, "openai_api_key")
	if err != nil {
		t.Fatalf("runtime secret lookup returned error: %v", err)
	}
	if value.Plaintext() != "sk-runtime" {
		t.Fatalf("secret plaintext = %q, want sk-runtime", value.Plaintext())
	}
	if value.String() == "sk-runtime" {
		t.Fatalf("secret String leaked plaintext")
	}
}

func TestRuntimeSecretProviderPrefersLocalFilesAndFallsBackToEnvironment(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	secretRoot := filepath.Join(root, "secrets")
	if err := os.MkdirAll(secretRoot, 0o700); err != nil {
		t.Fatalf("mkdir secret root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secretRoot, "api_token"), []byte("file-secret\n"), 0o600); err != nil {
		t.Fatalf("write file secret: %v", err)
	}
	t.Setenv("ORRERY_SECRET_API_TOKEN", "env-secret")
	t.Setenv("ORRERY_SECRET_ENV_ONLY", "env-fallback")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	value, err := rt.Secrets.Get(ctx, "api_token")
	if err != nil {
		t.Fatalf("file-backed secret lookup returned error: %v", err)
	}
	if value.Plaintext() != "file-secret" {
		t.Fatalf("api_token secret = %q, want file-secret", value.Plaintext())
	}

	value, err = rt.Secrets.Get(ctx, "env_only")
	if err != nil {
		t.Fatalf("env fallback secret lookup returned error: %v", err)
	}
	if value.Plaintext() != "env-fallback" {
		t.Fatalf("env_only secret = %q, want env-fallback", value.Plaintext())
	}
}

func TestRuntimeReloadComponentsRebuildsSecretProviderFromCurrentConfig(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")
	firstSecretRoot := filepath.Join(root, "secrets")
	if err := os.MkdirAll(firstSecretRoot, 0o700); err != nil {
		t.Fatalf("mkdir first secret root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(firstSecretRoot, "api_token"), []byte("first-secret\n"), 0o600); err != nil {
		t.Fatalf("write first secret: %v", err)
	}

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	first, err := rt.Secrets.Get(ctx, "api_token")
	if err != nil {
		t.Fatalf("first secret lookup returned error: %v", err)
	}
	if first.Plaintext() != "first-secret" {
		t.Fatalf("first secret = %q, want first-secret", first.Plaintext())
	}

	secondSecretRoot := filepath.Join(root, "rotated-secrets")
	if err := os.MkdirAll(secondSecretRoot, 0o700); err != nil {
		t.Fatalf("mkdir second secret root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secondSecretRoot, "api_token"), []byte("second-secret\n"), 0o600); err != nil {
		t.Fatalf("write second secret: %v", err)
	}
	writeRuntimeYAML(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "1ms")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read runtime yaml: %v", err)
	}
	raw = append(raw, []byte("\nsecret:\n  root: ./rotated-secrets\n")...)
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatalf("rewrite runtime yaml: %v", err)
	}

	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err != nil {
		t.Fatalf("ReloadComponents returned error: %v", err)
	}

	second, err := rt.Secrets.Get(ctx, "api_token")
	if err != nil {
		t.Fatalf("second secret lookup returned error: %v", err)
	}
	if second.Plaintext() != "second-secret" {
		t.Fatalf("second secret = %q, want second-secret", second.Plaintext())
	}
}

func TestBusinessModuleCanResolveSecretsThroughRegistrar(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	t.Setenv("ORRERY_SECRET_BUSINESS_TOKEN", "token-runtime")

	resolved := make(chan string, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		value, err := registrar.GetSecret(ctx, "business_token")
		if err != nil {
			return err
		}
		resolved <- value.Plaintext()
		return nil
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
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

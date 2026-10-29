package secret_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"orrery_v2/internal/platform/secret"
)

func TestMemoryProviderReturnsSecretsWithoutExposingPlaintextString(t *testing.T) {
	provider := secret.NewMemoryProvider(map[string]string{"api": "sk-real"})

	value, err := provider.Get(context.Background(), "api")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if value.Plaintext() != "sk-real" {
		t.Fatalf("Plaintext = %q, want sk-real", value.Plaintext())
	}
	if value.String() == "sk-real" {
		t.Fatalf("String exposed plaintext")
	}
	if value.String() != "[REDACTED]" {
		t.Fatalf("String = %q, want [REDACTED]", value.String())
	}
}

func TestMemoryProviderReturnsNotFoundForMissingSecret(t *testing.T) {
	provider := secret.NewMemoryProvider(nil)

	_, err := provider.Get(context.Background(), "missing")
	if err == nil {
		t.Fatalf("Get missing secret returned nil error")
	}
	if !secret.IsNotFound(err) {
		t.Fatalf("error = %v, want not found", err)
	}
}

func TestEnvProviderReturnsNamedSecrets(t *testing.T) {
	t.Setenv("ORRERY_SECRET_OPENAI_API_KEY", "sk-env")
	provider := secret.NewEnvProvider("ORRERY_SECRET_")

	value, err := provider.Get(context.Background(), "openai-api-key")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if value.Plaintext() != "sk-env" {
		t.Fatalf("Plaintext = %q, want sk-env", value.Plaintext())
	}
}

func TestEnvProviderReturnsNotFoundForMissingSecret(t *testing.T) {
	provider := secret.NewEnvProvider("ORRERY_SECRET_")

	_, err := provider.Get(context.Background(), "definitely-missing")
	if err == nil {
		t.Fatalf("Get missing secret returned nil error")
	}
	if !secret.IsNotFound(err) {
		t.Fatalf("error = %v, want not found", err)
	}
}

func TestFileProviderReadsSecretFilesWithStrictPermissions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "api-token")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
	}
	provider := secret.NewFileProvider(root)

	value, err := provider.Get(context.Background(), "api-token")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if value.Plaintext() != "file-secret" {
		t.Fatalf("Plaintext = %q, want file-secret", value.Plaintext())
	}
}

func TestFileProviderRejectsTraversalAndLoosePermissions(t *testing.T) {
	root := t.TempDir()
	loose := filepath.Join(root, "loose")
	if err := os.WriteFile(loose, []byte("secret"), 0o644); err != nil {
		t.Fatalf("WriteFile loose: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(loose, 0o644); err != nil {
			t.Fatalf("Chmod loose: %v", err)
		}
	}
	provider := secret.NewFileProvider(root)

	if _, err := provider.Get(context.Background(), "../outside"); err == nil {
		t.Fatalf("Get traversal returned nil error")
	}
	if runtime.GOOS != "windows" {
		if _, err := provider.Get(context.Background(), "loose"); err == nil {
			t.Fatalf("Get loose permission secret returned nil error")
		}
	}
}

func TestCompositeProviderFallsBackOnlyOnNotFound(t *testing.T) {
	primary := secret.NewMemoryProvider(nil)
	secondary := secret.NewMemoryProvider(map[string]string{"api": "fallback"})
	provider := secret.NewCompositeProvider(primary, secondary)

	value, err := provider.Get(context.Background(), "api")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if value.Plaintext() != "fallback" {
		t.Fatalf("Plaintext = %q, want fallback", value.Plaintext())
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = provider.Get(cancelled, "api")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Get error = %v, want context.Canceled", err)
	}
}

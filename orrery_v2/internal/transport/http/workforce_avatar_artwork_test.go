package httptransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGeneratedWorkforceAvatarCatalogIsStableAndIndependent(t *testing.T) {
	store := NewLocalWorkforceAvatarStore("")
	catalog, err := store.ListDefaults(context.Background())
	if err != nil || len(catalog) != 8 {
		t.Fatalf("catalog = %+v, err = %v", catalog, err)
	}
	seen := map[string]bool{}
	for _, item := range catalog {
		avatar, err := store.OpenDefault(context.Background(), item.ID)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(avatar.Bytes)
		if seen[avatar.Info.ETag] || avatar.Info.ETag != hex.EncodeToString(hash[:]) || avatar.Info.Size != int64(len(avatar.Bytes)) {
			t.Fatalf("default %s has duplicate or invalid content metadata", item.ID)
		}
		seen[avatar.Info.ETag] = true
		fresh, err := renderWorkforceAvatar(avatar.Info.FileName)
		if err != nil || !bytes.Equal(fresh, avatar.Bytes) {
			t.Fatalf("default %s is not reproducible: %v", item.ID, err)
		}
		avatar.Bytes[0] ^= 255
		reopened, err := store.OpenDefault(context.Background(), item.ID)
		if err != nil || !bytes.Equal(reopened.Bytes, fresh) {
			t.Fatalf("caller mutated cached default %s: %v", item.ID, err)
		}
	}
	if _, err := renderWorkforceAvatar("../lead-agent.png"); err == nil {
		t.Fatal("unknown generator input accepted")
	}
}

func TestGeneratedWorkforceAvatarPreservesStoredSelectionAndUploadPrecedence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := NewLocalWorkforceAvatarStore(root)
	profile := "A000010"
	updated := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	// An old selection stores a legacy ID and JPEG metadata, not image bytes.
	metadata, err := json.Marshal(WorkforceAvatarInfo{
		AgentProfileID: profile, DefaultID: "lead-agent", Source: "default",
		FileName: "lead-agent.jpg", ContentType: "image/jpeg", ETag: "old-jpeg-hash", UpdatedAt: updated,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, workforceAvatarProfileKey(profile)+".json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	selected, err := store.Open(ctx, profile)
	if err != nil || selected.Info.ContentType != "image/png" || !selected.Info.UpdatedAt.Equal(updated) || selected.Info.ETag == "old-jpeg-hash" {
		t.Fatalf("old selection = %+v, err = %v", selected.Info, err)
	}
	upload := testWorkforceAvatarPNG(t)
	if _, err := store.Save(ctx, profile, "image/png", upload); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Open(ctx, profile)
	if err != nil || stored.Info.Source != "upload" || !bytes.Equal(stored.Bytes, upload) {
		t.Fatalf("uploaded avatar lost precedence: %+v, %v", stored.Info, err)
	}
	if err := store.Delete(ctx, profile); err != nil {
		t.Fatal(err)
	}
	reset, err := store.Open(ctx, profile)
	if err != nil || reset.Info.Source != "default" || !bytes.Equal(reset.Bytes, selected.Bytes) {
		t.Fatalf("reset did not restore generated default: %+v, %v", reset.Info, err)
	}
}

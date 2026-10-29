package filestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	platformerrors "orrery_v2/internal/platform/errors"
	"orrery_v2/internal/platform/idgen"
)

func ownedLocal(t *testing.T) (*Local, string) {
	t.Helper()
	root := t.TempDir()
	store, err := OpenLocalStorage(LocalConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	return store, root
}

func TestOwnedLocalReadsExistingLayoutAndHTTPRanges(t *testing.T) {
	store, root := ownedLocal(t)
	// An existing installation's files, written without using the new writer.
	if err := os.WriteFile(filepath.Join(root, "legacy-object"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "legacy-object.meta.json"), []byte(`{"filename":"季度报告.pdf","content_type":"application/pdf"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.Get(context.Background(), "legacy-object")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact.Content)
	if artifact.Ref.Name != "季度报告.pdf" || artifact.Ref.ContentType != "application/pdf" || artifact.Ref.Checksum != hex.EncodeToString(digest[:]) {
		t.Fatalf("legacy artifact: %+v", artifact.Ref)
	}
	request := httptest.NewRequest("GET", "/uploads/legacy-object", nil)
	request.Header.Set("Range", "bytes=2-5")
	response := httptest.NewRecorder()
	store.ServeFile(response, request, "legacy-object")
	if response.Code != 206 || response.Body.String() != "2345" {
		t.Fatalf("range: %d %q", response.Code, response.Body.String())
	}
	kind, params, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if err != nil || kind != "inline" || params["filename"] != "季度报告.pdf" {
		t.Fatalf("disposition: %v %v %v", kind, params, err)
	}
	if location, err := store.URL(context.Background(), "legacy-object"); err != nil || location != "artifact://legacy-object" {
		t.Fatalf("location: %q %v", location, err)
	}
}

func TestOwnedLocalArtifactLifecycleAndInjectedDependencies(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	now := time.Date(2026, 10, 15, 6, 0, 0, 0, time.UTC)
	store, err := OpenFromConfig(ctx, config.FileStoreConfig{Backend: "local", Root: root, BaseURL: "https://files.example.test/"}, Dependencies{Clock: clock.NewFake(now), IDGenerator: idgen.NewDeterministic("owned")})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(ctx, "../contract.txt", "text/plain", strings.NewReader("revision-one"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != "owned-000001" || !ref.CreatedAt.Equal(now) || ref.Name != "contract.txt" || ref.Size != 12 || ref.LocalPath != "" {
		t.Fatalf("ref: %+v", ref)
	}
	if ref.URL != "https://files.example.test/uploads/"+ref.ID || store.CdnDomain() != "files.example.test" {
		t.Fatalf("public ref: %+v", ref)
	}
	reopened := NewLocal(root)
	recovered, err := reopened.Get(ctx, ref.ID)
	if err != nil || recovered.Ref.Checksum != ref.Checksum || string(recovered.Content) != "revision-one" {
		t.Fatalf("reopened: %+v %v", recovered, err)
	}
	if _, err = store.Upload(ctx, ref.ID, []byte("revision-two"), "text/markdown", "second.md"); err != nil {
		t.Fatal(err)
	}
	updated, err := store.Get(ctx, ref.ID)
	if err != nil || updated.Ref.Name != "second.md" || updated.Ref.ContentType != "text/markdown" {
		t.Fatalf("updated: %+v %v", updated, err)
	}
	if err = store.Delete(ctx, ref.ID); err != nil {
		t.Fatal(err)
	}
	_, err = store.Get(ctx, ref.ID)
	var coded *platformerrors.Error
	if !errors.As(err, &coded) || coded.Code != platformerrors.CodeNotFound {
		t.Fatalf("missing-object error: %v", err)
	}
	for _, suffix := range []string{"", metaSuffix} {
		if _, err := os.Stat(filepath.Join(root, ref.ID+suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("remaining object: %v", err)
		}
	}
}

func TestOwnedLocalRejectsUnsafeNamesOnEveryReadPath(t *testing.T) {
	store, _ := ownedLocal(t)
	ctx := context.Background()
	for _, key := range []string{"", ".", "..", "../private", "nested/../../private", "/private", "C:/private", "artifact.meta.json", "x/../private", `x\..\private`} {
		t.Run(key, func(t *testing.T) {
			if reader, err := store.GetReader(ctx, key); err == nil {
				reader.Close()
				t.Fatal("reader accepted unsafe name")
			}
			if _, err := store.Get(ctx, key); err == nil {
				t.Fatal("Get accepted unsafe name")
			}
			if _, err := store.URL(ctx, key); err == nil {
				t.Fatal("URL accepted unsafe name")
			}
			if _, err := store.Upload(ctx, key, nil, "text/plain", "x"); err == nil {
				t.Fatal("Upload accepted unsafe name")
			}
			if key != "" && store.Delete(ctx, key) == nil {
				t.Fatal("Delete accepted unsafe name")
			}
			response := httptest.NewRecorder()
			store.ServeFile(response, httptest.NewRequest("GET", "/uploads/x", nil), key)
			if response.Code != 404 {
				t.Fatalf("HTTP status: %d", response.Code)
			}
		})
	}
}

func TestOwnedLocalSymlinkIsolation(t *testing.T) {
	store, root := ownedLocal(t)
	outside := t.TempDir()
	private := filepath.Join(outside, "private")
	if err := os.WriteFile(private, []byte("outside-boundary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink privileges unavailable: %v", err)
	}
	ctx := context.Background()
	key := "escape/private"
	if _, err := store.Get(ctx, key); err == nil {
		t.Fatal("Get escaped root")
	}
	if reader, err := store.GetReader(ctx, key); err == nil {
		reader.Close()
		t.Fatal("GetReader escaped root")
	}
	if _, err := store.URL(ctx, key); err == nil {
		t.Fatal("URL escaped root")
	}
	if _, err := store.Upload(ctx, key, []byte("overwrite"), "text/plain", "x"); err == nil {
		t.Fatal("Upload escaped root")
	}
	if err := store.Delete(ctx, key); err == nil {
		t.Fatal("Delete escaped root")
	}
	if err := store.DeletePrefix(ctx, "escape/nested"); err == nil {
		t.Fatal("DeletePrefix escaped root")
	}
	response := httptest.NewRecorder()
	store.ServeFile(response, httptest.NewRequest("GET", "/uploads/x", nil), key)
	if response.Code != 404 {
		t.Fatalf("escaped HTTP response: %d", response.Code)
	}
	if body, err := os.ReadFile(private); err != nil || string(body) != "outside-boundary" {
		t.Fatalf("outside file changed: %q %v", body, err)
	}
	if err := store.DeletePrefix(ctx, "escape"); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(private); err != nil || string(body) != "outside-boundary" {
		t.Fatalf("recursive deletion followed a symlink: %q %v", body, err)
	}
}

func TestOwnedLocalFailedWritePreservesDataAndMetadata(t *testing.T) {
	for _, blocked := range []string{"data", "metadata", "reader"} {
		t.Run(blocked, func(t *testing.T) {
			store, root := ownedLocal(t)
			ctx := context.Background()
			if _, err := store.Upload(ctx, "record", []byte("before"), "text/plain", "before.txt"); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "record")
			if blocked == "metadata" {
				target += metaSuffix
			}
			if blocked != "reader" {
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var input io.Reader = strings.NewReader("after")
			if blocked == "reader" {
				input = ownedErrorReader{}
			}
			if _, err := store.UploadFromReader(ctx, "record", input, "text/markdown", "after.md"); err == nil {
				t.Fatal("failed destination accepted write")
			}
			if blocked != "data" {
				body, err := os.ReadFile(filepath.Join(root, "record"))
				if err != nil || string(body) != "before" {
					t.Fatalf("data was lost: %q %v", body, err)
				}
			}
			if blocked != "metadata" {
				metadata, err := os.ReadFile(filepath.Join(root, "record") + metaSuffix)
				if err != nil || !strings.Contains(string(metadata), "before.txt") {
					t.Fatalf("metadata was changed: %s %v", metadata, err)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".orrery-") {
					t.Fatalf("abandoned publication file %s", entry.Name())
				}
			}
		})
	}
}

type ownedErrorReader struct{}

func (ownedErrorReader) Read([]byte) (int, error) { return 0, errors.New("source unavailable") }

func TestOwnedLocalDeletionScopesAndMissingObjects(t *testing.T) {
	store, root := ownedLocal(t)
	ctx := context.Background()
	for _, key := range []string{"run/item", "run-nonmatching/item", "other"} {
		if _, err := store.Upload(ctx, key, []byte(key), "text/plain", "x"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeletePrefix(ctx, "run/"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "run")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prefix remains: %v", err)
	}
	if _, err := store.Get(ctx, "run-nonmatching/item"); err != nil {
		t.Fatalf("sibling prefix affected: %v", err)
	}
	if err := store.DeleteKeys(ctx, []string{"../outside", "other", "missing"}); err == nil {
		t.Fatal("batch hid validation error")
	}
	if _, err := store.Get(ctx, "other"); err == nil {
		t.Fatal("batch stopped before valid deletion")
	}
	for _, key := range []string{"/", ".", "../outside", "run/../other", "record.meta.json"} {
		if err := store.DeletePrefix(ctx, key); err == nil {
			t.Fatalf("unsafe prefix %q accepted", key)
		}
	}
}

func TestOwnedLocalCancellationAndEmptyMetadataReplacement(t *testing.T) {
	store, _ := ownedLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Put(ctx, "x", "text/plain", strings.NewReader("x")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.DeleteKeys(ctx, []string{"x"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := store.Upload(context.Background(), "record", []byte("old"), "text/plain", "old.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upload(context.Background(), "record", []byte("new"), "", ""); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.Get(context.Background(), "record")
	if err != nil || artifact.Ref.Name != "artifact" || artifact.Ref.ContentType != "" {
		t.Fatalf("stale metadata: %+v %v", artifact.Ref, err)
	}
}

func TestOwnedLocalReadersWaitForTheCompletePublication(t *testing.T) {
	store, _ := ownedLocal(t)
	ctx := context.Background()
	if _, err := store.Upload(ctx, "record", []byte("before"), "text/plain", "before.txt"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	written := make(chan error, 1)
	go func() {
		_, err := store.UploadFromReader(ctx, "record", io.MultiReader(strings.NewReader("after"), ownedPausedReader{entered, release}), "text/markdown", "after.md")
		written <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not start")
	}
	read := make(chan Artifact, 1)
	readFailure := make(chan error, 1)
	go func() {
		artifact, err := store.Get(ctx, "record")
		if err != nil {
			readFailure <- err
		} else {
			read <- artifact
		}
	}()
	select {
	case <-read:
		t.Fatal("reader observed uncommitted publication")
	case err := <-readFailure:
		t.Fatal(err)
	case <-time.After(50 * time.Millisecond):
	}
	unblock.Do(func() { close(release) })
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	select {
	case artifact := <-read:
		if string(artifact.Content) != "after" || artifact.Ref.Name != "after.md" {
			t.Fatalf("inconsistent publication: %+v", artifact)
		}
	case err := <-readFailure:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not resume")
	}
}

type ownedPausedReader struct {
	entered chan struct{}
	release chan struct{}
}

func (r ownedPausedReader) Read([]byte) (int, error) { close(r.entered); <-r.release; return 0, io.EOF }

func TestOwnedLocalPublicationRollbackRestoresBothFiles(t *testing.T) {
	store, directory := ownedLocal(t)
	ctx := context.Background()
	if _, err := store.Upload(ctx, "record", []byte("before"), "text/plain", "before.txt"); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	data, err := stageLocalFile(root, "record", strings.NewReader("after"))
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := stageLocalMetadata(root, "record", localMeta{Filename: "after.md"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a destination disappearing between staging and publication.
	if err := root.Remove(metadata.staged); err != nil {
		t.Fatal(err)
	}
	if err := publishLocalFiles(root, []localPublication{data, metadata}); err == nil {
		t.Fatal("partial publication succeeded")
	}
	artifact, err := store.Get(ctx, "record")
	if err != nil || string(artifact.Content) != "before" || artifact.Ref.Name != "before.txt" {
		t.Fatalf("rollback: %+v %v", artifact, err)
	}
}

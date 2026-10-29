package filestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"orrery_v2/internal/platform/logging"
)

// Local keeps the established object/JSON-sidecar layout. All filesystem I/O
// goes through os.Root, including reads, HTTP delivery and recursive deletion.
type Local struct {
	mu          sync.RWMutex
	uploadDir   string
	baseURL     string
	clock       Clock
	idGenerator ErrorIDGenerator
	logger      logging.Logger
}

type localMeta struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
}

func NewLocal(value any) *Local {
	var cfg LocalConfig
	switch v := value.(type) {
	case LocalConfig:
		cfg = v
	case string:
		cfg.Root = v
	}
	return NewLocalStorage(cfg)
}

func NewLocalStorage(cfg LocalConfig) *Local {
	store, err := OpenLocalStorage(cfg)
	if err != nil && cfg.Logger != nil {
		cfg.Logger.Error(context.Background(), "artifact storage initialization failed", logging.String("error", err.Error()))
	}
	return store
}

func OpenLocalStorage(cfg LocalConfig) (*Local, error) {
	directory := cfg.Root
	if directory == "" {
		directory = "./data/artifacts"
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact directory: %w", err)
	}
	if err = os.MkdirAll(absolute, 0o755); err != nil {
		return nil, fmt.Errorf("initialize artifact directory: %w", err)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("open artifact directory: %w", err)
	}
	if err = root.Close(); err != nil {
		return nil, err
	}
	return &Local{uploadDir: absolute, baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		clock: defaultClock(cfg.Clock), idGenerator: defaultIDGenerator(cfg.IDGenerator), logger: cfg.Logger}, nil
}

func (s *Local) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := localObjectName(key)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	root, err := os.OpenRoot(s.uploadDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open artifact %q: %w", key, err)
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("artifact %q is not a regular file", key)
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func (s *Local) Upload(ctx context.Context, key string, body []byte, contentType, filename string) (string, error) {
	return s.UploadFromReader(ctx, key, bytes.NewReader(body), contentType, filename)
}

func (s *Local) UploadFromReader(ctx context.Context, key string, body io.Reader, contentType, filename string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, err := localObjectName(key)
	if err != nil {
		return "", err
	}
	if body == nil {
		return "", fmt.Errorf("artifact reader is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := os.OpenRoot(s.uploadDir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err = root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return "", err
	}
	data, err := stageLocalFile(root, name, contextReader{ctx, body})
	if err != nil {
		return "", err
	}
	defer root.Remove(data.staged)
	metadata, err := stageLocalMetadata(root, name, localMeta{Filename: filename, ContentType: contentType})
	if err != nil {
		return "", err
	}
	defer root.Remove(metadata.staged)
	if err = ctx.Err(); err == nil {
		err = publishLocalFiles(root, []localPublication{data, metadata})
	}
	if err != nil {
		return "", fmt.Errorf("publish artifact %q: %w", key, err)
	}
	return s.baseURL + "/uploads/" + key, nil
}

func (s *Local) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return nil
	}
	name, err := localObjectName(key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := os.OpenRoot(s.uploadDir)
	if err != nil {
		return err
	}
	defer root.Close()
	var failures []error
	for _, target := range []string{name, name + metaSuffix} {
		if err := root.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, fmt.Errorf("delete artifact %q: %w", key, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Local) DeleteKeys(ctx context.Context, keys []string) error {
	return deleteObjectKeys(ctx, keys, s.Delete)
}

func (s *Local) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(prefix) == "" {
		return nil
	}
	normalized, err := normalizeObjectPrefix(prefix)
	if err != nil {
		return err
	}
	name, err := localObjectName(strings.TrimSuffix(normalized, "/"))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := os.OpenRoot(s.uploadDir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll(name)
}

// GetFilePath is a compatibility locator. Store operations never trust this
// path for I/O; they validate the object key and use an os.Root handle.
func (s *Local) GetFilePath(key string) string {
	name, err := localObjectName(key)
	if err != nil {
		return ""
	}
	return filepath.Join(s.uploadDir, name)
}

func (s *Local) ServeFile(w http.ResponseWriter, r *http.Request, key string) {
	name, err := localObjectName(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	root, err := os.OpenRoot(s.uploadDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	metadata := loadLocalMetadata(root, name)
	if metadata.Filename != "" {
		w.Header().Set("Content-Disposition", downloadDisposition(metadata.ContentType, metadata.Filename))
	}
	if validContentType(metadata.ContentType) {
		w.Header().Set("Content-Type", metadata.ContentType)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (s *Local) CdnDomain() string {
	parsed, _ := url.Parse(s.baseURL)
	if parsed == nil {
		return ""
	}
	return parsed.Hostname()
}

func (s *Local) KeyFromURL(raw string) string {
	if key, ok := strings.CutPrefix(raw, "artifact://"); ok {
		return key
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	for _, marker := range []string{"/uploads/", "/artifacts/"} {
		if _, key, ok := strings.Cut(parsed.Path, marker); ok {
			return key
		}
	}
	return lastURLComponent(raw, parsed)
}

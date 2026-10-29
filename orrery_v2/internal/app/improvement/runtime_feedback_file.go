package improvement

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const maxRuntimeFeedbackBytes = 768 * 1024

// FileRuntimeFeedbackJournal stores one immutable, bounded terminal per attempt
// on the trusted host. The directory must be persistent and private to Orrery.
// os.Root confines operations; no raw tenant or attempt enters a file path.
type FileRuntimeFeedbackJournal struct {
	dir     string
	mu      sync.Mutex
	scanner *os.File
}

func NewFileRuntimeFeedbackJournal(dir string) (*FileRuntimeFeedbackJournal, error) {
	if !filepath.IsAbs(dir) {
		return nil, ErrInvalid
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalid
	}
	return &FileRuntimeFeedbackJournal{dir: filepath.Clean(dir)}, nil
}

func runtimeFeedbackName(e RuntimeFeedback) string {
	return Digest([]string{e.TenantID, e.Terminal.AttemptID}) + ".json"
}

func validateRuntimeFeedback(e RuntimeFeedback) error {
	end := e.Terminal
	if !validScope(e.TenantID, end.AttemptID) || !textOK(end.RequestID, 512) {
		return ErrInvalid
	}
	end.ID = ""
	if e.Terminal.ID != "runtime-terminal-evidence:"+Digest(end) {
		return ErrInvalid
	}
	return nil
}

func readRuntimeFeedback(root *os.Root, name string) (RuntimeFeedback, error) {
	var result RuntimeFeedback
	info, err := root.Lstat(name)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxRuntimeFeedbackBytes {
		return result, ErrInvalid
	}
	file, err := root.Open(name)
	if err != nil {
		return result, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxRuntimeFeedbackBytes+1))
	if err != nil {
		return result, err
	}
	if len(data) > maxRuntimeFeedbackBytes {
		return result, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || validateRuntimeFeedback(result) != nil || runtimeFeedbackName(result) != name {
		return RuntimeFeedback{}, ErrInvalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return RuntimeFeedback{}, ErrInvalid
	}
	return result, nil
}

func syncRuntimeFeedbackDir(root *os.Root) error {
	// Production is Linux. Windows does not support syncing directory handles.
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (j *FileRuntimeFeedbackJournal) Save(ctx context.Context, e RuntimeFeedback) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRuntimeFeedback(e); err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil || len(data) > maxRuntimeFeedbackBytes {
		return ErrInvalid
	}
	root, err := os.OpenRoot(j.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := ".pending-" + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name := runtimeFeedbackName(e)
	if err := root.Link(temporary, name); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		previous, readErr := readRuntimeFeedback(root, name)
		if readErr != nil {
			return readErr
		}
		if Digest(previous) != Digest(e) {
			return ErrConflict
		}
	}
	return syncRuntimeFeedbackDir(root)
}

// Scan retains a directory cursor so a corrupt record cannot permanently hide
// later records. Each call reads at most limit directory entries and bounded
// file bytes. EOF closes the cursor; the next call starts a fresh pass.
func (j *FileRuntimeFeedbackJournal) Scan(ctx context.Context, limit int) ([]RuntimeFeedback, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	// Receipts may now include bounded dispatch digests. Bound aggregate scan
	// memory as well as individual file size; the retained directory cursor
	// continues with later entries on the next sweep.
	limit = min(limit, 16)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	root, err := os.OpenRoot(j.dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if j.scanner == nil {
		j.scanner, err = root.Open(".")
		if err != nil {
			return nil, err
		}
	}
	entries, readErr := j.scanner.ReadDir(limit)
	if readErr != nil {
		_ = j.scanner.Close()
		j.scanner = nil
	}
	var failures []error
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		failures = append(failures, readErr)
	}
	var result []RuntimeFeedback
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(failures, err)...)
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		item, err := readRuntimeFeedback(root, entry.Name())
		if errors.Is(err, os.ErrNotExist) {
			continue // A successful writer or another recovery worker acknowledged it.
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("invalid runtime feedback journal entry: %w", err))
			continue
		}
		result = append(result, item)
	}
	return result, errors.Join(failures...)
}

func (j *FileRuntimeFeedbackJournal) Acknowledge(ctx context.Context, e RuntimeFeedback) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRuntimeFeedback(e); err != nil {
		return err
	}
	root, err := os.OpenRoot(j.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	name := runtimeFeedbackName(e)
	previous, err := readRuntimeFeedback(root, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if Digest(previous) != Digest(e) {
		return ErrConflict
	}
	if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRuntimeFeedbackDir(root)
}

func (j *FileRuntimeFeedbackJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.scanner == nil {
		return nil
	}
	err := j.scanner.Close()
	j.scanner = nil
	return err
}

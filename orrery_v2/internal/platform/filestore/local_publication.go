package filestore

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(buffer)
}

func localObjectName(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("empty key")
	}
	portable := strings.ReplaceAll(key, "\\", "/")
	if !fs.ValidPath(portable) || portable == "." || strings.ContainsAny(portable, "\x00:") || strings.HasSuffix(portable, metaSuffix) {
		return "", fmt.Errorf("invalid artifact key %q", key)
	}
	return filepath.Localize(portable)
}

type localPublication struct {
	name, staged, backup string
	published            bool
}

func stageLocalFile(root *os.Root, name string, source io.Reader) (localPublication, error) {
	item := localPublication{name: name, staged: filepath.Join(filepath.Dir(name), ".orrery-write-"+rand.Text())}
	file, err := root.OpenFile(item.staged, os.O_EXCL|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return item, err
	}
	_, copyErr := io.Copy(file, source)
	err = errors.Join(copyErr, file.Close())
	if err != nil {
		_ = root.Remove(item.staged)
	}
	return item, err
}

func stageLocalMetadata(root *os.Root, name string, metadata localMeta) (localPublication, error) {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return localPublication{}, err
	}
	return stageLocalFile(root, name+metaSuffix, strings.NewReader(string(encoded)))
}

// A reader holding Local.mu cannot observe half a publication. Existing files
// are retained until both replacements succeed, including on Windows where
// replacing a directory or an open destination can fail.
func publishLocalFiles(root *os.Root, files []localPublication) error {
	for _, item := range files {
		info, err := root.Lstat(item.name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact destination %q is not a regular file", item.name)
		}
	}
	rollback := func(cause error) error {
		failures := []error{cause}
		for index := len(files) - 1; index >= 0; index-- {
			item := files[index]
			if item.published {
				if err := root.Remove(item.name); err != nil && !errors.Is(err, os.ErrNotExist) {
					failures = append(failures, err)
				}
			}
			if item.backup != "" {
				if err := root.Rename(item.backup, item.name); err != nil {
					failures = append(failures, fmt.Errorf("restore %q from %q: %w", item.name, item.backup, err))
				}
			}
		}
		return errors.Join(failures...)
	}
	for index := range files {
		item := &files[index]
		backup := filepath.Join(filepath.Dir(item.name), ".orrery-backup-"+rand.Text())
		err := root.Rename(item.name, backup)
		if err == nil {
			item.backup = backup
		} else if !errors.Is(err, os.ErrNotExist) {
			return rollback(err)
		}
	}
	for index := range files {
		item := &files[index]
		if err := root.Rename(item.staged, item.name); err != nil {
			return rollback(err)
		}
		item.published = true
	}
	for _, item := range files {
		if item.backup != "" {
			_ = root.Remove(item.backup)
		}
	}
	return nil
}

func loadLocalMetadata(root *os.Root, name string) localMeta {
	file, err := root.Open(name + metaSuffix)
	if err != nil {
		return localMeta{}
	}
	defer file.Close()
	var metadata localMeta
	if json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&metadata) != nil {
		return localMeta{}
	}
	return metadata
}

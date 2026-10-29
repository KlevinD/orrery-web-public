package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Leave room beneath the managed kernel's 256KiB receipt budget, including JSON
// escaping of BOTH streams. Large output is evidence on disk, not a larger
// inline model context. Captures use bounded memory even while a command runs.
const personalAssistantShellReceiptBytes = 192 * 1024

type personalAssistantOutputStore struct {
	root *os.Root
	cwd  string
	mu   sync.Mutex
	dir  string
}

func newPersonalAssistantOutputStore(workspace, cwd string) (*personalAssistantOutputStore, error) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	rel, err := filepath.Rel(workspace, cwd)
	if err != nil {
		return nil, err
	}
	// OpenRoot also fences existing cwd symlinks and later path replacements.
	outputRoot, err := root.OpenRoot(rel)
	if err != nil {
		return nil, fmt.Errorf("personal assistant output workspace: %w", err)
	}
	return &personalAssistantOutputStore{root: outputRoot, cwd: cwd}, nil
}

func (s *personalAssistantOutputStore) create(stream string) (*os.File, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		dir := ".orrery-shell-output-" + uuid.NewString()
		if err := s.root.Mkdir(dir, 0o700); err != nil {
			return nil, "", err
		}
		s.dir = dir
	}
	name := filepath.Join(s.dir, stream)
	file, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	return file, filepath.Join(s.cwd, name), err
}

type personalAssistantOutputCapture struct {
	store   *personalAssistantOutputStore
	stream  string
	limit   int
	preview []byte
	total   int64
	digest  hash.Hash
	file    *os.File
	path    string
	err     error
}

func newPersonalAssistantOutputCapture(store *personalAssistantOutputStore, stream string, limit int) *personalAssistantOutputCapture {
	return &personalAssistantOutputCapture{store: store, stream: stream,
		limit: limit + utf8.UTFMax, digest: sha256.New()}
}

func (c *personalAssistantOutputCapture) persist() error {
	if c.path != "" {
		return nil
	}
	file, path, err := c.store.create(c.stream)
	if err != nil {
		return err
	}
	c.file, c.path = file, path
	_, err = file.Write(c.preview)
	return err
}

func (c *personalAssistantOutputCapture) Write(p []byte) (int, error) {
	if len(p) > c.limit-len(c.preview) && c.file == nil {
		if err := c.persist(); err != nil {
			c.err = err
			return 0, err
		}
	}
	if c.file != nil {
		if _, err := c.file.Write(p); err != nil {
			c.err = err
			return 0, err
		}
	}
	count := min(len(p), c.limit-len(c.preview))
	c.preview = append(c.preview, p[:count]...)
	c.total += int64(len(p))
	_, _ = c.digest.Write(p)
	return len(p), nil
}

func (c *personalAssistantOutputCapture) close() error {
	if c.file == nil {
		return nil
	}
	err := c.file.Close()
	c.file = nil
	return err
}

func (c *personalAssistantOutputCapture) addPreview(output map[string]any, limit int) error {
	text, _ := truncatePersonalAssistantOutput(string(c.preview), limit)
	returned := len(text)
	truncated := int64(returned) < c.total
	output[c.stream] = text
	output[c.stream+"_bytes"] = c.total
	output[c.stream+"_returned_bytes"] = returned
	output[c.stream+"_truncated"] = truncated
	if truncated {
		if err := c.persist(); err != nil {
			return err
		}
		output[c.stream+"_output"] = map[string]any{
			"path": c.path, "size_bytes": c.total,
			"sha256":           hex.EncodeToString(c.digest.Sum(nil)),
			"next_byte_offset": returned,
		}
	}
	return nil
}

func personalAssistantShellOutput(output map[string]any, stdout, stderr *personalAssistantOutputCapture, limit int) (map[string]any, error) {
	if stdout.err != nil {
		return nil, stdout.err
	}
	if stderr.err != nil {
		return nil, stderr.err
	}
	stdoutLimit, stderrLimit := limit, limit
	for {
		if err := stdout.addPreview(output, stdoutLimit); err != nil {
			return nil, err
		}
		if err := stderr.addPreview(output, stderrLimit); err != nil {
			return nil, err
		}
		output["truncated"] = output["stdout_truncated"] == true || output["stderr_truncated"] == true
		if output["truncated"] == true {
			output["output_notice"] = "Partial output preview. Complete streams are saved at stdout_output/stderr_output.path. Read those files in byte ranges starting at next_byte_offset; advance by returned bytes. Do not rerun the original command to recover output. A partial preview does not establish full document coverage."
		}
		raw, err := json.Marshal(output)
		if err != nil {
			return nil, err
		}
		if len(raw) <= personalAssistantShellReceiptBytes {
			return output, nil
		}
		// JSON escaping can expand control bytes sixfold. Budget the encoded
		// receipt, including metadata, rather than assuming a raw text size.
		if stdoutLimit == 0 && stderrLimit == 0 {
			return nil, fmt.Errorf("personal assistant output metadata exceeds receipt budget")
		}
		if stdoutLimit >= stderrLimit {
			stdoutLimit /= 2
		} else {
			stderrLimit /= 2
		}
	}
}

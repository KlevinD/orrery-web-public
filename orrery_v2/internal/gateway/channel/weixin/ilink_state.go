package weixin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type IlinkBotState struct {
	GetUpdatesBuf   string                `json:"get_updates_buf,omitempty"`
	SeenMessageIDs  []string              `json:"seen_message_ids,omitempty"`
	MessageFailures []IlinkMessageFailure `json:"message_failures,omitempty"`
	DeadLetters     []IlinkDeadLetter     `json:"dead_letters,omitempty"`
}

type IlinkMessageFailure struct {
	MessageKey    string `json:"message_key"`
	Attempts      int    `json:"attempts"`
	FailureStage  string `json:"failure_stage,omitempty"`
	FirstFailedAt string `json:"first_failed_at"`
	LastFailedAt  string `json:"last_failed_at"`
}

type IlinkDeadLetter struct {
	MessageKey     string `json:"message_key"`
	Attempts       int    `json:"attempts"`
	FailureStage   string `json:"failure_stage,omitempty"`
	FirstFailedAt  string `json:"first_failed_at"`
	DeadLetteredAt string `json:"dead_lettered_at"`
}

type IlinkBotStateStore interface {
	Load(ctx context.Context) (IlinkBotState, error)
	Save(ctx context.Context, state IlinkBotState) error
}

type fileIlinkBotStateStore struct {
	path string
	mu   sync.Mutex
}

func newFileIlinkBotStateStore(path string) IlinkBotStateStore {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return &fileIlinkBotStateStore{path: path}
}

func (s *fileIlinkBotStateStore) Load(ctx context.Context) (IlinkBotState, error) {
	if err := ctx.Err(); err != nil {
		return IlinkBotState{}, err
	}
	if s == nil || strings.TrimSpace(s.path) == "" {
		return IlinkBotState{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return IlinkBotState{}, nil
		}
		return IlinkBotState{}, err
	}
	var state IlinkBotState
	if err := json.Unmarshal(raw, &state); err != nil {
		return IlinkBotState{}, fmt.Errorf("decode weixin ilink state: %w", err)
	}
	return normalizeIlinkBotState(state, -1), nil
}

func (s *fileIlinkBotStateStore) Save(ctx context.Context, state IlinkBotState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	state = normalizeIlinkBotState(state, -1)

	s.mu.Lock()
	defer s.mu.Unlock()

	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(directory, "."+filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		_ = tmp.Close()
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return err
	}
	removeTemp = false
	// Windows does not support fsync on directory handles through os.File.
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync weixin ilink state directory: %w", err)
	}
	return nil
}

func normalizeIlinkBotState(state IlinkBotState, limit int) IlinkBotState {
	state.GetUpdatesBuf = strings.TrimSpace(state.GetUpdatesBuf)
	if limit == 0 {
		limit = defaultSeenMessageLimit
	}
	seen := make(map[string]struct{}, len(state.SeenMessageIDs))
	out := make([]string, 0, len(state.SeenMessageIDs))
	for _, id := range state.SeenMessageIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	state.SeenMessageIDs = out
	failures := make(map[string]struct{}, len(state.MessageFailures))
	normalizedFailures := make([]IlinkMessageFailure, 0, len(state.MessageFailures))
	for _, failure := range state.MessageFailures {
		failure.MessageKey = strings.TrimSpace(failure.MessageKey)
		failure.FailureStage = strings.TrimSpace(failure.FailureStage)
		failure.FirstFailedAt = strings.TrimSpace(failure.FirstFailedAt)
		failure.LastFailedAt = strings.TrimSpace(failure.LastFailedAt)
		if failure.MessageKey == "" || failure.Attempts <= 0 {
			continue
		}
		if _, exists := failures[failure.MessageKey]; exists {
			continue
		}
		failures[failure.MessageKey] = struct{}{}
		normalizedFailures = append(normalizedFailures, failure)
	}
	state.MessageFailures = normalizedFailures
	deadLetters := make(map[string]struct{}, len(state.DeadLetters))
	normalizedDeadLetters := make([]IlinkDeadLetter, 0, len(state.DeadLetters))
	for _, deadLetter := range state.DeadLetters {
		deadLetter.MessageKey = strings.TrimSpace(deadLetter.MessageKey)
		deadLetter.FailureStage = strings.TrimSpace(deadLetter.FailureStage)
		deadLetter.FirstFailedAt = strings.TrimSpace(deadLetter.FirstFailedAt)
		deadLetter.DeadLetteredAt = strings.TrimSpace(deadLetter.DeadLetteredAt)
		if deadLetter.MessageKey == "" || deadLetter.Attempts <= 0 {
			continue
		}
		if _, exists := deadLetters[deadLetter.MessageKey]; exists {
			continue
		}
		deadLetters[deadLetter.MessageKey] = struct{}{}
		normalizedDeadLetters = append(normalizedDeadLetters, deadLetter)
	}
	if len(normalizedDeadLetters) > defaultDeadLetterLimit {
		normalizedDeadLetters = normalizedDeadLetters[len(normalizedDeadLetters)-defaultDeadLetterLimit:]
	}
	state.DeadLetters = normalizedDeadLetters
	return state
}

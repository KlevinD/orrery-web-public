package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
)

type StagedPackageDeletion struct {
	mu       sync.Mutex
	stageDir string
	entries  []stagedPackageEntry
	done     bool
}

type stagedPackageEntry struct {
	original string
	staged   string
}

func (s *PackageStore) StagePackageDeletions(ctx context.Context, ids []agentcore.SkillID) (*StagedPackageDeletion, error) {
	if s == nil {
		return nil, fmt.Errorf("skill package store is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dir, err := s.SkillDirectory(id)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		if _, err := os.Stat(dir); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		paths = append(paths, dir)
	}
	staged := &StagedPackageDeletion{}
	if len(paths) == 0 {
		return staged, nil
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return nil, err
	}
	stageDir, err := os.MkdirTemp(s.root, ".package-delete-")
	if err != nil {
		return nil, err
	}
	staged.stageDir = stageDir
	for index, original := range paths {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, staged.rollbackUnlocked(context.Background()))
		}
		destination := filepath.Join(stageDir, fmt.Sprintf("%06d", index))
		if err := os.Rename(original, destination); err != nil {
			return nil, errors.Join(err, staged.rollbackUnlocked(context.Background()))
		}
		staged.entries = append(staged.entries, stagedPackageEntry{original: original, staged: destination})
	}
	return staged, nil
}

func (s *StagedPackageDeletion) Commit(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.stageDir != "" {
		if err := os.RemoveAll(s.stageDir); err != nil {
			return err
		}
	}
	s.done = true
	return nil
}

func (s *StagedPackageDeletion) Rollback(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return nil
	}
	return s.rollbackUnlocked(ctx)
}

func (s *StagedPackageDeletion) rollbackUnlocked(ctx context.Context) error {
	var rollbackErr error
	for index := len(s.entries) - 1; index >= 0; index-- {
		entry := s.entries[index]
		if err := ctx.Err(); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
			break
		}
		if _, err := os.Stat(entry.original); err == nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore package %s: destination already exists", entry.original))
			continue
		} else if !os.IsNotExist(err) {
			rollbackErr = errors.Join(rollbackErr, err)
			continue
		}
		if err := os.Rename(entry.staged, entry.original); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore package %s: %w", entry.original, err))
		}
	}
	if rollbackErr != nil {
		return rollbackErr
	}
	if s.stageDir != "" {
		if err := os.Remove(s.stageDir); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	s.done = true
	return nil
}

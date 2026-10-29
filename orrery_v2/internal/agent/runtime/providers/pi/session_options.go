package pi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func piSessionAffinity(opts runtimecore.ExecOptions) string {
	discussionID := strings.TrimSpace(opts.ProviderSpecific["discussion_id"])
	agentID := strings.TrimSpace(opts.AgentID.String())
	if discussionID != "" {
		return "discussion:" + discussionID + ":agent:" + agentID
	}
	if sessionID := strings.TrimSpace(opts.ResumeSessionID); sessionID != "" {
		return "session:" + sessionID
	}
	return ""
}

func stabilizePiRPCOptions(opts runtimecore.ExecOptions) (runtimecore.ExecOptions, error) {
	affinity := piSessionAffinity(opts)
	if affinity == "" {
		return opts, nil
	}
	root := firstNonEmpty(opts.WorkspacePolicy.Root, opts.Cwd)
	if root == "" {
		return opts, nil
	}
	sum := sha256.Sum256([]byte(affinity))
	home := filepath.Join(filepath.Clean(root), ".orrery", "pi-rpc", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(home, 0o700); err != nil {
		return opts, fmt.Errorf("create stable pi rpc home: %w", err)
	}
	opts.Environment = mergePiEnvironment(opts.Environment, map[string]string{"PI_CODING_AGENT_DIR": home})
	if err := stabilizePiRPCSkills(&opts, home); err != nil {
		return opts, err
	}
	return opts, nil
}

func stabilizePiRPCSkills(opts *runtimecore.ExecOptions, home string) error {
	if opts == nil {
		return nil
	}
	sources := runtimeListEnv(opts.Environment["ORRERY_PI_SKILL_PATHS"])
	if len(sources) == 0 {
		return nil
	}
	root := filepath.Join(home, "orrery-runtime", "skills")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create stable pi rpc skills: %w", err)
	}
	stable := make([]string, 0, len(sources))
	for index, source := range sources {
		digest, err := hashPiRPCDirectory(source)
		if err != nil {
			return fmt.Errorf("hash pi rpc skill %s: %w", source, err)
		}
		leaf := safePiRPCPathSegment(filepath.Base(source))
		parent := filepath.Join(root, fmt.Sprintf("%03d-%s", index, digest[:16]))
		destination := filepath.Join(parent, leaf)
		if _, err := os.Stat(destination); errors.Is(err, os.ErrNotExist) {
			temporary, err := os.MkdirTemp(root, ".skill-")
			if err != nil {
				return fmt.Errorf("stage pi rpc skill %s: %w", source, err)
			}
			if err := copyPiRPCDirectory(source, filepath.Join(temporary, leaf)); err != nil {
				_ = os.RemoveAll(temporary)
				return fmt.Errorf("copy pi rpc skill %s: %w", source, err)
			}
			if err := os.Rename(temporary, parent); err != nil {
				_ = os.RemoveAll(temporary)
				if _, statErr := os.Stat(destination); statErr != nil {
					return fmt.Errorf("publish pi rpc skill %s: %w", source, err)
				}
			}
		} else if err != nil {
			return fmt.Errorf("inspect stable pi rpc skill %s: %w", destination, err)
		}
		stable = append(stable, destination)
	}
	opts.Environment["ORRERY_PI_SKILL_PATHS"] = strings.Join(stable, string(os.PathListSeparator))
	return nil
}

func hashPiRPCDirectory(source string) (string, error) {
	hash := sha256.New()
	var walk func(string, string) error
	walk = func(path, relative string) error {
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symbolic links are not allowed: %s", entry.Name())
			}
			entryRelative := filepath.ToSlash(filepath.Join(relative, entry.Name()))
			entryPath := filepath.Join(path, entry.Name())
			if entry.IsDir() {
				_, _ = fmt.Fprintf(hash, "dir:%s\n", entryRelative)
				if err := walk(entryPath, entryRelative); err != nil {
					return err
				}
				continue
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("unsupported skill file type: %s", entryRelative)
			}
			raw, err := os.ReadFile(entryPath)
			if err != nil {
				return err
			}
			fileHash := sha256.Sum256(raw)
			_, _ = fmt.Fprintf(hash, "file:%s:executable=%t:%x\n", entryRelative, info.Mode().Perm()&0o111 != 0, fileHash)
		}
		return nil
	}
	if err := walk(source, ""); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyPiRPCDirectory(source, destination string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed: %s", entry.Name())
		}
		sourcePath := filepath.Join(source, entry.Name())
		destinationPath := filepath.Join(destination, entry.Name())
		if entry.IsDir() {
			if err := copyPiRPCDirectory(sourcePath, destinationPath); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported skill file type: %s", entry.Name())
		}
		raw, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if info.Mode().Perm()&0o111 != 0 {
			mode |= 0o100
		}
		if err := os.WriteFile(destinationPath, raw, mode); err != nil {
			return err
		}
	}
	return nil
}

func safePiRPCPathSegment(value string) string {
	value = strings.TrimSpace(value)
	var segment strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			segment.WriteRune(r)
		}
	}
	if segment.Len() == 0 {
		return "skill"
	}
	return segment.String()
}

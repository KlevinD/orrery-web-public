package httptransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	agentidentity "orrery_v2/internal/agent/identity"
	"orrery_v2/internal/app/operations"
)

var errWorkforceAvatarNotFound = errors.New("workforce avatar not found")

//go:embed workforce_avatar_defaults/manifest.json
var workforceAvatarDefaults embed.FS

type WorkforceAvatarInfo struct {
	AgentProfileID string    `json:"agent_profile_id"`
	ContentType    string    `json:"content_type"`
	Size           int64     `json:"size"`
	UpdatedAt      time.Time `json:"updated_at"`
	ETag           string    `json:"etag"`
	FileName       string    `json:"file_name"`
	Source         string    `json:"source,omitempty"`
	DefaultID      string    `json:"default_id,omitempty"`
}

type WorkforceAvatarContent struct {
	Info  WorkforceAvatarInfo
	Bytes []byte
}

type WorkforceAvatarStore interface {
	Save(ctx context.Context, agentProfileID, contentType string, content []byte) (WorkforceAvatarInfo, error)
	SetDefault(ctx context.Context, agentProfileID, defaultID string) (WorkforceAvatarInfo, error)
	Open(ctx context.Context, agentProfileID string) (WorkforceAvatarContent, error)
	OpenDefault(ctx context.Context, defaultID string) (WorkforceAvatarContent, error)
	ListDefaults(ctx context.Context) ([]WorkforceAvatarDefaultInfo, error)
	Delete(ctx context.Context, agentProfileID string) error
}

type WorkforceAvatarDefaultInfo struct {
	ID          string
	ContentType string
	Size        int64
	ETag        string
	FileName    string
}

type LocalWorkforceAvatarStore struct {
	root string
	mu   sync.RWMutex
}

type workforceAvatarManifest struct {
	Profiles map[string]workforceAvatarDefault `json:"profiles"`
}

type workforceAvatarDefault struct {
	FileName    string `json:"file"`
	ContentType string `json:"content_type"`
	content     []byte
}

var (
	defaultWorkforceAvatarsOnce sync.Once
	defaultWorkforceAvatars     map[string]workforceAvatarDefault
	defaultWorkforceAvatarsErr  error
)

func NewLocalWorkforceAvatarStore(root string) *LocalWorkforceAvatarStore {
	return &LocalWorkforceAvatarStore{root: strings.TrimSpace(root)}
}

func (s *LocalWorkforceAvatarStore) Save(ctx context.Context, agentProfileID, contentType string, content []byte) (WorkforceAvatarInfo, error) {
	if err := ctx.Err(); err != nil {
		return WorkforceAvatarInfo{}, err
	}
	agentProfileID = strings.TrimSpace(agentProfileID)
	contentType = strings.TrimSpace(contentType)
	if s == nil || s.root == "" || agentProfileID == "" || contentType == "" || len(content) == 0 {
		return WorkforceAvatarInfo{}, operationsInvalidRequest("avatar storage input is invalid")
	}

	profileKey := workforceAvatarProfileKey(agentProfileID)
	contentHash := sha256.Sum256(content)
	etag := hex.EncodeToString(contentHash[:])
	fileName := fmt.Sprintf("%s-%s.avatar", profileKey, etag[:16])
	info := WorkforceAvatarInfo{
		AgentProfileID: agentProfileID,
		ContentType:    contentType,
		Size:           int64(len(content)),
		UpdatedAt:      time.Now().UTC(),
		ETag:           etag,
		FileName:       fileName,
		Source:         "upload",
	}
	metadata, err := json.Marshal(info)
	if err != nil {
		return WorkforceAvatarInfo{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return WorkforceAvatarInfo{}, err
	}
	if err := writeWorkforceAvatarFile(filepath.Join(s.root, fileName), content, 0o644); err != nil {
		return WorkforceAvatarInfo{}, err
	}
	metadataPath := filepath.Join(s.root, profileKey+".json")
	if err := writeWorkforceAvatarFile(metadataPath, metadata, 0o644); err != nil {
		_ = os.Remove(filepath.Join(s.root, fileName))
		return WorkforceAvatarInfo{}, err
	}
	s.removeStaleFiles(profileKey, fileName)
	return info, nil
}

func (s *LocalWorkforceAvatarStore) SetDefault(ctx context.Context, agentProfileID, defaultID string) (WorkforceAvatarInfo, error) {
	if err := ctx.Err(); err != nil {
		return WorkforceAvatarInfo{}, err
	}
	agentProfileID = strings.TrimSpace(agentProfileID)
	defaultID = strings.TrimSpace(defaultID)
	if s == nil || s.root == "" || agentProfileID == "" || defaultID == "" {
		return WorkforceAvatarInfo{}, operationsInvalidRequest("avatar default input is invalid")
	}
	avatar, err := s.OpenDefault(ctx, defaultID)
	if err != nil {
		return WorkforceAvatarInfo{}, err
	}
	info := avatar.Info
	info.AgentProfileID = agentProfileID
	info.UpdatedAt = time.Now().UTC()
	info.Source = "default"
	info.DefaultID = defaultID
	metadata, err := json.Marshal(info)
	if err != nil {
		return WorkforceAvatarInfo{}, err
	}

	profileKey := workforceAvatarProfileKey(agentProfileID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return WorkforceAvatarInfo{}, err
	}
	metadataPath := filepath.Join(s.root, profileKey+".json")
	if err := writeWorkforceAvatarFile(metadataPath, metadata, 0o644); err != nil {
		return WorkforceAvatarInfo{}, err
	}
	s.removeStaleFiles(profileKey, "")
	return info, nil
}

func (s *LocalWorkforceAvatarStore) Open(ctx context.Context, agentProfileID string) (WorkforceAvatarContent, error) {
	if err := ctx.Err(); err != nil {
		return WorkforceAvatarContent{}, err
	}
	agentProfileID = strings.TrimSpace(agentProfileID)
	if s == nil || agentProfileID == "" {
		return WorkforceAvatarContent{}, errWorkforceAvatarNotFound
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.root != "" {
		content, err := s.openStored(ctx, agentProfileID)
		if err == nil {
			return content, nil
		}
		if !errors.Is(err, errWorkforceAvatarNotFound) {
			return WorkforceAvatarContent{}, err
		}
	}
	return openDefaultWorkforceAvatar(agentProfileID)
}

func (s *LocalWorkforceAvatarStore) Delete(ctx context.Context, agentProfileID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	agentProfileID = strings.TrimSpace(agentProfileID)
	if s == nil || s.root == "" || agentProfileID == "" {
		return nil
	}
	profileKey := workforceAvatarProfileKey(agentProfileID)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(filepath.Join(s.root, profileKey+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	matches, err := filepath.Glob(filepath.Join(s.root, profileKey+"-*.avatar"))
	if err != nil {
		return err
	}
	for _, match := range matches {
		if err := os.Remove(match); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *LocalWorkforceAvatarStore) OpenDefault(ctx context.Context, defaultID string) (WorkforceAvatarContent, error) {
	if err := ctx.Err(); err != nil {
		return WorkforceAvatarContent{}, err
	}
	return openDefaultWorkforceAvatar(defaultID)
}

func (s *LocalWorkforceAvatarStore) ListDefaults(ctx context.Context) ([]WorkforceAvatarDefaultInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifest, err := defaultWorkforceAvatarManifest()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(manifest))
	for id := range manifest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	defaults := make([]WorkforceAvatarDefaultInfo, 0, len(ids))
	for _, id := range ids {
		avatar, err := s.OpenDefault(ctx, id)
		if err != nil {
			return nil, err
		}
		defaults = append(defaults, WorkforceAvatarDefaultInfo{
			ID:          id,
			ContentType: avatar.Info.ContentType,
			Size:        avatar.Info.Size,
			ETag:        avatar.Info.ETag,
			FileName:    avatar.Info.FileName,
		})
	}
	return defaults, nil
}

func (s *LocalWorkforceAvatarStore) openStored(ctx context.Context, agentProfileID string) (WorkforceAvatarContent, error) {
	profileKey := workforceAvatarProfileKey(agentProfileID)
	metadata, err := os.ReadFile(filepath.Join(s.root, profileKey+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return WorkforceAvatarContent{}, errWorkforceAvatarNotFound
	}
	if err != nil {
		return WorkforceAvatarContent{}, err
	}
	var info WorkforceAvatarInfo
	if err := json.Unmarshal(metadata, &info); err != nil {
		return WorkforceAvatarContent{}, err
	}
	if info.DefaultID != "" {
		content, err := s.OpenDefault(ctx, info.DefaultID)
		if err != nil {
			return WorkforceAvatarContent{}, err
		}
		content.Info.AgentProfileID = agentProfileID
		content.Info.UpdatedAt = info.UpdatedAt
		content.Info.Source = "default"
		content.Info.DefaultID = info.DefaultID
		return content, nil
	}
	if info.AgentProfileID != agentProfileID || filepath.Base(info.FileName) != info.FileName || info.FileName == "" {
		return WorkforceAvatarContent{}, fmt.Errorf("stored workforce avatar metadata is invalid")
	}
	content, err := os.ReadFile(filepath.Join(s.root, info.FileName))
	if errors.Is(err, os.ErrNotExist) {
		return WorkforceAvatarContent{}, errWorkforceAvatarNotFound
	}
	if err != nil {
		return WorkforceAvatarContent{}, err
	}
	info.Size = int64(len(content))
	return WorkforceAvatarContent{Info: info, Bytes: content}, nil
}

func (s *LocalWorkforceAvatarStore) removeStaleFiles(profileKey, keepFile string) {
	matches, err := filepath.Glob(filepath.Join(s.root, profileKey+"-*.avatar"))
	if err != nil {
		return
	}
	for _, match := range matches {
		if filepath.Base(match) != keepFile {
			_ = os.Remove(match)
		}
	}
}

func workforceAvatarProfileKey(agentProfileID string) string {
	hash := sha256.Sum256([]byte(agentProfileID))
	return hex.EncodeToString(hash[:16])
}

func writeWorkforceAvatarFile(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func openDefaultWorkforceAvatar(agentProfileID string) (WorkforceAvatarContent, error) {
	return openDefaultWorkforceAvatarForProfile(agentProfileID, agentProfileID)
}

func openDefaultWorkforceAvatarForProfile(agentProfileID, defaultID string) (WorkforceAvatarContent, error) {
	manifest, err := defaultWorkforceAvatarManifest()
	if err != nil {
		return WorkforceAvatarContent{}, err
	}
	defaultID = agentidentity.CanonicalProfileID(defaultID)
	entry, ok := manifest[defaultID]
	if !ok || strings.TrimSpace(entry.FileName) == "" {
		return WorkforceAvatarContent{}, errWorkforceAvatarNotFound
	}
	content := bytes.Clone(entry.content)
	hash := sha256.Sum256(content)
	return WorkforceAvatarContent{
		Info: WorkforceAvatarInfo{
			AgentProfileID: agentProfileID,
			ContentType:    entry.ContentType,
			Size:           int64(len(content)),
			ETag:           hex.EncodeToString(hash[:]),
			FileName:       filepath.Base(entry.FileName),
			Source:         "default",
			DefaultID:      defaultID,
		},
		Bytes: content,
	}, nil
}

func defaultWorkforceAvatarManifest() (map[string]workforceAvatarDefault, error) {
	defaultWorkforceAvatarsOnce.Do(func() {
		content, err := workforceAvatarDefaults.ReadFile("workforce_avatar_defaults/manifest.json")
		if err != nil {
			defaultWorkforceAvatarsErr = err
			return
		}
		var manifest workforceAvatarManifest
		if err := json.Unmarshal(content, &manifest); err != nil {
			defaultWorkforceAvatarsErr = err
			return
		}
		defaultWorkforceAvatars = manifest.Profiles
		if defaultWorkforceAvatars == nil {
			defaultWorkforceAvatars = map[string]workforceAvatarDefault{}
		}
		for id, entry := range defaultWorkforceAvatars {
			if entry.ContentType != "image/png" {
				defaultWorkforceAvatarsErr = fmt.Errorf("unsupported default avatar content type for %s", id)
				return
			}
			entry.content, err = renderWorkforceAvatar(entry.FileName)
			if err != nil {
				defaultWorkforceAvatarsErr = err
				return
			}
			defaultWorkforceAvatars[id] = entry
		}
	})
	return defaultWorkforceAvatars, defaultWorkforceAvatarsErr
}

func operationsInvalidRequest(message string) error {
	return fmt.Errorf("%w: %s", operations.ErrInvalidRequest, message)
}

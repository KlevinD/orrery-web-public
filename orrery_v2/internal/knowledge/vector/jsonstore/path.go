package jsonstore

import (
	"fmt"
	"path/filepath"
	"strings"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/vector"
)

const (
	// Keep the persisted backend identifier and directory name for existing data.
	BackendName      = "lancedb"
	DefaultTableName = "chunks"
	baseDirPrefix    = "kb_"
)

func DescriptorForKnowledgeBase(root string, id core.KnowledgeBaseID, dimensions int) (vector.Descriptor, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return vector.Descriptor{}, fmt.Errorf("resolve vector root: %w", err)
	}
	baseID := string(id)
	if !safeID(baseID) {
		return vector.Descriptor{}, fmt.Errorf("%w: invalid knowledge_base_id %q", vector.ErrUnsafePath, baseID)
	}
	storePath := filepath.Join(rootAbs, baseDirPrefix+baseID, BackendName)
	storeAbs, err := filepath.Abs(storePath)
	if err != nil {
		return vector.Descriptor{}, fmt.Errorf("resolve vector store path: %w", err)
	}
	if err := ensureUnderRoot(rootAbs, storeAbs); err != nil {
		return vector.Descriptor{}, err
	}
	return vector.Descriptor{
		KnowledgeBaseID: baseID,
		Backend:         BackendName,
		RootPath:        rootAbs,
		StorePath:       storeAbs,
		TableName:       DefaultTableName,
		Dimensions:      dimensions,
	}, nil
}

func ensureUnderRoot(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return fmt.Errorf("relative vector path: %w", err)
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return fmt.Errorf("%w: %s outside %s", vector.ErrUnsafePath, targetAbs, rootAbs)
	}
	return nil
}

func safeID(id string) bool {
	if strings.TrimSpace(id) == "" {
		return false
	}
	return !strings.ContainsAny(id, `/\:`) && id != "." && id != ".." && !strings.Contains(id, "..")
}

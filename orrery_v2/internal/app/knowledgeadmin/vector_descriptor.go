package knowledgeadmin

import (
	"path/filepath"
	"strings"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/vector"
	"orrery_v2/internal/knowledge/vector/jsonstore"
)

func vectorDescriptorForBase(base core.KnowledgeBase) vector.Descriptor {
	root := ""
	if strings.TrimSpace(base.VectorStorePath) != "" {
		root = filepath.Dir(filepath.Dir(base.VectorStorePath))
	}
	return vector.Descriptor{
		KnowledgeBaseID: string(base.ID),
		Backend:         string(base.VectorBackend),
		RootPath:        root,
		StorePath:       base.VectorStorePath,
		TableName:       jsonstore.DefaultTableName,
		Dimensions:      base.EmbeddingDimensions,
	}
}

package skill

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func ImportStandardPackage(ctx context.Context, reader io.ReaderAt, size int64) (SkillDefinition, error) {
	return ImportStandardPackageWithLimits(ctx, reader, size, PackageLimits{})
}

func ImportStandardPackageWithLimits(ctx context.Context, reader io.ReaderAt, size int64, limits PackageLimits) (SkillDefinition, error) {
	root, err := os.MkdirTemp("", "orrery-skill-import-*")
	if err != nil {
		return SkillDefinition{}, err
	}
	defer os.RemoveAll(root)
	definition, err := NewPackageStoreWithLimits(root, limits).ImportPackage(ctx, reader, size)
	if err != nil {
		return SkillDefinition{}, err
	}
	definition.SourceRef = ""
	for i := range definition.PromptAssets {
		definition.PromptAssets[i].ContentRef = ""
	}
	return definition, nil
}

func ExportStandardPackage(ctx context.Context, registry Registry, id agentcore.SkillID, writer io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	definition, err := registry.Get(ctx, id)
	if err != nil {
		return err
	}
	zipWriter := zip.NewWriter(writer)
	top := skillDirectoryName(id)
	for _, asset := range definition.PromptAssets {
		if strings.TrimSpace(asset.Path) == "" {
			continue
		}
		entryName, err := safePackagePath(top + "/" + asset.Path)
		if err != nil {
			_ = zipWriter.Close()
			return err
		}
		entry, err := zipWriter.Create(entryName)
		if err != nil {
			_ = zipWriter.Close()
			return err
		}
		content, err := assetPackageContent(asset)
		if err != nil {
			_ = zipWriter.Close()
			return err
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			_ = zipWriter.Close()
			return err
		}
	}
	return zipWriter.Close()
}

func assetPackageContent(asset PromptAsset) (string, error) {
	if asset.Content != "" {
		return asset.Content, nil
	}
	if strings.HasPrefix(asset.ContentRef, "file://") {
		filePath := strings.TrimPrefix(asset.ContentRef, "file://")
		raw, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("%w: read asset %s: %v", ErrHydration, asset.Path, err)
		}
		return string(raw), nil
	}
	return "", nil
}

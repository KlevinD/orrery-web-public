package skill

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func NormalizeRiskLevel(level RiskLevel) RiskLevel {
	switch RiskLevel(strings.ToLower(strings.TrimSpace(string(level)))) {
	case RiskHigh:
		return RiskHigh
	case RiskMedium:
		return RiskMedium
	case RiskLow:
		return RiskLow
	default:
		return RiskLow
	}
}

func ClassifyRiskFromDirectory(ctx context.Context, dir string) (RiskLevel, error) {
	risk := RiskLow
	err := filepath.WalkDir(dir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if filePath == dir || entry.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(dir, filePath)
		if err != nil {
			return err
		}
		fileRisk := RiskLevelForPath(filepath.ToSlash(relPath))
		if fileRisk == RiskHigh {
			risk = RiskHigh
			return nil
		}
		if fileRisk == RiskMedium && risk != RiskHigh {
			risk = RiskMedium
		}
		return nil
	})
	if err != nil {
		return RiskLow, err
	}
	return risk, nil
}

func RiskLevelForPath(relPath string) RiskLevel {
	clean := strings.TrimSpace(filepath.ToSlash(relPath))
	if clean == "" || clean == "SKILL.md" {
		return RiskLow
	}
	ext := strings.ToLower(path.Ext(clean))
	if isExecutableExtension(ext) {
		return RiskHigh
	}
	if fileRootName(clean) == "scripts" || isScriptExtension(ext) {
		return RiskMedium
	}
	return RiskLow
}

func isExecutableExtension(ext string) bool {
	switch ext {
	case ".exe", ".com", ".msi", ".dll", ".so", ".dylib", ".appimage":
		return true
	default:
		return false
	}
}

func isScriptExtension(ext string) bool {
	switch ext {
	case ".bat", ".cmd", ".ps1", ".sh", ".bash", ".zsh", ".fish", ".py", ".pyw", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".rb", ".pl", ".php", ".lua", ".r", ".jl", ".groovy", ".kts", ".sql":
		return true
	default:
		return false
	}
}

package httptransport

import (
	"archive/zip"
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/operations"
	skillres "orrery_v2/internal/resource/skill"
)

func TestParseAgencyMarkdownCreatesSelfContainedImportedSkillRef(t *testing.T) {
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	entry, err := writer.Create("marketing/social-ads.md")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	_, err = entry.Write([]byte(`---
name: 社交广告策略师
description: 制定社交广告投放策略、素材方向和效果复盘。
---

## 规则

用中文输出策略建议。
`))
	if err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	parsed, err := parseTalentMarketZip(body.Bytes())
	if err != nil {
		t.Fatalf("parseTalentMarketZip: %v", err)
	}
	candidates := parsed.Candidates
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if len(candidates[0].SkillRefs) != 1 ||
		candidates[0].SkillRefs[0].SkillID != "imported.social-ads" ||
		candidates[0].SkillRefs[0].Alias != "社交广告策略师" ||
		candidates[0].SkillRefs[0].Level != "reference" {
		t.Fatalf("markdown import should create a self-contained imported skill ref: %+v", candidates[0].SkillRefs)
	}
}

func TestTalentMarketExportZipIncludesAndParsesSkillDefinitions(t *testing.T) {
	rec := httptest.NewRecorder()
	err := writeTalentMarketExportZip(rec, operations.TalentMarketExportPackageDTO{
		Manifest: operations.TalentMarketExportManifestDTO{Format: talentMarketZipFormat, Version: 1, Count: 1},
		Agents: []operations.TalentMarketImportCandidateDTO{{
			AgentProfileID: "profile-social-ads",
			RealName:       "社交广告策略师",
			SkillRefs: []operations.WorkforceSkillRefDTO{{
				SkillID: "skill.social-ads",
				Alias:   "社交广告策略",
			}},
		}},
		Skills: []operations.TalentMarketSkillDefinitionDTO{{
			SkillID:     "skill.social-ads",
			Name:        "社交广告策略",
			Version:     "2.0.0",
			Description: "包内 skill 定义。",
			PromptAssets: []skillres.PromptAsset{{
				Path:    "SKILL.md",
				Kind:    skillres.PromptAssetMarkdown,
				Content: "包内完整 skill 正文。",
			}},
			Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("writeTalentMarketExportZip: %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("read export zip: %v", err)
	}
	if !zipHasPackageFile(reader, "skills/skill-social-ads.json") {
		t.Fatalf("export zip files = %+v", reader.File)
	}

	parsed, err := parseTalentMarketZip(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("parseTalentMarketZip: %v", err)
	}
	if len(parsed.Candidates) != 1 || len(parsed.Skills) != 1 {
		t.Fatalf("parsed package = %+v", parsed)
	}
	if parsed.Skills[0].SkillID != "skill.social-ads" ||
		!strings.Contains(parsed.Skills[0].PromptAssets[0].Content, "包内完整 skill 正文") {
		t.Fatalf("parsed skills = %+v", parsed.Skills)
	}
}

func zipHasPackageFile(reader *zip.Reader, name string) bool {
	for _, file := range reader.File {
		if file.Name == name {
			return true
		}
	}
	return false
}

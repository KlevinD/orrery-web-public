package hil

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeBusinessApprovalMaterialDefaultsLanguageAndTrimsFields(t *testing.T) {
	material, err := NormalizeBusinessApprovalMaterial(BusinessApprovalMaterial{
		Title:           "  公众号文章发布审批  ",
		DecisionRequest: "  是否批准发布本期公众号文章？  ",
		BusinessContext: "  本周产品更新需要对外同步。  ",
		ProposedAction:  "  使用主账号发布已完成校对的文章。  ",
		ExpectedResult:  "  用户能在公众号看到本期更新。  ",
		ImpactScope:     "  公众号主账号及其订阅用户。  ",
		Risks: []BusinessApprovalRisk{{
			Description: "  内容表述可能产生歧义。  ",
			Mitigation:  "  发布前由市场负责人完成终审。  ",
		}},
	})
	if err != nil {
		t.Fatalf("NormalizeBusinessApprovalMaterial: %v", err)
	}
	if material.Language != "zh-CN" || material.Title != "公众号文章发布审批" {
		t.Fatalf("normalized material = %+v", material)
	}
	if material.Risks[0].Description != "内容表述可能产生歧义。" || material.Risks[0].Mitigation != "发布前由市场负责人完成终审。" {
		t.Fatalf("normalized risks = %+v", material.Risks)
	}
}

func TestNormalizeBusinessApprovalMaterialReportsEveryMissingField(t *testing.T) {
	_, err := NormalizeBusinessApprovalMaterial(BusinessApprovalMaterial{
		Language: "zh-CN",
		Title:    "只有标题",
		Risks:    []BusinessApprovalRisk{{Description: "有风险"}},
	})
	if !errors.Is(err, ErrApprovalMaterialRequired) {
		t.Fatalf("error = %v, want ErrApprovalMaterialRequired", err)
	}
	var materialErr *ApprovalMaterialError
	if !errors.As(err, &materialErr) {
		t.Fatalf("error = %T %v, want *ApprovalMaterialError", err, err)
	}
	want := []string{"decision_request", "business_context", "proposed_action", "expected_result", "impact_scope", "risks[0].mitigation"}
	if !reflect.DeepEqual(materialErr.Fields, want) {
		t.Fatalf("missing fields = %#v, want %#v", materialErr.Fields, want)
	}
	if !strings.HasPrefix(err.Error(), "approval_material_required:") {
		t.Fatalf("error = %q, want stable approval_material_required code", err)
	}
}

func TestNormalizeBusinessApprovalMaterialRequiresAtLeastOneCompleteRisk(t *testing.T) {
	material := completeBusinessApprovalMaterial()
	material.Risks = nil
	_, err := NormalizeBusinessApprovalMaterial(material)
	var materialErr *ApprovalMaterialError
	if !errors.As(err, &materialErr) || !reflect.DeepEqual(materialErr.Fields, []string{"risks"}) {
		t.Fatalf("error = %#v, want risks missing", err)
	}
}

func TestBusinessApprovalMaterialHashUsesNormalizedMaterial(t *testing.T) {
	first, err := NormalizeBusinessApprovalMaterial(completeBusinessApprovalMaterial())
	if err != nil {
		t.Fatalf("normalize first: %v", err)
	}
	spaced := completeBusinessApprovalMaterial()
	spaced.Title = "  " + spaced.Title + "  "
	spaced.Risks[0].Mitigation += "  "
	second, err := NormalizeBusinessApprovalMaterial(spaced)
	if err != nil {
		t.Fatalf("normalize second: %v", err)
	}
	firstHash := HashBusinessApprovalMaterial(first)
	secondHash := HashBusinessApprovalMaterial(second)
	if firstHash == "" || !strings.HasPrefix(firstHash, "sha256:") || firstHash != secondHash {
		t.Fatalf("hashes = %q and %q", firstHash, secondHash)
	}
	second.ExpectedResult = "获得不同结果"
	if changed := HashBusinessApprovalMaterial(second); changed == firstHash {
		t.Fatalf("changed hash = %q, want value different from %q", changed, firstHash)
	}
}

func completeBusinessApprovalMaterial() BusinessApprovalMaterial {
	return BusinessApprovalMaterial{
		Language:        "zh-CN",
		Title:           "公众号文章发布审批",
		DecisionRequest: "是否批准发布本期公众号文章？",
		BusinessContext: "本周产品更新需要对外同步。",
		ProposedAction:  "使用主账号发布已完成校对的文章。",
		ExpectedResult:  "用户能在公众号看到本期更新。",
		ImpactScope:     "公众号主账号及其订阅用户。",
		Risks: []BusinessApprovalRisk{{
			Description: "内容表述可能产生歧义。",
			Mitigation:  "发布前由市场负责人完成终审。",
		}},
	}
}

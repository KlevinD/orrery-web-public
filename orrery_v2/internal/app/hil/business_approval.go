package hil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

var ErrApprovalMaterialRequired = errors.New("approval_material_required")

type BusinessApprovalRisk struct {
	Description string `json:"description"`
	Mitigation  string `json:"mitigation"`
}

type BusinessApprovalMaterial struct {
	Language        string                 `json:"language"`
	Title           string                 `json:"title"`
	DecisionRequest string                 `json:"decision_request"`
	BusinessContext string                 `json:"business_context"`
	ProposedAction  string                 `json:"proposed_action"`
	ExpectedResult  string                 `json:"expected_result"`
	ImpactScope     string                 `json:"impact_scope"`
	Risks           []BusinessApprovalRisk `json:"risks"`
}

type ApprovalMaterialError struct {
	Fields []string
}

func (e *ApprovalMaterialError) Error() string {
	if e == nil || len(e.Fields) == 0 {
		return ErrApprovalMaterialRequired.Error()
	}
	return ErrApprovalMaterialRequired.Error() + ": " + strings.Join(e.Fields, ", ")
}

func (e *ApprovalMaterialError) Unwrap() error {
	return ErrApprovalMaterialRequired
}

func NormalizeBusinessApprovalMaterial(input BusinessApprovalMaterial) (BusinessApprovalMaterial, error) {
	material := BusinessApprovalMaterial{
		Language:        strings.TrimSpace(input.Language),
		Title:           strings.TrimSpace(input.Title),
		DecisionRequest: strings.TrimSpace(input.DecisionRequest),
		BusinessContext: strings.TrimSpace(input.BusinessContext),
		ProposedAction:  strings.TrimSpace(input.ProposedAction),
		ExpectedResult:  strings.TrimSpace(input.ExpectedResult),
		ImpactScope:     strings.TrimSpace(input.ImpactScope),
		Risks:           make([]BusinessApprovalRisk, len(input.Risks)),
	}
	if material.Language == "" {
		material.Language = "zh-CN"
	}
	for index, risk := range input.Risks {
		material.Risks[index] = BusinessApprovalRisk{
			Description: strings.TrimSpace(risk.Description),
			Mitigation:  strings.TrimSpace(risk.Mitigation),
		}
	}

	missing := make([]string, 0, 8)
	for _, field := range []struct {
		name  string
		value string
	}{
		{"title", material.Title},
		{"decision_request", material.DecisionRequest},
		{"business_context", material.BusinessContext},
		{"proposed_action", material.ProposedAction},
		{"expected_result", material.ExpectedResult},
		{"impact_scope", material.ImpactScope},
	} {
		if field.value == "" {
			missing = append(missing, field.name)
		}
	}
	if len(material.Risks) == 0 {
		missing = append(missing, "risks")
	}
	for index, risk := range material.Risks {
		if risk.Description == "" {
			missing = append(missing, riskField(index, "description"))
		}
		if risk.Mitigation == "" {
			missing = append(missing, riskField(index, "mitigation"))
		}
	}
	if len(missing) > 0 {
		return BusinessApprovalMaterial{}, &ApprovalMaterialError{Fields: missing}
	}
	return material, nil
}

func HashBusinessApprovalMaterial(material BusinessApprovalMaterial) string {
	encoded, _ := json.Marshal(material)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func riskField(index int, field string) string {
	return "risks[" + strconv.Itoa(index) + "]." + field
}

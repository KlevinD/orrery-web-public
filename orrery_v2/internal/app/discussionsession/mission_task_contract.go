package discussionsession

import (
	"encoding/json"
	"fmt"
	"strings"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func (l coordinatorLane) expectedDeliverables() []missioncontrol.ExpectedDeliverableCommand {
	return []missioncontrol.ExpectedDeliverableCommand{{Title: l.goal()}}
}

func (l coordinatorLane) acceptanceCriteria() []string {
	criteria := nonEmptyStrings(l.AcceptanceCriteria)
	if len(criteria) > 0 {
		return criteria
	}
	return []string{"The lane goal is complete and its deliverable is verifiable: " + l.goal()}
}

func (l coordinatorLane) evidenceRequirements() []missioncontrol.EvidenceRequirementCommand {
	return taskEvidenceRequirements(nonEmptyStrings(l.RequiredEvidence))
}

func stringListFromTool(call ToolCallResult, keys ...string) []string {
	raw := firstToolValue(call, keys...)
	if raw == "" {
		return nil
	}
	var decoded []string
	if json.Unmarshal([]byte(raw), &decoded) == nil {
		return nonEmptyStrings(decoded)
	}
	return nonEmptyStrings(strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ';' || r == '|'
	}))
}

func evidenceRequirementsFromTool(call ToolCallResult) []missioncontrol.EvidenceRequirementCommand {
	return taskEvidenceRequirements(stringListFromTool(call, "required_evidence", "evidence_requirements"))
}

func taskEvidenceRequirements(items []string) []missioncontrol.EvidenceRequirementCommand {
	out := make([]missioncontrol.EvidenceRequirementCommand, 0, len(items))
	for index, item := range items {
		out = append(out, missioncontrol.EvidenceRequirementCommand{
			ID: fmt.Sprintf("evidence-%d", index+1), Description: item, Kind: "artifact", Required: true,
		})
	}
	return out
}

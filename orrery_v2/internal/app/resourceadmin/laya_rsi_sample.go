package resourceadmin

import (
	"encoding/json"

	"orrery_v2/internal/decision"
)

// RSILearningSample is an observation, never a SkillVersionEvaluation. A worker
// can reread evidence by reference before generating an improvement candidate.
type RSILearningSample struct {
	Scope       string
	EvidenceRef string
	Tool        string
	ErrorCode   string
	Summary     string
	SkillRefs   []string
	// Evidence is retained locally in full; it is never sent to Laya.
	Evidence json.RawMessage `json:"evidence,omitempty"`
}

func BuildDecisionRSISampleJob(sample RSILearningSample) decision.Job {
	full, _ := json.Marshal(sample)
	return decision.Job{ObservationID: decision.Hash(string(full)), Kind: "rsi_sample", Scope: sample.Scope, PromptVersion: "rsi-failure-triage-v1",
		InputTruncated: len(sample.Tool) > 64 || len(sample.ErrorCode) > 64 || len(sample.Summary) > 200,
		Sources:        []string{sample.EvidenceRef}, InputHashes: []string{decision.Hash(string(full))},
		States: []string{"Tool: " + decision.Excerpt(sample.Tool, 64) + "\nCode: " + decision.Excerpt(sample.ErrorCode, 64) + "\nObservation: " + decision.Excerpt(sample.Summary, 200)},
		Question: decision.Question{Type: "choice", Instructions: "Classify observed failure. Select insufficient when cause is not evidenced.",
			Criteria: map[string]string{
				"skill_defect": "evidenced reusable skill logic defect",
				"tool_usage":   "wrong tool or arguments",
				"environment":  "temporary network or service failure",
				"insufficient": "missing information or uncertain cause",
			}},
	}
}

// Compatibility with existing callers.
func BuildLayaRSISampleJob(sample RSILearningSample) decision.Job {
	return BuildDecisionRSISampleJob(sample)
}

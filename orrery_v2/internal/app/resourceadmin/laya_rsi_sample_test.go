package resourceadmin

import (
	"strings"
	"testing"

	"orrery_v2/internal/decision/laya"
)

func TestLayaRSISampleRetainsEvidenceAndHashesUnabridgedObservation(t *testing.T) {
	sample := RSILearningSample{Scope: "tenant:a/agent:a", EvidenceRef: "run:a/tool:b", Tool: "query_order", Summary: strings.Repeat("事", 200), SkillRefs: []string{"skill@v1"}}
	one := BuildLayaRSISampleJob(sample)
	sample.Summary += "new evidence"
	two := BuildLayaRSISampleJob(sample)
	if one.Kind != "rsi_sample" || one.Sources[0] != sample.EvidenceRef || one.States[0] != two.States[0] || one.InputHashes[0] == two.InputHashes[0] {
		t.Fatal("lost immutable source identity")
	}
	if len(one.States[0]) > 400 || len(one.InputHashes[0]) != 64 || laya.Hash(sample.Summary) == one.InputHashes[0] {
		t.Fatal("invalid bounded sample or hash")
	}
}

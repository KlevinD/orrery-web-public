package main

import (
	"testing"
)

func writePiMissionResultRuntimeFixture(t *testing.T) string {
	t.Helper()
	message := "Backend runtime completed the assigned work.\n\n```orrery-agent-result\n" +
		`{"version":1,"summary":"Backend runtime completed the assigned work.","deliverables":[{"expected_deliverable_id":"expected-1","source_kind":"runtime_output","source_ref":""}],"evidence":[]}` +
		"\n```"
	return buildPiRuntimeFixture(t, piRuntimeFixtureSpec{MessageEndText: message})
}

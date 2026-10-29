package discussionsession

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunDTOExposesTraceAvailabilityWithoutInternalRef(t *testing.T) {
	dto := mapRuns([]RunRecord{{
		RunID: "run-1", ConversationID: "conv-1", AgentID: "agent-1",
		TraceRef: "artifact://private/trace", TraceVersion: 2, TraceStatus: "hot",
	}})[0]
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"trace_available":true`)) || !bytes.Contains(encoded, []byte(`"trace_status":"hot"`)) {
		t.Fatalf("dto=%s", encoded)
	}
	if bytes.Contains(encoded, []byte("artifact://private/trace")) {
		t.Fatalf("internal ref leaked: %s", encoded)
	}
}

func TestApplyResponseDebugRecordsSanitizedTraceMetadata(t *testing.T) {
	service := &Service{}
	run := RunRecord{RunID: "run-1", ConversationID: "conv-1"}
	service.applyResponseDebug(&run, DiscussionAgentResponse{
		TraceRef:        "artifact://private/trace-v2",
		RuntimeResponse: "Authorization: Bearer provider-token-value",
	})
	if run.TraceRef != "artifact://private/trace-v2" || run.TraceVersion != 2 || run.TraceStatus != "hot" {
		t.Fatalf("trace metadata=%+v", run)
	}
	if run.Debug == nil || !run.Debug.Redacted || strings.Contains(run.Debug.RuntimeResponse, "provider-token-value") {
		t.Fatalf("debug was not sanitized: %+v", run.Debug)
	}
}

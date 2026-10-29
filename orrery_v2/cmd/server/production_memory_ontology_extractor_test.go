package main

import (
	"net/http"
	"testing"
	"time"
)

func TestMemoryOntologyModelRequestUsesLowReasoning(t *testing.T) {
	request := memoryOntologyModelRequest()
	if request.LLMProfileID != "" ||
		request.Model != "" || request.ReasoningEffort != "low" {
		t.Fatalf("request = %+v", request)
	}
}

func TestMemoryOntologyHTTPClientIsBounded(t *testing.T) {
	client := memoryOntologyHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil {
		t.Fatalf("transport = %#v, want environment-aware HTTP transport", client.Transport)
	}
	if got := client.Timeout; got != 20*time.Second {
		t.Fatalf("timeout = %s, want 20s", got)
	}
}

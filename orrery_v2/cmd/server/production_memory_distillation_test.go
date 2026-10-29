package main

import (
	"net/http"
	"testing"
	"time"
)

func TestProductionMemoryNaturalLanguageModelRequestInheritsPlatformDefault(t *testing.T) {
	request := memoryNaturalLanguageModelRequest()
	if request.LLMProfileID != "" || request.Model != "" || request.ReasoningEffort != "max" {
		t.Fatalf("model request = %+v", request)
	}
}

func TestProductionMemoryNaturalLanguageHTTPClientUsesEnvironmentProxy(t *testing.T) {
	client := memoryNaturalLanguageHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil {
		t.Fatalf("transport = %#v, want environment-aware HTTP transport", client.Transport)
	}
	if client.Timeout != 4*time.Minute {
		t.Fatalf("timeout = %s, want 4m", client.Timeout)
	}
}

func TestProductionMemoryNaturalLanguageLocaleUsesRequestThenPlatformDefault(t *testing.T) {
	if got := memoryNaturalLanguageLocale("en-US", "zh-CN"); got != "en-US" {
		t.Fatalf("requested locale = %q", got)
	}
	if got := memoryNaturalLanguageLocale("", "en-US"); got != "en-US" {
		t.Fatalf("platform locale = %q", got)
	}
	if got := memoryNaturalLanguageLocale("", ""); got != "zh-CN" {
		t.Fatalf("default locale = %q", got)
	}
}

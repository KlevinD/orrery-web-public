package codex

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestThreadOpenPinsPlatformSelectedModel(t *testing.T) {
	s := &NativeSession{config: agentcore.SessionConfig{Model: "gpt-6-luna"}}
	if got := s.threadOpenParams()["model"]; got != "gpt-6-luna" {
		t.Fatalf("thread model=%v", got)
	}
	s.config.Model = ""
	if _, exists := s.threadOpenParams()["model"]; exists {
		t.Fatal("empty override must retain provider default")
	}
}

package outputpolicy

import "testing"

func TestChatTextNeverSynthesizesFallback(t *testing.T) {
	for _, input := range []string{"", "<think>private</think>", "```orrery-agent-result\nsecret\n```", "local_path=/data/private/secret"} {
		if got := ChatText(input); got != "" {
			t.Fatalf("private output became a chat reply: %q", got)
		}
	}
	if got := ChatText("网页超时，尚未取得内容。"); got != "网页超时，尚未取得内容。" {
		t.Fatalf("Agent answer changed: %q", got)
	}
}

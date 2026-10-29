package process

import "testing"

func TestFilterArgsRemovesBlockedValueAndStandaloneFlags(t *testing.T) {
	blocked := map[string]BlockedArgMode{
		"--listen":   BlockedWithValue,
		"--danger":   BlockedStandalone,
		"--mcp-file": BlockedWithValue,
	}

	got := FilterArgs([]string{"--listen", "stdio://", "--model", "o3", "--danger", "--mcp-file=config.json", "--safe"}, blocked)

	want := []string{"--model", "o3", "--safe"}
	if len(got) != len(want) {
		t.Fatalf("args = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %#v", got)
		}
	}
}

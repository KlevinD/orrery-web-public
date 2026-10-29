package outbound

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitWithCodeFences(t *testing.T) {
	t.Run("short text", func(t *testing.T) {
		text := "Hello world"
		out := SplitWithCodeFences(text, 100)
		if !reflect.DeepEqual(out, []string{text}) {
			t.Errorf("Expected %v, got %v", []string{text}, out)
		}
	})

	t.Run("split plain text", func(t *testing.T) {
		text := strings.Repeat("A", 40)
		out := SplitWithCodeFences(text, 30)
		if !reflect.DeepEqual(out, []string{strings.Repeat("A", 30), strings.Repeat("A", 10)}) {
			t.Errorf("unexpected chunks: %#v", out)
		}
	})

	t.Run("split with code fences", func(t *testing.T) {
		text := "Line 1\n```go\nfunc main() {\n\tfmt.Println(\"Hello\")\n}\n```\nLine 2"
		out := SplitWithCodeFences(text, 40)

		if len(out) != 2 {
			t.Fatalf("Expected 2 chunks, got %d", len(out))
		}

		if !strings.HasSuffix(out[0], "```") {
			t.Errorf("First chunk should close the fence, got: %s", out[0])
		}

		if !strings.HasPrefix(out[1], "```go") {
			t.Errorf("Second chunk should reopen the fence, got: %s", out[1])
		}
	})

	t.Run("split with heading", func(t *testing.T) {
		text := strings.Repeat("A", 30) + "\n# Heading\nMore text"
		out := SplitWithCodeFences(text, 35)

		if len(out) != 2 {
			t.Fatalf("Expected 2 chunks, got %d", len(out))
		}

		if strings.Contains(out[0], "# Heading") {
			t.Errorf("First chunk should break before heading, got: %s", out[0])
		}
		if !strings.HasPrefix(out[1], "# Heading") {
			t.Errorf("Second chunk should start with heading, got: %s", out[1])
		}
	})

	t.Run("hard splits a long line inside a code fence", func(t *testing.T) {
		text := "```go\n" + strings.Repeat("x", 90) + "\n```"
		out := SplitWithCodeFences(text, 30)
		if len(out) < 4 {
			t.Fatalf("expected multiple chunks, got %#v", out)
		}
		for index, chunk := range out {
			if len([]rune(chunk)) > 30 {
				t.Fatalf("chunk %d has %d runes: %q", index, len([]rune(chunk)), chunk)
			}
			if !strings.HasPrefix(chunk, "```go") || !strings.HasSuffix(chunk, "```") {
				t.Fatalf("chunk %d does not contain a complete fence: %q", index, chunk)
			}
		}
	})

	t.Run("counts unicode characters instead of bytes", func(t *testing.T) {
		text := strings.Repeat("你", 17)
		out := SplitWithCodeFences(text, 6)
		if len(out) != 3 || len([]rune(out[0])) != 6 || len([]rune(out[1])) != 6 || len([]rune(out[2])) != 5 {
			t.Fatalf("unexpected unicode chunks: %#v", out)
		}
		if strings.Join(out, "") != text {
			t.Fatalf("unicode content changed: %#v", out)
		}
	})

	t.Run("non-positive limit is safe", func(t *testing.T) {
		if out := SplitWithCodeFences("content", 0); out != nil {
			t.Fatalf("expected nil chunks, got %#v", out)
		}
	})

	t.Run("preserves leading and empty lines", func(t *testing.T) {
		text := "\n\nfirst\n\nsecond"
		out := SplitWithCodeFences(text, 8)
		if strings.Join(out, "") != text {
			t.Fatalf("line breaks changed: %#v", out)
		}
		for _, chunk := range out {
			if len([]rune(chunk)) > 8 {
				t.Fatalf("oversized chunk: %q", chunk)
			}
		}
	})

	t.Run("tiny code fence limit falls back without looping", func(t *testing.T) {
		text := "```go\nx\n```"
		out := SplitWithCodeFences(text, 10)
		if strings.Join(out, "") != text {
			t.Fatalf("fallback changed content: %#v", out)
		}
		for _, chunk := range out {
			if len([]rune(chunk)) > 10 {
				t.Fatalf("oversized fallback chunk: %q", chunk)
			}
		}
	})

	t.Run("one-rune limit preserves newlines", func(t *testing.T) {
		text := "a\nb"
		out := SplitWithCodeFences(text, 1)
		if strings.Join(out, "") != text {
			t.Fatalf("one-rune chunks changed content: %#v", out)
		}
		for _, chunk := range out {
			if len([]rune(chunk)) != 1 {
				t.Fatalf("unexpected one-rune chunk: %q", chunk)
			}
		}
	})
}

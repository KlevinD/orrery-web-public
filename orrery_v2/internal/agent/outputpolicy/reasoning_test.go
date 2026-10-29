package outputpolicy

import (
	"strings"
	"testing"
)

func TestFilterPrivateReasoning(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		want       string
		suppressed bool
	}{
		{name: "plain", input: "public answer", want: "public answer"},
		{name: "block", input: "before <think>private</think> after", want: "before  after", suppressed: true},
		{name: "case insensitive", input: "<THINK>private</ThInK>public", want: "public", suppressed: true},
		{name: "multiple blocks", input: "a<think>x</think>b<think>y</think>c", want: "abc", suppressed: true},
		{name: "unfinished block", input: "public<think>private", want: "public", suppressed: true},
		{name: "orphan close", input: "</think>public", want: "public", suppressed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := FilterPrivateReasoning(test.input)
			if got.Text != test.want || got.Suppressed != test.suppressed {
				t.Fatalf("FilterPrivateReasoning(%q) = %+v, want text %q suppressed %v", test.input, got, test.want, test.suppressed)
			}
		})
	}
}

func TestPrivateReasoningFilterHandlesSplitTags(t *testing.T) {
	filter := &PrivateReasoningFilter{}
	var output strings.Builder
	for _, chunk := range []string{"visible <th", "ink>private", " content</thi", "nk>answer"} {
		output.WriteString(filter.Write(chunk))
	}
	output.WriteString(filter.Finish())

	result := filter.Result(output.String())
	if result.Text != "visible answer" {
		t.Fatalf("filtered text = %q", result.Text)
	}
	if !result.Suppressed || result.SuppressedBytes != int64(len("<think>private content</think>")) {
		t.Fatalf("filter audit = %+v", result)
	}
}

func TestPrivateReasoningFilterSuppressesTrailingPartialTag(t *testing.T) {
	filter := &PrivateReasoningFilter{}
	got := filter.Write("public<thi") + filter.Finish()
	if got != "public" || !filter.Result("").Suppressed {
		t.Fatalf("partial tag result = %q, audit = %+v", got, filter.Result(""))
	}
}

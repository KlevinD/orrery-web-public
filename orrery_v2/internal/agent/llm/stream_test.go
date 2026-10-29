package llm

import (
	"errors"
	"io"
	"testing"
	"time"
)

func TestReaderCloseReleasesProducer(t *testing.T) {
	reader, writer := MessagePipe(0)
	done := make(chan bool, 1)
	go func() { done <- writer.Send(UserMessage("late"), nil) }()
	reader.Close()
	select {
	case closed := <-done:
		if !closed {
			t.Fatal("producer reported delivery after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled reader left producer blocked")
	}
	writer.Close()
	if _, err := reader.Recv(); err != io.EOF {
		t.Fatalf("closed stream: %v", err)
	}
}

func TestWriterCloseDrainsBufferedItemsAndPreservesErrors(t *testing.T) {
	reader, writer := MessagePipe(2)
	failure := errors.New("provider disconnected")
	if writer.Send(AssistantMessage("first", nil), nil) || writer.Send(nil, failure) {
		t.Fatal("stream closed early")
	}
	writer.Close()
	message, err := reader.Recv()
	if err != nil || message.Content != "first" {
		t.Fatalf("first item: %+v %v", message, err)
	}
	if _, err = reader.Recv(); !errors.Is(err, failure) {
		t.Fatalf("provider error lost: %v", err)
	}
	if _, err = reader.Recv(); err != io.EOF {
		t.Fatalf("stream not exhausted: %v", err)
	}
	if !writer.Send(UserMessage("too late"), nil) {
		t.Fatal("send to closed writer succeeded")
	}
}

func TestWriterCloseReleasesBlockedSend(t *testing.T) {
	reader, writer := MessagePipe(0)
	done := make(chan bool, 1)
	go func() { done <- writer.Send(UserMessage("pending"), nil) }()
	writer.Close()
	select {
	case closed := <-done:
		if !closed {
			t.Fatal("undelivered message reported sent")
		}
	case <-time.After(time.Second):
		t.Fatal("writer close deadlocked")
	}
	if _, err := reader.Recv(); err != io.EOF {
		t.Fatalf("stream not closed: %v", err)
	}
}

func TestMergeToolDeltasPreservesOrderIdentityAndOwnership(t *testing.T) {
	zero, one := 0, 1
	chunks := []*Message{
		{Role: Assistant, Content: "checking", ToolCalls: []ToolCall{
			{Index: &one, ID: "b", Function: FunctionCall{Name: "lookup", Arguments: `{"id":`}},
			{Index: &zero, ID: "a", Function: FunctionCall{Name: "search", Arguments: `{"q":"`}},
		}},
		{Content: " sources", ToolCalls: []ToolCall{
			{Index: &zero, Function: FunctionCall{Arguments: `orrery"}`}},
			{Index: &one, Function: FunctionCall{Arguments: `2}`}},
		}, ResponseMeta: &ResponseMeta{FinishReason: "tool_calls", Usage: &TokenUsage{TotalTokens: 12}}},
	}
	merged, err := MergeChunks(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Content != "checking sources" || len(merged.ToolCalls) != 2 || merged.ToolCalls[0].ID != "a" || merged.ToolCalls[0].Function.Arguments != `{"q":"orrery"}` || merged.ToolCalls[1].Function.Arguments != `{"id":2}` {
		t.Fatalf("bad deltas: %+v", merged)
	}
	zero = 99
	chunks[1].ResponseMeta.Usage.TotalTokens = 100
	if *merged.ToolCalls[0].Index != 0 || merged.ResponseMeta.Usage.TotalTokens != 12 {
		t.Fatal("provider mutated accumulated response")
	}
}

func TestMergeRejectsAmbiguousToolIdentityAndMixedRoles(t *testing.T) {
	index := 0
	for _, chunks := range [][]*Message{
		{nil},
		{{Role: Assistant}, {Role: ToolRole}},
		{{ToolCalls: []ToolCall{{Index: &index, ID: "a"}}}, {ToolCalls: []ToolCall{{Index: &index, ID: "b"}}}},
	} {
		if _, err := MergeChunks(chunks); err == nil {
			t.Fatal("ambiguous response accepted")
		}
	}
}

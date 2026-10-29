package cliinternal

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/process"
)

func TestStreamSessionParsesOutputAndAddsProviderErrorContext(t *testing.T) {
	if os.Getenv("ORRERY_CLIINTERNAL_HELPER") == "1" {
		fmt.Println(`{"text":"partial"}`)
		fmt.Fprintln(os.Stderr, "provider boom")
		os.Exit(7)
	}
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestStreamSessionParsesOutputAndAddsProviderErrorContext")
	cmd.Env = append(os.Environ(), "ORRERY_CLIINTERNAL_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	stderrTail := process.NewStderrTail(nil, 2048)
	cmd.Stderr = stderrTail
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	session := StreamSession(StreamOptions{
		Context:     ctx,
		Cancel:      func() {},
		Command:     cmd,
		Stdout:      stdout,
		StderrTail:  stderrTail,
		Provider:    "helper",
		StderrLabel: "helper",
		Timeout:     time.Minute,
		NewParser: func() Parser {
			return &jsonTextParser{}
		},
	})

	result := <-session.Result
	if result.Status != runtimecore.ResultStatusFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	if result.Output != "partial" {
		t.Fatalf("output = %q", result.Output)
	}
	if result.Error == "" || !containsAll(result.Error, "helper exited with error", "[helper stderr]", "provider boom") {
		t.Fatalf("error missing provider context: %q", result.Error)
	}
}

func TestMergeEnvLetsRightOverrideLeft(t *testing.T) {
	got := MergeEnv(map[string]string{"A": "1", "B": "left"}, map[string]string{"B": "right", "C": "3"})
	if got["A"] != "1" || got["B"] != "right" || got["C"] != "3" {
		t.Fatalf("merged env = %+v", got)
	}
}

func TestSendWaitsForDeliveryOrCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	messages := make(chan runtimecore.RuntimeMessage)
	done := make(chan error, 1)
	go func() {
		done <- Send(ctx, messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeText, Content: "must not be dropped"})
	}()
	select {
	case err := <-done:
		t.Fatalf("send returned before delivery or cancellation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("send error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("send did not unblock on cancellation")
	}
}

func TestStreamSessionTurnsParserErrorsIntoVisibleFailure(t *testing.T) {
	if os.Getenv("ORRERY_CLIINTERNAL_PARSE_HELPER") == "1" {
		fmt.Println("No API key found for the selected model.")
		return
	}
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestStreamSessionTurnsParserErrorsIntoVisibleFailure")
	cmd.Env = append(os.Environ(), "ORRERY_CLIINTERNAL_PARSE_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	session := StreamSession(StreamOptions{
		Context:  ctx,
		Cancel:   func() {},
		Command:  cmd,
		Stdout:   stdout,
		Provider: "helper",
		Timeout:  time.Minute,
		NewParser: func() Parser {
			return parseErrorParser{}
		},
	})

	var messages []runtimecore.RuntimeMessage
	for message := range session.Messages {
		messages = append(messages, message)
	}
	result := <-session.Result
	if result.Status != runtimecore.ResultStatusFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	if !containsAll(result.Error, "helper stdout parse failed", "No API key found for the selected model.") {
		t.Fatalf("error missing stdout parse context: %q", result.Error)
	}
	if len(messages) != 1 || messages[0].Type != runtimecore.MessageTypeError ||
		!strings.Contains(messages[0].Content, "No API key found for the selected model.") {
		t.Fatalf("messages = %#v, want visible parser error", messages)
	}
}

func TestStreamSessionPrefersParserErrorOverEmptyOutputFailure(t *testing.T) {
	if os.Getenv("ORRERY_CLIINTERNAL_PARSE_AFTER_STATUS_HELPER") == "1" {
		fmt.Println(`{"type":"session","id":"session-1"}`)
		fmt.Println("No API key found for minimax-cn.")
		return
	}
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestStreamSessionPrefersParserErrorOverEmptyOutputFailure")
	cmd.Env = append(os.Environ(), "ORRERY_CLIINTERNAL_PARSE_AFTER_STATUS_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	session := StreamSession(StreamOptions{
		Context:  ctx,
		Cancel:   func() {},
		Command:  cmd,
		Stdout:   stdout,
		Provider: "pi",
		Timeout:  time.Minute,
		NewParser: func() Parser {
			return &emptyOutputAfterStatusParser{}
		},
	})

	var messages []runtimecore.RuntimeMessage
	for message := range session.Messages {
		messages = append(messages, message)
	}
	result := <-session.Result
	if result.Status != runtimecore.ResultStatusFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	if !containsAll(result.Error, "pi stdout parse failed", "No API key found for minimax-cn.") ||
		strings.Contains(result.Error, "completed without assistant output") {
		t.Fatalf("error = %q, want stdout parser failure over empty-output fallback", result.Error)
	}
	if len(messages) == 0 || messages[len(messages)-1].Type != runtimecore.MessageTypeError ||
		!strings.Contains(messages[len(messages)-1].Content, "No API key found for minimax-cn.") {
		t.Fatalf("messages = %#v, want visible stdout parser error", messages)
	}
}

func TestStreamSessionFailsOnParserErrorAfterValidOutput(t *testing.T) {
	if os.Getenv("ORRERY_CLIINTERNAL_PARTIAL_PARSE_HELPER") == "1" {
		fmt.Println(`{"text":"partial"}`)
		fmt.Println(`{"text":`)
		return
	}
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestStreamSessionFailsOnParserErrorAfterValidOutput")
	cmd.Env = append(os.Environ(), "ORRERY_CLIINTERNAL_PARTIAL_PARSE_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	session := StreamSession(StreamOptions{
		Context: ctx, Cancel: func() {}, Command: cmd, Stdout: stdout, Provider: "helper", Timeout: time.Minute,
		NewParser: func() Parser { return &strictJSONTextParser{} },
	})
	var messages []runtimecore.RuntimeMessage
	for message := range session.Messages {
		messages = append(messages, message)
	}
	result := <-session.Result
	if result.Status != runtimecore.ResultStatusFailed || !containsAll(result.Error, "stdout parse failed", `{"text":`) {
		t.Fatalf("result = %#v, want protocol failure after partial output", result)
	}
	if len(messages) < 2 || messages[len(messages)-1].Type != runtimecore.MessageTypeError {
		t.Fatalf("messages = %#v, want partial text followed by protocol error", messages)
	}
}

func TestStreamSessionBackpressuresWithoutDroppingMessages(t *testing.T) {
	if os.Getenv("ORRERY_CLIINTERNAL_BACKPRESSURE_HELPER") == "1" {
		for i := 0; i < 600; i++ {
			fmt.Printf(`{"text":"%03d"}`+"\n", i)
		}
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestStreamSessionBackpressuresWithoutDroppingMessages")
	cmd.Env = append(os.Environ(), "ORRERY_CLIINTERNAL_BACKPRESSURE_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	session := StreamSession(StreamOptions{
		Context: ctx, Cancel: func() {}, Command: cmd, Stdout: stdout, Provider: "helper", Timeout: 5 * time.Second,
		NewParser: func() Parser { return &strictJSONTextParser{} },
	})
	time.Sleep(50 * time.Millisecond)
	count := 0
	for message := range session.Messages {
		if message.Type == runtimecore.MessageTypeText {
			count++
		}
	}
	result := <-session.Result
	if result.Status != runtimecore.ResultStatusCompleted {
		t.Fatalf("result = %#v", result)
	}
	if count != 600 {
		t.Fatalf("text message count = %d, want 600", count)
	}
}

func TestStreamSessionRejectsEmptySuccessfulStream(t *testing.T) {
	if os.Getenv("ORRERY_CLIINTERNAL_EMPTY_HELPER") == "1" {
		os.Exit(0)
	}
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestStreamSessionRejectsEmptySuccessfulStream")
	cmd.Env = append(os.Environ(), "ORRERY_CLIINTERNAL_EMPTY_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	session := StreamSession(StreamOptions{
		Context: ctx, Cancel: func() {}, Command: cmd, Stdout: stdout, Provider: "helper", Timeout: time.Minute,
		NewParser: func() Parser { return emptySuccessfulParser{} },
	})
	var messages []runtimecore.RuntimeMessage
	for message := range session.Messages {
		messages = append(messages, message)
	}
	result := <-session.Result
	if result.Status != runtimecore.ResultStatusFailed || !strings.Contains(result.Error, "completed without assistant output") {
		t.Fatalf("result = %#v, want empty-output failure", result)
	}
	if len(messages) != 1 || messages[0].Type != runtimecore.MessageTypeError {
		t.Fatalf("messages = %#v, want visible empty-output error", messages)
	}
}

func TestStreamSessionStopsWhenParserReportsNativeApproval(t *testing.T) {
	if os.Getenv("ORRERY_CLIINTERNAL_APPROVAL_HELPER") == "1" {
		fmt.Println(`{"type":"approval_request"}`)
		time.Sleep(30 * time.Second)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestStreamSessionStopsWhenParserReportsNativeApproval")
	cmd.Env = append(os.Environ(), "ORRERY_CLIINTERNAL_APPROVAL_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var captured runtimecore.ApprovalRequest
	session := StreamSession(StreamOptions{
		Context:  ctx,
		Cancel:   cancel,
		Command:  cmd,
		Stdout:   stdout,
		Provider: "helper",
		Timeout:  5 * time.Second,
		ApprovalBroker: runtimecore.ApprovalBrokerFunc(func(_ context.Context, req runtimecore.ApprovalRequest, _ runtimecore.ApprovalResponder) error {
			captured = req
			return nil
		}),
		NewParser: func() Parser {
			return &approvalLineParser{}
		},
	})

	result := <-session.Result
	if result.Status != runtimecore.ResultStatusBlocked {
		t.Fatalf("status = %s, want blocked (error=%q)", result.Status, result.Error)
	}
	if captured.RequestID != "approval-1" || captured.Source != "helper" {
		t.Fatalf("broker request = %#v", captured)
	}
	if result.Metadata["approval_source"] != "helper" || result.Metadata["approval_request_id"] != "approval-1" {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
}

type jsonTextParser struct {
	messages []runtimecore.RuntimeMessage
	output   string
}

type approvalLineParser struct {
	pending bool
}

type parseErrorParser struct{}

type emptyOutputAfterStatusParser struct {
	messages []runtimecore.RuntimeMessage
}

type strictJSONTextParser struct {
	messages []runtimecore.RuntimeMessage
	output   strings.Builder
}

type emptySuccessfulParser struct{}

func (p *strictJSONTextParser) HandleLine(line []byte) error {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(line, &payload); err != nil {
		return err
	}
	p.output.WriteString(payload.Text)
	p.messages = append(p.messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeText, Content: payload.Text})
	return nil
}

func (p *strictJSONTextParser) DrainMessages() []runtimecore.RuntimeMessage {
	out := append([]runtimecore.RuntimeMessage(nil), p.messages...)
	p.messages = nil
	return out
}

func (p *strictJSONTextParser) Result() runtimecore.RuntimeResult {
	return runtimecore.RuntimeResult{Status: runtimecore.ResultStatusCompleted, Output: p.output.String()}
}

func (emptySuccessfulParser) HandleLine([]byte) error                     { return nil }
func (emptySuccessfulParser) DrainMessages() []runtimecore.RuntimeMessage { return nil }
func (emptySuccessfulParser) Result() runtimecore.RuntimeResult {
	return runtimecore.RuntimeResult{Status: runtimecore.ResultStatusCompleted}
}

func (p *approvalLineParser) HandleLine(line []byte) error {
	if strings.Contains(string(line), "approval_request") {
		p.pending = true
	}
	return nil
}

func (parseErrorParser) HandleLine([]byte) error {
	return fmt.Errorf("invalid runtime event")
}

func (parseErrorParser) DrainMessages() []runtimecore.RuntimeMessage {
	return nil
}

func (parseErrorParser) Result() runtimecore.RuntimeResult {
	return runtimecore.RuntimeResult{Status: runtimecore.ResultStatusCompleted}
}

func (p *emptyOutputAfterStatusParser) HandleLine(line []byte) error {
	text := strings.TrimSpace(string(line))
	if strings.HasPrefix(text, "{") {
		p.messages = append(p.messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeStatus, Status: "running", SessionID: "session-1"})
		return nil
	}
	return fmt.Errorf("invalid runtime event")
}

func (p *emptyOutputAfterStatusParser) DrainMessages() []runtimecore.RuntimeMessage {
	out := append([]runtimecore.RuntimeMessage(nil), p.messages...)
	p.messages = nil
	return out
}

func (p *emptyOutputAfterStatusParser) Result() runtimecore.RuntimeResult {
	return runtimecore.RuntimeResult{Status: runtimecore.ResultStatusFailed, Error: "pi completed without assistant output", SessionID: "session-1"}
}

func (p *approvalLineParser) PendingApproval() (runtimecore.ApprovalRequest, bool) {
	if !p.pending {
		return runtimecore.ApprovalRequest{}, false
	}
	return runtimecore.ApprovalRequest{
		Source:    "helper",
		Provider:  "helper",
		RequestID: "approval-1",
		Kind:      runtimecore.ApprovalKindTool,
		ToolName:  "tool",
	}, true
}

func (p *approvalLineParser) DrainMessages() []runtimecore.RuntimeMessage {
	return nil
}

func (p *approvalLineParser) Result() runtimecore.RuntimeResult {
	return runtimecore.RuntimeResult{
		Status: runtimecore.ResultStatusCompleted,
		Output: "still waiting",
	}
}

func (p *jsonTextParser) HandleLine(_ []byte) error {
	p.output = "partial"
	p.messages = append(p.messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeText, Content: "partial"})
	return nil
}

func (p *jsonTextParser) DrainMessages() []runtimecore.RuntimeMessage {
	out := append([]runtimecore.RuntimeMessage(nil), p.messages...)
	p.messages = nil
	return out
}

func (p *jsonTextParser) Result() runtimecore.RuntimeResult {
	return runtimecore.RuntimeResult{
		Status: runtimecore.ResultStatusCompleted,
		Output: p.output,
		Usage:  map[string]agentcore.TokenUsage{},
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !contains(text, part) {
			return false
		}
	}
	return true
}

func contains(text, part string) bool {
	for i := 0; i+len(part) <= len(text); i++ {
		if text[i:i+len(part)] == part {
			return true
		}
	}
	return part == ""
}

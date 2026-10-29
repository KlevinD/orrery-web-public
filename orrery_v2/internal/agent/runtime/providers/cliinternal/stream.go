package cliinternal

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/process"
)

type Parser interface {
	HandleLine([]byte) error
	DrainMessages() []runtimecore.RuntimeMessage
	Result() runtimecore.RuntimeResult
}

type ApprovalParser interface {
	PendingApproval() (runtimecore.ApprovalRequest, bool)
}

type StreamOptions struct {
	Context        context.Context
	Cancel         context.CancelFunc
	Command        *exec.Cmd
	Stdout         io.Reader
	StderrTail     *process.StderrTail
	Provider       string
	StderrLabel    string
	Timeout        time.Duration
	ApprovalBroker runtimecore.ApprovalBroker
	Cleanup        func()
	ResultMetadata map[string]string
	NewParser      func() Parser
}

func StreamSession(opts StreamOptions) *runtimecore.RuntimeSession {
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	if opts.Cancel == nil {
		opts.Cancel = func() {}
	}
	msgCh := make(chan runtimecore.RuntimeMessage, 256)
	resCh := make(chan runtimecore.RuntimeResult, 1)
	go func() {
		if opts.Cleanup != nil {
			defer opts.Cleanup()
		}
		defer opts.Cancel()
		defer close(msgCh)
		defer close(resCh)
		if opts.Command == nil || opts.Stdout == nil || opts.NewParser == nil {
			message := normalizedProviderName(opts.Provider) + " stream configuration is incomplete"
			msgCh <- runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeError, Content: message}
			resCh <- runtimecore.RuntimeResult{Status: runtimecore.ResultStatusFailed, Error: message}
			return
		}
		parser := opts.NewParser()
		if parser == nil {
			message := normalizedProviderName(opts.Provider) + " parser is not configured"
			msgCh <- runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeError, Content: message}
			resCh <- runtimecore.RuntimeResult{Status: runtimecore.ResultStatusFailed, Error: message}
			return
		}
		started := time.Now()
		var pendingApproval *runtimecore.ApprovalRequest
		var firstParseErr error
		var firstParseErrLine string
		var errorMessageSent bool
		var deliveryStopped bool
		scanner := bufio.NewScanner(opts.Stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if err := parser.HandleLine(line); err != nil {
				firstParseErr = err
				firstParseErrLine = safeCLIOutputLine(line)
				message := stdoutParseFailure(opts.Provider, firstParseErrLine, firstParseErr)
				if Send(opts.Context, msgCh, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeError, Content: message}) == nil {
					errorMessageSent = true
				}
				opts.Cancel()
				break
			}
			for _, msg := range parser.DrainMessages() {
				if Send(opts.Context, msgCh, msg) != nil {
					deliveryStopped = true
					break
				}
				if msg.Type == runtimecore.MessageTypeError {
					errorMessageSent = true
				}
			}
			if deliveryStopped {
				break
			}
			if pendingApproval == nil {
				if req, ok := pendingApprovalFromParser(parser); ok {
					pendingApproval = &req
					requestApproval(opts.Context, opts.ApprovalBroker, req)
					opts.Cancel()
					break
				}
			}
		}
		waitErr := opts.Command.Wait()
		result := parser.Result()
		result.DurationMs = time.Since(started).Milliseconds()
		if pendingApproval != nil {
			result.Status = runtimecore.ResultStatusBlocked
			if result.Error == "" {
				result.Error = opts.Provider + " waiting for user approval"
			}
			if result.Metadata == nil {
				result.Metadata = map[string]string{}
			}
			for key, value := range runtimecore.ApprovalMetadata(*pendingApproval) {
				if value != "" {
					result.Metadata[key] = value
				}
			}
		}
		switch {
		case result.Status == runtimecore.ResultStatusBlocked:
		case firstParseErr != nil:
			result.Status = runtimecore.ResultStatusFailed
			result.Error = stdoutParseFailure(opts.Provider, firstParseErrLine, firstParseErr)
		case opts.Context.Err() == context.DeadlineExceeded:
			result.Status = runtimecore.ResultStatusTimeout
			result.Error = fmt.Sprintf("%s timed out after %s", opts.Provider, opts.Timeout)
		case opts.Context.Err() == context.Canceled:
			result.Status = runtimecore.ResultStatusCancelled
			result.Error = "execution cancelled"
		case scanner.Err() != nil:
			result.Status = runtimecore.ResultStatusFailed
			result.Error = fmt.Sprintf("%s stdout scan failed: %v", opts.Provider, scanner.Err())
		case waitErr != nil && result.Status == runtimecore.ResultStatusCompleted:
			result.Status = runtimecore.ResultStatusFailed
			result.Error = fmt.Sprintf("%s exited with error: %v", opts.Provider, waitErr)
		case result.Status == runtimecore.ResultStatusCompleted && strings.TrimSpace(result.Output) == "":
			result.Status = runtimecore.ResultStatusFailed
			result.Error = fmt.Sprintf("%s completed without assistant output", normalizedProviderName(opts.Provider))
		}
		for _, msg := range parser.DrainMessages() {
			if Send(opts.Context, msgCh, msg) != nil {
				break
			}
			if msg.Type == runtimecore.MessageTypeError {
				errorMessageSent = true
			}
		}
		if result.Error != "" && opts.StderrTail != nil && opts.StderrTail.Tail() != "" {
			label := opts.StderrLabel
			if label == "" {
				label = opts.Provider
			}
			result.Error += "\n[" + label + " stderr]\n" + opts.StderrTail.Tail()
		}
		if result.Status == runtimecore.ResultStatusFailed && result.Error != "" && !errorMessageSent {
			_ = Send(opts.Context, msgCh, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeError, Content: result.Error})
		}
		if len(opts.ResultMetadata) > 0 {
			if result.Metadata == nil {
				result.Metadata = map[string]string{}
			}
			for key, value := range opts.ResultMetadata {
				if value != "" {
					result.Metadata[key] = value
				}
			}
		}
		resCh <- result
	}()
	return &runtimecore.RuntimeSession{Messages: msgCh, Result: resCh}
}

func pendingApprovalFromParser(parser Parser) (runtimecore.ApprovalRequest, bool) {
	approvalParser, ok := parser.(ApprovalParser)
	if !ok {
		return runtimecore.ApprovalRequest{}, false
	}
	return approvalParser.PendingApproval()
}

func requestApproval(ctx context.Context, broker runtimecore.ApprovalBroker, req runtimecore.ApprovalRequest) {
	if broker == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = broker.RequestApproval(ctx, req, func(runtimecore.ApprovalResponse) error {
			return nil
		})
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
	}
}

func MergeEnv(left, right map[string]string) map[string]string {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}
	out := make(map[string]string, len(left)+len(right))
	for k, v := range left {
		out[k] = v
	}
	for k, v := range right {
		out[k] = v
	}
	return out
}

func Send(ctx context.Context, ch chan<- runtimecore.RuntimeMessage, msg runtimecore.RuntimeMessage) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case ch <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func normalizedProviderName(provider string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return "runtime"
	}
	return provider
}

func stdoutParseFailure(provider string, line string, err error) string {
	provider = normalizedProviderName(provider)
	message := fmt.Sprintf("%s stdout parse failed: %v", provider, err)
	if strings.TrimSpace(line) != "" {
		message += "\n[stdout]\n" + line
	}
	return message
}

func safeCLIOutputLine(line []byte) string {
	const maxRunes = 2048
	text := strings.TrimSpace(string(line))
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "... [truncated]"
}

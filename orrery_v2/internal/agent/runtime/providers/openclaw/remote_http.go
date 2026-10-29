package openclaw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/workspaceprompt"
)

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage map[string]any `json:"usage"`
}

type responsesRequest struct {
	Model        string `json:"model"`
	Input        string `json:"input"`
	Instructions string `json:"instructions,omitempty"`
}

type responsesResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Model  string `json:"model"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage map[string]any `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func executeRemoteHTTP(ctx context.Context, prompt string, opts runtimecore.ExecOptions, cfg RuntimeConfig) (*runtimecore.RuntimeSession, error) {
	baseURL := strings.TrimRight(cfg.remoteURL(), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("openclaw remote_http gateway_url is required")
	}
	model := cfg.ModelID
	if strings.TrimSpace(model) == "" {
		model = openClawAgentModel(cfg.AgentID)
	}
	if strings.TrimSpace(model) == "" {
		model = openClawAgentModel(opts.Model)
	}
	if strings.TrimSpace(model) == "" {
		model = "openclaw/default"
	}
	systemPrompt := workspaceprompt.SystemPrompt(opts.SystemPrompt, opts)
	if cfg.httpAPI() == "chat_completions" {
		return executeRemoteHTTPChatCompletions(ctx, baseURL, prompt, systemPrompt, model, opts, cfg)
	}
	return executeRemoteHTTPResponses(ctx, baseURL, prompt, systemPrompt, model, opts, cfg)
}

func executeRemoteHTTPChatCompletions(ctx context.Context, baseURL, prompt, systemPrompt, model string, opts runtimecore.ExecOptions, cfg RuntimeConfig) (*runtimecore.RuntimeSession, error) {
	if systemPrompt != "" {
		prompt = systemPrompt + "\n\n" + prompt
	}
	body, err := json.Marshal(chatCompletionRequest{
		Model: strings.TrimSpace(model),
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal openclaw remote_http request: %w", err)
	}
	sessionID := opts.ResumeSessionID
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	req, err := newRemoteHTTPRequest(runCtx, http.MethodPost, baseURL+"/v1/chat/completions", body, cfg, sessionID)
	if err != nil {
		cancel()
		return nil, err
	}
	client, err := remoteHTTPClient(timeout, cfg)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("openclaw remote_http request: %w", err)
	}
	defer cancel()
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read openclaw remote_http response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return singleResultSession(runtimecore.RuntimeResult{
			Status: runtimecore.ResultStatusFailed,
			Error:  fmt.Sprintf("openclaw remote_http returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))),
		}), nil
	}
	var decoded chatCompletionResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode openclaw remote_http response: %w", err)
	}
	output := ""
	if len(decoded.Choices) > 0 {
		output = decoded.Choices[0].Message.Content
	}
	status := runtimecore.ResultStatusCompleted
	errMessage := ""
	if strings.TrimSpace(output) == "" {
		status = runtimecore.ResultStatusFailed
		errMessage = "openclaw remote_http chat completion contained no assistant output"
	}
	usageModel := strings.TrimSpace(decoded.Model)
	if usageModel == "" {
		usageModel = strings.TrimSpace(model)
	}
	usage := map[string]agentcore.TokenUsage{}
	parsedUsage := parseUsage(decoded.Usage)
	if parsedUsage.InputTokens > 0 || parsedUsage.OutputTokens > 0 || parsedUsage.CacheReadTokens > 0 || parsedUsage.CacheWriteTokens > 0 {
		usage[usageModel] = parsedUsage
	}
	return singleResultSession(runtimecore.RuntimeResult{
		Status:    status,
		Output:    output,
		Error:     errMessage,
		SessionID: decoded.ID,
		Usage:     usage,
		Metadata: map[string]string{
			"transport": "remote_http",
			"http_api":  "chat_completions",
			"model":     usageModel,
		},
	}), nil
}

func executeRemoteHTTPResponses(ctx context.Context, baseURL, prompt, systemPrompt, model string, opts runtimecore.ExecOptions, cfg RuntimeConfig) (*runtimecore.RuntimeSession, error) {
	body, err := json.Marshal(responsesRequest{
		Model:        strings.TrimSpace(model),
		Input:        prompt,
		Instructions: systemPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal openclaw remote_http responses request: %w", err)
	}
	sessionID := opts.ResumeSessionID
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	req, err := newRemoteHTTPRequest(runCtx, http.MethodPost, baseURL+"/v1/responses", body, cfg, sessionID)
	if err != nil {
		cancel()
		return nil, err
	}
	client, err := remoteHTTPClient(timeout, cfg)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("openclaw remote_http responses request: %w", err)
	}
	defer cancel()
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read openclaw remote_http responses response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return singleResultSession(runtimecore.RuntimeResult{
			Status: runtimecore.ResultStatusFailed,
			Error:  fmt.Sprintf("openclaw remote_http responses returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))),
		}), nil
	}
	var decoded responsesResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode openclaw remote_http responses response: %w", err)
	}
	status := runtimecore.ResultStatusCompleted
	errMessage := ""
	switch decoded.Status {
	case "completed":
	case "failed":
		status = runtimecore.ResultStatusFailed
		if decoded.Error != nil {
			errMessage = decoded.Error.Message
		}
		if strings.TrimSpace(errMessage) == "" {
			errMessage = "openclaw remote_http response failed without an error message"
		}
	default:
		status = runtimecore.ResultStatusFailed
		errMessage = "openclaw remote_http returned non-terminal response status " + fmt.Sprintf("%q", decoded.Status)
	}
	output := outputFromResponses(decoded)
	if status == runtimecore.ResultStatusCompleted && strings.TrimSpace(output) == "" {
		status = runtimecore.ResultStatusFailed
		errMessage = "openclaw remote_http response contained no assistant output"
	}
	usageModel := strings.TrimSpace(decoded.Model)
	if usageModel == "" {
		usageModel = strings.TrimSpace(model)
	}
	usage := map[string]agentcore.TokenUsage{}
	parsedUsage := parseUsage(decoded.Usage)
	if parsedUsage.InputTokens > 0 || parsedUsage.OutputTokens > 0 || parsedUsage.CacheReadTokens > 0 || parsedUsage.CacheWriteTokens > 0 {
		usage[usageModel] = parsedUsage
	}
	return singleResultSession(runtimecore.RuntimeResult{
		Status:    status,
		Output:    output,
		Error:     errMessage,
		SessionID: decoded.ID,
		Usage:     usage,
		Metadata: map[string]string{
			"transport": "remote_http",
			"http_api":  "responses",
			"model":     usageModel,
		},
	}), nil
}

func newRemoteHTTPRequest(ctx context.Context, method, url string, body []byte, cfg RuntimeConfig, sessionID string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := cfg.tokenValue(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if agentID := strings.TrimSpace(cfg.AgentID); agentID != "" {
		req.Header.Set("X-OpenClaw-Agent-Id", normalizeOpenClawAgentID(agentID))
	}
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" && strings.TrimSpace(cfg.AgentID) != "" {
		req.Header.Set("X-OpenClaw-Session-Key", openClawExplicitSessionKey(cfg.AgentID, sessionID))
	}
	return req, nil
}

func remoteHTTPClient(timeout time.Duration, cfg RuntimeConfig) (*http.Client, error) {
	tlsConfig, err := tlsConfigForFingerprint(cfg.TLSFingerprint)
	if err != nil {
		return nil, fmt.Errorf("openclaw remote_http %w", err)
	}
	if tlsConfig == nil {
		return &http.Client{Timeout: timeout}, nil
	}
	return &http.Client{Timeout: timeout, Transport: &http.Transport{TLSClientConfig: tlsConfig}}, nil
}

func outputFromResponses(decoded responsesResponse) string {
	var parts []string
	for _, output := range decoded.Output {
		for _, content := range output.Content {
			if strings.TrimSpace(content.Text) != "" {
				parts = append(parts, content.Text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func singleResultSession(result runtimecore.RuntimeResult) *runtimecore.RuntimeSession {
	messages := make(chan runtimecore.RuntimeMessage)
	results := make(chan runtimecore.RuntimeResult, 1)
	close(messages)
	results <- result
	close(results)
	return &runtimecore.RuntimeSession{Messages: messages, Result: results}
}

func openClawAgentModel(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	if strings.HasPrefix(agentID, "openclaw/") {
		return agentID
	}
	return "openclaw/" + agentID
}

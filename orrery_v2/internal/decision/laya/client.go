package laya

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"orrery_v2/internal/decision"
	"os"
	"strings"
	"time"
)

const (
	ModelRevision = "convaiinnovations/laya-multilingual@1720e3e3357cfe1e281542e223f8273b0890ca34"
	MaxCandidates = 6
	// A conservative serialized-byte ceiling leaves room within the deployed
	// 1024-token budget. Oversized requests are rejected before inference.
	MaxRequestBytes = 900
)

var (
	ErrBudget   = decision.ErrBudget
	ErrResponse = decision.ErrResponse
)

type Question = decision.Question
type Decision = decision.Decision
type Judge = decision.Judge

type Client struct {
	endpoint string
	key      string
	http     *http.Client
}

func NewClient(endpoint, key string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u == nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("laya: invalid endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return nil, errors.New("laya: endpoint must be local HTTP")
	}
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") || timeout <= 0 {
		return nil, errors.New("laya: credential and positive timeout required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxConnsPerHost = 1
	transport.MaxIdleConnsPerHost = 1
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), key: key, http: &http.Client{
		Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

// ReadKeyFile accepts a plain bearer key or the deployed LAYA_API_KEY env file.
// Neither parse errors nor HTTP errors include file contents or response bodies.
func ReadKeyFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", errors.New("laya: cannot read credential file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("laya: invalid credential file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(raw) > 8192 {
		return "", errors.New("laya: invalid credential file")
	}
	value := strings.TrimSpace(string(raw))
	for _, line := range strings.Split(value, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "LAYA_API_KEY=") {
			value = strings.TrimSpace(strings.SplitN(strings.TrimSpace(line), "=", 2)[1])
			break
		}
	}
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("laya: invalid credential file")
	}
	return value, nil
}

func validateInput(states []string, question Question) error {
	if len(states) == 0 || len(states) > MaxCandidates || question.Type != "choice" || len(question.Criteria) < 2 || len(question.Criteria) > 5 || question.Instructions == "" {
		return ErrBudget
	}
	for _, state := range states {
		body, err := json.Marshal(map[string]any{"model": "multilingual", "state": state, "questions": map[string]Question{"judgment": question}})
		if state == "" || err != nil || len(body) > MaxRequestBytes {
			return ErrBudget
		}
	}
	return nil
}

func (c *Client) Choose(ctx context.Context, states []string, question Question) ([]Decision, error) {
	if err := validateInput(states, question); err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"model": "multilingual", "states": states, "questions": map[string]Question{"judgment": question}, "batch_size": 2})
	if err != nil {
		return nil, ErrBudget
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/systemone/batch", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("laya: invalid request")
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, errors.New("laya: inference unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("laya: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, ErrResponse
	}
	var batch struct {
		Results []struct {
			Answers map[string]struct {
				Decision
				Type string `json:"type"`
			} `json:"answers"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &batch) != nil || len(batch.Results) != len(states) {
		return nil, ErrResponse
	}
	out := make([]Decision, len(states))
	for i, item := range batch.Results {
		answer, ok := item.Answers["judgment"]
		if !ok || answer.Type != "choice" {
			return nil, ErrResponse
		}
		if _, ok := question.Criteria[answer.Choice]; !ok {
			return nil, ErrResponse
		}
		if !validProbability(answer.Confidence) || !validProbability(answer.AnswerConfidence) || len(answer.Probabilities) != len(question.Criteria) {
			return nil, ErrResponse
		}
		sum := 0.0
		for choice := range question.Criteria {
			p, ok := answer.Probabilities[choice]
			if !ok || !validProbability(p) {
				return nil, ErrResponse
			}
			sum += p
		}
		if math.Abs(sum-1) > 0.02 {
			return nil, ErrResponse
		}
		out[i] = answer.Decision
	}
	return out, nil
}

func validProbability(p float64) bool { return !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 && p <= 1 }

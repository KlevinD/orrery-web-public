package pi

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

const (
	piRPCMaxRecordBytes          = 32 * 1024 * 1024
	piRPCDefaultPromptAckTimeout = 60 * time.Second
)

type rpcEnvelope struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Command string          `json:"command,omitempty"`
	Success bool            `json:"success,omitempty"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func readStrictJSONLRecord(reader *bufio.Reader, maxBytes int) ([]byte, error) {
	if reader == nil {
		return nil, io.EOF
	}
	var record []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		record = append(record, fragment...)
		if len(record) > maxBytes {
			return nil, fmt.Errorf("pi rpc record exceeds %d bytes", maxBytes)
		}
		if err == nil {
			trimmed := strings.TrimSuffix(string(record), "\n")
			trimmed = strings.TrimSuffix(trimmed, "\r")
			return []byte(trimmed), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(record) > 0 {
			return record, io.EOF
		}
		return nil, err
	}
}

func decodeRPCEnvelope(line []byte) (rpcEnvelope, error) {
	var envelope rpcEnvelope
	if err := json.Unmarshal(line, &envelope); err != nil {
		return rpcEnvelope{}, fmt.Errorf("decode pi rpc record: %w", err)
	}
	if strings.TrimSpace(envelope.Type) == "" {
		return rpcEnvelope{}, errors.New("pi rpc record is missing type")
	}
	return envelope, nil
}

func piRPCPromptAckTimeout(opts runtimecore.ExecOptions) time.Duration {
	timeoutMs := parseRuntimeConfig(opts.ProviderConfig).PromptAckTimeoutMs
	timeout := piRPCDefaultPromptAckTimeout
	if timeoutMs > 0 && timeoutMs <= int64((1<<63-1)/time.Millisecond) {
		configured := time.Duration(timeoutMs) * time.Millisecond
		if configured > 0 {
			timeout = configured
		}
	}
	taskTimeoutMs := piBridgeToolCallTimeoutMs(opts)
	if taskTimeoutMs > 0 && taskTimeoutMs <= int64((1<<63-1)/time.Millisecond) {
		taskTimeout := time.Duration(taskTimeoutMs) * time.Millisecond
		if taskTimeout > timeout {
			return taskTimeout
		}
	}
	return timeout
}

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
)

// Retain only response shape/lengths for live test diagnostics, never content,
// authorization headers, credentials or provider reasoning.
type runtimeDiagnosisResponseObserver struct {
	status       int
	contentBytes int
	summaryBytes int
	keys         []string
}

func (o *runtimeDiagnosisResponseObserver) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		return response, err
	}
	o.status = response.StatusCode
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		response.Body.Close()
		return nil, err
	}
	response.Body = &runtimeDiagnosisObservedBody{Reader: io.MultiReader(bytes.NewReader(data), response.Body), closer: response.Body}
	var wire struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &wire) == nil && len(wire.Choices) > 0 {
		content := wire.Choices[0].Message.Content
		o.contentBytes = len(content)
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(content), &fields) == nil {
			for key := range fields {
				o.keys = append(o.keys, key)
			}
			sort.Strings(o.keys)
			var summary string
			_ = json.Unmarshal(fields["summary"], &summary)
			o.summaryBytes = len(summary)
		}
	}
	return response, nil
}

type runtimeDiagnosisObservedBody struct {
	io.Reader
	closer io.Closer
}

func (b *runtimeDiagnosisObservedBody) Close() error { return b.closer.Close() }

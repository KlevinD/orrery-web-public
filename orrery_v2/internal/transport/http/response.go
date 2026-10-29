package httptransport

import (
	"encoding/json"
	"net/http"
)

type ErrorResponse struct {
	Code          string            `json:"code"`
	Message       string            `json:"message"`
	Details       map[string]string `json:"details,omitempty"`
	CorrelationID string            `json:"correlation_id"`
	Retryable     bool              `json:"retryable"`
	cause         error
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	if body, ok := value.(ErrorResponse); ok && body.cause != nil {
		if recorder, ok := w.(interface{ RecordError(error) }); ok {
			recorder.RecordError(body.cause)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

package tracing

import (
	"context"
	"strings"

	"orrery_v2/internal/platform/logging"
)

func ExtractTraceParent(ctx context.Context, header string) (context.Context, bool) {
	traceID, spanID, ok := parseTraceParent(header)
	if !ok {
		return ctx, false
	}
	ctx = WithTraceID(ctx, traceID)
	ctx = WithSpanID(ctx, spanID)
	ctx = logging.WithCorrelationID(ctx, traceID)
	return ctx, true
}

func FormatTraceParent(ctx context.Context) (string, bool) {
	traceID := TraceID(ctx)
	spanID := SpanID(ctx)
	if !validHex(traceID, 32) || !validHex(spanID, 16) {
		return "", false
	}
	return "00-" + traceID + "-" + spanID + "-01", true
}

func parseTraceParent(header string) (string, string, bool) {
	parts := strings.Split(strings.TrimSpace(header), "-")
	if len(parts) != 4 {
		return "", "", false
	}
	if parts[0] != "00" {
		return "", "", false
	}
	traceID := strings.ToLower(parts[1])
	spanID := strings.ToLower(parts[2])
	flags := strings.ToLower(parts[3])
	if !validHex(traceID, 32) || !validHex(spanID, 16) || !validHex(flags, 2) {
		return "", "", false
	}
	if allZeros(traceID) || allZeros(spanID) {
		return "", "", false
	}
	return traceID, spanID, true
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func allZeros(value string) bool {
	for _, r := range value {
		if r != '0' {
			return false
		}
	}
	return true
}

package main

import (
	"context"

	"orrery_v2/internal/platform/logging"
)

type httpRequestLogger struct {
	logger logging.Logger
}

func (l httpRequestLogger) Info(ctx context.Context, msg string, fields map[string]any) {
	if l.logger == nil {
		return
	}
	l.logger.Info(ctx, msg, requestLogFields(fields)...)
}

func (l httpRequestLogger) Error(ctx context.Context, msg string, fields map[string]any) {
	if l.logger == nil {
		return
	}
	l.logger.Error(ctx, msg, requestLogFields(fields)...)
}

func requestLogFields(fields map[string]any) []logging.Field {
	out := make([]logging.Field, 0, len(fields))
	for key, value := range fields {
		out = append(out, logging.Any(key, value))
	}
	return out
}

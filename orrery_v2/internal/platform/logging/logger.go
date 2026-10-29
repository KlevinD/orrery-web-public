package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

type Level = slog.Level

const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

type Logger interface {
	Debug(ctx context.Context, msg string, fields ...Field)
	Info(ctx context.Context, msg string, fields ...Field)
	Warn(ctx context.Context, msg string, fields ...Field)
	Error(ctx context.Context, msg string, fields ...Field)
	With(fields ...Field) Logger
}

type SlogLogger struct {
	logger *slog.Logger
}

type MultiLogger struct {
	loggers []Logger
}

type Config struct {
	Level   string
	Console bool
	File    string
}

func NewJSONLogger(w io.Writer, level Level) Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return &SlogLogger{logger: slog.New(handler)}
}

func NewTextLogger(w io.Writer, level Level) Logger {
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return &SlogLogger{logger: slog.New(handler)}
}

func NewMultiLogger(loggers ...Logger) Logger {
	copied := make([]Logger, 0, len(loggers))
	for _, logger := range loggers {
		if logger != nil {
			copied = append(copied, logger)
		}
	}
	return &MultiLogger{loggers: copied}
}

func NewConsoleAndFileLogger(console io.Writer, file io.Writer, level Level) Logger {
	return NewMultiLogger(
		NewTextLogger(console, level),
		NewJSONLogger(file, level),
	)
}

func NewFileLogger(path string, level Level) (Logger, func() error, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return NewJSONLogger(file, level), file.Close, nil
}

func NewConfiguredLogger(cfg Config, console io.Writer) (Logger, func() error, error) {
	level, err := ParseLevel(cfg.Level)
	if err != nil {
		return nil, nil, err
	}
	var loggers []Logger
	var closers []func() error
	if cfg.Console {
		if console == nil {
			console = os.Stderr
		}
		if os.Getenv("JOURNAL_STREAM") != "" {
			console = &journalWriter{next: console}
		}
		loggers = append(loggers, NewTextLogger(console, level))
	}
	if strings.TrimSpace(cfg.File) != "" {
		fileLogger, closeLog, err := NewFileLogger(cfg.File, level)
		if err != nil {
			return nil, nil, err
		}
		loggers = append(loggers, fileLogger)
		closers = append(closers, closeLog)
	}
	if len(loggers) == 0 {
		return nil, nil, fmt.Errorf("at least one logging sink must be enabled")
	}
	closeAll := func() error {
		var firstErr error
		for _, closeLog := range closers {
			if err := closeLog(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
	return observable(NewMultiLogger(loggers...)), closeAll, nil
}

func ParseLevel(level string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return LevelInfo, nil
	case "debug":
		return LevelDebug, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	default:
		return LevelInfo, fmt.Errorf("unsupported log level %q", level)
	}
}

func (l *SlogLogger) Debug(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelDebug, msg, fields...)
}

func (l *SlogLogger) Info(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelInfo, msg, fields...)
}

func (l *SlogLogger) Warn(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelWarn, msg, fields...)
}

func (l *SlogLogger) Error(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelError, msg, fields...)
}

func (l *SlogLogger) With(fields ...Field) Logger {
	attrs := make([]any, 0, len(fields))
	for _, field := range fields {
		attrs = append(attrs, redactField(field))
	}
	return &SlogLogger{logger: l.logger.With(attrs...)}
}

func (l *SlogLogger) log(ctx context.Context, level slog.Level, msg string, fields ...Field) {
	attrs := make([]slog.Attr, 0, len(fields)+1)
	if correlationID := CorrelationID(ctx); correlationID != "" {
		attrs = append(attrs, slog.String("correlation_id", correlationID))
	}
	for _, field := range fields {
		attrs = append(attrs, redactField(field))
	}
	l.logger.LogAttrs(ctx, level, msg, attrs...)
}

func (l *MultiLogger) Debug(ctx context.Context, msg string, fields ...Field) {
	for _, logger := range l.loggers {
		logger.Debug(ctx, msg, fields...)
	}
}

func (l *MultiLogger) Info(ctx context.Context, msg string, fields ...Field) {
	for _, logger := range l.loggers {
		logger.Info(ctx, msg, fields...)
	}
}

func (l *MultiLogger) Warn(ctx context.Context, msg string, fields ...Field) {
	for _, logger := range l.loggers {
		logger.Warn(ctx, msg, fields...)
	}
}

func (l *MultiLogger) Error(ctx context.Context, msg string, fields ...Field) {
	for _, logger := range l.loggers {
		logger.Error(ctx, msg, fields...)
	}
}

func (l *MultiLogger) With(fields ...Field) Logger {
	loggers := make([]Logger, 0, len(l.loggers))
	for _, logger := range l.loggers {
		loggers = append(loggers, logger.With(fields...))
	}
	return &MultiLogger{loggers: loggers}
}

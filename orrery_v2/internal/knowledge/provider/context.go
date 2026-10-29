package provider

import (
	"context"
	"strings"
	"unicode"
)

type requestMetadataContextKey struct{}

type RequestMetadata struct {
	RequestID string
	Actor     string
}

func WithRequestMetadata(ctx context.Context, metadata RequestMetadata) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	metadata.RequestID = safeRequestMetadataValue(metadata.RequestID, 128)
	metadata.Actor = safeRequestMetadataValue(metadata.Actor, 160)
	return context.WithValue(ctx, requestMetadataContextKey{}, metadata)
}

func RequestMetadataFromContext(ctx context.Context) RequestMetadata {
	if ctx == nil {
		return RequestMetadata{}
	}
	metadata, _ := ctx.Value(requestMetadataContextKey{}).(RequestMetadata)
	metadata.RequestID = safeRequestMetadataValue(metadata.RequestID, 128)
	metadata.Actor = safeRequestMetadataValue(metadata.Actor, 160)
	return metadata
}

func safeRequestMetadataValue(value string, maxLength int) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxLength || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ""
	}
	return value
}

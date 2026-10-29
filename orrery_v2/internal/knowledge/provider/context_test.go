package provider

import (
	"context"
	"strings"
	"testing"
)

func TestRequestMetadataSanitizesHeaderValues(t *testing.T) {
	ctx := WithRequestMetadata(context.Background(), RequestMetadata{
		RequestID: "request\nforged",
		Actor:     " human:operator ",
	})

	metadata := RequestMetadataFromContext(ctx)
	if metadata.RequestID != "" || metadata.Actor != "human:operator" {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestRequestMetadataRejectsExcessiveValues(t *testing.T) {
	ctx := WithRequestMetadata(context.Background(), RequestMetadata{
		RequestID: strings.Repeat("r", 129),
		Actor:     strings.Repeat("a", 161),
	})

	metadata := RequestMetadataFromContext(ctx)
	if metadata.RequestID != "" || metadata.Actor != "" {
		t.Fatalf("metadata = %+v", metadata)
	}
}

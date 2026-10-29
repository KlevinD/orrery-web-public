package pi

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestPiToolResultContentsPreservesTextAndImage(t *testing.T) {
	image := []byte("jpeg-data")
	content := piToolResultContents(map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "Took a screenshot."},
		map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(image), "mimeType": "image/jpeg"},
	}})
	if len(content) != 2 || content[0].Kind != agentcore.ToolResultText || content[0].Text != "Took a screenshot." {
		t.Fatalf("text content = %+v", content)
	}
	if content[1].Kind != agentcore.ToolResultImage || content[1].MediaType != "image/jpeg" || !bytes.Equal(content[1].Data, image) {
		t.Fatalf("image content = %+v", content[1])
	}
}

func TestPiToolResultContentsRejectsOversizedImage(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", maxPiToolResultImageBytes+1)))
	content := piToolResultContents(map[string]any{
		"type": "image", "data": encoded, "mimeType": "image/jpeg",
	})
	if len(content) != 0 {
		t.Fatalf("oversized image content = %+v", content)
	}
}

package session

import (
	"bytes"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRuntimeRequestContentIncludesImageBlocks(t *testing.T) {
	imageBytes := []byte{1, 2, 3, 4}
	request := agentcore.RuntimeRequest{
		InputMessages: []agentcore.RuntimeChatMessage{{Role: "user", Content: "What is shown here?"}},
		InputMedia:    []agentcore.ContentBlock{{Kind: agentcore.ContentBlockImage, Data: imageBytes, MediaType: "image/png"}},
	}
	content := runtimeRequestContent(request)
	if len(content) != 2 || content[0].Kind != agentcore.ContentBlockText || content[1].Kind != agentcore.ContentBlockImage || !bytes.Equal(content[1].Data, imageBytes) {
		t.Fatalf("runtime request content = %+v", content)
	}
	content[1].Data[0] = 9
	if request.InputMedia[0].Data[0] != 1 {
		t.Fatal("runtime request content shares mutable image bytes")
	}
}

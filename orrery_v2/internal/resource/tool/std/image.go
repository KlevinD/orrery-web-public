package std

import (
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/resource/tool"
	"time"
)

func HasImageGenerateToolRef(refs []agentcore.ToolRef) bool {
	for _, ref := range refs {
		if ref.ToolID == ImageGenerate && ref.PermissionScope == PermissionImageGenerate {
			return true
		}
	}
	return false
}

func EnsureImageGenerateToolRef(refs []agentcore.ToolRef) []agentcore.ToolRef {
	out := append([]agentcore.ToolRef(nil), refs...)
	for i := range out {
		if out[i].ToolID == ImageGenerate {
			out[i].PermissionScope = PermissionImageGenerate
			return out
		}
	}
	return append(out, agentcore.ToolRef{ToolID: ImageGenerate, PermissionScope: PermissionImageGenerate})
}

func imageGenerateTool(now time.Time) tool.ToolDefinition {
	d := systemTool(ImageGenerate, "Generate image", "Generate one image only when the user asks for it in their private personal-assistant conversation. Uses the Orrery administrator's configured image gateway and saves a private attachment. Return the exact markdown from the result. Never claim success before completion. Never automatically retry a failed or uncertain generation; it may have been billed. One generation per user turn.", PermissionImageGenerate, "Generates a billable image for the current personal-assistant owner only.", tool.RiskMedium, now)
	d.InputSchema = objectSchema("image_generate input", map[string]any{
		"prompt": map[string]any{"type": "string", "minLength": 1, "maxLength": 4000, "description": "Image description requested by the user. Do not include secrets or unrelated private context."},
		"size":   enumSchema("Image size. Defaults to 1024x1024.", "1024x1024", "1024x1536", "1536x1024"),
	}, []string{"prompt"})
	d.OutputSchema = objectSchema("image_generate output", map[string]any{"status": stringSchema("completed or failed"), "attachment_id": stringSchema("Private attachment ID"), "download_url": stringSchema("Authenticated download URL"), "markdown": stringSchema("Exact image markdown for the reply")}, []string{"status"})
	d.Tags = []string{"system", "personal-assistant", "image", "std"}
	return d
}

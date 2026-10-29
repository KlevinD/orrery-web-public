package resourceadmin

import "strings"

func updateMaskHas(mask []string, field string) bool {
	if mask == nil {
		return true
	}
	field = strings.TrimSpace(field)
	if field == "" {
		return false
	}
	for _, item := range mask {
		if strings.TrimSpace(item) == field {
			return true
		}
	}
	return false
}

func (req ToolDefinitionRequest) partialUpdate() bool {
	return req.UpdateMask != nil
}

func (req ToolDefinitionRequest) updatesField(field string) bool {
	return updateMaskHas(req.UpdateMask, field)
}

func (req RegisterMCPServerRequest) partialUpdate() bool {
	return req.UpdateMask != nil
}

func (req RegisterMCPServerRequest) updatesField(field string) bool {
	return updateMaskHas(req.UpdateMask, field)
}

func (req RegisterMCPServerRequest) updatesAny(fields ...string) bool {
	for _, field := range fields {
		if req.updatesField(field) {
			return true
		}
	}
	return false
}

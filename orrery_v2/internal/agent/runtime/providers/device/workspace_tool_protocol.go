package device

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
)

// WorkspaceToolCall v1 is deliberately independent of an LLM or local shell.
// Older v1 device clients reject the unknown task kind; never fall back to agent_turn.
type WorkspaceToolCall struct {
	Version            int     `json:"version"`
	CallID             string  `json:"call_id"`
	Operation          string  `json:"operation"`
	RelativeName       string  `json:"relative_name,omitempty"`
	SheetName          string  `json:"sheet_name,omitempty"`
	StartRow           int     `json:"start_row,omitempty"`
	Limit              int     `json:"limit,omitempty"`
	SourceRelativeName string  `json:"source_relative_name,omitempty"`
	ExpectedSHA256     string  `json:"expected_sha256,omitempty"`
	Rows               [][]any `json:"rows,omitempty"`
}

var workspaceSegment = regexp.MustCompile(`^[^<>:"\\|?*\x00-\x1f]+$`)

// Retains the same project identity mapping as whole-Agent device delegation.
func WorkspaceToolProjectID(value string) string { return opaqueIdentifier(value, "project") }

func ValidWorkspaceRelativeName(s string) bool {
	if len(s) == 0 || len(s) > 240 || strings.HasPrefix(s, "/") || strings.Contains(s, "\\") {
		return false
	}
	for _, segment := range strings.Split(s, "/") {
		if segment == "" || segment == "." || segment == ".." || !workspaceSegment.MatchString(segment) || strings.TrimRight(segment, " .") != segment {
			return false
		}
	}
	return true
}

func (c WorkspaceToolCall) Capabilities() []Capability {
	if c.Operation == "write_excel" {
		return []Capability{CapabilityWorkspaceRead, CapabilityWorkspaceWrite}
	}
	return []Capability{CapabilityWorkspaceRead}
}

func (c WorkspaceToolCall) Validate() error {
	bad := errors.New("invalid workspace tool v1 request")
	raw, _ := json.Marshal(c)
	if c.Version != 1 || !validIdentifier(c.CallID) || len(raw) > 131072 {
		return bad
	}
	if c.RelativeName != "" && !ValidWorkspaceRelativeName(c.RelativeName) {
		return bad
	}
	switch c.Operation {
	case "list_files":
		if c.SheetName != "" || c.StartRow != 0 || c.SourceRelativeName != "" || c.ExpectedSHA256 != "" || c.Rows != nil || c.Limit < 0 || c.Limit > 100 {
			return bad
		}
	case "read_excel":
		if !strings.HasSuffix(strings.ToLower(c.RelativeName), ".xlsx") || (c.SheetName != "" && !bounded(c.SheetName, 1, 31)) || c.StartRow < 0 || c.StartRow > 10000 || c.Limit < 0 || c.Limit > 200 || c.Rows != nil || c.SourceRelativeName != "" || (c.ExpectedSHA256 != "" && !sha256Pattern.MatchString(c.ExpectedSHA256)) {
			return bad
		}
	case "write_excel":
		if !strings.HasSuffix(strings.ToLower(c.RelativeName), ".xlsx") || !ValidWorkspaceRelativeName(c.SourceRelativeName) || !strings.HasSuffix(strings.ToLower(c.SourceRelativeName), ".xlsx") || !sha256Pattern.MatchString(c.ExpectedSHA256) || !bounded(c.SheetName, 1, 31) || strings.ContainsAny(c.SheetName, `[]:*?/\`) || c.StartRow != 0 || c.Limit != 0 || len(c.Rows) < 1 || len(c.Rows) > 1000 {
			return bad
		}
		cells := 0
		for _, row := range c.Rows {
			if len(row) > 64 {
				return bad
			}
			cells += len(row)
			for _, cell := range row {
				switch v := cell.(type) {
				case nil, bool:
				case string:
					if !bounded(v, 0, 4096) {
						return bad
					}
				case float64:
					if math.IsNaN(v) || math.IsInf(v, 0) {
						return bad
					}
				default:
					return bad
				}
			}
		}
		if cells > 10000 {
			return bad
		}
	default:
		return bad
	}
	return nil
}

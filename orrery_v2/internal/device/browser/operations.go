// Package browser defines the fixed browser operations shared by the device
// channel and the Agent tool surface. It never executes browser or OS commands.
package browser

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const Version = "1"
const MaxResultBytes = 900 * 1024

//go:embed operations.schema.json
var operationSchemas []byte

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,100}$`)
var refPattern = regexp.MustCompile(`^e[1-9][0-9]{0,5}$`)

// Schemas returns a fresh copy of the generated, closed argument schemas.
func Schemas() map[string]map[string]any {
	var schemas map[string]map[string]any
	if err := json.Unmarshal(operationSchemas, &schemas); err != nil {
		panic("invalid embedded browser schemas")
	}
	return schemas
}

func IsControl(operation string) bool {
	switch operation {
	case "click", "fill", "press", "select":
		return true
	}
	return false
}

func IsMutation(operation string) bool {
	switch operation {
	case "navigate", "click", "fill", "press", "scroll", "select":
		return true
	}
	return false
}

func ValidToken(value string) bool { return tokenPattern.MatchString(value) }

// ValidateArguments validates against the same generated fields and constraints
// as the extension. The small supported schema subset is deliberately closed.
func ValidateArguments(operation string, arguments map[string]any) error {
	schema, found := Schemas()[operation]
	if !found || arguments == nil {
		return fmt.Errorf("invalid browser operation")
	}
	properties := schema["properties"].(map[string]any)
	if len(arguments) != len(properties) {
		return fmt.Errorf("invalid browser argument fields")
	}
	for key, raw := range properties {
		value, exists := arguments[key]
		if !exists {
			return fmt.Errorf("missing browser argument")
		}
		field := raw.(map[string]any)
		switch field["type"] {
		case "integer":
			number, ok := value.(float64)
			if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number != math.Trunc(number) || number < field["minimum"].(float64) || number > field["maximum"].(float64) {
				return fmt.Errorf("invalid browser integer")
			}
		case "string":
			text, ok := value.(string)
			if !ok || !utf8.ValidString(text) {
				return fmt.Errorf("invalid browser text")
			}
			// JavaScript string.length counts UTF-16 code units.
			length := 0
			for _, r := range text {
				length++
				if r > 0xffff {
					length++
				}
			}
			if maximum, ok := field["maxLength"].(float64); ok && length > int(maximum) {
				return fmt.Errorf("browser text is too long")
			}
			if pattern, ok := field["pattern"].(string); ok {
				valid := tokenPattern.MatchString(text)
				if pattern == refPattern.String() {
					valid = refPattern.MatchString(text)
				}
				if !valid {
					return fmt.Errorf("invalid browser reference")
				}
			}
			if values, ok := field["enum"].([]any); ok {
				matched := false
				for _, candidate := range values {
					if candidate == text {
						matched = true
					}
				}
				if !matched {
					return fmt.Errorf("invalid browser enum")
				}
			}
			if key == "url" {
				parsed, err := url.Parse(text)
				if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
					return fmt.Errorf("invalid browser URL")
				}
				host := strings.ToLower(parsed.Hostname())
				if host == "chromewebstore.google.com" || host == "microsoftedge.microsoft.com" || (host == "chrome.google.com" && strings.HasPrefix(parsed.Path, "/webstore")) {
					return fmt.Errorf("restricted browser URL")
				}
			}
		default:
			return fmt.Errorf("unsupported browser argument schema")
		}
	}
	return nil
}

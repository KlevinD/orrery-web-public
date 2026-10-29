package main

import (
	"fmt"
	"net/url"
	"strings"
)

func lockedServerBrowserArguments(downstreamTool string, input map[string]any) (map[string]any, error) {
	switch strings.TrimSpace(downstreamTool) {
	case defaultServerBrowserNavigateTool:
		rawURL, ok := input["url"].(string)
		rawURL = strings.TrimSpace(rawURL)
		if !ok || rawURL == "" || len(rawURL) > 4096 {
			return nil, fmt.Errorf("server browser navigation requires a valid url")
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return nil, fmt.Errorf("server browser navigation only allows absolute HTTP(S) URLs without embedded credentials")
		}
		return map[string]any{"url": rawURL}, nil
	case defaultServerBrowserScreenshotTool:
		arguments := map[string]any{}
		if value, exists := input["fullPage"]; exists {
			fullPage, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("server browser screenshot fullPage must be a boolean")
			}
			arguments["fullPage"] = fullPage
		}
		return arguments, nil
	default:
		return nil, fmt.Errorf("server browser tool %s is not allowed", strings.TrimSpace(downstreamTool))
	}
}

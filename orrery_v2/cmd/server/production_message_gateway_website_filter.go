package main

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"orrery_v2/internal/agent/outputpolicy"
)

const websiteMaxOutputSize = 128 << 10

var websiteKnowledgeSourcePattern = regexp.MustCompile(`(?i)minviabrain://[^\s)\]}>，。]+`)

type websiteAdvisorTextFilter struct {
	privacy outputpolicy.PrivateReasoningFilter
}

func (f *websiteAdvisorTextFilter) Write(value string) string {
	if f == nil {
		return value
	}
	return f.privacy.Write(value)
}

func (f *websiteAdvisorTextFilter) Flush() string {
	if f == nil {
		return ""
	}
	return f.privacy.Finish()
}

func appendWebsiteAdvisorText(output *strings.Builder, value string) bool {
	if output == nil {
		return false
	}
	if output.Len()+len(value) > websiteMaxOutputSize {
		return false
	}
	output.WriteString(value)
	return true
}

func websiteAdvisorApprovedSources(result map[string]string) []string {
	seen := map[string]struct{}{}
	var sources []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if len(value) > 2048 {
			return
		}
		parsed, err := url.Parse(value)
		if err != nil || !strings.EqualFold(parsed.Scheme, "minviabrain") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		sources = append(sources, value)
	}
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, nested := range typed {
				if strings.EqualFold(strings.TrimSpace(key), "source_uri") {
					if source, ok := nested.(string); ok {
						add(source)
					}
				}
				visit(nested)
			}
		case []any:
			for _, nested := range typed {
				visit(nested)
			}
		}
	}
	for key, value := range result {
		if strings.EqualFold(strings.TrimSpace(key), "source_uri") {
			add(value)
		}
		var decoded any
		if json.Unmarshal([]byte(value), &decoded) == nil {
			visit(decoded)
		}
	}
	return sources
}

func websiteAdvisorAnswerWithApprovedSources(answer string, sources []string) string {
	if len(sources) == 0 {
		return websiteAdvisorVisibleAnswer(answer)
	}
	seen := map[string]struct{}{}
	var unique []string
	for _, source := range sources {
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		unique = append(unique, source)
	}
	if len(unique) == 0 {
		return answer
	}
	citation := "\n\n来源：" + strings.Join(unique, "\n")
	answer = websiteAdvisorVisibleAnswer(answer)
	if len(answer)+len(citation) > websiteMaxOutputSize {
		return answer
	}
	return answer + citation
}

func websiteAdvisorVisibleAnswer(answer string) string {
	answer = websiteKnowledgeSourcePattern.ReplaceAllString(answer, "")
	lines := strings.Split(strings.ReplaceAll(answer, "\r\n", "\n"), "\n")
	visible := lines[:0]
	for _, line := range lines {
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "来源：", "来源:", "source:", "sources:":
			continue
		default:
			visible = append(visible, strings.TrimRight(line, " \t"))
		}
	}
	return strings.TrimSpace(strings.Join(visible, "\n"))
}

func websiteAdvisorOutputAllowed(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	for _, forbidden := range []string{
		"<think>", "</think>", "鉴权", "授权凭证", "凭证缺失", "后台错误", "后台鉴权",
		"系统提示授权", "接口暂时无法", "知识库通道", "credential", "authentication failed", "unauthorized",
		"/data/", "/home/", "/var/", "当前会话工作区内", "工作区限定在",
	} {
		if strings.Contains(lower, forbidden) {
			return false
		}
	}
	if strings.Contains(lower, "minviabrain://") {
		return true
	}
	for _, refusal := range []string{
		"当前公开资料不足", "公开资料不足以", "无法基于已审核的公开资料", "无法从已审核的公开资料",
		"approved public information is insufficient", "cannot confirm from approved public information",
	} {
		if strings.Contains(lower, refusal) {
			return true
		}
	}
	return false
}

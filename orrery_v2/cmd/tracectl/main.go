package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type auditFinding struct {
	Rule   string `json:"rule"`
	Path   string `json:"path"`
	RunID  string `json:"run_id,omitempty"`
	SHA256 string `json:"sha256"`
}

type auditReport struct {
	Scanned  int            `json:"scanned"`
	Findings []auditFinding `json:"findings"`
}

var auditCredentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`\beyJ[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\b`),
	regexp.MustCompile(`(?s)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?-----END [^-\r\n]*PRIVATE KEY-----`),
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "audit" {
		fmt.Fprintln(stderr, "usage: tracectl audit --root <artifact-root> --format json")
		return 2
	}
	flags := flag.NewFlagSet("audit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", "", "artifact root")
	format := flags.String("format", "json", "output format")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if strings.TrimSpace(*root) == "" || *format != "json" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "audit requires --root and --format json")
		return 2
	}
	report, err := auditTraceRoot(*root)
	if err != nil {
		fmt.Fprintln(stderr, "trace audit failed")
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(report); err != nil {
		return 1
	}
	return 0
}

func auditTraceRoot(root string) (auditReport, error) {
	root, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return auditReport{}, err
	}
	report := auditReport{Findings: []auditFinding{}}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !traceAuditFile(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		report.Scanned++
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		digest := sha256.Sum256(data)
		hash := hex.EncodeToString(digest[:])
		runID := auditRunID(relative)
		add := func(rule string) {
			report.Findings = append(report.Findings, auditFinding{Rule: rule, Path: relative, RunID: runID, SHA256: hash})
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm() != 0o600 {
			add("insecure_mode")
		}
		var value any
		if json.Unmarshal(data, &value) != nil {
			add("invalid_json")
			return nil
		}
		if filepath.Base(path) == "runtime-trace.json" {
			if object, ok := value.(map[string]any); ok {
				version, _ := object["trace_version"].(float64)
				if version >= 2 && strings.TrimSpace(stringValue(object["terminal_ref"])) == "" {
					add("missing_terminal")
				}
			}
		}
		if auditHasSensitiveKey(value) {
			add("sensitive_key")
		}
		if auditHasCredential(value) {
			add("credential_pattern")
		}
		return nil
	})
	sort.Slice(report.Findings, func(i, j int) bool {
		if report.Findings[i].Path != report.Findings[j].Path {
			return report.Findings[i].Path < report.Findings[j].Path
		}
		return report.Findings[i].Rule < report.Findings[j].Rule
	})
	return report, err
}

func traceAuditFile(path string) bool {
	name := filepath.Base(path)
	return name == "runtime-trace.json" || strings.Contains(filepath.ToSlash(path), "/trace-v2/") && strings.HasSuffix(name, ".json")
}

func auditRunID(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for index, part := range parts {
		if part == "runs" && index+1 < len(parts) {
			return parts[index+1]
		}
		if part == "agent-runs" && index+1 < len(parts) {
			return parts[index+1]
		}
	}
	return ""
}

func auditHasSensitiveKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.Map(func(r rune) rune {
				if unicode.IsLetter(r) || unicode.IsDigit(r) {
					return unicode.ToLower(r)
				}
				return -1
			}, key)
			for _, marker := range []string{"secret", "password", "apikey", "authorization", "credential", "cookie", "privatekey", "accesskey", "refreshtoken"} {
				if strings.Contains(normalized, marker) {
					return true
				}
			}
			if strings.Contains(normalized, "token") && !auditTokenMetricKey(normalized) {
				return true
			}
			if auditHasSensitiveKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if auditHasSensitiveKey(child) {
				return true
			}
		}
	}
	return false
}

func auditTokenMetricKey(normalized string) bool {
	switch normalized {
	case "inputtokens", "outputtokens", "totaltokens", "cachereadtokens", "cachewritetokens",
		"reasoningtokens", "cachedtokens", "maxtokens", "maxoutputtokens", "contexttokens",
		"contexttokenlimit", "prompttokenbudget", "tokenconsumed", "evaluationtokenconsumed", "tokenvalueper10k":
		return true
	default:
		return false
	}
}

func auditHasCredential(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if auditHasCredential(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if auditHasCredential(child) {
				return true
			}
		}
	case string:
		for _, pattern := range auditCredentialPatterns {
			if pattern.MatchString(typed) {
				return true
			}
		}
	}
	return false
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

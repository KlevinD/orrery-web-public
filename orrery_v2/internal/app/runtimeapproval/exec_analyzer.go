package runtimeapproval

import (
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type ExecRequest struct {
	Command          string
	WorkingDirectory string
	WorkspaceRoots   []string
	EnvironmentNames []string
}

type ParsedCommand struct {
	Argv []string `json:"argv"`
}

type ExecAnalysis struct {
	Safe                  bool
	ForcedHuman           bool
	ConfiguredForcedHuman bool
	Reason                string
	WorkingDirectory      string
	Commands              []ParsedCommand
	MatchedRuleIDs        []string
}

func AnalyzeExec(input ExecRequest, policy ExecPolicy, forcedPolicies ...ForcedHumanPolicy) ExecAnalysis {
	analysis := ExecAnalysis{
		WorkingDirectory: strings.TrimSpace(input.WorkingDirectory),
		Commands:         []ParsedCommand{},
		MatchedRuleIDs:   []string{},
	}
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := parser.Parse(strings.NewReader(strings.TrimSpace(input.Command)), "runtime-approval")
	if err != nil {
		analysis.Reason = "exec_parse_error"
		return analysis
	}
	if len(file.Stmts) == 0 {
		analysis.Reason = "exec_empty"
		return analysis
	}
	commands, reason := staticStatementCommands(file.Stmts)
	analysis.Commands = commands
	if reason != "" {
		analysis.Reason = reason
		return analysis
	}
	for _, command := range commands {
		if len(forcedPolicies) > 0 && containsString(forcedPolicies[0].ExecExecutables, command.Argv[0]) {
			analysis.Reason = "forced_configured_exec"
			analysis.ForcedHuman = true
			analysis.ConfiguredForcedHuman = true
			return analysis
		}
		if reason, forced := dangerousExec(command.Argv); reason != "" {
			analysis.Reason = reason
			analysis.ForcedHuman = forced
			return analysis
		}
	}

	policy = normalizeExecPolicy(policy)
	switch policy.Security {
	case SecurityDeny:
		analysis.Reason = "exec_policy_deny"
		return analysis
	case SecurityFull:
		analysis.Safe = true
		analysis.Reason = "exec_full"
		return analysis
	case SecurityAllowlist:
	default:
		analysis.Reason = "exec_policy_invalid"
		return analysis
	}

	for _, command := range commands {
		rule, reason, ok := matchExecRule(command.Argv, analysis.WorkingDirectory, input.WorkspaceRoots, policy.Allowlist)
		if !ok {
			analysis.Reason = reason
			return analysis
		}
		analysis.MatchedRuleIDs = append(analysis.MatchedRuleIDs, rule.RuleID)
	}
	analysis.Safe = true
	analysis.Reason = "exec_allowlist_match"
	return analysis
}

func staticStatementCommands(statements []*syntax.Stmt) ([]ParsedCommand, string) {
	commands := make([]ParsedCommand, 0, len(statements))
	for _, statement := range statements {
		parsed, reason := staticCommands(statement)
		if reason != "" {
			return nil, reason
		}
		commands = append(commands, parsed...)
	}
	return commands, ""
}

func staticCommands(stmt *syntax.Stmt) ([]ParsedCommand, string) {
	if stmt == nil {
		return nil, "exec_empty"
	}
	if stmt.Background {
		return nil, "exec_background"
	}
	if stmt.Negated || stmt.Coprocess {
		return nil, "exec_compound"
	}
	if len(stmt.Redirs) > 0 {
		return nil, "exec_redirection"
	}
	switch command := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		parsed, reason := staticCall(command)
		if reason != "" {
			return nil, reason
		}
		return []ParsedCommand{parsed}, ""
	case *syntax.BinaryCmd:
		if command.Op != syntax.Pipe && command.Op != syntax.AndStmt {
			return nil, "exec_compound"
		}
		left, reason := staticCommands(command.X)
		if reason != "" {
			return nil, reason
		}
		right, reason := staticCommands(command.Y)
		if reason != "" {
			return nil, reason
		}
		return append(left, right...), ""
	default:
		return nil, "exec_compound"
	}
}

func staticCall(call *syntax.CallExpr) (ParsedCommand, string) {
	if call == nil || len(call.Args) == 0 {
		return ParsedCommand{}, "exec_empty"
	}
	if len(call.Assigns) > 0 {
		return ParsedCommand{}, "exec_assignment"
	}
	argv := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		value, ok := staticWord(word)
		if !ok {
			return ParsedCommand{}, "exec_dynamic_word"
		}
		argv = append(argv, value)
	}
	return ParsedCommand{Argv: argv}, ""
}

func staticWord(word *syntax.Word) (string, bool) {
	if word == nil {
		return "", false
	}
	var value strings.Builder
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			value.WriteString(part.Value)
		case *syntax.SglQuoted:
			if part.Dollar {
				return "", false
			}
			value.WriteString(part.Value)
		case *syntax.DblQuoted:
			if part.Dollar {
				return "", false
			}
			quoted, ok := staticQuotedParts(part.Parts)
			if !ok {
				return "", false
			}
			value.WriteString(quoted)
		default:
			return "", false
		}
	}
	return value.String(), true
}

func staticQuotedParts(parts []syntax.WordPart) (string, bool) {
	var value strings.Builder
	for _, part := range parts {
		lit, ok := part.(*syntax.Lit)
		if !ok {
			return "", false
		}
		value.WriteString(lit.Value)
	}
	return value.String(), true
}

func dangerousExec(argv []string) (string, bool) {
	if len(argv) == 0 {
		return "exec_empty", false
	}
	executable := strings.ToLower(filepath.Base(argv[0]))
	if argv[0] != filepath.Base(argv[0]) {
		return "exec_executable_path", false
	}
	if inlineInterpreter(executable, argv[1:]) {
		return "exec_inline_interpreter", false
	}
	for _, argument := range argv[1:] {
		lower := strings.ToLower(argument)
		switch executable {
		case "sed":
			if lower == "-i" || strings.HasPrefix(lower, "-i") || strings.HasPrefix(lower, "--in-place") {
				return "exec_mutation_flag", true
			}
		case "find":
			switch lower {
			case "-delete", "-exec", "-execdir", "-ok", "-okdir":
				return "exec_mutation_flag", true
			}
		}
	}
	if containsString([]string{"rm", "rmdir", "unlink", "shred"}, executable) {
		return "exec_destructive", true
	}
	if containsString([]string{"chmod", "chown", "chgrp", "passwd", "sudo", "su"}, executable) {
		return "exec_permission_change", true
	}
	if executable == "git" {
		subcommand := firstPositional(argv[1:])
		if containsString([]string{"add", "am", "apply", "branch", "checkout", "cherry-pick", "clean", "commit", "merge", "mv", "pull", "push", "rebase", "reset", "restore", "revert", "rm", "switch", "tag"}, subcommand) {
			return "exec_mutating_command", true
		}
	}
	return "", false
}

func inlineInterpreter(executable string, arguments []string) bool {
	if containsString([]string{"bash", "dash", "fish", "ksh", "sh", "zsh"}, executable) {
		return true
	}
	flags := map[string][]string{
		"node":    {"-e", "--eval", "-p", "--print"},
		"perl":    {"-e", "-E"},
		"php":     {"-r"},
		"python":  {"-c"},
		"python3": {"-c"},
		"ruby":    {"-e"},
	}
	for _, argument := range arguments {
		if containsString(flags[executable], argument) {
			return true
		}
	}
	return false
}

func matchExecRule(argv []string, workingDirectory string, workspaceRoots []string, rules []ExecRule) (ExecRule, string, bool) {
	if len(argv) == 0 {
		return ExecRule{}, "exec_empty", false
	}
	for _, rule := range rules {
		if argv[0] != rule.Executable {
			continue
		}
		subcommandIndex := -1
		if len(rule.Subcommands) > 0 {
			for i, argument := range argv[1:] {
				if !strings.HasPrefix(argument, "-") {
					subcommandIndex = i + 1
					break
				}
			}
			if subcommandIndex < 0 || !containsString(rule.Subcommands, argv[subcommandIndex]) {
				continue
			}
		}
		if !flagsAllowed(argv[1:], rule.SafeFlags) {
			continue
		}
		if rule.WorkspaceOnly {
			if !pathWithinRoots(workingDirectory, workingDirectory, workspaceRoots) {
				return ExecRule{}, "exec_workspace_escape", false
			}
			for i, argument := range argv[1:] {
				argumentIndex := i + 1
				if argumentIndex == subcommandIndex || strings.HasPrefix(argument, "-") || argument == "" {
					continue
				}
				if !pathWithinRoots(argument, workingDirectory, workspaceRoots) {
					return ExecRule{}, "exec_workspace_escape", false
				}
			}
		}
		return rule, "", true
	}
	return ExecRule{}, "exec_allowlist_miss", false
}

func flagsAllowed(arguments []string, allowed []string) bool {
	for _, argument := range arguments {
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			continue
		}
		matched := false
		for _, flag := range allowed {
			if argument == flag || strings.HasPrefix(argument, flag+"=") {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func pathWithinRoots(argument string, workingDirectory string, roots []string) bool {
	if len(roots) == 0 {
		return false
	}
	path := argument
	if !isAbsoluteExecPath(path) {
		path = filepath.Join(workingDirectory, path)
	}
	path, ok := resolvePathForBoundary(path)
	if !ok {
		return false
	}
	for _, root := range roots {
		root, ok = resolvePathForBoundary(strings.TrimSpace(root))
		if !ok {
			continue
		}
		if root == "" || root == string(filepath.Separator) {
			continue
		}
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func isAbsoluteExecPath(path string) bool {
	path = strings.TrimSpace(path)
	return filepath.IsAbs(path) || strings.HasPrefix(path, "/")
}

func resolvePathForBoundary(path string) (string, bool) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return "", false
	}
	unresolved := make([]string, 0, 4)
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for index := len(unresolved) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, unresolved[index])
			}
			return filepath.Clean(resolved), true
		}
		if !os.IsNotExist(err) {
			return "", false
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", false
		}
		unresolved = append(unresolved, filepath.Base(path))
		path = parent
	}
}

func firstPositional(arguments []string) string {
	for _, argument := range arguments {
		if !strings.HasPrefix(argument, "-") {
			return strings.ToLower(argument)
		}
	}
	return ""
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

package execenv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type PrepareRequest struct {
	Root           string
	RunID          agentcore.AgentRunID
	AgentID        agentcore.AgentID
	Runtime        string
	WorkDir        string
	Context        agentcore.ContextPack
	Tools          []agentcore.RuntimeToolBinding
	Skills         []agentcore.RuntimeSkillBinding
	CleanupOld     bool
	MCPConfig      json.RawMessage
	SandboxProfile agentcore.RuntimeSandboxProfile
}

type Environment struct {
	RootDir       string
	WorkDir       string
	SystemDir     string
	OutputDir     string
	LogsDir       string
	RuntimeHome   string
	ContextPath   string
	ToolsPath     string
	SkillsPath    string
	MetadataPath  string
	MCPConfigPath string
	RuntimeEnv    map[string]string
}

func Prepare(req PrepareRequest) (Environment, error) {
	if req.Root == "" {
		return Environment{}, fmt.Errorf("execenv root is required")
	}
	if req.RunID == "" {
		return Environment{}, fmt.Errorf("run id is required")
	}
	runRoot := filepath.Join(req.Root, req.RunID.String())
	if req.CleanupOld {
		if err := os.RemoveAll(runRoot); err != nil {
			return Environment{}, fmt.Errorf("remove old execenv: %w", err)
		}
	}
	workDir, err := prepareWorkDir(runRoot, req.WorkDir)
	if err != nil {
		return Environment{}, err
	}
	env := Environment{
		RootDir:     runRoot,
		WorkDir:     workDir,
		SystemDir:   filepath.Join(runRoot, "system"),
		OutputDir:   filepath.Join(runRoot, "output"),
		LogsDir:     filepath.Join(runRoot, "logs"),
		RuntimeHome: filepath.Join(runRoot, runtimeHomeDir(req.Runtime)),
		RuntimeEnv:  map[string]string{},
	}
	for _, dir := range []string{env.WorkDir, env.SystemDir, env.RuntimeHome, env.OutputDir, env.LogsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Environment{}, fmt.Errorf("create execenv dir %s: %w", dir, err)
		}
	}
	if err := removeLegacyRuntimeContextFiles(env.WorkDir); err != nil {
		return Environment{}, err
	}
	if strings.EqualFold(strings.TrimSpace(req.Runtime), "codex") {
		if err := ensureCodexRuntimeConfig(env.RuntimeHome); err != nil {
			return Environment{}, err
		}
		if err := prepareCodexRuntimeAuth(env.RuntimeHome); err != nil {
			return Environment{}, err
		}
	}
	env.RuntimeEnv = runtimeEnv(req.Runtime, env.RuntimeHome)
	if strings.EqualFold(strings.TrimSpace(req.Runtime), "openclaw") {
		result, err := prepareOpenClawConfig(runRoot, env.WorkDir, OpenClawConfigPrep{AgentID: req.AgentID})
		if err != nil {
			return Environment{}, err
		}
		env.RuntimeEnv["OPENCLAW_CONFIG_PATH"] = result.ConfigPath
		env.RuntimeEnv["ORRERY_OPENCLAW_AGENT_ID"] = result.AgentID
		env.MCPConfigPath = result.ConfigPath
		if result.IncludeRoot != "" {
			env.RuntimeEnv["OPENCLAW_INCLUDE_ROOTS"] = prependPathList(result.IncludeRoot, os.Getenv("OPENCLAW_INCLUDE_ROOTS"))
		}
	}
	contextPath := filepath.Join(env.SystemDir, "AGENT_CONTEXT.md")
	contextText := renderContext(req)
	if err := os.WriteFile(contextPath, []byte(contextText), 0o600); err != nil {
		return Environment{}, fmt.Errorf("write context file: %w", err)
	}
	env.ContextPath = contextPath
	env.RuntimeEnv["ORRERY_AGENT_CONTEXT_PATH"] = env.ContextPath
	if strings.EqualFold(strings.TrimSpace(req.Runtime), "pi") {
		agentsPath := filepath.Join(env.SystemDir, "AGENTS.md")
		if err := os.WriteFile(agentsPath, []byte(contextText), 0o600); err != nil {
			return Environment{}, fmt.Errorf("write pi AGENTS.md: %w", err)
		}
		env.RuntimeEnv["ORRERY_PI_AGENTS_PATH"] = agentsPath
		skillPaths, err := preparePiSkills(runRoot, req.Skills, req.Context.SkillSummaries)
		if err != nil {
			return Environment{}, err
		}
		if len(skillPaths) > 0 {
			env.RuntimeEnv["ORRERY_PI_SKILL_PATHS"] = strings.Join(skillPaths, string(os.PathListSeparator))
		}
	}
	env.ToolsPath = filepath.Join(env.SystemDir, "TOOL_SUMMARY.md")
	if err := os.WriteFile(env.ToolsPath, []byte(renderTools(req.Tools, req.Context.ToolSummaries)), 0o600); err != nil {
		return Environment{}, fmt.Errorf("write tool summary: %w", err)
	}
	env.RuntimeEnv["ORRERY_TOOL_SUMMARY_PATH"] = env.ToolsPath
	env.SkillsPath = filepath.Join(env.SystemDir, "SKILLS.md")
	if err := os.WriteFile(env.SkillsPath, []byte(renderSkills(req.Skills, req.Context.SkillSummaries)), 0o600); err != nil {
		return Environment{}, fmt.Errorf("write skill summary: %w", err)
	}
	env.RuntimeEnv["ORRERY_SKILLS_PATH"] = env.SkillsPath
	if len(req.MCPConfig) > 0 {
		var err error
		env, err = applyMCPInjection(env, req.Runtime, req.MCPConfig)
		if err != nil {
			return Environment{}, err
		}
		if env.MCPConfigPath != "" {
			env.RuntimeEnv["ORRERY_MCP_CONFIG_PATH"] = env.MCPConfigPath
		}
	}
	env.MetadataPath = filepath.Join(runRoot, "gc_meta.json")
	if err := os.WriteFile(env.MetadataPath, []byte(renderGCMeta(req, env)), 0o600); err != nil {
		return Environment{}, fmt.Errorf("write gc metadata: %w", err)
	}
	return env, nil
}

func removeLegacyRuntimeContextFiles(workDir string) error {
	legacyFiles := map[string]string{
		"AGENT_CONTEXT.md": "# Agent Runtime Context\n",
		"AGENTS.md":        "# Agent Runtime Context\n",
		"TOOL_SUMMARY.md":  "# Tool Summary\n",
		"SKILLS.md":        "# Skills\n",
	}
	for name, generatedPrefix := range legacyFiles {
		path := filepath.Join(workDir, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect legacy runtime context file %s: %w", path, err)
		}
		if !info.Mode().IsRegular() || info.Size() > 4<<20 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read legacy runtime context file %s: %w", path, err)
		}
		if !strings.HasPrefix(strings.ReplaceAll(string(data), "\r\n", "\n"), generatedPrefix) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove legacy runtime context file %s: %w", path, err)
		}
	}
	return nil
}

func runtimeHomeDir(runtime string) string {
	if strings.TrimSpace(runtime) == "" {
		return "runtime-home"
	}
	return strings.ToLower(runtime) + "-home"
}

func prepareCodexRuntimeAuth(runtimeHome string) error {
	source, required, err := codexAuthSource(runtimeHome)
	if err != nil {
		return err
	}
	if source == "" {
		return nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		if os.IsNotExist(err) && !required {
			return nil
		}
		return fmt.Errorf("read Codex authentication file: %w", err)
	}
	var auth map[string]json.RawMessage
	if err := json.Unmarshal(data, &auth); err != nil || len(auth) == 0 {
		return fmt.Errorf("read Codex authentication file: invalid JSON object")
	}
	destination := filepath.Join(runtimeHome, "auth.json")
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		return fmt.Errorf("write run-scoped Codex authentication file: %w", err)
	}
	return nil
}

func codexAuthSource(runtimeHome string) (string, bool, error) {
	if explicit := strings.TrimSpace(os.Getenv("ORRERY_CODEX_AUTH_FILE")); explicit != "" {
		return filepath.Clean(explicit), true, nil
	}
	if inheritedHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); inheritedHome != "" {
		candidate := filepath.Join(filepath.Clean(inheritedHome), "auth.json")
		if !sameCleanPath(filepath.Dir(candidate), runtimeHome) {
			return candidate, false, nil
		}
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", false, nil
	}
	return filepath.Join(userHome, ".codex", "auth.json"), false, nil
}

func sameCleanPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func prepareWorkDir(runRoot, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return filepath.Join(runRoot, "workdir"), nil
	}
	clean := filepath.Clean(requested)
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return "", fmt.Errorf("create requested workdir %s: %w", clean, err)
	}
	return clean, nil
}

func runtimeEnv(runtime string, home string) map[string]string {
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "codex":
		return map[string]string{"CODEX_HOME": home}
	case "claude":
		// TODO: Remove this temporary host-config fallback. Claude must regain a
		// run-scoped CLAUDE_CONFIG_DIR once authentication is explicitly inherited.
		return map[string]string{}
	case "gemini":
		return map[string]string{"GEMINI_CLI_TRUST_WORKSPACE": "true", "GEMINI_HOME": home}
	case "cursor":
		return map[string]string{"CURSOR_AGENT_HOME": home}
	case "openclaw":
		return map[string]string{"OPENCLAW_HOME": home}
	case "pi":
		return map[string]string{"PI_CODING_AGENT_DIR": home}
	default:
		return map[string]string{"AGENT_RUNTIME_HOME": home}
	}
}

func preparePiSkills(runRoot string, runtimeSkills []agentcore.RuntimeSkillBinding, contextSkills []agentcore.ContextSkillSummary) ([]string, error) {
	skills := mergedPiSkills(runtimeSkills, contextSkills)
	if len(skills) == 0 {
		return nil, nil
	}
	root := filepath.Join(runRoot, "pi-skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create pi skills dir: %w", err)
	}
	paths := make([]string, 0, len(skills))
	for _, skill := range skills {
		dir := filepath.Join(root, skillNameForPi(skill))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create pi skill dir: %w", err)
		}
		path := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(path, []byte(renderPiSkill(skill)), 0o600); err != nil {
			return nil, fmt.Errorf("write pi skill %s: %w", skill.SkillID, err)
		}
		paths = append(paths, dir)
	}
	return paths, nil
}

type piSkillSummary struct {
	SkillID string
	Alias   string
	Version string
	Summary string
}

func mergedPiSkills(runtimeSkills []agentcore.RuntimeSkillBinding, contextSkills []agentcore.ContextSkillSummary) []piSkillSummary {
	out := make([]piSkillSummary, 0, len(runtimeSkills)+len(contextSkills))
	seen := map[string]bool{}
	for _, skill := range runtimeSkills {
		id := strings.TrimSpace(skill.SkillID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, piSkillSummary{SkillID: id, Alias: strings.TrimSpace(skill.Alias), Version: strings.TrimSpace(skill.Version), Summary: strings.TrimSpace(skill.Summary)})
	}
	for _, skill := range contextSkills {
		id := strings.TrimSpace(string(skill.SkillID))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, piSkillSummary{SkillID: id, Alias: strings.TrimSpace(skill.Alias), Version: strings.TrimSpace(skill.Version), Summary: strings.TrimSpace(skill.Summary)})
	}
	return out
}

func renderPiSkill(skill piSkillSummary) string {
	name := skillNameForPi(skill)
	description := firstNonBlank(skill.Summary, "Use Orrery bound skill "+skill.SkillID+".")
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "description: %s\n", yamlSingleLine(description))
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", skill.SkillID)
	if skill.Version != "" {
		fmt.Fprintf(&b, "Version: `%s`\n\n", skill.Version)
	}
	b.WriteString("This skill is bound by Orrery for the current Agent run.\n\n")
	fmt.Fprintf(&b, "Use the `load_skill` runtime tool with `skill_id: %q` to load the full SKILL.md instructions before applying this skill.\n", skill.SkillID)
	b.WriteString("Use `list_skill_resources` and `load_resource` for referenced skill assets or documents.\n")
	return b.String()
}

func skillNameForPi(skill piSkillSummary) string {
	return safePiSkillName(firstNonBlank(skill.Alias, skill.SkillID))
}

func safePiSkillName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastHyphen := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastHyphen = false
			continue
		}
		if !lastHyphen && b.Len() > 0 {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "orrery-skill"
	}
	if len(out) > 64 {
		out = strings.Trim(out[:64], "-")
	}
	if out == "" {
		return "orrery-skill"
	}
	return out
}

func yamlSingleLine(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.TrimSpace(value)
	return strconv.Quote(value)
}

func prependPathList(first, rest string) string {
	if strings.TrimSpace(rest) == "" {
		return first
	}
	return first + string(os.PathListSeparator) + rest
}

func renderContext(req PrepareRequest) string {
	var b strings.Builder
	b.WriteString("# Agent Runtime Context\n\n")
	writeSection(&b, "Mission Final Goal", req.Context.MissionFinalGoal)
	writeSection(&b, "Discussion Group Goal", req.Context.DiscussionGroupGoal)
	writeSection(&b, "Agent Organization Identity", renderOrg(req.Context.AgentOrganizationIdentity))
	writeSection(&b, "Agent Mission Role", req.Context.AgentMissionRole)
	writeSection(&b, "Worker Personal Goal", req.Context.WorkerPersonalGoal)
	writeSection(&b, "Task Contract", req.Context.TaskContractSummary)
	if len(req.Context.AcceptanceCriteria) > 0 {
		b.WriteString("## Acceptance Criteria\n\n")
		for _, item := range req.Context.AcceptanceCriteria {
			fmt.Fprintf(&b, "- `%s`: %s\n", item.ID, item.Description)
		}
		b.WriteString("\n")
	}
	if len(req.Context.RequiredEvidence) > 0 {
		b.WriteString("## Required Evidence\n\n")
		for _, item := range req.Context.RequiredEvidence {
			fmt.Fprintf(&b, "- `%s` (%s): %s\n", item.ID, item.Kind, item.Description)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Memory Digests\n\n")
	if metadata := agentcore.BuildMemoryEvidencePrompt(req.Context); metadata != "" {
		b.WriteString(metadata + "\n")
	}
	if req.Context.PersonalMemoryDigestSummary != "" {
		fmt.Fprintf(&b, "- personal `%s`: %s\n", req.Context.PersonalMemoryDigestRef.ID, req.Context.PersonalMemoryDigestSummary)
	}
	for _, item := range req.Context.CollectiveMemoryDigestSummaries {
		fmt.Fprintf(&b, "- %s `%s`: %s\n", item.MemoryKind, item.ID, item.Summary)
	}
	b.WriteString("\n")
	writeSection(&b, "Discussion Digest", req.Context.DiscussionDigestSummary)
	if len(req.Context.PermissionConstraints) > 0 {
		b.WriteString("## Permission Constraints\n\n")
		for _, item := range req.Context.PermissionConstraints {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if len(req.Context.ToolSummaries) > 0 || len(req.Tools) > 0 {
		b.WriteString("## Tools\n\n")
		b.WriteString(renderTools(req.Tools, req.Context.ToolSummaries))
		b.WriteString("\n")
	}
	if len(req.Context.SkillSummaries) > 0 || len(req.Skills) > 0 {
		b.WriteString("## Skills\n\n")
		b.WriteString(renderSkills(req.Skills, req.Context.SkillSummaries))
		b.WriteString("\n")
	}
	b.WriteString("## Source Refs\n\n")
	for _, ref := range req.Context.SourceRefs {
		fmt.Fprintf(&b, "- %s immutable=%v priority=%d\n", ref.Kind, ref.Immutable, ref.Priority)
	}
	b.WriteString("\n")
	return b.String()
}

func renderTools(runtimeTools []agentcore.RuntimeToolBinding, contextTools []agentcore.ContextToolSummary) string {
	var b strings.Builder
	b.WriteString("# Tool Summary\n\n")
	written := map[string]bool{}
	for _, tool := range runtimeTools {
		writeToolSummary(&b, string(tool.ToolID), tool.Alias, tool.Version, tool.Summary, tool.GrantedPermissions, tool.SecretEnv)
		written[string(tool.ToolID)] = true
	}
	for _, tool := range contextTools {
		if written[string(tool.ToolID)] {
			continue
		}
		writeToolSummary(&b, string(tool.ToolID), tool.Alias, tool.Version, tool.Summary, tool.GrantedPermissions, tool.SecretEnv)
	}
	return b.String()
}

func writeToolSummary(b *strings.Builder, id, alias, version, summary string, permissions []string, secretEnv map[string]string) {
	fmt.Fprintf(b, "- `%s`", id)
	if alias != "" {
		fmt.Fprintf(b, " alias=%s", alias)
	}
	if version != "" {
		fmt.Fprintf(b, " version=%s", version)
	}
	if summary != "" {
		fmt.Fprintf(b, ": %s", summary)
	}
	b.WriteString("\n")
	for _, permission := range permissions {
		fmt.Fprintf(b, "  - permission: %s\n", permission)
	}
	for env, ref := range secretEnv {
		fmt.Fprintf(b, "  - secret_ref: %s=%s\n", env, ref)
	}
}

func renderSkills(runtimeSkills []agentcore.RuntimeSkillBinding, contextSkills []agentcore.ContextSkillSummary) string {
	var b strings.Builder
	b.WriteString("# Skills\n\n")
	written := map[string]bool{}
	for _, skill := range runtimeSkills {
		writeSkillSummary(&b, skill.SkillID, skill.Alias, skill.Version, skill.Summary)
		written[skill.SkillID] = true
	}
	for _, skill := range contextSkills {
		if written[string(skill.SkillID)] {
			continue
		}
		writeSkillSummary(&b, string(skill.SkillID), skill.Alias, skill.Version, skill.Summary)
	}
	return b.String()
}

func writeSkillSummary(b *strings.Builder, id, alias, version, summary string) {
	fmt.Fprintf(b, "- `%s`", id)
	if alias != "" {
		fmt.Fprintf(b, " alias=%s", alias)
	}
	if version != "" {
		fmt.Fprintf(b, " version=%s", version)
	}
	if summary != "" {
		fmt.Fprintf(b, ": %s", summary)
	}
	b.WriteString("\n")
}

func renderGCMeta(req PrepareRequest, env Environment) string {
	payload := map[string]any{
		"kind":       "agent_run",
		"run_id":     req.RunID.String(),
		"runtime":    req.Runtime,
		"agent_id":   req.Context.AgentID.String(),
		"mission_id": string(req.Context.MissionID),
		"task_id":    string(req.Context.TaskID),
		"root_dir":   env.RootDir,
		"created_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if !req.SandboxProfile.Empty() {
		payload["sandbox_profile"] = map[string]any{
			"mode":                   req.SandboxProfile.Mode,
			"approval":               req.SandboxProfile.Approval,
			"network":                req.SandboxProfile.Network,
			"runtime_home_isolation": req.SandboxProfile.RuntimeHomeIsolation,
			"retention":              req.SandboxProfile.Retention,
			"root":                   req.SandboxProfile.Root,
			"work_dir":               req.SandboxProfile.WorkDir,
			"writable_roots":         append([]string(nil), req.SandboxProfile.WritableRoots...),
			"enforce_boundary":       req.SandboxProfile.EnforceBoundary,
		}
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func renderOrg(identity agentcore.AgentOrgIdentity) string {
	var parts []string
	if identity.OrganizationRef.ID != "" {
		parts = append(parts, "organization="+string(identity.OrganizationRef.ID))
	}
	if identity.DepartmentRef.ID != "" {
		parts = append(parts, "department="+string(identity.DepartmentRef.ID))
	}
	if identity.PositionRef.ID != "" {
		parts = append(parts, "position="+string(identity.PositionRef.ID))
	}
	if identity.AuthorityScope != "" {
		parts = append(parts, "authority="+identity.AuthorityScope)
	}
	return strings.Join(parts, "\n")
}

func writeSection(b *strings.Builder, title, body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	fmt.Fprintf(b, "## %s\n\n%s\n\n", title, body)
}

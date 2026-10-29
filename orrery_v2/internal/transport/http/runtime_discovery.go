package httptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type RuntimeDiscoveryResponseDTO struct {
	Candidates []RuntimeDiscoveryCandidateDTO `json:"candidates"`
}

type RuntimeDiscoveryCandidateDTO struct {
	Kind          string         `json:"kind"`
	Label         string         `json:"label"`
	Executable    string         `json:"executable"`
	ConfigRef     string         `json:"config_ref"`
	Version       string         `json:"version,omitempty"`
	Status        string         `json:"status"`
	Source        string         `json:"source"`
	RuntimeConfig map[string]any `json:"runtime_config"`
	Notes         []string       `json:"notes,omitempty"`
}

type RuntimeDiscoveryService interface {
	DiscoverRuntimeCandidates(ctx context.Context) (RuntimeDiscoveryResponseDTO, error)
}

type localRuntimeDiscoveryService struct {
	opts runtimeDiscoveryOptions
}

type runtimeDiscoveryOptions struct {
	lookPath             func(string) (string, error)
	runCommand           func(context.Context, string, ...string) (string, error)
	resolveViaLoginShell func([]string) map[string]string
	goos                 string
	timeout              time.Duration
}

type runtimeProviderDiscoverySpec struct {
	kind      string
	label     string
	command   string
	configRef string
	configure func(context.Context, *localRuntimeDiscoveryService, string, map[string]any) []string
}

func newDefaultRuntimeDiscoveryService() *localRuntimeDiscoveryService {
	return newLocalRuntimeDiscoveryService(runtimeDiscoveryOptions{})
}

func newLocalRuntimeDiscoveryService(opts runtimeDiscoveryOptions) *localRuntimeDiscoveryService {
	if opts.lookPath == nil {
		opts.lookPath = exec.LookPath
	}
	if opts.runCommand == nil {
		opts.runCommand = runRuntimeDiscoveryCommand
	}
	if opts.resolveViaLoginShell == nil {
		opts.resolveViaLoginShell = resolveRuntimeProvidersViaLoginShell
	}
	if opts.goos == "" {
		opts.goos = runtime.GOOS
	}
	if opts.timeout <= 0 {
		opts.timeout = 5 * time.Second
	}
	return &localRuntimeDiscoveryService{opts: opts}
}

func (s *localRuntimeDiscoveryService) DiscoverRuntimeCandidates(ctx context.Context) (RuntimeDiscoveryResponseDTO, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeDiscoveryResponseDTO{}, err
	}
	specs := runtimeDiscoverySpecs()
	commands := make([]string, 0, len(specs))
	for _, spec := range specs {
		commands = append(commands, spec.command)
	}
	shellResolved := map[string]string{}
	useLoginShell := s.opts.goos != "windows"
	var candidates []RuntimeDiscoveryCandidateDTO
	for _, spec := range specs {
		executable, source := s.resolveExecutable(spec)
		if executable == "" && useLoginShell {
			if len(shellResolved) == 0 {
				shellResolved = s.opts.resolveViaLoginShell(commands)
			}
			if resolved := strings.TrimSpace(shellResolved[spec.command]); resolved != "" {
				if _, err := s.opts.lookPath(resolved); err == nil {
					executable = resolved
					source = "login_shell"
				}
			}
		}
		if executable == "" {
			continue
		}
		config := map[string]any{"executable": executable}
		notes := []string{}
		if spec.configure != nil {
			notes = append(notes, spec.configure(ctx, s, executable, config)...)
		}
		version, err := s.version(ctx, executable)
		status := "ready"
		if err != nil {
			status = "warning"
			notes = append(notes, err.Error())
		}
		candidates = append(candidates, RuntimeDiscoveryCandidateDTO{
			Kind:          spec.kind,
			Label:         spec.label,
			Executable:    executable,
			ConfigRef:     spec.configRef,
			Version:       version,
			Status:        status,
			Source:        source,
			RuntimeConfig: config,
			Notes:         notes,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Kind < candidates[j].Kind
	})
	return RuntimeDiscoveryResponseDTO{Candidates: candidates}, nil
}

func runtimeDiscoverySpecs() []runtimeProviderDiscoverySpec {
	return []runtimeProviderDiscoverySpec{
		{kind: "codex", label: "Codex", command: "codex", configRef: "runtime-codex-local"},
		{kind: "claude", label: "Claude Code", command: "claude", configRef: "runtime-claude-local"},
		{kind: "cursor", label: "Cursor", command: "cursor-agent", configRef: "runtime-cursor-local"},
		{kind: "gemini", label: "Gemini", command: "gemini", configRef: "runtime-gemini-local"},
		{kind: "openclaw", label: "OpenClaw", command: "openclaw", configRef: "runtime-openclaw-local", configure: configureOpenClawDiscovery},
		{kind: "hermes", label: "Hermes", command: "hermes", configRef: "runtime-hermes-local"},
		{kind: "pi", label: "PI", command: "pi", configRef: "runtime-pi-local", configure: configurePiDiscovery},
	}
}

func (s *localRuntimeDiscoveryService) resolveExecutable(spec runtimeProviderDiscoverySpec) (string, string) {
	if _, err := s.opts.lookPath(spec.command); err == nil {
		return spec.command, "PATH"
	}
	return "", ""
}

func (s *localRuntimeDiscoveryService) version(ctx context.Context, executable string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, s.opts.timeout)
	defer cancel()
	out, err := s.opts.runCommand(runCtx, executable, "--version")
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", executable, err)
	}
	return strings.TrimSpace(out), nil
}

func configureOpenClawDiscovery(ctx context.Context, s *localRuntimeDiscoveryService, executable string, config map[string]any) []string {
	notes := []string{}
	runCtx, cancel := context.WithTimeout(ctx, s.opts.timeout)
	defer cancel()
	if out, err := s.opts.runCommand(runCtx, executable, "config", "file"); err == nil {
		if path := strings.TrimSpace(out); path != "" {
			config["config_path"] = path
		}
	} else {
		notes = append(notes, "openclaw config file: "+err.Error())
	}
	runCtx, cancel = context.WithTimeout(ctx, s.opts.timeout)
	defer cancel()
	out, err := s.opts.runCommand(runCtx, executable, "config", "get", "agents.list", "--json")
	if err != nil {
		notes = append(notes, "openclaw agents.list: "+err.Error())
		return notes
	}
	agentID := firstOpenClawAgentID(out)
	if agentID == "" {
		notes = append(notes, "openclaw agents.list did not contain an agent id")
		return notes
	}
	config["agent_id"] = agentID
	return notes
}

func configurePiDiscovery(ctx context.Context, s *localRuntimeDiscoveryService, executable string, config map[string]any) []string {
	config["mode"] = "local"
	config["approval_mode"] = piDefaultApprovalMode
	config["approval_timeout_ms"] = piDefaultApprovalTimeoutMs
	config["prompt_ack_timeout_ms"] = piDefaultPromptAckTimeoutMs
	return nil
}

func firstOpenClawAgentID(raw string) string {
	var items []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &items); err != nil {
		return ""
	}
	for _, item := range items {
		for _, key := range []string{"id", "agent_id", "name"} {
			if value, ok := item[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func runRuntimeDiscoveryCommand(ctx context.Context, command string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(raw))
		if msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return string(raw), nil
}

func resolveRuntimeProvidersViaLoginShell(names []string) map[string]string {
	out := map[string]string{}
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		return out
	}
	base := filepath.Base(shell)
	if base != "bash" && base != "zsh" && base != "sh" && base != "dash" && base != "ksh" {
		return out
	}
	safe := make([]string, 0, len(names))
	for _, name := range names {
		if isSafeRuntimeCommandName(name) {
			safe = append(safe, name)
		}
	}
	if len(safe) == 0 {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-ilc", buildRuntimeLoginShellResolveScript(safe))
	raw, err := cmd.Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		name, path := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if name == "" || !filepath.IsAbs(path) {
			continue
		}
		if _, err := exec.LookPath(path); err == nil {
			out[name] = path
		}
	}
	return out
}

func isSafeRuntimeCommandName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func buildRuntimeLoginShellResolveScript(names []string) string {
	var b strings.Builder
	b.WriteString("for name in")
	for _, name := range names {
		b.WriteByte(' ')
		b.WriteString(name)
	}
	b.WriteString(`; do
  unalias "$name" >/dev/null 2>&1 || true
  unset -f "$name" >/dev/null 2>&1 || true
  p=$(command -v "$name" 2>/dev/null || true)
  case "$p" in
    /*)
      d=$(dirname "$p")
      f=$(basename "$p")
      cd "$d" >/dev/null 2>&1 && printf '%s\t%s/%s\n' "$name" "$(pwd -P)" "$f"
      ;;
  esac
done`)
	return b.String()
}

func (r *Router) handleRuntimeDiscovery(w http.ResponseWriter, req *http.Request) {
	response, err := r.runtimeDiscovery.DiscoverRuntimeCandidates(req.Context())
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

package desktopagent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	processutil "orrery_v2/internal/agent/runtime/process"
)

const maxToolBytes = 64 * 1024

type toolPolicy struct {
	root string
	caps map[string]bool
}

func newToolPolicy(root string, capabilities []string) (*toolPolicy, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, ErrInput
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return nil, ErrInput
	}
	allowed := map[string]bool{"workspace.read": true, "workspace.write": true, "process.shell": true, "network.access": true, "artifact.patch": true, "artifact.summary": true, "artifact.selected": true}
	p := &toolPolicy{root: canonical, caps: map[string]bool{}}
	for _, capability := range capabilities {
		if !allowed[capability] || p.caps[capability] {
			return nil, ErrInput
		}
		p.caps[capability] = true
	}
	return p, nil
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func (p *toolPolicy) bindings() []agentcore.RuntimeToolBinding {
	text := func(max int) map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
	}
	var out []agentcore.RuntimeToolBinding
	add := func(capability, name, description string, schema map[string]any) {
		if p.caps[capability] {
			out = append(out, agentcore.RuntimeToolBinding{ToolID: agentcore.ToolID(name), Alias: strings.ReplaceAll(name, ".", "_"), Summary: description, InputSchema: schema})
		}
	}
	add("workspace.read", "workspace.read", "Read a UTF-8 text file by relative path inside the selected workspace.", objectSchema(map[string]any{"path": text(1024)}, "path"))
	add("workspace.read", "workspace.list", "List names inside a relative workspace directory. Use . for its root.", objectSchema(map[string]any{"path": text(1024)}, "path"))
	add("workspace.write", "workspace.write", "Atomically write a UTF-8 file in the workspace after local approval; existing parents are required.", objectSchema(map[string]any{"path": text(1024), "content": map[string]any{"type": "string", "maxLength": maxToolBytes}}, "path", "content"))
	add("process.shell", "process.shell", "Run a shell command in the workspace after explicit local approval. This is not an OS sandbox.", objectSchema(map[string]any{"command": text(8192)}, "command"))
	add("network.access", "network.get", "GET one HTTPS URL after explicit local approval; redirects are rejected.", objectSchema(map[string]any{"url": text(2048)}, "url"))
	return out
}

func (p *toolPolicy) path(relative string, write bool) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || strings.ContainsAny(relative, "\\:\x00") {
		return "", agentkernel.ErrToolDenied
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", agentkernel.ErrToolDenied
	}
	for _, segment := range strings.Split(filepath.ToSlash(clean), "/") {
		lower := strings.ToLower(segment)
		if lower == ".git" || lower == ".ssh" || lower == ".env" || strings.HasPrefix(lower, ".env.") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") {
			return "", agentkernel.ErrToolDenied
		}
	}
	target := filepath.Join(p.root, clean)
	check := target
	if write {
		if info, err := os.Lstat(target); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
			return "", agentkernel.ErrToolDenied
		}
		check = filepath.Dir(target)
	}
	real, err := filepath.EvalSymlinks(check)
	if err != nil {
		return "", agentkernel.ErrToolDenied
	}
	rel, err := filepath.Rel(p.root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", agentkernel.ErrToolDenied
	}
	if write {
		return filepath.Join(real, filepath.Base(target)), nil
	}
	return real, nil
}

func (p *toolPolicy) authorize(ctx context.Context, call runtimecore.RuntimeToolCallRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if call.TenantID != "local-device" || call.AgentID != "A000001" || call.ExecutionPrincipal.ID != "local-owner" {
		return agentkernel.ErrToolDenied
	}
	capability := map[string]string{"workspace.read": "workspace.read", "workspace.list": "workspace.read", "workspace.write": "workspace.write", "process.shell": "process.shell", "network.get": "network.access"}[call.Name]
	if capability == "" || !p.caps[capability] {
		return agentkernel.ErrToolDenied
	}
	if strings.HasPrefix(call.Name, "workspace.") {
		path, ok := call.Input["path"].(string)
		if !ok {
			return agentkernel.ErrToolDenied
		}
		_, err := p.path(path, call.Name == "workspace.write")
		return err
	}
	if call.Name == "network.get" {
		value, _ := call.Input["url"].(string)
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
			return agentkernel.ErrToolDenied
		}
	}
	return nil
}

func (p *toolPolicy) CallRuntimeTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
	if err := p.authorize(ctx, call); err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	result, err := p.execute(ctx, call)
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, errors.New("local_tool_failed")
	}
	return runtimecore.RuntimeToolCallResult{Output: result}, nil
}

func (p *toolPolicy) execute(ctx context.Context, call runtimecore.RuntimeToolCallRequest) (map[string]any, error) {
	value, _ := call.Input["path"].(string)
	switch call.Name {
	case "workspace.read":
		target, err := p.path(value, false)
		if err != nil {
			return nil, err
		}
		file, err := os.Open(target)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, ErrInput
		}
		raw, err := io.ReadAll(io.LimitReader(file, maxToolBytes+1))
		if err != nil || len(raw) > maxToolBytes || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
			return nil, ErrInput
		}
		return map[string]any{"text": string(raw)}, nil
	case "workspace.list":
		target, err := p.path(value, false)
		if err != nil {
			return nil, err
		}
		file, err := os.Open(target)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		entries, err := file.ReadDir(501)
		if err != nil && err != io.EOF {
			return nil, err
		}
		if len(entries) > 500 {
			return nil, ErrInput
		}
		items := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			items = append(items, map[string]any{"name": entry.Name(), "directory": entry.IsDir()})
		}
		return map[string]any{"entries": items}, nil
	case "workspace.write":
		target, err := p.path(value, true)
		if err != nil {
			return nil, err
		}
		content, ok := call.Input["content"].(string)
		if !ok || len(content) > maxToolBytes {
			return nil, ErrInput
		}
		file, err := os.CreateTemp(filepath.Dir(target), ".orrery-write-*")
		if err != nil {
			return nil, err
		}
		name := file.Name()
		defer os.Remove(name)
		if _, err := io.WriteString(file, content); err != nil {
			file.Close()
			return nil, err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
		if _, err := p.path(value, true); err != nil {
			return nil, err
		}
		if err := os.Rename(name, target); err != nil {
			return nil, err
		}
		return map[string]any{"writtenBytes": len(content)}, nil
	case "network.get":
		value, _ := call.Input["url"].(string)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, value, nil)
		if err != nil {
			return nil, err
		}
		client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, ErrInput
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, maxToolBytes+1))
		if err != nil || len(raw) > maxToolBytes {
			return nil, ErrInput
		}
		return map[string]any{"status": response.StatusCode, "text": string(raw)}, nil
	case "process.shell":
		command, _ := call.Input["command"].(string)
		return p.shell(ctx, command)
	}
	return nil, ErrInput
}

type boundedOutput struct {
	mu   sync.Mutex
	data []byte
}

func (b *boundedOutput) Write(raw []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data)+len(raw) > maxToolBytes {
		return 0, errors.New("output_limit")
	}
	b.data = append(b.data, raw...)
	return len(raw), nil
}

func (p *toolPolicy) shell(ctx context.Context, text string) (map[string]any, error) {
	if text == "" || len(text) > 8192 {
		return nil, ErrInput
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", "-c", text)
	if runtime.GOOS == "windows" {
		command = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", text)
	}
	command.Dir = p.root
	// Never pass model/device credentials, arbitrary caller env or launch paths.
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "LANG"} {
		if value := os.Getenv(name); value != "" {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	output := &boundedOutput{}
	command.Stdout = output
	command.Stderr = output
	command.WaitDelay = 2 * time.Second
	processutil.ConfigureProcessTree(command)
	command.Cancel = func() error { return processutil.TerminateProcessTree(command) }
	if err := command.Start(); err != nil {
		return nil, err
	}
	if err := processutil.AttachProcessTree(command); err != nil {
		_ = processutil.TerminateProcessTree(command)
		_ = command.Wait()
		return nil, err
	}
	defer processutil.ReleaseProcessTree(command)
	err := command.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	exit := 0
	if err != nil {
		var status *exec.ExitError
		if !errors.As(err, &status) {
			return nil, err
		}
		exit = status.ExitCode()
	}
	return map[string]any{"exitCode": exit, "output": string(output.data)}, nil
}

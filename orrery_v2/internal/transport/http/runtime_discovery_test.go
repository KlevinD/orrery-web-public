package httptransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

type stubRuntimeDiscovery struct {
	response RuntimeDiscoveryResponseDTO
	err      error
}

func (s stubRuntimeDiscovery) DiscoverRuntimeCandidates(ctx context.Context) (RuntimeDiscoveryResponseDTO, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeDiscoveryResponseDTO{}, err
	}
	return s.response, s.err
}

func TestSettingsRuntimeDiscoveryRouteReturnsCandidates(t *testing.T) {
	router := newTestRouter(RouterOptions{
		RuntimeDiscovery: stubRuntimeDiscovery{
			response: RuntimeDiscoveryResponseDTO{
				Candidates: []RuntimeDiscoveryCandidateDTO{
					{
						Kind:          "codex",
						Executable:    "codex",
						ConfigRef:     "runtime-codex-local",
						Version:       "codex 1.2.3",
						Status:        "ready",
						Source:        "PATH",
						RuntimeConfig: map[string]any{"executable": "codex"},
					},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodGet, "/api/settings/runtime/discovery", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings/runtime/discovery status = %d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		`"kind":"codex"`,
		`"executable":"codex"`,
		`"config_ref":"runtime-codex-local"`,
		`"version":"codex 1.2.3"`,
		`"status":"ready"`,
		`"source":"PATH"`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("runtime discovery response missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestLocalRuntimeDiscoveryIgnoresExplicitEnvPathAndUsesPATH(t *testing.T) {
	svc := newLocalRuntimeDiscoveryService(runtimeDiscoveryOptions{
		lookPath: func(name string) (string, error) {
			if name == "C:/tools/codex.exe" {
				return name, nil
			}
			if name == "codex" {
				return "C:/Windows/System32/codex.exe", nil
			}
			return "", exec.ErrNotFound
		},
		runCommand: func(ctx context.Context, command string, args ...string) (string, error) {
			if command == "codex" && strings.Join(args, " ") == "--version" {
				return "codex 1.2.3", nil
			}
			return "", errors.New("unexpected command")
		},
		goos: "windows",
	})

	result, err := svc.DiscoverRuntimeCandidates(context.Background())
	if err != nil {
		t.Fatalf("DiscoverRuntimeCandidates: %v", err)
	}
	candidate := findRuntimeCandidate(result.Candidates, "codex")
	if candidate == nil {
		t.Fatalf("codex candidate missing: %#v", result.Candidates)
	}
	if candidate.Executable != "codex" || candidate.Source != "PATH" {
		t.Fatalf("codex candidate executable/source = %q/%q, want PATH candidate", candidate.Executable, candidate.Source)
	}
	if candidate.Version != "codex 1.2.3" || candidate.Status != "ready" {
		t.Fatalf("codex candidate version/status = %q/%q", candidate.Version, candidate.Status)
	}
}

func TestLocalRuntimeDiscoveryIgnoresProviderArgsFromEnvironment(t *testing.T) {
	svc := newLocalRuntimeDiscoveryService(runtimeDiscoveryOptions{
		lookPath: func(name string) (string, error) {
			if name == "claude" {
				return "claude", nil
			}
			return "", exec.ErrNotFound
		},
		runCommand: func(ctx context.Context, command string, args ...string) (string, error) {
			if command == "claude" && strings.Join(args, " ") == "--version" {
				return "claude 2.0.0", nil
			}
			return "", errors.New("unexpected command")
		},
		goos: "windows",
	})

	result, err := svc.DiscoverRuntimeCandidates(context.Background())
	if err != nil {
		t.Fatalf("DiscoverRuntimeCandidates: %v", err)
	}
	candidate := findRuntimeCandidate(result.Candidates, "claude")
	if candidate == nil {
		t.Fatalf("claude candidate missing: %#v", result.Candidates)
	}
	if _, ok := candidate.RuntimeConfig["extra_args"]; ok {
		t.Fatalf("extra_args should not be imported from environment: %#v", candidate.RuntimeConfig["extra_args"])
	}
	for _, note := range candidate.Notes {
		if strings.Contains(note, "ORRERY_CLAUDE_ARGS") {
			t.Fatalf("notes should not reference environment args: %#v", candidate.Notes)
		}
	}
}

func TestLocalRuntimeDiscoveryFindsPiWithApprovalDefaults(t *testing.T) {
	svc := newLocalRuntimeDiscoveryService(runtimeDiscoveryOptions{
		lookPath: func(name string) (string, error) {
			if name == "pi" {
				return "pi", nil
			}
			return "", exec.ErrNotFound
		},
		runCommand: func(ctx context.Context, command string, args ...string) (string, error) {
			if command == "pi" && strings.Join(args, " ") == "--version" {
				return "pi 1.3.0", nil
			}
			return "", errors.New("unexpected command")
		},
		goos: "windows",
	})

	result, err := svc.DiscoverRuntimeCandidates(context.Background())
	if err != nil {
		t.Fatalf("DiscoverRuntimeCandidates: %v", err)
	}
	candidate := findRuntimeCandidate(result.Candidates, "pi")
	if candidate == nil {
		t.Fatalf("pi candidate missing: %#v", result.Candidates)
	}
	if candidate.ConfigRef != "runtime-pi-local" || candidate.RuntimeConfig["approval_mode"] != "on-request" ||
		candidate.RuntimeConfig["approval_timeout_ms"] != 600000 {
		t.Fatalf("pi candidate = %+v", candidate)
	}
}

func TestLocalRuntimeDiscoveryUsesLoginShellFallbackOnUnix(t *testing.T) {
	svc := newLocalRuntimeDiscoveryService(runtimeDiscoveryOptions{
		lookPath: func(name string) (string, error) {
			if name == "/Users/alice/.local/bin/claude" {
				return name, nil
			}
			return "", exec.ErrNotFound
		},
		runCommand: func(ctx context.Context, command string, args ...string) (string, error) {
			if command == "/Users/alice/.local/bin/claude" && strings.Join(args, " ") == "--version" {
				return "claude 2.0.0", nil
			}
			return "", errors.New("unexpected command")
		},
		resolveViaLoginShell: func(names []string) map[string]string {
			return map[string]string{"claude": "/Users/alice/.local/bin/claude"}
		},
		goos: "darwin",
	})

	result, err := svc.DiscoverRuntimeCandidates(context.Background())
	if err != nil {
		t.Fatalf("DiscoverRuntimeCandidates: %v", err)
	}
	candidate := findRuntimeCandidate(result.Candidates, "claude")
	if candidate == nil {
		t.Fatalf("claude candidate missing: %#v", result.Candidates)
	}
	if candidate.Executable != "/Users/alice/.local/bin/claude" || candidate.Source != "login_shell" {
		t.Fatalf("claude candidate executable/source = %q/%q, want login shell path", candidate.Executable, candidate.Source)
	}
}

func TestLocalRuntimeDiscoveryFillsOpenClawAgentFromResolvedConfig(t *testing.T) {
	svc := newLocalRuntimeDiscoveryService(runtimeDiscoveryOptions{
		lookPath: func(name string) (string, error) {
			if name == "openclaw" {
				return "openclaw", nil
			}
			return "", exec.ErrNotFound
		},
		runCommand: func(ctx context.Context, command string, args ...string) (string, error) {
			switch strings.Join(append([]string{command}, args...), " ") {
			case "openclaw --version":
				return "openclaw 2026.5.5", nil
			case "openclaw config file":
				return "/Users/alice/.openclaw/openclaw.json", nil
			case "openclaw config get agents.list --json":
				return `[{"id":"planner","model":"openai/gpt-5"}]`, nil
			default:
				return "", errors.New("unexpected command")
			}
		},
		goos: "windows",
	})

	result, err := svc.DiscoverRuntimeCandidates(context.Background())
	if err != nil {
		t.Fatalf("DiscoverRuntimeCandidates: %v", err)
	}
	candidate := findRuntimeCandidate(result.Candidates, "openclaw")
	if candidate == nil {
		t.Fatalf("openclaw candidate missing: %#v", result.Candidates)
	}
	if candidate.RuntimeConfig["agent_id"] != "planner" {
		t.Fatalf("openclaw agent_id = %#v, want planner", candidate.RuntimeConfig["agent_id"])
	}
	if candidate.ConfigRef != "runtime-openclaw-local" {
		t.Fatalf("openclaw config_ref = %q", candidate.ConfigRef)
	}
}

func findRuntimeCandidate(candidates []RuntimeDiscoveryCandidateDTO, kind string) *RuntimeDiscoveryCandidateDTO {
	for i := range candidates {
		if candidates[i].Kind == kind {
			return &candidates[i]
		}
	}
	return nil
}

package openclaw

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const minOpenClawVersion = "2026.5.5"

var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func checkVersion(ctx context.Context, execPath string) error {
	if _, err := exec.LookPath(execPath); err != nil {
		return fmt.Errorf("openclaw executable not found at %q: %w", execPath, err)
	}
	cmd := exec.CommandContext(ctx, execPath, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("openclaw --version failed: %w", err)
	}
	detected, ok := parseVersion(string(out))
	if !ok {
		return fmt.Errorf("could not parse openclaw version from output: %q", strings.TrimSpace(string(out)))
	}
	if compareVersion(detected, minOpenClawVersion) < 0 {
		return fmt.Errorf("openclaw %s is below the minimum supported version %s. Run `openclaw update` to upgrade and try again.", detected, minOpenClawVersion)
	}
	return nil
}

func parseVersion(raw string) (string, bool) {
	value := versionPattern.FindString(raw)
	return value, value != ""
}

func compareVersion(a, b string) int {
	left := strings.SplitN(a, ".", 3)
	right := strings.SplitN(b, ".", 3)
	for i := 0; i < 3; i++ {
		li, _ := strconv.Atoi(left[i])
		ri, _ := strconv.Atoi(right[i])
		if li < ri {
			return -1
		}
		if li > ri {
			return 1
		}
	}
	return 0
}

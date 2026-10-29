package process

import (
	"os"
	"strings"
)

func BuildEnv(extra map[string]string) []string {
	env := os.Environ()
	if len(extra) == 0 {
		return env
	}
	index := make(map[string]int, len(env))
	for i, pair := range env {
		key, _, ok := strings.Cut(pair, "=")
		if ok {
			index[key] = i
		}
	}
	for key, value := range extra {
		pair := key + "=" + value
		if i, ok := index[key]; ok {
			env[i] = pair
			continue
		}
		env = append(env, pair)
	}
	return env
}

func buildEnv(extra map[string]string) []string {
	return BuildEnv(extra)
}

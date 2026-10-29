package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type assignments []string

func (a *assignments) String() string { return strings.Join(*a, ",") }

func (a *assignments) Set(value string) error {
	if _, _, ok := strings.Cut(value, "="); !ok {
		return fmt.Errorf("assignment must be path=value")
	}
	*a = append(*a, value)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("configctl render", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	input := fs.String("input", "", "source YAML path")
	output := fs.String("output", "", "rendered YAML path")
	var sets assignments
	fs.Var(&sets, "set", "existing dotted path assignment")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*input) == "" || strings.TrimSpace(*output) == "" {
		return errors.New("-input and -output are required")
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		return fmt.Errorf("read input config: %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("parse input config: %w", err)
	}
	for _, assignment := range sets {
		path, value, _ := strings.Cut(assignment, "=")
		if err := setExistingScalar(&document, strings.Split(path, "."), value); err != nil {
			return err
		}
	}
	var rendered bytes.Buffer
	encoder := yaml.NewEncoder(&rendered)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("close config encoder: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o700); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(*output, rendered.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write output config: %w", err)
	}
	return nil
}

func setExistingScalar(document *yaml.Node, path []string, value string) error {
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("config root must be a mapping")
	}
	node := document.Content[0]
	for _, segment := range path {
		if node.Kind != yaml.MappingNode {
			return fmt.Errorf("configuration path %q is not a mapping", strings.Join(path, "."))
		}
		var next *yaml.Node
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == segment {
				next = node.Content[i+1]
				break
			}
		}
		if next == nil {
			return fmt.Errorf("configuration path %q does not exist", strings.Join(path, "."))
		}
		node = next
	}
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("configuration path %q is not a scalar", strings.Join(path, "."))
	}
	switch node.Tag {
	case "!!bool":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("configuration path %q expects a boolean", strings.Join(path, "."))
		}
		node.Value = strconv.FormatBool(parsed)
	case "!!int":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("configuration path %q expects an integer", strings.Join(path, "."))
		}
		node.Value = value
	default:
		node.Tag = "!!str"
		node.Value = value
	}
	return nil
}

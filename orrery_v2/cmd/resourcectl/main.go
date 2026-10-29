package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"orrery_v2/internal/platform/db"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: resourcectl <register-mcp|load-skills|openclaw-shadow|native-memory-schedules|native-scheduled|scheduler-mission-profiles|rsi-samples|improvements> [flags]")
	}
	switch args[0] {
	case "register-mcp":
		return runRegisterMCP(ctx, args[1:], out)
	case "load-skills":
		return runLoadSkills(ctx, args[1:], out)
	case "openclaw-shadow":
		return runOpenClawShadow(ctx, args[1:], out)
	case "native-memory-schedules":
		return runNativeMemorySchedules(ctx, args[1:], out)
	case "native-scheduled":
		return runNativeScheduled(ctx, args[1:], out)
	case "scheduler-mission-profiles":
		return runSchedulerMissionProfiles(ctx, args[1:], out)
	case "rsi-samples":
		return runRSISamples(ctx, args[1:], out)
	case "improvements":
		return runImprovements(ctx, args[1:], out)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runRegisterMCP(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("register-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbURL := fs.String("db", "", "platform database URL, for example file://resources.db")
	configPath := fs.String("config", "", "MCP config JSON path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*dbURL) == "" {
		return fmt.Errorf("--db is required")
	}
	if strings.TrimSpace(*configPath) == "" {
		return fmt.Errorf("--config is required")
	}
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: *dbURL, Name: "resource-registry"})
	if err != nil {
		return err
	}
	defer store.Close()
	loaded, err := toolres.RegisterMCPConfig(ctx, toolres.NewPlatformRegistry(store), *configPath)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "registered %d mcp server(s)\n", loaded)
	return err
}

func runLoadSkills(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("load-skills", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbURL := fs.String("db", "", "platform database URL, for example file://resources.db")
	root := fs.String("root", "", "root directory containing SKILL.md directories")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*dbURL) == "" {
		return fmt.Errorf("--db is required")
	}
	if strings.TrimSpace(*root) == "" {
		return fmt.Errorf("--root is required")
	}
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: *dbURL, Name: "resource-registry"})
	if err != nil {
		return err
	}
	defer store.Close()
	loaded, err := skillres.LoadDirectorySeeds(ctx, skillres.NewPlatformRegistry(store), *root)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "registered %d skill(s)\n", loaded)
	return err
}

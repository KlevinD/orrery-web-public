// rsiruntime manages a standalone, explicitly admitted source task runtime.
// It does not deploy Orrery services or authorize production domain actions.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/platform/db"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readJSON(path string, into any) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read runtime input failed")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return improvement.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(into) != nil || d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("invalid runtime input JSON")
	}
	return nil
}

func executorDigest() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("expected prepare, authorize, cycle, observe, rollback or active")
	}
	command := args[0]
	flags := flag.NewFlagSet("rsiruntime", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	database := flags.String("db", os.Getenv("ORRERY_RSI_DB_URL"), "admitted runtime database")
	input := flags.String("input", "", "runtime request (prepare) or frozen plan")
	authorization := flags.String("authorization", "", "host-signed scoped runtime grant")
	issuer := flags.String("issuer", "", "host operator identity for authorize")
	ttl := flags.Duration("ttl", time.Hour, "scoped authorization lifetime")
	workRoot := flags.String("work-root", "", "absolute project temporary directory")
	tenant := flags.String("tenant", "", "tenant for active")
	runtimeID := flags.String("runtime", "", "runtime ID for active")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return improvement.ErrInvalid
	}
	switch command {
	case "prepare", "authorize", "cycle", "observe", "rollback", "active":
	default:
		return improvement.ErrInvalid
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("ORRERY_RSI_AUTH_KEY"))
	if err != nil || len(key) < 32 {
		return fmt.Errorf("ORRERY_RSI_AUTH_KEY must be a separate host authority key (base64, >=32 bytes)")
	}
	authority, _ := improvement.NewRuntimeAuthority(key)
	digest, err := executorDigest()
	if err != nil {
		return err
	}
	var plan improvement.RuntimePlan
	if command != "prepare" && command != "active" {
		if err := readJSON(*input, &plan); err != nil {
			return err
		}
		if plan.ExecutorSHA256 != digest {
			return fmt.Errorf("runtime executor differs from the frozen plan")
		}
	}
	if command == "authorize" {
		grant, err := authority.Authorize(plan, *issuer, time.Now(), *ttl)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(grant)
	}
	if *database == "" || !filepath.IsAbs(*workRoot) {
		return fmt.Errorf("--db and absolute --work-root are required")
	}
	seal, err := base64.StdEncoding.DecodeString(os.Getenv("ORRERY_RSI_SEAL_KEY"))
	if err != nil || len(seal) < 32 || bytes.Equal(key, seal) {
		return fmt.Errorf("independent ORRERY_RSI_SEAL_KEY (base64, >=32 bytes) is required")
	}
	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: *database})
	if err != nil {
		return fmt.Errorf("open runtime database failed")
	}
	defer registry.Close()
	core, err := registry.Store(db.RoleCore)
	if err != nil {
		return err
	}
	vault, err := improvement.NewArtifactVault(core, seal)
	if err != nil {
		return err
	}
	runtime := &improvement.SourceRuntime{Store: improvement.NewStore(core), Vault: vault, Authority: authority, Runner: &improvement.DockerTaskRunner{WorkRoot: *workRoot}, ExecutorSHA256: digest}
	ctx, cancel := context.WithTimeout(ctx, 9*time.Minute)
	defer cancel()
	var result any
	switch command {
	case "prepare":
		var request improvement.RuntimeRequest
		if err := readJSON(*input, &request); err != nil {
			return err
		}
		result, err = runtime.Prepare(ctx, request)
	case "active":
		result, err = runtime.Active(ctx, *tenant, *runtimeID)
	case "observe":
		var c improvement.Case
		c, err = runtime.Observe(ctx, plan)
		c.Lease = nil
		result = c
	case "cycle", "rollback":
		var grant improvement.RuntimeGrant
		if err := readJSON(*authorization, &grant); err != nil {
			return err
		}
		if command == "cycle" {
			result, err = runtime.Cycle(ctx, plan, grant)
		} else {
			var c improvement.Case
			c, err = runtime.Rollback(ctx, plan, grant)
			c.Lease = nil
			result = c
		}
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(result)
}

// rsictl admits and runs one frozen task experiment. It does not deploy code,
// mutate live resources or enable automatic processing of arbitrary tasks.
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
	"net/http"
	"os"
	"time"

	llmprofile "orrery_v2/internal/agent/runtime/llmprofile"
	profilemodelruntime "orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/app/improvement/einomodel"
	"orrery_v2/internal/platform/db"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("rsictl", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	specPath := flags.String("spec", "", "operator-admitted frozen task replay JSON")
	database := flags.String("db", os.Getenv("ORRERY_RSI_DB_URL"), "experiment database URL")
	workRoot := flags.String("work-root", "", "absolute project temp directory for isolated source snapshots")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *specPath == "" || *database == "" || *workRoot == "" {
		return fmt.Errorf("--spec, --db (or ORRERY_RSI_DB_URL) and --work-root are required")
	}
	raw, err := os.ReadFile(*specPath)
	if err != nil {
		return fmt.Errorf("read replay spec failed")
	}
	if len(raw) > 4<<20 {
		return improvement.ErrInvalid
	}
	var spec improvement.ReplaySpec
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return fmt.Errorf("invalid replay spec JSON")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return improvement.ErrInvalid
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err := os.Open(executable)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, binary)
	_ = binary.Close()
	if copyErr != nil || hex.EncodeToString(hash.Sum(nil)) != spec.ExecutorSHA256 {
		return fmt.Errorf("executor binary does not match the frozen build digest")
	}
	if spec.Generator.Version != einomodel.PromptVersion || spec.Generator.PromptSHA256 != einomodel.PromptSHA256() {
		return fmt.Errorf("candidate prompt does not match the frozen generator digest")
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("ORRERY_RSI_SEAL_KEY"))
	if err != nil || len(key) < 32 {
		return fmt.Errorf("ORRERY_RSI_SEAL_KEY must contain at least 32 random bytes encoded as base64")
	}
	modelURL, apiKey := os.Getenv("ORRERY_RSI_MODEL_URL"), os.Getenv("ORRERY_RSI_MODEL_API_KEY")
	if modelURL == "" || apiKey == "" {
		return fmt.Errorf("candidate model URL and API key environment variables are required")
	}
	if spec.Generator.Endpoint != modelURL {
		return fmt.Errorf("candidate model endpoint differs from the frozen generator")
	}
	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: *database})
	if err != nil {
		return fmt.Errorf("open experiment database failed")
	}
	defer registry.Close()
	core, err := registry.Store(db.RoleCore)
	if err != nil {
		return err
	}
	vault, err := improvement.NewArtifactVault(core, key)
	if err != nil {
		return err
	}
	models := profilemodelruntime.NewProfileModelFactory(profilemodelruntime.ProfileModelFactoryConfig{
		Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: spec.Generator.ProfileID, Model: spec.Generator.Model, BaseURL: modelURL, APIKey: apiKey, Enabled: true, ContextLength: 128000}}},
		Client:   &http.Client{Timeout: time.Duration(spec.Policy.ModelTimeoutMS) * time.Millisecond, Transport: candidateTransport{base: http.DefaultTransport}},
	})
	worker := improvement.ReplayCoordinator{Store: improvement.NewStore(core), Vault: vault, Model: &einomodel.Generator{Models: models, Descriptor: spec.Generator}, Runner: &improvement.DockerTaskRunner{WorkRoot: *workRoot}}
	c, err := worker.Run(ctx, spec)
	if err != nil {
		return err
	}
	c.Lease = nil
	return json.NewEncoder(out).Encode(c)
}

type candidateTransport struct{ base http.RoundTripper }

func (t candidateTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	request.Header.Set("User-Agent", "orrery-rsi-candidate/1.0")
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = &boundedResponseBody{Reader: io.LimitReader(response.Body, 2<<20), Closer: response.Body}
	return response, nil
}

type boundedResponseBody struct {
	io.Reader
	io.Closer
}

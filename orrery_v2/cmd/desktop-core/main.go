// Orrery Core runs the repository-owned desktop session API.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const coreVersion = "0.1.0"

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("orrery-core", flag.ContinueOnError)
	flags.SetOutput(stderr)
	port := flags.Int("port", 0, "loopback HTTP port")
	dataDir := flags.String("data-dir", "", "local conversation storage directory")
	logDir := flags.String("log-dir", "", "desktop log directory")
	_ = flags.String("work-dir", "", "desktop work directory")
	_ = flags.String("log-level", "info", "log level")
	_ = flags.String("app-version", "", "desktop application version")
	_ = flags.String("managed-resources-mode", "", "managed resource mode")
	_ = flags.Int("parent-pid", 0, "desktop parent process ID")
	_ = flags.Bool("dump-prompts", false, "reserved prompt diagnostics flag")
	_ = flags.Bool("local", true, "run local core mode")
	_ = flags.Bool("recover-corrupted-database", false, "reserved database recovery flag")
	version := flags.Bool("version", false, "print core version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *version {
		_, err := fmt.Fprintln(stdout, coreVersion)
		return err
	}
	if *port < 0 || *port > 65535 || flags.NArg() != 0 {
		return errors.New("invalid core launch arguments")
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	if *logDir != "" {
		if err := os.MkdirAll(*logDir, 0700); err != nil {
			return err
		}
		logFile, err := os.OpenFile(filepath.Join(*logDir, "orrery-core.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		defer logFile.Close()
		logger = slog.New(slog.NewJSONHandler(io.MultiWriter(stderr, logFile), nil))
	}
	core, err := newLocalCore(os.Getenv("ORRERY_CLAUDE_EXECUTABLE"))
	if err != nil {
		return err
	}
	defer core.close()
	if *dataDir == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		*dataDir = filepath.Join(configDir, "Orrery", "core")
	}
	if err := core.openStore(*dataDir); err != nil {
		return errors.New("local core storage could not be opened")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return err
	}
	defer listener.Close()
	actualPort := listener.Addr().(*net.TCPAddr).Port
	if _, err := fmt.Fprintf(stdout, "ORRERY_CORE_LISTENING {\"port\":%d}\n", actualPort); err != nil {
		return err
	}
	logger.Info("Orrery Core listening", "port", actualPort, "version", coreVersion)
	server := &http.Server{Handler: core, ReadHeaderTimeout: 10 * time.Second}
	stopShutdown := make(chan struct{})
	defer close(stopShutdown)
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		case <-stopShutdown:
		}
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid JSON request")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"code": "ORRERY_CORE_ERROR", "error": err.Error()})
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		slog.Error("Orrery Core stopped", "error", err)
		os.Exit(1)
	}
}

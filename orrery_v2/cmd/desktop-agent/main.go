// desktop-agent is Orrery's local AgentKernel host. Its only control transport
// is the inherited stdio pipe owned by the Electron main process.
package main

import (
	"flag"
	"log/slog"
	"os"

	"orrery_v2/internal/desktopagent"
)

func main() {
	state := flag.String("state", "", "absolute path to the local runtime database")
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := desktopagent.Serve(os.Stdin, os.Stdout, *state); err != nil {
		slog.Error("desktop_agent_stopped", "code", "runtime_host_failure")
		os.Exit(1)
	}
}

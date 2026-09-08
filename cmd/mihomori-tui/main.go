// Command mihomori-tui is an interactive TUI client for mihomori.
package main

import (
	"flag"
	"log"
	"os"

	tea "charm.land/bubbletea/v2"

	"mihomori/internal/tui/client"
	"mihomori/internal/tui"
)

// defaultEndpoint mirrors the server's CONTROL_PORT default (8080) so a local
// server on the default port can be reached without any flags.
func defaultEndpoint() string {
	port := os.Getenv("CONTROL_PORT")
	if port == "" {
		port = "8080"
	}
	return "http://127.0.0.1:" + port
}

func main() {
	var (
		endpoint = flag.String("endpoint", defaultEndpoint(), "mihomori endpoint URL")
		token    = flag.String("token", os.Getenv("CONTROL_TOKEN"), "control API Bearer token (CONTROL_TOKEN env)")
		insecure = flag.Bool("insecure", false, "skip TLS certificate verification")
	)
	flag.Parse()

	client := client.NewClient(*endpoint, *token, *insecure)

	// Probe the control API before entering the TUI so a wrong endpoint or
	// missing auth fails fast with a clear message instead of a blank screen.
	if _, err := client.KernelStatus(); err != nil {
		log.Fatalf("mihomori-tui: cannot reach %s: %v", *endpoint, err)
	}

	if _, err := tea.NewProgram(tui.New(client)).Run(); err != nil {
		log.Fatalf("mihomori-tui: %v", err)
	}
}
